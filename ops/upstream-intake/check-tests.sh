#!/usr/bin/env bash
#
# Test gate that tolerates only recorded upstream test failures.
#
# Failing test names must be reproduced on a pristine checkout of the release
# being replayed and recorded in known-upstream-test-failures.txt. Any other
# failure, and any build or panic failure, still blocks.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 1

known_file="ops/upstream-intake/known-upstream-test-failures.txt"

output="$(go test ./... -count=1 2>&1)"
status=$?
if [ "$status" -eq 0 ]; then
  exit 0
fi

if printf '%s\n' "$output" | grep -qE '^(panic:|# )|build failed'; then
  printf '%s\n' "$output" | grep -E '^(panic:|# )|build failed'
  printf 'build or panic failure; only named upstream test failures are tolerated\n'
  exit 1
fi

failed="$(printf '%s\n' "$output" | sed -n 's/^--- FAIL: \([^ ]*\).*/\1/p' | sort -u)"
if [ -z "$failed" ]; then
  printf '%s\n' "$output" | tail -60
  printf 'no test names in failure output; refusing to treat it as a known defect\n'
  exit 1
fi

allowed="$(mktemp)" || exit 1
trap 'rm -f "$allowed"' EXIT
grep -v '^[[:space:]]*#' "$known_file" 2>/dev/null | sed '/^[[:space:]]*$/d' >"$allowed"

unexpected="$(printf '%s\n' "$failed" | grep -vxF -f "$allowed")"
if [ -n "$unexpected" ]; then
  pattern="$(printf '%s\n' "$unexpected" | sed 's/[][\.*^$(){}?+|/]/\\&/g' | paste -sd'|' -)"
  printf '%s\n' "$output" | awk -v pat="$pattern" '
    $0 ~ "^--- FAIL: (" pat ")( |$)" { printing = 1 }
    printing && /^(FAIL|ok |=== RUN)/ { printing = 0 }
    printing { print }
  ' | head -200
  printf 'failing tests:\n%s\n' "$failed"
  printf 'unexpected failures:\n%s\n' "$unexpected"
  exit 1
fi

printf 'tolerated recorded upstream test failures:\n%s\n' "$failed"
exit 0
