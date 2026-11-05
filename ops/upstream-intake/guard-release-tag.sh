#!/usr/bin/env bash
# Verify the reviewed fork target and its accepted upstream baseline.
# All ledger and release-note data is read from the tagged Git tree.
set -euo pipefail

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

valid_release_tag() {
  [[ "$1" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-upstream(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

if [ "${1:-}" = "--ci-relevant" ]; then
  [ "$#" -eq 2 ] || die "usage: guard-release-tag.sh --ci-relevant <base-commit>"
  base="$2"
  [[ "$base" =~ ^[0-9a-f]{40}$ ]] || die "CI base must be a full commit SHA"
  git cat-file -e "$base^{commit}" 2>/dev/null || die "CI base commit is missing"
  git merge-base --is-ancestor "$base" HEAD || die "CI base is not an ancestor of HEAD"
  changed="$(git diff --no-renames --name-only "$base" HEAD)" || die "cannot inspect CI changes"
  command -v grep >/dev/null 2>&1 || die "grep is required to inspect CI changes"
  if printf '%s\n' "$changed" | grep -E '^(\.github/workflows/(release\.yaml|pr-test-build\.yml)|ops/upstream-intake/(guard-release-tag\.sh|accepted-baselines\.json|absorbed\.md|verify-absorb\.sh)|ops/release-notes/validate\.py|ops/tests/(test_release_guard\.py|test_release_workflow\.py|test_maintenance\.py))$' >/dev/null; then
    printf 'true\n'
  else
    result="$?"
    [ "$result" -eq 1 ] || die "cannot classify CI changes"
    printf 'false\n'
  fi
  exit 0
fi

if [ "${1:-}" = "--verify-remote" ]; then
  [ "$#" -eq 3 ] || die "usage: guard-release-tag.sh --verify-remote <tag> <commit>"
  remote_tag="$2"
  expected="$3"
  valid_release_tag "$remote_tag" || die "invalid release tag"
  [[ "$expected" =~ ^[0-9a-f]{40}$ ]] || die "expected a full release commit SHA"
  refs="$(git ls-remote --exit-code --tags origin "refs/tags/$remote_tag" "refs/tags/$remote_tag^{}")" ||
    die "cannot read release tag from origin"
  tag_object=""
  peeled=""
  while IFS=$'\t' read -r sha ref; do
    [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || die "invalid remote tag SHA"
    case "$ref" in
      "refs/tags/$remote_tag") [ -z "$tag_object" ] || die "duplicate remote tag"; tag_object="$sha" ;;
      "refs/tags/$remote_tag^{}") [ -z "$peeled" ] || die "duplicate peeled tag"; peeled="$sha" ;;
      *) die "unexpected remote tag response" ;;
    esac
  done <<< "$refs"
  [ -n "$tag_object" ] || die "remote release tag is missing"
  [ "${peeled:-$tag_object}" = "$expected" ] || die "remote tag moved from the validated commit"
  printf 'Remote tag %s still resolves to %s.\n' "$remote_tag" "$expected"
  exit 0
fi

[ "$#" -eq 1 ] || die "usage: guard-release-tag.sh <tag> | --verify-upstream"
tag="$1"
if [ "$tag" != "--verify-upstream" ]; then
  valid_release_tag "$tag" || die "expected a stable release tag with upstream suffix, got '$tag'"
fi
command -v python3 >/dev/null 2>&1 || die "python3 is required"
command -v git >/dev/null 2>&1 || die "git is required"
command -v gh >/dev/null 2>&1 || die "gh is required"

python3 - "$tag" <<'PY'
import datetime as dt
import json
import re
import subprocess
import sys

TAG = sys.argv[1]
LIVE_CHECK = TAG == "--verify-upstream"
UPSTREAM_REPO = "router-for-me/CLIProxyAPI"
REGISTRY = "ops/upstream-intake/accepted-baselines.json"
LEDGER = "ops/upstream-intake/absorbed.md"
NOTE = f"docs/releases/{TAG}.md"
SHA = re.compile(r"[0-9a-f]{40}")
VERSION = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")
UPSTREAM = None if LIVE_CHECK else "v" + TAG.rsplit("-upstream", 1)[1]


def fail(message):
    print(f"error: {message}", file=sys.stderr)
    sys.exit(1)


def git(*args):
    result = subprocess.run(["git", *args], text=True, capture_output=True)
    if result.returncode:
        fail(f"git {' '.join(args)} failed or required object/ref is missing")
    return result.stdout.rstrip("\n")


def ancestor(older, newer):
    result = subprocess.run(["git", "merge-base", "--is-ancestor", older, newer],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if result.returncode != 0:
        fail(f"required ancestry is missing: {older} -> {newer}")


def tree_file(commit, path):
    entry = git("ls-tree", commit, "--", path)
    if not re.fullmatch(r"100(?:644|755) blob [0-9a-f]{40}\t" + re.escape(path), entry):
        fail(f"{path} must be a regular tracked file at {commit}")
    return git("show", f"{commit}:{path}")


def parse_json(data, label):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"duplicate key {key}")
            result[key] = value
        return result
    try:
        return json.loads(data, object_pairs_hook=unique)
    except (ValueError, TypeError):
        fail(f"invalid or ambiguous JSON in {label}")


def api(path, allow_not_found=False):
    try:
        result = subprocess.run(["gh", "api", "--hostname", "github.com", path],
                                text=True, capture_output=True, timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        fail(f"upstream API unavailable for {path}")
    if result.returncode:
        # An absent same-name ref is not an API, rate-limit, or auth failure.
        # Require both the gh HTTP diagnostic and GitHub's 404 response body.
        if allow_not_found and re.search(r"\(HTTP 404\)(?:\s|$)", result.stderr):
            body = parse_json(result.stdout, path)
            if (isinstance(body, dict) and body.get("message") == "Not Found"
                    and body.get("status") in ("404", 404)):
                return None
        fail(f"upstream API request failed for {path}")
    return parse_json(result.stdout, path)


def peel(obj, label):
    seen = set()
    for _ in range(16):
        if not isinstance(obj, dict):
            fail(f"invalid upstream {label} reference")
        kind, sha = obj.get("type"), obj.get("sha")
        if not isinstance(sha, str) or not SHA.fullmatch(sha):
            fail(f"invalid upstream {label} object SHA")
        if kind == "commit":
            return sha
        if kind != "tag" or sha in seen:
            fail(f"invalid or cyclic upstream {label} tag object")
        seen.add(sha)
        response = api(f"repos/{UPSTREAM_REPO}/git/tags/{sha}")
        obj = response.get("object") if isinstance(response, dict) else None
    fail(f"upstream {label} tag chain exceeds 16 objects")


def verify_upstream(tag, expected_sha):
    base = f"repos/{UPSTREAM_REPO}"
    release_info = api(f"{base}/releases/tags/{tag}")
    if (not isinstance(release_info, dict) or release_info.get("tag_name") != tag or
            release_info.get("draft") is not False or release_info.get("prerelease") is not False):
        fail("upstream suffix does not name an official stable published release")
    published = release_info.get("published_at")
    try:
        if not isinstance(published, str) or not re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", published):
            raise ValueError("invalid published_at")
        date = dt.datetime.strptime(published, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=dt.timezone.utc)
    except ValueError:
        fail("upstream release must have a valid published time")
    if dt.datetime.now(dt.timezone.utc) - date < dt.timedelta(hours=72):
        fail("upstream release must be published for at least 72 hours")
    ref = api(f"{base}/git/ref/tags/{tag}")
    resolved = peel(ref.get("object") if isinstance(ref, dict) else None, tag)
    if resolved != expected_sha:
        fail("official upstream tag does not resolve to the accepted SHA")


def required_sha(record, key):
    value = record.get(key)
    if not isinstance(value, str) or not SHA.fullmatch(value):
        fail(f"accepted baseline requires a full SHA for {key}")
    return value


def commit(sha):
    if git("rev-parse", "--verify", f"{sha}^{{commit}}") != sha:
        fail(f"not a commit: {sha}")


def parents(sha):
    line = git("rev-list", "--parents", "-n", "1", sha).split()
    return line[1:]


def ledger_sections(text, record, required=True):
    comparison = re.findall(r"(?ms)^## Comparison baseline[ \t]*\n(.*?)(?=^## |\Z)", text)
    heading = record["assessment_heading"]
    assessment = re.findall(r"(?ms)^### " + re.escape(heading) +
                            r"[ \t]*\n(.*?)(?=^#{2,3} |\Z)", text)
    if len(comparison) != 1 or len(assessment) != 1:
        if required:
            fail("ledger must contain unique comparison and accepted assessment sections")
        return None
    return comparison[0].rstrip(), assessment[0].rstrip()


def check_ledger(sections, record):
    comparison, assessment = sections
    pattern = (r"(?m)^\*\*Accepted comparison baseline: `([^`]+)`\*\*\s*"
               r"\(`([0-9a-f]{40})`\)")
    accepted = re.findall(pattern, comparison)
    if accepted != [(record["upstream"], record["upstream_sha"])]:
        fail("ledger must identify exactly the accepted upstream tag and SHA")
    previous = re.findall(r"(?m)^Previous comparison baseline:[ \t]*`([^`\n]+)`[ \t]*$",
                          comparison)
    if previous != [record["previous_baseline"]]:
        fail("ledger previous comparison baseline does not match the registry")
    if record["kind"] == "selective":
        # This is the existing v8.0.35 historical record, whose wording predates
        # the structured replay format. Its PR #32 context identifies the intake.
        historical = re.findall(
            r"(?m)^\(`([0-9a-f]{40})`\)\. The initial 25 selective ports[ \t]*\n"
            r"reached main through PR #32 \(`([0-9a-f]{40})`\)", comparison)
        if (record["upstream"] != "v8.0.35" or
                historical != [(record["upstream_sha"], record["intake_commit"])]):
            fail("historical selective intake does not match the accepted PR #32 evidence")
        if f"`{record['upstream_sha']}`" not in assessment:
            fail("historical assessment lacks its upstream commit")
        return

    fields = {
        "Previous intake baseline": record["previous_baseline"],
        "Upstream commit (U)": record["upstream_sha"],
        "Main before replay (M)": record["main_before"],
        "Candidate commit (C)": record["candidate_commit"],
        "Integration commit (I)": record["integration_commit"],
        "Intake commit (J)": record["intake_commit"],
        "Candidate tree": record["candidate_tree"],
    }
    for label, expected in fields.items():
        values = re.findall(r"(?m)^- " + re.escape(label) + r":[ \t]*`([^`\n]+)`[ \t]*$",
                            assessment)
        if values != [expected]:
            fail(f"confirmed replay ledger field {label} does not match the registry")


def check_note(text, record):
    sections = re.findall(r"(?ms)^## Upstream alignment\s*\n(.*?)(?=^## |\Z)", text)
    if len(sections) != 1:
        fail("release note must have one upstream alignment section")
    values = {}
    for field in ("Upstream release", "Previous baseline", "Intake record", "Ported", "Skipped"):
        found = re.findall(rf"(?m)^- {re.escape(field)}:[ \t]*(\S[^\n]*)$", sections[0])
        if len(found) != 1:
            fail(f"release note requires one {field} field")
        values[field] = found[0].strip()
    if values["Upstream release"] != f"`{UPSTREAM}`":
        fail("release note and accepted upstream baseline disagree")
    previous = values["Previous baseline"]
    if previous != "none" and not re.fullmatch(r"`" + VERSION.pattern + r"`", previous):
        fail("release note previous baseline must be a stable tag or none")
    if "ops/upstream-intake/absorbed.md" not in values["Intake record"]:
        fail("release note must link to the accepted ledger")


if LIVE_CHECK:
    release = git("rev-parse", "--verify", "HEAD^{commit}")
else:
    release = git("rev-parse", "--verify", f"refs/tags/{TAG}^{{commit}}")
    if git("rev-parse", "--verify", "HEAD^{commit}") != release:
        fail("the checked out commit must be the release target")
    main = git("rev-parse", "--verify", "refs/remotes/origin/main^{commit}")
    main_chain = set(git("rev-list", "--first-parent", main).splitlines())
    if release not in main_chain:
        fail("release target is not on the origin/main first-parent chain")
    release_chain = set(git("rev-list", "--first-parent", release).splitlines())

registry = parse_json(tree_file(release, REGISTRY), REGISTRY)
if not isinstance(registry, dict) or set(registry) != {"schema_version", "accepted"}:
    fail("registry must contain one accepted baseline")
if type(registry["schema_version"]) is not int or registry["schema_version"] != 1:
    fail("unsupported accepted-baseline schema")
record = registry["accepted"]
common = {"kind", "upstream", "upstream_sha", "previous_baseline", "intake_commit",
          "ledger_commit", "assessment_heading"}
replay_fields = {"main_before", "candidate_commit", "integration_commit", "candidate_tree"}
if not isinstance(record, dict) or record.get("kind") not in ("selective", "replay"):
    fail("accepted baseline must declare selective or replay evidence")
expected = common | (replay_fields if record["kind"] == "replay" else set())
if set(record) != expected:
    fail("accepted baseline has missing or unexpected fields")
if LIVE_CHECK:
    UPSTREAM = record["upstream"]
    if not isinstance(UPSTREAM, str) or not VERSION.fullmatch(UPSTREAM):
        fail("accepted baseline requires a stable upstream tag")
elif record["upstream"] != UPSTREAM:
    fail("tag suffix is not the unique accepted upstream baseline")
if (not isinstance(record["assessment_heading"], str) or
        not record["assessment_heading"].startswith(UPSTREAM + " ") or
        "\n" in record["assessment_heading"] or "\r" in record["assessment_heading"]):
    fail("accepted baseline requires an assessment heading for its upstream release")
if (not isinstance(record["previous_baseline"], str) or
        not VERSION.fullmatch(record["previous_baseline"]) or
        record["previous_baseline"] == UPSTREAM):
    fail("accepted baseline requires a distinct, stable previous baseline")
u = required_sha(record, "upstream_sha")
intake = required_sha(record, "intake_commit")
ledger = required_sha(record, "ledger_commit")
if LIVE_CHECK:
    verify_upstream(UPSTREAM, u)
    print(f"Official upstream {UPSTREAM} resolves to accepted SHA {u}.")
    sys.exit(0)
for sha in (intake, ledger):
    commit(sha)
if release == u or intake == u:
    fail("an upstream release commit cannot be published as a fork release")
if intake not in release_chain:
    fail("accepted intake is not on the release target's first-parent chain")
if ledger == intake:
    fail("accepted ledger confirmation must follow the intake")
ancestor(intake, ledger)
ancestor(ledger, release)
if record["kind"] == "selective" and ledger not in release_chain:
    fail("historical selective ledger confirmation must be on the main first-parent chain")

ledger_text = tree_file(ledger, LEDGER)
confirmed_sections = ledger_sections(ledger_text, record)
if ledger_sections(tree_file(release, LEDGER), record) != confirmed_sections:
    fail("release baseline or assessment differs from the confirmed ledger commit")
check_ledger(confirmed_sections, record)
# The confirmation must actually record the evidence, not merely inherit it.
ledger_parents = parents(ledger)
if not ledger_parents:
    fail("ledger confirmation must follow an existing commit")
previous_entry = git("ls-tree", ledger_parents[0], "--", LEDGER)
current_entry = git("ls-tree", ledger, "--", LEDGER)
if previous_entry == current_entry:
    fail("ledger confirmation did not change the intake record")
if previous_entry:
    prior_ledger = git("show", f"{ledger_parents[0]}:{LEDGER}")
    if ledger_sections(prior_ledger, record, required=False) == confirmed_sections:
        fail("ledger confirmation did not establish the accepted evidence sections")
check_note(tree_file(release, NOTE), record)

if record["kind"] == "replay":
    m = required_sha(record, "main_before")
    c = required_sha(record, "candidate_commit")
    i = required_sha(record, "integration_commit")
    expected_tree = required_sha(record, "candidate_tree")
    for sha in (u, m, c, i):
        commit(sha)
    if parents(i) != [m, c] or parents(intake) != [m, i]:
        fail("replay integration or main merge has incorrect parents")
    for sha in (c, i, intake):
        if git("rev-parse", f"{sha}^{{tree}}") != expected_tree:
            fail("replay candidate, integration, and main merge trees must match")
    ancestor(u, c)
    ancestor(c, intake)

base = f"repos/{UPSTREAM_REPO}"
verify_upstream(UPSTREAM, u)
same_name = api(f"{base}/git/ref/tags/{TAG}", allow_not_found=True)
if same_name is not None:
    upstream_target = peel(same_name.get("object") if isinstance(same_name, dict) else None, TAG)
    if upstream_target == release:
        fail("the release tag points to upstream's same-name tag target")

print(f"Accepted {TAG}: {release} on origin/main first-parent; "
      f"{record['kind']} baseline {UPSTREAM} resolves to {u}.")
print("The accepted registry is reviewed evidence; Git checks do not prove human review or test coverage.")
PY
