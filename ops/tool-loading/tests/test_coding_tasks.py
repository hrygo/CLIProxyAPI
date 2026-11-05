"""Behavioral code scoring and bounded source access, without a Provider."""

import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from coding_tasks import TASKS, CodingProject, evaluate_source, async_evaluate_source
from catalog_fixture import CatalogFixture


SOLUTIONS = {
    "budget": """def remaining_budget(limit, entries):
    if type(limit) != int or type(entries) != list:
        raise ValueError()
    total = 0
    for entry in entries:
        if type(entry) != dict or type(entry['amount']) != int:
            raise ValueError()
        if entry['state'] == 'posted':
            total += entry['amount']
    return limit - total
""",
    "retry": """def retryable(status, exposed):
    if type(status) != int or type(exposed) != bool:
        raise ValueError()
    return not exposed and (status == 429 or (500 <= status <= 599 and status != 501 and status != 505))
""",
    "identity": """def unique_call_ids(values):
    if type(values) != list:
        raise ValueError()
    result = []
    for value in values:
        if type(value) != str:
            raise ValueError()
        if value and value not in result:
            result += [value]
    return result
""",
}


class CodingTests(unittest.TestCase):
    def test_independent_checker_rejects_each_baseline_and_accepts_behavior(self):
        for name, task in TASKS.items():
            with self.subTest(name=name):
                self.assertFalse(evaluate_source(name, task.baseline)["passed"])
                result = evaluate_source(name, SOLUTIONS[name])
                self.assertTrue(result["passed"], result)
                self.assertGreaterEqual(result["tests"], 8)

    def test_claims_and_constant_answers_do_not_pass_code_scoring(self):
        for source in ("def retryable(status, exposed):\n    return True\n",
                       "def retryable(status, exposed):\n    return False\n",
                       "# all tests pass\n", "{}"):
            self.assertFalse(evaluate_source("retry", source)["passed"])

    def test_checker_denies_imports_attributes_dynamic_calls_and_top_level_effects(self):
        for source in ("import os\ndef retryable(status, exposed): return False\n",
                       "def retryable(status, exposed): return status.__class__\n",
                       "def retryable(status, exposed): return eval('True')\n",
                       "def retryable(status, exposed): return (lambda: True)()\n",
                       "def retryable(status, exposed): return False\nraise ValueError()\n"):
            result = evaluate_source("retry", source)
            self.assertFalse(result["passed"])
            self.assertEqual(result["reason"], "unsupported_source")

    def test_execution_is_bounded_and_runtime_errors_are_not_framework_success(self):
        result = evaluate_source("retry", "def retryable(status, exposed):\n    while True:\n        pass\n")
        self.assertFalse(result["passed"])
        self.assertIn(result["reason"], {"execution_budget", "worker_failed"})
        self.assertFalse(evaluate_source("retry", "def retryable(status, exposed):\n    return 1 / 0\n")["passed"])

    def test_source_replacement_is_scoped_and_optimistic(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            project = CodingProject.create(root, "retry")
            snapshot = project.read()
            original = (root / "module.py").read_bytes()
            with self.assertRaises(ValueError):
                project.replace("a" * 64, SOLUTIONS["retry"])
            self.assertEqual((root / "module.py").read_bytes(), original)
            project.replace(snapshot["source_sha256"], SOLUTIONS["retry"])
            self.assertTrue(project.score()["passed"])
            self.assertNotEqual(snapshot["source_sha256"], project.read()["source_sha256"])
            self.assertEqual((root / "module.py").stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                CodingProject.create(root, "retry")

    def test_symlink_and_hardlink_cannot_redirect_source_access(self):
        for link in ("symlink", "hardlink"):
            with self.subTest(link=link), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                workspace = root / "project"
                workspace.mkdir()
                project = CodingProject.create(workspace, "retry")
                external = root / "external.py"
                external.write_text("operator data")
                (workspace / "module.py").unlink()
                if link == "symlink":
                    (workspace / "module.py").symlink_to(external)
                else:
                    import os
                    os.link(external, workspace / "module.py")
                with self.assertRaises(ValueError): project.read()
                with self.assertRaises(ValueError): project.replace("a" * 64, SOLUTIONS["retry"])
                self.assertEqual(external.read_text(), "operator data")

    def test_revision_is_rechecked_on_the_descriptor_before_truncation(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as temporary:
            project = CodingProject.create(Path(temporary), "retry")
            snapshot = project.read()
            original_read = project.read
            def intervening_change():
                result = original_read()
                (project.root / "module.py").write_text("# a different revision\n")
                return result
            with patch.object(project, "read", side_effect=intervening_change):
                with self.assertRaises(ValueError): project.replace(snapshot["source_sha256"], SOLUTIONS["retry"])
            self.assertEqual((project.root / "module.py").read_text(), "# a different revision\n")

    def test_oversized_allocations_and_formatting_are_rejected_before_allocation(self):
        for expression in ("[0] * 1000000000", "'x' * 1000000000", "2 ** 1000000000", "'%1000000000s' % 'x'"):
            result = evaluate_source("retry", "def retryable(status, exposed):\n    return " + expression + "\n")
            self.assertFalse(result["passed"])
            self.assertIn(result["reason"], {"execution_budget", "unsupported_source"})


class CodingMCPTests(unittest.IsolatedAsyncioTestCase):
    async def test_cancellation_during_worker_acquisition_does_not_orphan_process(self):
        from unittest.mock import patch
        spawned, release = asyncio.Event(), asyncio.Event()
        live = []
        original = asyncio.create_subprocess_exec
        async def delayed(*args, **kwargs):
            process = await original(*args, **kwargs)
            live.append(process)
            spawned.set()
            await release.wait()
            return process
        with patch("coding_tasks.asyncio.create_subprocess_exec", side_effect=delayed):
            task = asyncio.create_task(async_evaluate_source("retry", SOLUTIONS["retry"]))
            try:
                await asyncio.wait_for(spawned.wait(), 5)
                task.cancel()
                release.set()
                with self.assertRaises(asyncio.CancelledError): await task
                self.assertIsNotNone(live[0].returncode)
            finally:
                release.set()
                for process in live:
                    if process.returncode is None:
                        process.kill()
                        await process.wait()

    async def test_real_stdio_read_replace_test_and_independent_host_score(self):
        from mcp import ClientSession, StdioServerParameters
        from mcp.client.stdio import stdio_client
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            workspace = root / "workspace"
            workspace.mkdir()
            project = CodingProject.create(workspace, "identity")
            events = root / "events.jsonl"
            parameters = StdioServerParameters(command=sys.executable, args=[
                str(Path(__file__).resolve().parents[1] / "catalog_fixture.py"),
                "--nonce", "actual-stdio-code-receipt", "--events", str(events),
                "--coding-task", "identity", "--coding-workspace", str(workspace)])
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    self.assertIn("coding_source_replace", {tool.name for tool in (await session.list_tools()).tools})
                    first = await session.call_tool("coding_task_read", {})
                    changed = await session.call_tool("coding_source_replace", {
                        "source": SOLUTIONS["identity"], "expected_sha256": first.structuredContent["source_sha256"]})
                    self.assertFalse(changed.isError)
                    tested = await session.call_tool("coding_tests_run", {})
                    self.assertTrue(tested.structuredContent["passed"])
                    self.assertEqual(tested.structuredContent["verification_code"], "actual-stdio-code-receipt")
            score = project.score()
            self.assertTrue(score["passed"] and score["changed"])
            self.assertEqual(score["source_sha256"], tested.structuredContent["source_sha256"])
            records = [json.loads(line) for line in events.read_text().splitlines()]
            self.assertEqual([event["tool"] for event in records if event["phase"] == "dispatch"],
                             ["coding_task_read", "coding_source_replace", "coding_tests_run"])
            self.assertEqual(records[-1]["active_count"], 0)

    async def test_explicit_profile_edits_real_source_and_rechecks_revocation(self):
        with tempfile.TemporaryDirectory() as temporary:
            project = CodingProject.create(Path(temporary), "retry")
            fixture = CatalogFixture("fresh-code-receipt", coding_project=project)
            first = await fixture.call("coding_task_read", {})
            self.assertIn("requirements", first["value"])
            invalid = await fixture.call("coding_source_replace", {"source": SOLUTIONS["retry"], "expected_sha256": 1})
            self.assertEqual(invalid["value"]["error"], "invalid_arguments")
            self.assertEqual(fixture.dispatch_count, 1)
            fixture.set_denied({"fixture_mutation", "coding_source_replace"})
            revoked = await fixture.call("coding_source_replace", {"source": SOLUTIONS["retry"], "expected_sha256": first["value"]["source_sha256"]})
            self.assertEqual(revoked["value"]["error"], "permission_denied")
            self.assertEqual(fixture.dispatch_count, 1)
            fixture.set_denied({"fixture_mutation"})
            changed = await fixture.call("coding_source_replace", {"source": SOLUTIONS["retry"], "expected_sha256": first["value"]["source_sha256"]})
            self.assertFalse(changed["isError"])
            tested = await fixture.call("coding_tests_run", {})
            self.assertTrue(tested["value"]["passed"])
            self.assertEqual(tested["value"]["verification_code"], "fresh-code-receipt")
            self.assertTrue(project.score()["passed"])
            self.assertEqual((fixture.effect_count, fixture.active_count), (0, 0))

    async def test_read_only_profile_has_no_coding_writes(self):
        fixture = CatalogFixture("read-only")
        self.assertNotIn("coding_source_replace", {tool["name"] for tool in fixture.list_tools()})
        result = await fixture.call("coding_source_replace", {})
        self.assertEqual(result["value"]["error"], "unknown_tool")
        self.assertEqual(fixture.dispatch_count, 0)

    async def test_cancelled_checker_joins_worker_before_releasing(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as temporary:
            project = CodingProject.create(Path(temporary), "retry")
            project.replace(project.read()["source_sha256"], "def retryable(status, exposed):\n    while True:\n        pass\n")
            fixture = CatalogFixture("cancelled-code", coding_project=project)
            live = []
            original = asyncio.create_subprocess_exec
            async def observe(*args, **kwargs):
                process = await original(*args, **kwargs)
                live.append(process)
                return process
            with patch("coding_tasks.asyncio.create_subprocess_exec", side_effect=observe):
                task = asyncio.create_task(fixture.call("coding_tests_run", {}))
                while not live: await asyncio.sleep(0)
                task.cancel()
                with self.assertRaises(asyncio.CancelledError): await task
            self.assertIsNotNone(live[0].returncode)
            self.assertEqual(fixture.active_count, 0)
            self.assertNotIn("completed", [event["phase"] for event in fixture.events])


if __name__ == "__main__":
    unittest.main()
