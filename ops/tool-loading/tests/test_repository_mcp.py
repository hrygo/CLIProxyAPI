"""Real MCP discovery and patch boundaries; no model-code execution claims."""

from copy import deepcopy
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from catalog_fixture import CatalogFixture
from repository_project import RepositoryProject


class RepositoryFixtureTests(unittest.IsolatedAsyncioTestCase):
    def arguments(self, snapshot, path="api.py"):
        return {"expected_revision": snapshot["revision"], "changes": [{"path": path,
                "expected_sha256": snapshot["files"][path]["sha256"],
                "source": snapshot["files"][path]["source"] + "\n# MCP patch.\n"}]}

    async def test_invalid_denied_revoked_stale_and_duplicate_edits_have_zero_dispatch(self):
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            snapshot = project.read()
            arguments = self.arguments(snapshot)
            invalid_path = self.arguments(snapshot)
            invalid_path["changes"][0]["path"] = "../gold.py"
            invalid_bytes = self.arguments(snapshot)
            invalid_bytes["changes"][0]["source"] = "界" * 8192
            invalid_revision = {**arguments, "expected_revision": "0" * 64}
            duplicates = {**arguments, "changes": arguments["changes"] * 2}
            for value in (invalid_path, invalid_bytes, invalid_revision, duplicates):
                result = await fixture.call("repository_patch", value)
                self.assertTrue(result["isError"])
            fixture.list_tools()
            fixture.set_denied({"fixture_mutation", "repository_patch"})
            result = await fixture.call("repository_patch", arguments)
            self.assertEqual(result["value"]["error"], "permission_denied")
            self.assertEqual((fixture.dispatch_count, fixture.effect_count, fixture.active_count), (0, 0, 0))
            self.assertEqual(project.read(), snapshot)

    async def test_read_patch_discovery_and_completion_bind_actual_tree_revision(self):
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            names = {tool["name"] for tool in fixture.list_tools()}
            self.assertTrue({"repository_read", "repository_patch"} <= names)
            self.assertNotIn("repository_tests_run", names)
            before = (await fixture.call("repository_read", {}))["value"]
            result = await fixture.call("repository_patch", self.arguments(before))
            self.assertFalse(result["isError"])
            after = (await fixture.call("repository_read", {}))["value"]
            self.assertEqual(result["value"]["revision"], after["revision"])
            completed = [event for event in fixture.events if event["phase"] == "completed"]
            self.assertEqual(completed[-1]["repository_revision"], after["revision"])
            self.assertEqual(completed[1]["changed_files"], ["api.py"])
            self.assertEqual(fixture.active_count, 0)
            result = await fixture.call("repository_tests_run", {})
            self.assertEqual(result["value"]["error"], "unknown_tool")
            self.assertEqual(fixture.dispatch_count, 3)

    async def test_result_budget_rejects_repository_read_before_dispatch(self):
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project, max_result_bytes=512)
            result = await fixture.call("repository_read", {})
            self.assertEqual(result["value"]["error"], "result_budget")
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (0, 0))

    async def test_patch_result_budget_rejects_before_dispatch_and_any_write(self):
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project, max_result_bytes=256)
            before = project.read()
            entries = sorted(path.name for path in project.root.iterdir())
            result = await fixture.call("repository_patch", self.arguments(before))
            self.assertEqual(result["value"]["error"], "result_budget")
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (0, 0))
            self.assertEqual(project.read(), before)
            self.assertEqual(sorted(path.name for path in project.root.iterdir()), entries)

    async def test_commit_uncertain_is_not_rejected_as_uncommitted(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            before = project.read()
            actual_fsync = os.fsync
            def fail_after_publication(descriptor):
                info = os.fstat(descriptor)
                if ((info.st_dev, info.st_ino) == project.identity
                        and json.loads((project.root / "state.json").read_text())["revision"] != before["revision"]):
                    raise OSError("synthetic durability failure")
                return actual_fsync(descriptor)
            with patch("repository_project.os.fsync", side_effect=fail_after_publication):
                result = await fixture.call("repository_patch", self.arguments(before))
            self.assertTrue(result["isError"])
            self.assertEqual(result["value"]["error"], "repository_commit_uncertain")
            self.assertEqual(result["value"]["revision"], project.read()["revision"])
            self.assertEqual(result["value"]["changed_files"], ["api.py"])
            phases = [event["phase"] for event in fixture.events]
            self.assertIn("commit_uncertain", phases)
            self.assertNotIn("rejected", phases)
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (1, 0))

    async def test_arguments_are_snapshotted_before_preflight_reads(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            before = project.read()
            arguments = self.arguments(before)
            read = project.read
            def mutate_caller_then_read():
                arguments["changes"][0]["path"] = "../private.py"
                return read()
            with patch.object(project, "read", side_effect=mutate_caller_then_read):
                result = await fixture.call("repository_patch", arguments)
            self.assertFalse(result["isError"])
            self.assertEqual(result["value"]["changed_files"], ["api.py"])
            self.assertEqual(fixture.dispatch_count, 1)

    async def test_permission_revoked_during_preflight_has_zero_dispatch(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            before = project.read()
            read = project.read
            def revoke_then_read():
                fixture.set_denied({"fixture_mutation", "repository_patch"})
                return read()
            with patch.object(project, "read", side_effect=revoke_then_read):
                result = await fixture.call("repository_patch", self.arguments(before))
            self.assertEqual(result["value"]["error"], "permission_revoked")
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (0, 0))
            self.assertEqual(project.read(), before)

    async def test_permission_revoked_after_staging_does_not_publish(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            before = project.read()
            write_generation = project._write_generation
            def stage_then_revoke(*arguments):
                write_generation(*arguments)
                fixture.set_denied({"fixture_mutation", "repository_patch"})
            with patch.object(project, "_write_generation", side_effect=stage_then_revoke):
                result = await fixture.call("repository_patch", self.arguments(before))
            self.assertEqual(result["value"]["error"], "permission_revoked")
            self.assertEqual(project.read(), before)
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (1, 0))

    async def test_permission_rechecked_after_state_fsync_before_pointer_replacement(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            fixture = CatalogFixture("nonce", repository_project=project)
            before = project.read()
            actual_fsync = os.fsync
            revoked_after_state_fsync = False
            def revoke_after_state_fsync(descriptor):
                nonlocal revoked_after_state_fsync
                actual_fsync(descriptor)
                info = os.fstat(descriptor)
                for pending in project.root.glob(".state_*"):
                    expected = pending.stat()
                    if (info.st_dev, info.st_ino) == (expected.st_dev, expected.st_ino):
                        revoked_after_state_fsync = True
                        fixture.set_denied({"fixture_mutation", "repository_patch"})
            with patch("repository_project.os.fsync", side_effect=revoke_after_state_fsync):
                result = await fixture.call("repository_patch", self.arguments(before))
            self.assertTrue(revoked_after_state_fsync)
            self.assertEqual(result["value"]["error"], "permission_revoked")
            self.assertEqual(project.read(), before)
            self.assertEqual((fixture.dispatch_count, fixture.active_count), (1, 0))

    async def test_post_commit_audit_failure_preserves_revision_and_marks_evidence_incomplete(self):
        from unittest.mock import patch
        for phase in ("completed", "released", "commit_uncertain"):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                project = RepositoryProject.create(Path(directory) / "repository")
                fixture = CatalogFixture("nonce", repository_project=project)
                before = project.read()
                record = fixture._record
                actual_fsync = os.fsync
                def fail_after_publication(descriptor):
                    info = os.fstat(descriptor)
                    if (phase == "commit_uncertain" and (info.st_dev, info.st_ino) == project.identity
                            and json.loads((project.root / "state.json").read_text())["revision"] != before["revision"]):
                        raise OSError("synthetic durability failure")
                    return actual_fsync(descriptor)
                def fail_record(selected, *arguments, **keywords):
                    if selected == phase:
                        raise OSError("synthetic audit failure")
                    return record(selected, *arguments, **keywords)
                with patch.object(fixture, "_record", side_effect=fail_record), patch("repository_project.os.fsync", side_effect=fail_after_publication):
                    result = await fixture.call("repository_patch", self.arguments(before))
                self.assertEqual(result["isError"], phase == "commit_uncertain")
                if phase == "commit_uncertain":
                    self.assertEqual(result["value"]["error"], "repository_commit_uncertain")
                self.assertEqual(result["value"]["revision"], project.read()["revision"])
                self.assertEqual(result["value"]["changed_files"], ["api.py"])
                self.assertFalse(result["value"]["evidence_complete"])
                self.assertEqual((fixture.dispatch_count, fixture.active_count), (1, 0))

    async def test_stdio_actual_client_lists_reads_patches_and_rechecks_changed_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            project = RepositoryProject.create(Path(directory) / "repository")
            events = Path(directory) / "events.jsonl"
            policy = Path(directory) / "permissions.json"
            policy.write_text(json.dumps({"generation": 1, "denied": ["fixture_mutation"]}))
            parameters = StdioServerParameters(command=sys.executable, args=[
                str(Path(__file__).resolve().parents[1] / "catalog_fixture.py"), "--nonce", "fixture-nonce",
                "--events", str(events), "--permissions", str(policy),
                "--repository-workspace", str(project.root), "--repository-owner", project.owner])
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    names = {tool.name for tool in (await session.list_tools()).tools}
                    self.assertTrue({"repository_read", "repository_patch"} <= names)
                    before = (await session.call_tool("repository_read", {})).structuredContent
                    arguments = self.arguments(before)
                    result = await session.call_tool("repository_patch", arguments)
                    self.assertFalse(result.isError)
                    self.assertEqual(result.structuredContent["revision"], project.read()["revision"])
                    stale = await session.call_tool("repository_patch", arguments)
                    self.assertEqual(stale.structuredContent["error"], "repository_revision_mismatch")
                    policy.write_text(json.dumps({"generation": 2, "denied": ["fixture_mutation", "repository_patch"]}))
                    revoked = await session.call_tool("repository_patch", arguments)
                    self.assertEqual(revoked.structuredContent["error"], "permission_denied")
            records = [json.loads(line) for line in events.read_text().splitlines()]
            self.assertEqual([event["tool"] for event in records if event["phase"] == "dispatch"], ["repository_read", "repository_patch"])
            self.assertEqual(records[-1]["active_count"], 0)
            self.assertEqual(project.read()["revision"], result.structuredContent["revision"])


if __name__ == "__main__":
    unittest.main()
