#!/usr/bin/env bash
#
# Derive the inherited upstream baseline for a ref.
#
# Ancestry identifies inherited upstream history, not a reviewed tree replay.
# Tree-based integration preserves fork history without making a later upstream
# release an ancestor, so consult the ledger for the accepted baseline.
#
# Usage: ops/upstream-intake/establish-baseline.sh [ref] [--remote]

set -euo pipefail

UPSTREAM_REPO="router-for-me/CLIProxyAPI"
target_ref="main"
check_remote=0

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

for arg in "$@"; do
  case "$arg" in
    --remote) check_remote=1 ;;
    -*) die "unknown option: $arg" ;;
    *) target_ref="$arg" ;;
  esac
done

git rev-parse --git-dir >/dev/null 2>&1 || die "not inside a git repository"
git rev-parse --verify --quiet "$target_ref^{commit}" >/dev/null ||
  die "ref '$target_ref' not found"

target_sha="$(git rev-parse "$target_ref^{commit}")"
printf 'Target ref: %s (%s)\n\n' "$target_ref" "$target_sha"

releases=""
if [ "$check_remote" -eq 1 ]; then
  command -v gh >/dev/null 2>&1 || die "gh is required for --remote"
  while IFS= read -r tag; do
    [ -n "$tag" ] && releases="$releases$tag
"
  done < <(gh api "repos/$UPSTREAM_REPO/releases?per_page=100" \
    --paginate --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name' \
    2>/dev/null || true)
  release_source="$UPSTREAM_REPO (remote)"
else
  while IFS= read -r tag; do
    tag="${tag#upstream/tags/}"
    [ -n "$tag" ] && releases="$releases$tag
"
  done < <(git for-each-ref --format='%(refname:short)' refs/upstream/tags/ 2>/dev/null)
  release_source="refs/upstream/tags/* (local; pass --remote to include unreviewed releases)"
fi

[ -n "$releases" ] ||
  die "no upstream releases available from $release_source; fetch tags into refs/upstream/tags/* first"

printf 'Checking upstream releases from %s, newest first:\n\n' "$release_source"
printf '  %-12s %-10s %s\n' "RELEASE" "IN $target_ref?" "COMMIT"

baseline=""
baseline_sha=""
while IFS= read -r tag; do
  [ -n "$tag" ] || continue
  ref=""
  for candidate in "refs/upstream/tags/$tag" "refs/tags/$tag"; do
    if git rev-parse --verify --quiet "$candidate^{commit}" >/dev/null; then
      ref="$candidate"
      break
    fi
  done
  if [ -z "$ref" ]; then
    printf '  %-12s %-10s %s\n' "$tag" "unknown" "(tag not fetched)"
    continue
  fi

  sha="$(git rev-parse "$ref^{commit}")"
  if git merge-base --is-ancestor "$sha" "$target_sha" 2>/dev/null; then
    printf '  %-12s %-10s %s\n' "$tag" "yes" "${sha:0:8}"
    if [ -z "$baseline" ]; then
      baseline="$tag"
      baseline_sha="$sha"
    fi
  else
    printf '  %-12s %-10s %s\n' "$tag" "no" "${sha:0:8}"
  fi
done <<EOF
$(printf '%s' "$releases" | sort -Vr)
EOF

printf '\n'
if [ -z "$baseline" ]; then
  cat >&2 <<EOF
No upstream release is an ancestor of $target_ref.

Ancestry cannot identify a tree-based replay. Consult the accepted baseline
and evidence in ops/upstream-intake/absorbed.md; do not infer a baseline from tags.
EOF
  exit 1
fi

cat <<EOF
Newest upstream ancestor: $baseline ($baseline_sha)

This identifies inherited history only, not the accepted comparison baseline or
a completed tree replay. Consult ops/upstream-intake/absorbed.md for those facts.
EOF
