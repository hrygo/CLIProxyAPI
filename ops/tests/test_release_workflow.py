#!/usr/bin/env python3
"""Offline checks for immutable release workflow inputs."""

import os
import re
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/release.yaml"
TAG = "v1.2.3-upstream8.0.6"
BUILD_JOBS = ("build-hosted", "build-linux-glibc", "build-linux-no-plugin", "build-freebsd")


def job(text, name):
    match = re.search(rf"(?ms)^  {re.escape(name)}:\n(.*?)(?=^  [a-z][a-z0-9-]*:\n|\Z)", text)
    if not match:
        raise AssertionError(f"missing workflow job: {name}")
    return match.group(1)


def step_script(job_text, name):
    step = job_text.split(f"      - name: {name}\n", 1)[1]
    script = step.split("        run: |\n", 1)[1].split("\n      - ", 1)[0]
    return textwrap.dedent(script)


class ReleaseWorkflowStructureTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = WORKFLOW.read_text()

    def test_validate_freezes_sha_and_shared_metadata(self):
        validate = job(self.workflow, "validate")
        for output in ("release_sha", "short_sha", "build_date", "release_version", "previous_tag"):
            self.assertIn(f"{output}: ${{{{ steps.release.outputs.{output} }}}}", validate)
        script = step_script(validate, "Validate release tag and main ancestry")
        self.assertIn('refs/tags/${RELEASE_TAG}^{commit}', script)
        self.assertIn('HEAD^{commit}', script)
        self.assertIn('release_sha=$release_sha', script)
        self.assertIn('${release_sha}^', script)

    def test_later_checkouts_and_build_metadata_use_validate_outputs(self):
        for name in ("prepare-release", *BUILD_JOBS, "publish-checksums"):
            with self.subTest(job=name):
                current = job(self.workflow, name)
                self.assertRegex(current, r"(?m)^    needs: .*\bvalidate\b")
                self.assertIn('ref: ${{ needs.validate.outputs.release_sha }}', current)
                self.assertNotIn('ref: ${{ env.RELEASE_TAG }}', current)
        for name in BUILD_JOBS:
            with self.subTest(job=name):
                current = job(self.workflow, name)
                for env, output in (("RELEASE_SHA", "release_sha"),
                                    ("RELEASE_VERSION", "release_version"),
                                    ("COMMIT", "short_sha"), ("BUILD_DATE", "build_date")):
                    self.assertIn(f"{env}: ${{{{ needs.validate.outputs.{output} }}}}", current)
                self.assertNotIn("git rev-parse --short HEAD", current)
                self.assertNotIn("date -u +", current)

    def test_guard_is_authenticated_and_runs_before_release_mutations(self):
        validate = job(self.workflow, "validate")
        guard_step = validate.split("      - name: Reject tags that belong to upstream\n", 1)[1]
        guard_step = guard_step.split("\n      - ", 1)[0]
        self.assertIn('GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}', guard_step)
        self.assertIn('ops/upstream-intake/guard-release-tag.sh "$RELEASE_TAG"', guard_step)

        for name, step, mutation in (
            ("prepare-release", "Create release", "if gh release view"),
            ("publish-checksums", "Verify complete assets and publish release", "gh release edit"),
        ):
            with self.subTest(job=name):
                current = job(self.workflow, name)
                self.assertIn('RELEASE_SHA: ${{ needs.validate.outputs.release_sha }}', current)
                script = step_script(current, step)
                check = 'ops/upstream-intake/guard-release-tag.sh --verify-remote "$RELEASE_TAG" "$RELEASE_SHA"'
                self.assertIn('set -euo pipefail', script)
                self.assertLess(script.index(check), script.index(mutation))
        self.assertIn('PREVIOUS_TAG: ${{ needs.validate.outputs.previous_tag }}',
                      job(self.workflow, "prepare-release"))
        check = 'ops/upstream-intake/guard-release-tag.sh --verify-remote "$RELEASE_TAG" "$RELEASE_SHA"'
        for name in BUILD_JOBS:
            with self.subTest(job=name):
                script = step_script(job(self.workflow, name), "Upload release assets")
                self.assertLess(script.index(check), script.index("gh release upload"))
        final_script = step_script(job(self.workflow, "publish-checksums"),
                                   "Verify complete assets and publish release")
        upload = final_script.index("gh release upload")
        publish = final_script.index("gh release edit")
        self.assertEqual(final_script.count(check), 2)
        self.assertLess(final_script.index(check), upload)
        self.assertGreater(final_script.rindex(check), upload)
        self.assertLess(final_script.rindex(check), publish)


class ValidateReleaseRefTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "work"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.invalid")
        self.commit("previous")
        self.git("tag", "v1.2.2-upstream8.0.5")
        self.release = self.commit("release")
        self.git("tag", "-a", TAG, "-m", "annotated release")
        self.later = self.commit("later")
        subprocess.run(["git", "clone", "-q", "--bare", str(self.repo), str(self.root / "remote.git")],
                       check=True, capture_output=True)
        self.git("remote", "add", "origin", str(self.root / "remote.git"))
        self.git("fetch", "-q", "origin", "main:refs/remotes/origin/main")
        self.git("checkout", "-q", "--detach", self.release)
        self.output = self.root / "outputs"
        self.script = step_script(job(WORKFLOW.read_text(), "validate"),
                                  "Validate release tag and main ancestry")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.repo, check=True,
                              text=True, capture_output=True).stdout.strip()

    def commit(self, content):
        (self.repo / "tracked").write_text(content)
        self.git("add", "tracked")
        self.git("commit", "-qm", content)
        return self.git("rev-parse", "HEAD")

    def validate(self, tag=TAG):
        return subprocess.run(["bash", "-euo", "pipefail", "-c", self.script], cwd=self.repo,
                              env=dict(os.environ, RELEASE_TAG=tag, GITHUB_OUTPUT=str(self.output)),
                              text=True, capture_output=True)

    def test_annotated_tag_is_peeled_once_and_metadata_uses_its_parent(self):
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stderr)
        values = dict(line.split("=", 1) for line in self.output.read_text().splitlines())
        self.assertEqual(values["release_sha"], self.release)
        self.assertEqual(values["short_sha"], self.git("rev-parse", "--short", self.release))
        self.assertEqual(values["release_version"], TAG[1:])
        self.assertEqual(values["previous_tag"], "v1.2.2-upstream8.0.5")
        self.assertRegex(values["build_date"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")

    def test_lightweight_tag_resolves_to_the_same_commit(self):
        self.git("update-ref", f"refs/tags/{TAG}", self.release)
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stderr)
        values = dict(line.split("=", 1) for line in self.output.read_text().splitlines())
        self.assertEqual(values["release_sha"], self.release)

    def test_changed_local_tag_does_not_validate_a_different_checkout(self):
        self.git("update-ref", f"refs/tags/{TAG}", self.later)
        result = self.validate()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists() and self.output.read_text())

    def test_invalid_tag_produces_no_outputs(self):
        result = self.validate("v1.2.3-upstream8.0.6-extra")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.output.exists() and self.output.read_text())


if __name__ == "__main__":
    unittest.main()
