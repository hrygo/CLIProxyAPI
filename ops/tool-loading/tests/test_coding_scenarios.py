"""Code outcome and actual tool trace are separate from final JSON claims."""

import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from task_scenarios import get_scenario, output_schema, answer_matches, dispatch_matches
from live_clients import make_case_spec, result_checks


class CodingScenarioTests(unittest.TestCase):
    def test_runner_scores_source_independently_from_a_clients_fixed_claim(self):
        from contextlib import contextmanager
        from types import SimpleNamespace
        from catalog_fixture import build_catalog, digest
        from coding_tasks import CodingProject
        from client_adapters import profile_permissions
        from live_clients import run_case, PERMISSIONS
        from run_acceptance import CaseJournal
        from test_http_attempts import evidence
        for fixed in (False, True):
            with self.subTest(fixed=fixed), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                executable, config, stdout = (root / name for name in ("client", "config", "stdout"))
                executable.write_bytes(b"synthetic-client")
                config.write_bytes(b"synthetic-config")
                capture = root / "capture"
                capture.mkdir()
                body = json.dumps({"choices": [{"finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 20, "completion_tokens": 1, "total_tokens": 21}}).encode()
                records = evidence(capture, bodies=(body,), contract="openai-chat-v1")
                for event in records:
                    event["version"] = 2
                    if event["event"] == "request-started":
                        event["executor_identity"] = {"executor": "openai-compatible-fixture", "model_sha256": "b" * 64, "auth_sha256": "c" * 64}
                (capture / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in records))
                (capture / "admission.json").write_text(json.dumps({"version": 1, "max_attempts": 8,
                    "reserved_attempts": 1, "budget_rejections": 0, "closed_rejections": 0, "failed_rejections": 0,
                    "active_bodies": 0, "journal_closed": True, "journal_failed": False}))
                @contextmanager
                def candidate(*args, **kwargs):
                    yield {"base_url": "http://127.0.0.1:1"}, capture
                workspace = root / "private" / "workspace"
                workspace.mkdir(parents=True)
                project = CodingProject.create(workspace, "retry")
                if fixed:
                    from test_coding_tasks import SOLUTIONS
                    project.replace(project.read()["source_sha256"], SOLUTIONS["retry"])
                source_hash = project.read()["source_sha256"]
                answer = {"status": "fixed", "source_sha256": source_hash, "verification_code": "fresh"}
                native = {"client_output_profile": "opencode-http-session-v1", "protocol_terminal_verified": True,
                    "protocol_terminal_status": "completed", "structured_output": answer, "forced_process_cleanup": False,
                    "stdout_path": str(stdout), "termination": "exited", "exit_code": 0}
                stdout.write_text("")
                prepared = SimpleNamespace(run_dir=root / "private", permission_hash=digest(profile_permissions(
                    PERMISSIONS, structured_output=True, coding_task="retry")), catalog_hash=digest(build_catalog("retry")),
                    run_task=lambda task: native, collect_boundary_events=lambda: [{"phase": "catalog", "effect_count": 0, "active_count": 0}])
                profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
                journal = CaseJournal(root / "cases.jsonl", "run-coding-host")
                with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process", candidate), \
                        patch("live_clients.fetch_model_metadata", return_value={}), patch("live_clients.prepare_isolated_client", return_value=prepared), \
                        patch("live_clients.uuid.uuid4", return_value=SimpleNamespace(hex="fresh")):
                    result = run_case(root, journal, executable, config, profile, "opencode", "eager", "a" * 40,
                        "ep_" + "b" * 32, 90, scenario="coding-retry", structured_output=True)
                self.assertEqual(result["checks"]["code"], fixed)
                self.assertFalse(result["safe_task_success"])
                case_root = root / result["case_id"]
                self.assertEqual(json.loads((case_root / "code-outcome.json").read_text())["passed"], fixed)
                self.assertEqual(bool((case_root / "source.diff").read_text()), fixed)

    def test_coding_gold_requires_independent_code_check(self):
        for name in ("coding-budget", "coding-retry", "coding-identity"):
            task = get_scenario(name)
            self.assertTrue(task.gold["requires_code"])
            self.assertTrue(task.gold["requires_nonce"])
            self.assertIn("module.py", task.prompt)
            self.assertNotIn('"status":"fixed"', json.dumps(output_schema(name)))

    def test_final_hash_and_nonce_must_match_actual_source_and_test_receipt(self):
        answer = {"status": "fixed", "source_sha256": "a" * 64, "verification_code": "fresh"}
        self.assertTrue(answer_matches("coding-retry", answer, "fresh", source_sha256="a" * 64)[0])
        self.assertFalse(answer_matches("coding-retry", answer, "fresh", source_sha256="b" * 64)[0])
        self.assertFalse(answer_matches("coding-retry", answer, "other", source_sha256="a" * 64)[0])
        native = {"client_output_profile": "opencode-http-session-v1", "protocol_terminal_verified": True,
                  "protocol_terminal_status": "completed", "structured_output": answer}
        checks = result_checks("opencode", "", "fresh", scenario="coding-retry", native_result=native, source_sha256="b" * 64)
        self.assertFalse(checks["answer"])
        self.assertTrue(checks["format"])
        self.assertIsNone(checks["code"])

    def test_dispatch_requires_read_edit_and_current_passing_test_with_releases(self):
        events = []
        for tool in ("coding_task_read", "coding_source_replace", "coding_tests_run"):
            events += [{"phase": "dispatch", "tool": tool},
                       {"phase": "completed", "tool": tool, "source_sha256": "a" * 64, "tests_passed": True},
                       {"phase": "released", "tool": tool}]
        self.assertTrue(dispatch_matches("coding-retry", events, source_sha256="a" * 64))
        self.assertFalse(dispatch_matches("coding-retry", events, source_sha256="b" * 64))
        self.assertFalse(dispatch_matches("coding-retry", events[:-1], source_sha256="a" * 64))
        self.assertFalse(dispatch_matches("coding-retry", events[3:], source_sha256="a" * 64))
        failed = [dict(event) for event in events]
        failed[-2]["tests_passed"] = False
        self.assertFalse(dispatch_matches("coding-retry", failed, source_sha256="a" * 64))

    def test_predeclared_coding_scope_binds_catalog_gold_permission_and_checker(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, executable = root / "config", root / "client"
            config.write_bytes(b"synthetic-config")
            executable.write_bytes(b"synthetic-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                plain = make_case_spec(config, profile, "opencode", "lazy", "a" * 40, "ep_" + "b" * 32, 90)
                coding = make_case_spec(config, profile, "opencode", "lazy", "a" * 40, "ep_" + "b" * 32, 90, scenario="coding-retry")
            self.assertTrue(coding.gold["requires_code"])
            self.assertNotEqual(coding.scope["catalog_hash"], plain.scope["catalog_hash"])
            self.assertNotEqual(coding.scope["permission_hash"], plain.scope["permission_hash"])
            self.assertNotEqual(coding.scope["fixture_hash"], plain.scope["fixture_hash"])


if __name__ == "__main__":
    unittest.main()
