#!/usr/bin/env bash
#
# Fetch an upstream release tag locally and summarize commits since the
# accepted comparison baseline. Read-only with respect to the working tree and
# to `origin`; the only ref it writes is refs/upstream/tags/*.
#
# Usage: ops/upstream-intake/assess-release.sh v8.0.6 previous-tag
#
# Selection policy: the candidate must be the newest stable release when the
# assessment starts. There is no age threshold, because elapsed time does not
# repair a release and a fixed wait only replays an older tree against a larger
# fork delta. Regression risk is covered by the complete-tree replay gate and by
# the post-install canary paired with a prepared versioned rollback formula.

set -euo pipefail

UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"
UPSTREAM_REPO="${UPSTREAM_REPO:-router-for-me/CLIProxyAPI}"
TAG_PREFIX="refs/upstream/tags"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
Usage: assess-release.sh <new-tag> <previous-tag>

  <new-tag>       upstream release tag to assess, e.g. v8.0.6
  <previous-tag>  accepted comparison baseline recorded in absorbed.md;
                  fetched tags are not evidence of an accepted baseline
EOF
  exit 2
}

[ "$#" -eq 2 ] || usage

new_tag="$1"
for tag in "$@"; do
  [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    die "expected a stable vX.Y.Z release tag, got '$tag'"
done
git rev-parse --git-dir >/dev/null 2>&1 || die "not inside a git repository"
git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1 ||
  die "remote '$UPSTREAM_REMOTE' not found; it must point at router-for-me/CLIProxyAPI"

# Selection gate. The candidate must be the newest stable release upstream has
# published, so a tree upstream already replaced is never replayed. Everything
# else about the artifact (tag shape, ancestry, tree) is verified downstream.
require_newest_release() {
  command -v gh >/dev/null 2>&1 ||
    die "gh is required to read upstream release metadata"

  published_at="$(gh release view "$1" --repo "$UPSTREAM_REPO" \
    --json publishedAt,isDraft,isPrerelease \
    --jq 'if .isDraft or .isPrerelease then empty else .publishedAt end' 2>/dev/null)" ||
    die "upstream release '$1' not found in $UPSTREAM_REPO"

  [ -n "$published_at" ] && [ "$published_at" != "null" ] ||
    die "upstream release '$1' must be published, stable, and have a publish date"

  now="$(date -u +%s)"
  published="$(date -u -d "$published_at" +%s 2>/dev/null)" ||
    published="$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$published_at" +%s)" ||
    die "could not parse upstream publish date '$published_at'"
  [ "$published" -le "$now" ] ||
    die "upstream release '$1' has a publish date in the future"

  latest="$(gh api "repos/$UPSTREAM_REPO/releases/latest" --jq .tag_name 2>/dev/null)" ||
    die "could not read the newest upstream release from $UPSTREAM_REPO"
  [ -n "$latest" ] && [ "$latest" != "null" ] ||
    die "upstream $UPSTREAM_REPO has no published stable release"

  if [ "$latest" != "$1" ]; then
    printf 'error: upstream %s is not the newest stable release; %s is\n' \
      "$1" "$latest" >&2
    printf 'Assess %s instead: replaying a superseded tree wastes the fork delta review.\n' \
      "$latest" >&2
    exit 3
  fi

  printf 'Upstream %s is the newest stable release, published %s\n' "$1" "$published_at"
}

require_newest_release "$new_tag"

printf 'Fetching %s into %s/%s (local only)\n' "$new_tag" "$TAG_PREFIX" "$new_tag"
git -c fetch.pruneTags=false fetch --quiet --no-tags "$UPSTREAM_REMOTE" \
  "refs/tags/$new_tag:$TAG_PREFIX/$new_tag"

new_ref="$TAG_PREFIX/$new_tag"

prev_ref="$TAG_PREFIX/$2"
if ! git rev-parse --verify --quiet "$prev_ref" >/dev/null; then
  printf 'Fetching previous tag %s\n' "$2"
  git -c fetch.pruneTags=false fetch --quiet --no-tags "$UPSTREAM_REMOTE" "refs/tags/$2:$prev_ref" ||
    die "previous tag '$2' does not exist upstream"
fi
git merge-base --is-ancestor "$prev_ref" "$new_ref" ||
  die "previous release is not an ancestor of the candidate; review the baseline"
[ "$(git rev-parse "$prev_ref^{commit}")" != "$(git rev-parse "$new_ref^{commit}")" ] ||
  die "candidate and baseline point to the same commit"

printf '\n== %s..%s ==\n' "${prev_ref##*/}" "${new_tag}"
git log --oneline --no-decorate "$prev_ref..$new_ref"

count="$(git rev-list --count "$prev_ref..$new_ref")"
printf '\n%d commit(s) between the two releases.\n' "$count"

printf '\n== Commit subjects, grouped as review aids ==\n'
git log --format='%H' "$prev_ref..$new_ref" | while read -r sha; do
  subject="$(git log -1 --format='%s' "$sha")"
  case "$subject" in
    fix*|Fix*|hotfix*|HOTFIX*|bugfix*|BUGFIX*)
      printf '  fix-like   %s  %s\n' "${sha:0:8}" "$subject"
      ;;
    *)
      printf '  other      %s  %s\n' "${sha:0:8}" "$subject"
      ;;
  esac
done

printf '\n== Files touched ==\n'
git diff --stat "$prev_ref..$new_ref"

cat <<EOF

Assessment only: build the replay candidate from the complete release tree,
review fork deltas and equivalence, and follow ops/upstream-intake/README.md.
Do not record a new accepted baseline until the verified replay reaches main.
EOF
