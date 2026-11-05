#!/usr/bin/env python3
"""Exercise the real release guard against isolated Git DAGs and a fake API."""

import datetime as dt
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
GUARD = ROOT / "ops/upstream-intake/guard-release-tag.sh"
REGISTRY = Path("ops/upstream-intake/accepted-baselines.json")
LEDGER = Path("ops/upstream-intake/absorbed.md")
TAG = "v1.2.3-upstream8.0.35"
UPSTREAM = "v8.0.35"
API = "repos/router-for-me/CLIProxyAPI"


class GuardTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.repo = self.home / "repo"
        self.repo.mkdir()
        self.bin = self.home / "bin"
        self.bin.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.write("content", "base\n")
        self.commit("base")
        self.m = self.sha("HEAD")
        self.git("switch", "-qc", "upstream")
        self.write("content", "upstream\n")
        self.commit("upstream release")
        self.u = self.sha("HEAD")
        self.write("fork-change", "replayed fork capability\n")
        self.commit("replay fork capability")
        self.c = self.sha("HEAD")
        self.tree = self.sha("HEAD^{tree}")
        self.git("switch", "-q", "main")
        self.gh_data = self.home / "api.json"
        fake_gh = self.bin / "gh"
        fake_gh.write_text('''#!/usr/bin/env python3
import json, os, sys
records = json.load(open(os.environ["GUARD_FAKE_API"]))
endpoint = sys.argv[-1]
response = records.get(endpoint)
if response is None:
    print("unhandled fixture API endpoint: " + endpoint, file=sys.stderr)
    sys.exit(2)
status = response.get("status", 200)
body = response.get("body", {})
sys.stdout.write(body if isinstance(body, str) else json.dumps(body))
if status != 200:
    message = "Not Found" if status == 404 else "API failure"
    print(f"gh: {message} (HTTP {status})", file=sys.stderr)
    sys.exit(1)
''')
        fake_gh.chmod(0o755)
        self.api = {
            f"{API}/releases/tags/{UPSTREAM}": {
                "body": {"tag_name": UPSTREAM, "draft": False,
                         "prerelease": False, "published_at": "2025-01-01T00:00:00Z"}},
            f"{API}/git/ref/tags/{UPSTREAM}": {
                "body": {"object": {"type": "commit", "sha": self.u}}},
            f"{API}/git/ref/tags/{TAG}": {
                "status": 404, "body": {"message": "Not Found", "status": "404"}},
        }

    def git(self, *args, check=True, repo=None):
        return subprocess.run(
            ["git", *args], cwd=repo or self.repo, check=check,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )

    def sha(self, rev):
        return self.git("rev-parse", rev).stdout.strip()

    def write(self, path, text):
        path = self.repo / path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-qm", message)

    def commit_tree(self, tree, parents, message):
        args = ["commit-tree", tree]
        for parent in parents:
            args += ["-p", parent]
        return self.git(*args, "-m", message).stdout.strip()

    def prepare(self, kind="selective", replay_fault=None):
        if kind == "selective":
            self.write("fork-change", "selected upstream repairs\n")
            self.commit("selective intake")
            intake = self.sha("HEAD")
            extra = {}
        else:
            candidate = self.c
            if replay_fault == "upstream_ancestry":
                candidate = self.commit_tree(self.tree, [self.m], "candidate without upstream")
            integration_tree = self.sha(f"{self.u}^{{tree}}") if replay_fault == "integration_tree" else self.tree
            integration_parents = ([self.m, self.m] if replay_fault == "integration_parents"
                                   else [self.m, candidate])
            integration = self.commit_tree(integration_tree, integration_parents, "integration")
            intake_parents = ([self.m, self.m] if replay_fault == "main_parents"
                              else [self.m, integration])
            intake = self.commit_tree(self.tree, intake_parents, "protected PR merge")
            self.git("switch", "-q", "-C", "main", intake)
            extra = {"main_before": self.m, "candidate_commit": candidate,
                     "integration_commit": integration, "candidate_tree": self.tree}
        evidence = ""
        if kind == "replay":
            evidence = "\n".join([
                "- Previous intake baseline: `v8.0.23`",
                f"- Upstream commit (U): `{self.u}`",
                f"- Main before replay (M): `{self.m}`",
                f"- Candidate commit (C): `{extra['candidate_commit']}`",
                f"- Integration commit (I): `{extra['integration_commit']}`",
                f"- Intake commit (J): `{intake}`",
                f"- Candidate tree: `{self.tree}`",
            ])
            if replay_fault == "swap_ledger_labels":
                evidence = evidence.replace(f"- Main before replay (M): `{self.m}`",
                                            f"- Main before replay (M): `{extra['candidate_commit']}`")
                evidence = evidence.replace(f"- Candidate commit (C): `{extra['candidate_commit']}`",
                                            f"- Candidate commit (C): `{self.m}`")
            elif replay_fault == "duplicate_ledger_label":
                evidence += f"\n- Candidate commit (C): `{extra['candidate_commit']}`"
        self.write(LEDGER, f"""# Absorbed upstream releases

## Comparison baseline

**Accepted comparison baseline: `{UPSTREAM}`**
(`{self.u}`). The initial 25 selective ports
reached main through PR #32 (`{intake}`).
Previous comparison baseline: `v8.0.23`
Earlier baseline: `v8.0.5`.
Inherited-history evidence checked main at `{self.m}`.

## Assessments

### {UPSTREAM} — accepted intake

Upstream `{self.u}`, intake `{intake}`.
{evidence}
""")
        self.commit("confirm accepted baseline in ledger")
        ledger = self.sha("HEAD")
        self.registry = {"schema_version": 1, "accepted": {
            "kind": kind, "upstream": UPSTREAM, "upstream_sha": self.u,
            "previous_baseline": "v8.0.23", "intake_commit": intake,
            "ledger_commit": ledger,
            "assessment_heading": f"{UPSTREAM} — accepted intake", **extra}}
        self.note = f"""# {TAG} - 2025-01-02
+
+## Upstream alignment
+
+- Upstream release: `{UPSTREAM}`
+- Previous baseline: `{UPSTREAM}`
+- Intake record: [absorbed.md](../../ops/upstream-intake/absorbed.md)
+- Ported: None. No new upstream work was taken.
+- Skipped: The prior exclusions remain in force.
+""".replace("\n+", "\n")
        self.update_release("prepare release")
        return self

    def update_release(self, message):
        self.write(REGISTRY, json.dumps(self.registry, indent=2) + "\n")
        self.write(f"docs/releases/{TAG}.md", self.note)
        self.commit(message)
        self.target = self.sha("HEAD")
        self.git("update-ref", f"refs/tags/{TAG}", self.target)
        self.git("update-ref", "refs/remotes/origin/main", self.target)

    def run_guard(self, repo=None):
        self.gh_data.write_text(json.dumps(self.api))
        env = os.environ.copy()
        env["PATH"] = f"{self.bin}{os.pathsep}{env['PATH']}"
        env["GUARD_FAKE_API"] = str(self.gh_data)
        return subprocess.run(
            [str(GUARD), TAG], cwd=repo or self.repo, env=env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )

    def run_upstream_check(self):
        self.gh_data.write_text(json.dumps(self.api))
        env = os.environ.copy()
        env["PATH"] = f"{self.bin}{os.pathsep}{env['PATH']}"
        env["GUARD_FAKE_API"] = str(self.gh_data)
        return subprocess.run([str(GUARD), "--verify-upstream"], cwd=self.repo,
                              env=env, text=True, capture_output=True)

    def assert_passes(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def assert_blocked(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_valid_selective_baseline(self):
        self.prepare()
        self.assert_passes(self.run_guard())

    def test_valid_replay_checks_real_merge_dag(self):
        self.prepare("replay")
        self.assert_passes(self.run_guard())

    def test_replay_ledger_can_be_merged_as_a_reviewed_side_branch(self):
        self.prepare("replay")
        j = self.registry["accepted"]["intake_commit"]
        merge = self.commit_tree(self.sha("HEAD^{tree}"), [j, self.target],
                                 "merge ledger and registry PR")
        self.git("switch", "-q", "-C", "main", merge)
        self.git("update-ref", f"refs/tags/{TAG}", merge)
        self.git("update-ref", "refs/remotes/origin/main", merge)
        self.assert_passes(self.run_guard())

    def test_upstream_api_failure_blocks_release(self):
        self.prepare()
        self.api[f"{API}/releases/tags/{UPSTREAM}"] = {"status": 502}
        self.assert_blocked(self.run_guard())

    def test_bare_or_malformed_release_tags_are_rejected_by_shape(self):
        for tag in ("v1.2.3", "v1.2.3-upstreamv8.0.35", "v1.02.3-upstream8.0.35",
                    "v1.2.3-upstream8.0", "v1.2.3-upstream8.0.35-extra"):
            with self.subTest(tag=tag):
                result = subprocess.run([str(GUARD), tag], cwd=self.repo,
                                        text=True, capture_output=True)
                self.assert_blocked(result)
                self.assertIn("stable release tag with upstream suffix", result.stderr)

    def test_api_errors_and_missing_upstream_release_fail_closed(self):
        self.prepare()
        endpoint = f"{API}/releases/tags/{UPSTREAM}"
        for status in (401, 403, 404, 429, 500, 502):
            with self.subTest(status=status):
                self.api[endpoint] = {"status": status}
                self.assert_blocked(self.run_guard())

    def test_invalid_upstream_release_metadata_is_rejected(self):
        self.prepare()
        endpoint = f"{API}/releases/tags/{UPSTREAM}"
        valid = self.api[endpoint]["body"].copy()
        for changes in ({"draft": True}, {"prerelease": True},
                        {"tag_name": "v8.0.34"}, {"published_at": None},
                        {"published_at": "not-a-date"}, {"draft": None}):
            with self.subTest(changes=changes):
                self.api[endpoint] = {"body": {**valid, **changes}}
                self.assert_blocked(self.run_guard())
        self.api[endpoint] = {"body": "{invalid"}
        self.assert_blocked(self.run_guard())

    def test_upstream_must_be_published_for_at_least_72_hours(self):
        self.prepare()
        info = self.api[f"{API}/releases/tags/{UPSTREAM}"]["body"]
        for age_hours, should_pass in ((71, False), (73, True)):
            with self.subTest(age_hours=age_hours):
                published = dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=age_hours)
                info["published_at"] = published.strftime("%Y-%m-%dT%H:%M:%SZ")
                result = self.run_guard()
                api_result = self.run_upstream_check()
                if should_pass:
                    self.assert_passes(result)
                    self.assert_passes(api_result)
                else:
                    self.assert_blocked(result)
                    self.assertIn("72 hours", result.stderr)
                    self.assert_blocked(api_result)
                    self.assertIn("72 hours", api_result.stderr)

    def test_upstream_tag_must_resolve_to_the_accepted_sha(self):
        self.prepare()
        endpoint = f"{API}/git/ref/tags/{UPSTREAM}"
        for value in ({"status": 404}, {"status": 403}, {"body": {}},
                      {"body": {"object": {"type": "tree", "sha": self.u}}},
                      {"body": {"object": {"type": "commit", "sha": self.m}}}):
            with self.subTest(value=value):
                self.api[endpoint] = value
                self.assert_blocked(self.run_guard())

    def test_annotated_upstream_tag_is_peeled_and_cycles_rejected(self):
        self.prepare()
        tag_object = "a" * 40
        endpoint = f"{API}/git/ref/tags/{UPSTREAM}"
        self.api[endpoint] = {"body": {"object": {"type": "tag", "sha": tag_object}}}
        self.api[f"{API}/git/tags/{tag_object}"] = {
            "body": {"object": {"type": "commit", "sha": self.u}}}
        self.assert_passes(self.run_guard())
        self.api[f"{API}/git/tags/{tag_object}"] = {
            "body": {"object": {"type": "tag", "sha": tag_object}}}
        self.assert_blocked(self.run_guard())

    def test_same_name_lookup_requires_a_verified_404(self):
        self.prepare()
        endpoint = f"{API}/git/ref/tags/{TAG}"
        for status in (401, 403, 429, 500, 502):
            with self.subTest(status=status):
                self.api[endpoint] = {"status": status}
                self.assert_blocked(self.run_guard())
        self.api[endpoint] = {"status": 404, "body": {"message": "masked error"}}
        self.assert_blocked(self.run_guard())

    def test_same_name_upstream_target_is_rejected(self):
        self.prepare()
        self.api[f"{API}/git/ref/tags/{TAG}"] = {
            "body": {"object": {"type": "commit", "sha": self.target}}}
        self.assert_blocked(self.run_guard())

    def test_missing_main_and_off_first_parent_release_are_rejected(self):
        self.prepare()
        self.git("update-ref", "-d", "refs/remotes/origin/main")
        self.assert_blocked(self.run_guard())
        side = self.commit_tree(self.sha("HEAD^{tree}"), [self.target], "side commit")
        main = self.commit_tree(self.sha("HEAD^{tree}"), [self.target, side], "merge side")
        self.git("update-ref", "refs/remotes/origin/main", main)
        self.git("update-ref", f"refs/tags/{TAG}", side)
        self.git("switch", "-q", "--detach", side)
        self.assertEqual(self.sha("HEAD"), side)
        self.git("merge-base", "--is-ancestor", side, main)
        result = self.run_guard()
        self.assert_blocked(result)
        self.assertIn("first-parent chain", result.stderr)

    def test_registry_is_read_from_the_tagged_commit(self):
        self.prepare()
        self.write(REGISTRY, "not JSON\n")
        self.write(LEDGER, "not the committed ledger\n")
        self.assert_passes(self.run_guard())

    def test_selective_does_not_require_local_upstream_object(self):
        self.prepare()
        clean = self.home / "clean"
        clean.mkdir()
        self.git("init", "-q", "-b", "main", repo=clean)
        self.git("fetch", "-q", "--no-tags", str(self.repo),
                 f"{self.target}:refs/remotes/origin/main", repo=clean)
        self.git("update-ref", "refs/heads/main", self.target, repo=clean)
        self.git("update-ref", f"refs/tags/{TAG}", self.target, repo=clean)
        self.git("update-ref", "refs/remotes/origin/main", self.target, repo=clean)
        self.assertNotEqual(self.git("cat-file", "-e", f"{self.u}^{{commit}}",
                                     check=False, repo=clean).returncode, 0)
        self.assert_passes(self.run_guard(repo=clean))

    def test_registry_mismatch_or_missing_required_data_blocks(self):
        self.prepare()
        original = self.registry["accepted"].copy()
        for changes in ({"upstream": "v8.0.34"}, {"upstream_sha": self.m},
                        {"ledger_commit": self.m}, {"intake_commit": self.m},
                        {"kind": "candidate"}, {"upstream_sha": None}):
            with self.subTest(changes=changes):
                self.registry["accepted"] = {**original, **changes}
                self.update_release("bad registry")
                self.assert_blocked(self.run_guard())

    def test_earlier_history_cannot_impersonate_selective_acceptance(self):
        self.prepare()
        original = self.registry["accepted"].copy()
        for changes in ({"previous_baseline": "v8.0.5"}, {"intake_commit": self.m},
                        {"previous_baseline": "v8.0.5", "intake_commit": self.m}):
            with self.subTest(changes=changes):
                self.registry["accepted"] = {**original, **changes}
                self.update_release("incorrect historical acceptance")
                self.assert_blocked(self.run_guard())

    def test_replay_ledger_fields_cannot_swap_commit_identities(self):
        self.prepare("replay", replay_fault="swap_ledger_labels")
        self.assert_blocked(self.run_guard())

    def test_replay_ledger_fields_must_be_unique(self):
        self.prepare("replay", replay_fault="duplicate_ledger_label")
        self.assert_blocked(self.run_guard())

    def test_actual_historical_ledger_rejects_earlier_baseline_and_main(self):
        read_text = Path.read_text

        def reject_active_registry(path, *args, **kwargs):
            if path == ROOT / REGISTRY:
                raise AssertionError("historical fixture must not read the active registry")
            return read_text(path, *args, **kwargs)

        registry_read_guard = mock.patch.object(Path, "read_text", reject_active_registry)
        registry_read_guard.start()
        self.addCleanup(registry_read_guard.stop)
        base = "415b85360ab9c883efe5598e5b94cf08a7f214b3"
        available = subprocess.run(["git", "cat-file", "-e", f"{base}^{{commit}}"],
                                   cwd=ROOT, capture_output=True)
        if available.returncode:
            self.skipTest("actual fork history is unavailable in this checkout")
        historical = self.home / "historical"
        historical.mkdir()
        self.git("init", "-q", "-b", "main", repo=historical)
        self.git("config", "user.name", "Fixture", repo=historical)
        self.git("config", "user.email", "fixture@example.invalid", repo=historical)
        self.git("fetch", "-q", "--no-tags", str(ROOT),
                 f"{base}:refs/remotes/origin/main", repo=historical)
        # Preserve the actual historical decision independently of whichever
        # baseline the live registry accepts in a future replay.
        registry = {"schema_version": 1, "accepted": {
            "kind": "selective",
            "upstream": "v8.0.35",
            "upstream_sha": "ae3ee3be303fb07fb8173279923413005bf32177",
            "previous_baseline": "v8.0.23",
            "intake_commit": "6efd7088ef740214f018d993c46f5f335d32fac7",
            "ledger_commit": "052d8d8dfe8899dbd980c9babd8b9065e3fa19f0",
            "assessment_heading": "v8.0.35 — conservative selective absorption, 2026-10-23",
        }}
        self.api[f"{API}/git/ref/tags/{UPSTREAM}"] = {
            "body": {"object": {"type": "commit", "sha": registry["accepted"]["upstream_sha"]}}}
        note = self.git("show", f"{base}:docs/releases/v1.3.5-upstream8.0.35.md",
                        repo=historical).stdout.replace("v1.3.5-upstream8.0.35", TAG, 1)
        original = registry["accepted"].copy()
        cases = ({}, {"previous_baseline": "v8.0.5"},
                 {"intake_commit": "f667aaf566ecabd5e419d11bd50a79a9b6d1637d"},
                 {"previous_baseline": "v8.0.5",
                  "intake_commit": "f667aaf566ecabd5e419d11bd50a79a9b6d1637d"})
        for changes in cases:
            with self.subTest(changes=changes):
                registry["accepted"] = {**original, **changes}
                self.git("read-tree", base, repo=historical)
                for path, content in ((str(REGISTRY), json.dumps(registry)),
                                      (f"docs/releases/{TAG}.md", note)):
                    blob = subprocess.run(["git", "hash-object", "-w", "--stdin"],
                                          cwd=historical, input=content, text=True,
                                          check=True, capture_output=True).stdout.strip()
                    self.git("update-index", "--add", "--cacheinfo", f"100644,{blob},{path}",
                             repo=historical)
                tree = self.git("write-tree", repo=historical).stdout.strip()
                target = self.git("commit-tree", tree, "-p", base, "-m", "isolated fixture",
                                  repo=historical).stdout.strip()
                for ref in ("refs/heads/main", "refs/remotes/origin/main", f"refs/tags/{TAG}"):
                    self.git("update-ref", ref, target, repo=historical)
                result = self.run_guard(repo=historical)
                if changes:
                    self.assert_blocked(result)
                else:
                    self.assert_passes(result)

    def test_ledger_and_note_must_agree_with_accepted_record(self):
        self.prepare()
        good_note = self.note
        for note in (good_note.replace(f"`{UPSTREAM}`", "`v8.0.34`", 1),
                     good_note.replace("Previous baseline: `v8.0.35`", "Previous baseline: malformed"),
                     good_note.replace("Ported: None. No new upstream work was taken.", "Ported:"),
                     good_note.replace("Ported: None", "Ported: None\n- Ported: None")):
            with self.subTest(note=note):
                self.note = note
                self.update_release("bad note")
                self.assert_blocked(self.run_guard())
        self.note = good_note
        self.write(LEDGER, "# Accepted without any corroborating evidence\n")
        self.update_release("bad ledger")
        self.assert_blocked(self.run_guard())

    def test_prior_release_baseline_can_differ_from_previous_intake(self):
        self.prepare()
        self.note = self.note.replace("Previous baseline: `v8.0.35`",
                                      "Previous baseline: `v8.0.7`")
        self.update_release("release after multiple intakes")
        self.assert_passes(self.run_guard())

    def test_ledger_unrelated_changes_are_allowed_but_evidence_changes_are_not(self):
        self.prepare()
        ledger = (self.repo / LEDGER).read_text()
        self.write(LEDGER, ledger + "\n## Other notes\n\nUpdated instructions.\n")
        self.update_release("update unrelated ledger section")
        self.assert_passes(self.run_guard())
        self.write(LEDGER, ledger.replace("Upstream `", "Official upstream `"))
        self.update_release("change accepted evidence")
        result = self.run_guard()
        self.assert_blocked(result)
        self.assertIn("differs from the confirmed ledger", result.stderr)

    def test_unrelated_ledger_edit_cannot_pose_as_confirmation(self):
        self.prepare()
        ledger = (self.repo / LEDGER).read_text()
        self.write(LEDGER, ledger + "\n## Other notes\n\nUnrelated update.\n")
        self.commit("unrelated ledger edit")
        self.registry["accepted"]["ledger_commit"] = self.sha("HEAD")
        self.update_release("claim unrelated edit as confirmation")
        self.assert_blocked(self.run_guard())

    def test_replay_requires_all_dag_links_and_matching_tree(self):
        self.prepare("replay")
        original = self.registry["accepted"].copy()
        for changes in ({"main_before": self.u}, {"candidate_commit": self.m},
                        {"integration_commit": self.m}, {"intake_commit": self.c},
                        {"candidate_tree": self.sha(f"{self.u}^{{tree}}")},
                        {"ledger_commit": self.registry["accepted"]["intake_commit"]}):
            with self.subTest(changes=changes):
                self.registry["accepted"] = {**original, **changes}
                self.update_release("bad replay metadata")
                self.assert_blocked(self.run_guard())

    def test_replay_rejects_dag_errors_even_with_consistent_ledger_claims(self):
        self.prepare("replay", replay_fault="integration_tree")
        result = self.run_guard()
        self.assert_blocked(result)
        self.assertIn("trees must match", result.stderr)

    def test_replay_rejects_wrong_integration_parents_with_consistent_ledger(self):
        self.prepare("replay", replay_fault="integration_parents")
        result = self.run_guard()
        self.assert_blocked(result)
        self.assertIn("incorrect parents", result.stderr)

    def test_replay_rejects_missing_upstream_ancestry_with_consistent_ledger(self):
        self.prepare("replay", replay_fault="upstream_ancestry")
        result = self.run_guard()
        self.assert_blocked(result)
        self.assertIn("required ancestry is missing", result.stderr)

    def test_remote_tag_must_still_resolve_to_validated_commit(self):
        self.prepare()
        self.git("tag", "-a", "-f", TAG, "-m", "fixture release")
        remote = self.home / "origin.git"
        subprocess.run(["git", "clone", "-q", "--bare", str(self.repo), str(remote)],
                       check=True, capture_output=True)
        self.git("remote", "add", "origin", str(remote))

        def verify():
            return subprocess.run([str(GUARD), "--verify-remote", TAG, self.target],
                                  cwd=self.repo, text=True, capture_output=True)

        def remote_ref(*args):
            subprocess.run(["git", "--git-dir", str(remote), "update-ref", *args],
                           check=True, capture_output=True)

        self.assert_passes(verify())  # Annotated tag is peeled.
        remote_ref(f"refs/tags/{TAG}", self.target)
        self.assert_passes(verify())  # Lightweight tags are also supported.
        remote_ref(f"refs/tags/{TAG}", self.m)
        self.assert_blocked(verify())
        remote_ref("-d", f"refs/tags/{TAG}")
        self.assert_blocked(verify())
        self.git("remote", "set-url", "origin", str(self.home / "missing.git"))
        self.assert_blocked(verify())

    def test_live_api_check_uses_committed_registry_and_fails_on_api_errors(self):
        self.prepare()
        self.assert_passes(self.run_upstream_check())
        self.write(REGISTRY, "dirty working-tree content must not be used")
        self.assert_passes(self.run_upstream_check())
        self.api[f"{API}/git/ref/tags/{UPSTREAM}"] = {"status": 401}
        self.assert_blocked(self.run_upstream_check())

    def test_ci_relevance_fails_closed_when_base_is_missing_or_unrelated(self):
        self.prepare()
        base = self.target

        def relevance(revision):
            return subprocess.run([str(GUARD), "--ci-relevant", revision], cwd=self.repo,
                                  text=True, capture_output=True)

        self.write("unrelated.txt", "change\n")
        self.commit("unrelated")
        result = relevance(base)
        self.assert_passes(result)
        self.assertEqual(result.stdout.strip(), "false")
        self.write("ops/upstream-intake/guard-release-tag.sh", "changed\n")
        self.commit("guard change")
        result = relevance(base)
        self.assert_passes(result)
        self.assertEqual(result.stdout.strip(), "true")
        before_rename = self.sha("HEAD")
        self.git("mv", "ops/upstream-intake/guard-release-tag.sh", "renamed.txt")
        self.commit("rename guard")
        result = relevance(before_rename)
        self.assert_passes(result)
        self.assertEqual(result.stdout.strip(), "true")
        self.assert_blocked(relevance("0" * 40))
        self.assert_blocked(relevance(self.u))


if __name__ == "__main__":
    unittest.main()
