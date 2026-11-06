#!/usr/bin/env python3
"""Fail when a replay silently drops fork integration points.

A complete-tree replay replaces shared files wholesale. Resolving a conflict by
taking upstream's version can remove fork behaviour that lived inside that file
while leaving a tree that compiles and whose remaining tests pass. This check
compares per-file occurrence counts of the recorded hook symbols against a
baseline ref and fails when a file that still exists lost all of them.

Deleted files are not failures: retiring a capability removes its tests and
helpers on purpose. The intake review records that decision.

Usage:
    python3 ops/upstream-intake/check-fork-hooks.py [--baseline REF] [--hooks FILE]
"""

from __future__ import annotations

import argparse
import collections
import os
import subprocess
import sys

DEFAULT_HOOKS = "ops/upstream-intake/fork-hooks.txt"
DEFAULT_ALLOW = "ops/upstream-intake/fork-hooks-allow.txt"

# Baseline candidates in order. A release build checks out a tag, so only the
# remote-tracking ref of the trunk is guaranteed to exist there; the intake run
# in a worktree resolves plain main.
BASELINE_CANDIDATES = ("main", "origin/main")


def git(*args: str) -> str:
    return subprocess.run(
        ["git", *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    ).stdout


def occurrence_counts(symbol: str, ref: str | None) -> dict[str, int]:
    """Per-file occurrence counts for one symbol in a ref or the worktree."""
    args = ["grep", "-c", "-F", symbol]
    if ref is not None:
        args.append(ref)
    args.extend(["--", "*.go"])
    try:
        output = git(*args)
    except subprocess.CalledProcessError as err:
        if err.returncode == 1:
            return {}
        raise
    counts: dict[str, int] = {}
    prefix = f"{ref}:" if ref is not None else ""
    for line in output.splitlines():
        path, _, raw = line.rpartition(":")
        if not path:
            continue
        # git prefixes both the tree and the path when a ref is given.
        if prefix and path.startswith(prefix):
            path = path[len(prefix):]
        counts[path] = int(raw)
    return counts


def resolve_baseline(explicit: str | None) -> str | None:
    """First baseline ref that resolves, or None when this checkout has none."""
    candidates = (explicit,) if explicit else BASELINE_CANDIDATES
    for candidate in candidates:
        try:
            subprocess.run(
                ["git", "rev-parse", "--verify", "--quiet", f"{candidate}^{{commit}}"],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            return candidate
        except subprocess.CalledProcessError:
            continue
    return None


def read_hooks(path: str) -> list[str]:
    hooks: list[str] = []
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            hooks.append(stripped)
    return hooks


def read_allow(path: str) -> set[tuple[str, str]]:
    """Recorded relocations: symbol and the file it moved out of."""
    if not path or not os.path.exists(path):
        return set()
    allowed: set[tuple[str, str]] = set()
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            stripped = line.split("#", 1)[0].strip()
            if not stripped:
                continue
            fields = stripped.split()
            if len(fields) != 2:
                raise SystemExit(
                    f"allow entry needs a symbol and a path: {line.rstrip()}"
                )
            allowed.add((fields[0], fields[1]))
    return allowed


def lost_occurrences(
    hooks: list[str], baseline: str, allowed: set[tuple[str, str]]
) -> tuple[list[tuple[str, str, int]], list[tuple[str, str, int]], dict[str, tuple[int, int]]]:
    """Files that still exist but lost every occurrence of a hook symbol."""
    lost: list[tuple[str, str, int]] = []
    allowed_losses: list[tuple[str, str, int]] = []
    totals: dict[str, tuple[int, int]] = collections.OrderedDict()
    for hook in hooks:
        before = occurrence_counts(hook, baseline)
        after = occurrence_counts(hook, None)
        totals[hook] = (sum(before.values()), sum(after.values()))
        for path, count in sorted(before.items()):
            if count <= 0 or after.get(path, 0) > 0:
                continue
            if not os.path.exists(path):
                continue
            if (hook, path) in allowed:
                allowed_losses.append((hook, path, count))
                continue
            lost.append((hook, path, count))
    return lost, allowed_losses, totals


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", default=None)
    parser.add_argument("--hooks", default=DEFAULT_HOOKS)
    parser.add_argument("--allow", default=DEFAULT_ALLOW)
    args = parser.parse_args(argv)

    if not os.path.exists(args.hooks):
        print(f"hook list not found: {args.hooks}", file=sys.stderr)
        return 2

    hooks = read_hooks(args.hooks)
    if not hooks:
        print(f"hook list is empty: {args.hooks}", file=sys.stderr)
        return 2

    baseline = resolve_baseline(args.baseline)
    if baseline is None:
        print(
            "no baseline ref resolves in this checkout (looked for: "
            + ", ".join((args.baseline,) if args.baseline else BASELINE_CANDIDATES)
            + "); pass --baseline, or run the inventory during intake",
            file=sys.stderr,
        )
        return 2

    allowed = read_allow(args.allow)
    try:
        lost, allowed_losses, totals = lost_occurrences(hooks, baseline, allowed)
    except subprocess.CalledProcessError as err:
        print(f"git failed: {err.stderr.strip()}", file=sys.stderr)
        return 2

    for hook, (before, after) in totals.items():
        print(f"{hook}: {before} -> {after}")

    for hook, path, count in allowed_losses:
        print(f"recorded relocation: {path} no longer calls {hook} ({count}x before)")

    if lost:
        print("")
        print("fork integration points lost in files that still exist:")
        for hook, path, count in lost:
            print(f"  {path}: {hook} was used {count}x, now absent")
        print("")
        print(
            "Restore the wiring, or delete the file as part of a recorded "
            "retirement. Taking upstream's version of a shared file is not a "
            "reason to drop a fork capability."
        )
        return 1

    print("")
    print(f"fork hooks intact against {baseline}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
