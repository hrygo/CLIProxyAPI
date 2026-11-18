#!/usr/bin/env bash
#
# go vet gate that tolerates only recorded upstream defects.
#
# Upstream releases sometimes ship with their own vet diagnostics. Leaving those
# defects in place is an intake decision, so they are listed in
# known-upstream-defects.txt and every other diagnostic still fails. Module
# download chatter is not a diagnostic and never matches. Keep this script as the
# single source of truth for both the local gate and CI.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 1

known_file="ops/upstream-intake/known-upstream-defects.txt"

output="$(go vet ./... 2>&1)"
status=$?
if [ "$status" -eq 0 ]; then
  exit 0
fi

# A vet diagnostic is "path.go:line:col: message"; everything else in the
# stream is tool progress such as "go: downloading ...".
diagnostics="$(printf '%s\n' "$output" | grep -E '\.go:[0-9]+:[0-9]+: ')"
if [ -z "$diagnostics" ]; then
  printf '%s\n' "$output" | tail -40
  printf 'go vet failed without a diagnostic; refusing to treat it as a known defect\n'
  exit 1
fi

allowed="$(mktemp)" || exit 1
trap 'rm -f "$allowed"' EXIT
grep -v '^[[:space:]]*#' "$known_file" 2>/dev/null | sed '/^[[:space:]]*$/d' >"$allowed"

unexpected="$(printf '%s\n' "$diagnostics" | grep -vxF -f "$allowed")"
if [ -n "$unexpected" ]; then
  printf '%s\n' "$diagnostics"
  printf '%s\n' "--- unexpected vet diagnostics (not in $known_file) ---"
  printf '%s\n' "$unexpected"
  exit 1
fi

printf 'tolerated recorded upstream defects:\n%s\n' "$diagnostics"
exit 0
