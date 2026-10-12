"""Predeclare paired runs; validation is not a live certificate.

Only explicit material files are read. No credentials, paths or tool bodies are
exported. Unknown scope stays unknown; physical-attempt reservations are never
refunded from missing evidence. This single-process budget has no resume mode.
"""

from copy import deepcopy
from dataclasses import asdict
import hashlib
import json
import os
import random
import re
import stat

from run_acceptance import _identifier, _validate_spec, summarize

HASH = re.compile(r"[0-9a-f]{64}\Z")
REVISION = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
MATERIAL = re.compile(r"[A-Za-z][A-Za-z0-9_.-]{0,159}\Z")


def _digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()).hexdigest()


def _material_hash(path):
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or not 0 <= before.st_size <= 1024**3:
            raise ValueError("invalid batch material file")
        digest = hashlib.sha256()
        observed = 0
        while chunk := stream.read(1024**2):
            observed += len(chunk)
            if observed > 1024**3:
                raise ValueError("batch material exceeded capture limit")
            digest.update(chunk)
        after = os.fstat(stream.fileno())
    identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if observed != before.st_size or identity(before) != identity(after) or identity(after) != identity(os.stat(path, follow_symlinks=False)):
        raise ValueError("batch material changed while hashing")
    return digest.hexdigest()


def fingerprint_materials(paths):
    if not isinstance(paths, dict) or not paths or any(not isinstance(key, str) or not MATERIAL.fullmatch(key) for key in paths):
        raise ValueError("explicit batch material identities are required")
    try:
        return {key: _material_hash(path) for key, path in sorted(paths.items())}
    except (OSError, TypeError):
        raise ValueError("batch material file cannot be verified") from None


def verify_materials(plan, paths):
    validate_plan(plan)
    if fingerprint_materials(paths) != plan["materials"]:
        raise ValueError("batch material fingerprint changed")


def _pairs(cases):
    groups, identities, revisions = {}, set(), set()
    for case in cases:
        _validate_spec(case)
        if case["case_id"] in identities:
            raise ValueError("duplicate planned case")
        identities.add(case["case_id"])
        revision = case["scope"]["gateway_revision"]
        if not REVISION.fullmatch(revision):
            raise ValueError("a concrete gateway revision is required")
        revisions.add(revision)
        pair = groups.setdefault(case["pair_id"], {})
        if case["mode"] in pair:
            raise ValueError("duplicate planned pair mode")
        pair[case["mode"]] = case
    if len(revisions) != 1 or not groups:
        raise ValueError("one gateway revision is required per batch")
    routes = {}
    for pair_id, pair in sorted(groups.items()):
        if set(pair) != {"eager", "lazy"}:
            raise ValueError("paired batch requires both modes")
        eager, lazy = pair["eager"], pair["lazy"]
        left = {key: value for key, value in eager["scope"].items() if key != "client_mode"}
        right = {key: value for key, value in lazy["scope"].items() if key != "client_mode"}
        fields = ("template_id", "cache_condition", "prompt_hash", "fixture_seed", "limits", "gold")
        if left != right or any(eager[key] != lazy[key] for key in fields):
            raise ValueError("planned pair has incompatible conditions")
        routes.setdefault(json.dumps(left, sort_keys=True), []).append(pair)
    return routes


def _schedule(cases, seed):
    routes = _pairs(cases)
    rng = random.Random(int(hashlib.sha256(seed.encode()).hexdigest(), 16))
    keys = sorted(routes)
    rng.shuffle(keys)
    blocks = []
    offset = rng.randrange(2)
    for route_index, key in enumerate(keys):
        pairs = routes[key]
        rng.shuffle(pairs)
        blocks.append([[pair[mode]["case_id"] for mode in
                        (("eager", "lazy") if (index + route_index + offset) % 2 == 0 else ("lazy", "eager"))]
                       for index, pair in enumerate(pairs)])
    # Round-robin route blocks, retaining each pair's adjacent order.
    return [case_id for index in range(max(map(len, blocks))) for route in blocks
            if index < len(route) for case_id in route[index]]


def build_plan(run_id, cases, materials, seed, max_requests, max_dispatches):
    if not _identifier(run_id) or not _identifier(seed):
        raise ValueError("invalid batch identity or seed")
    if any(type(count) is not int or not 1 <= count <= 2**31 - 1 for count in (max_requests, max_dispatches)):
        raise ValueError("positive batch budgets are required")
    if (not isinstance(materials, dict) or not materials or any(
            not isinstance(key, str) or not MATERIAL.fullmatch(key) or not isinstance(value, str) or not HASH.fullmatch(value)
            for key, value in materials.items())):
        raise ValueError("verified batch material digests are required")
    declarations = [asdict(case) if hasattr(case, "__dataclass_fields__") else deepcopy(case) for case in cases]
    declarations.sort(key=lambda case: case["case_id"])
    schedule = _schedule(declarations, seed)
    plan = {"schema_version": 1, "run_id": run_id, "schedule_seed": seed, "cases": declarations,
            "schedule": schedule, "materials": deepcopy(materials),
            "limits": {"max_requests": max_requests, "max_dispatches": max_dispatches}, "gate_status": "unverified"}
    return {**plan, "plan_sha256": _digest(plan)}


def validate_plan(plan):
    fields = {"schema_version", "run_id", "schedule_seed", "cases", "schedule", "materials", "limits", "gate_status", "plan_sha256"}
    if (not isinstance(plan, dict) or set(plan) != fields or type(plan["schema_version"]) is not int
            or plan["schema_version"] != 1 or plan["gate_status"] != "unverified"
            or not isinstance(plan["limits"], dict) or set(plan["limits"]) != {"max_requests", "max_dispatches"}
            or not isinstance(plan["cases"], list) or not 1 <= len(plan["cases"]) <= 10000):
        raise ValueError("invalid batch plan contract")
    rebuilt = build_plan(plan["run_id"], plan["cases"], plan["materials"], plan["schedule_seed"],
                         plan["limits"]["max_requests"], plan["limits"]["max_dispatches"])
    if plan != rebuilt:
        raise ValueError("batch plan digest or schedule mismatch")


def write_plan(path, plan):
    validate_plan(plan)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(plan, stream, sort_keys=True, allow_nan=False)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())


def _unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate batch document field")
        result[key] = value
    return result


def _invalid_constant(value):
    raise ValueError("nonfinite batch document value")


def read_plan(path):
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
        with os.fdopen(descriptor, "rb") as stream:
            before = os.fstat(stream.fileno())
            if not stat.S_ISREG(before.st_mode) or before.st_size > 64 * 1024**2:
                raise ValueError("invalid batch document file")
            content = stream.read(64 * 1024**2 + 1)
            after = os.fstat(stream.fileno())
        identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
        if (len(content) != before.st_size or identity(before) != identity(after)
                or identity(after) != identity(os.stat(path, follow_symlinks=False))):
            raise ValueError("batch document changed while reading")
        plan = json.loads(content, object_pairs_hook=_unique_fields, parse_constant=_invalid_constant)
        validate_plan(plan)
        return plan
    except (OSError, KeyError, TypeError):
        raise ValueError("batch document cannot be verified") from None


class BatchBudget:
    """Reserve ceilings before launch; refund only verified actual counts."""

    def __init__(self, plan):
        validate_plan(plan)
        self.plan = deepcopy(plan)
        self.remaining_requests = plan["limits"]["max_requests"]
        self.remaining_dispatches = plan["limits"]["max_dispatches"]
        self.index, self.active, self.stop_reason = 0, None, None
        self.cases = {case["case_id"]: case for case in self.plan["cases"]}

    def admit_next(self):
        if self.active is not None:
            raise ValueError("settle the active reservation before admission")
        if self.stop_reason is not None:
            return None
        if self.index == len(self.plan["schedule"]):
            self.stop_reason = "complete"
            return None
        case = self.cases[self.plan["schedule"][self.index]]
        for name in ("requests", "dispatches"):
            if case["limits"]["max_" + name] > getattr(self, "remaining_" + name):
                self.stop_reason = "request_budget" if name == "requests" else "dispatch_budget"
                return None
        self.remaining_requests -= case["limits"]["max_requests"]
        self.remaining_dispatches -= case["limits"]["max_dispatches"]
        self.active = case
        self.index += 1
        return deepcopy(case)

    def settle(self, case_id, *, requests, dispatches):
        if self.active is None or self.active["case_id"] != case_id:
            raise ValueError("settlement has no matching reservation")
        values = {"requests": requests, "dispatches": dispatches}
        if any(value is not None and (type(value) is not int or not 0 <= value <= self.active["limits"]["max_" + name])
               for name, value in values.items()):
            raise ValueError("settlement exceeded reserved budget")
        for name, value in values.items():
            if value is not None:
                remaining = getattr(self, "remaining_" + name)
                setattr(self, "remaining_" + name, remaining + self.active["limits"]["max_" + name] - value)
        self.active = None


def audit_plan(plan, events):
    validate_plan(plan)
    if not events or events[0].get("phase") != "batch_locked" or events[0].get("plan_sha256") != plan["plan_sha256"]:
        raise ValueError("batch plan has no matching durable pre-task lock")
    report = summarize(events)
    if events and report["run_id"] != plan["run_id"]:
        raise ValueError("journal run differs from predeclared batch")
    declarations = {case["case_id"]: case for case in plan["cases"]}
    started = [case["spec"]["case_id"] for case in report["cases"]]
    if started != plan["schedule"][:len(started)] or any(
            case["spec"] != declarations.get(case["spec"]["case_id"]) for case in report["cases"]):
        raise ValueError("journal differs from predeclared cases or order")
    attempts = sum(case["usage"]["request_count"] for case in report["cases"])
    # With no request record, this journal alone cannot distinguish pre-dispatch
    # failure from missing physical-attempt evidence. Keep recorded zero separate.
    unknown_requests = sum(case["usage"]["request_count"] == 0 for case in report["cases"])
    dispatches = [(case["boundary_counts"] or {}).get("dispatch") for case in report["cases"]]
    known_dispatches = sum(value for value in dispatches if value is not None)
    unknown = sum(value is None for value in dispatches)
    within = attempts <= plan["limits"]["max_requests"] and known_dispatches <= plan["limits"]["max_dispatches"]
    return {"plan_sha256": plan["plan_sha256"], "planned_cases": len(plan["cases"]),
            "started_cases": report["started_cases"], "planned_not_started": len(plan["cases"]) - len(started),
            "unfinished_cases": report["unfinished_cases"], "safe_successes": report["safe_successes"],
            "request_attempts": None if unknown_requests else attempts,
            "journaled_request_attempts": attempts, "request_evidence_unknown_cases": unknown_requests,
            "known_dispatches": known_dispatches,
            "unknown_dispatch_cases": unknown, "total_dispatches": None if unknown else known_dispatches,
            "budget_within_limits": within if not (unknown or unknown_requests) or not within else None,
            "unknown_scope_cases": sum("unknown" in case["spec"]["scope"].values() for case in report["cases"]),
            "gate_status": "unverified"}
