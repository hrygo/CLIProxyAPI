"""Focused checks for native result binding and manifest admission."""
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from reproduce import client_result, validate_manifest
from reproduce import wire_observations
from catalog_fixture import build_catalog
import reproduce


class ReproductionTests(unittest.TestCase):
    def test_eager_mode_counts_only_tools_admitted_by_both_permission_boundaries(self):
        names = [x["name"] for x in build_catalog("budget") if x["name"] != "fixture_mutation"]
        description = "Run JavaScript code. ALL_TOOLS. " + " ".join(
            "declare const tools: { mcp__fixture_" + name + "(args: {}): Promise<unknown>; };" for name in names)
        tool = {"type": "custom", "name": "exec", "description": description,
                "format": {"type": "grammar", "syntax": "lark", "definition": "start: SOURCE\nSOURCE: /.+/"}}
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "request-000001.body").write_text(json.dumps({"tools": [
                {"type": "namespace", "name": "functions", "tools": [tool]}]}))
            effective, _, observed = wire_observations(root, [{"request_id": "request-000001",
                "contract": "openai-responses-v1"}], "codex", "eager", "budget")
            self.assertEqual(observed["mode"]["nested_fixture_signatures"], 70)
            self.assertTrue(effective)

    def result(self, client, events):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "stdout.jsonl").write_text("".join(json.dumps(x) + "\n" for x in events))
            return client_result(SimpleNamespace(client_id=client, run_dir=root))

    def test_claude_structured_result_must_match_latest_real_invocation(self):
        answer = {"passed": True, "source_sha256": "a" * 64, "verification_code": "receipt"}
        events = [{"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "StructuredOutput",
                   "id": "call-a", "input": answer}]}},
                  {"type": "result", "subtype": "success", "is_error": False, "structured_output": answer}]
        self.assertEqual(self.result("claude", events), (True, answer))
        events[1]["structured_output"] = {**answer, "verification_code": "fabricated"}
        self.assertFalse(self.result("claude", events)[0])

    def test_error_result_and_duplicate_terminal_do_not_pass(self):
        events = [{"type": "result", "subtype": "error", "is_error": True, "structured_output": {}}]
        self.assertFalse(self.result("claude", events)[0])
        events += events
        self.assertFalse(self.result("claude", events)[0])

    def test_codex_needs_real_json_answer_and_completed_turn(self):
        answer = {"passed": True}
        events = [{"type": "item.completed", "item": {"type": "agent_message", "text": json.dumps(answer)}},
                  {"type": "turn.completed"}]
        self.assertEqual(self.result("codex", events), (True, answer))
        events[-1] = {"type": "turn.failed"}
        self.assertFalse(self.result("codex", events)[0])

    def test_scope_cannot_omit_attempt_budget_or_request_unbounded_cases(self):
        with self.assertRaises(ValueError):
            validate_manifest({})
        with tempfile.TemporaryDirectory() as tmp:
            file = Path(tmp) / "fixture"
            file.write_text("public")
            value = {"version": 1, "gateway_revision": "a" * 40, "config": str(file),
                     "codex": str(file), "claude": str(file), "fixture_python": str(file),
                     "seconds": 120, "max_attempts": 16, "max_physical_attempts": 160,
                     "routes": [{"client": "codex", "task": "budget", "modes": ["eager", "lazy"],
                       "profile": {"client_model": "fixture", "upstream_model": "fixture",
                                   "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}}]}
            validate_manifest(value)
            value["routes"] *= 13
            with self.assertRaises(ValueError):
                validate_manifest(value)


class CatalogReadinessTests(unittest.TestCase):
    def test_pending_state_requires_same_process_and_strict_counter(self):
        ready = {"base_url": "http://127.0.0.1:12345/v1", "pid": 123, "ready_nonce": "same-process"}
        good = {"pid": 123, "ready_nonce": "same-process", "active_auxiliary_work": 1}
        invalid = [json.dumps({**good, "pid": True}), json.dumps({**good, "ready_nonce": "other-process"}),
                   json.dumps({**good, "active_auxiliary_work": True}), json.dumps({**good, "active_auxiliary_work": -1}),
                   json.dumps({**good, "extra": "untrusted"}), json.dumps(good).replace('"pid": 123', '"pid": 999, "pid": 123'),
                   "x" * 8193]
        connection = MagicMock()
        response = connection.getresponse.return_value
        response.status = 200
        response.read.return_value = json.dumps(good).encode()
        with patch("reproduce.http.client.HTTPConnection", return_value=connection):
            self.assertEqual(reproduce._catalog_work_active(ready), 1)
            for body in invalid:
                response.read.return_value = body.encode()
                with self.assertRaisesRegex(ValueError, "^catalog_readiness_unverified$"):
                    reproduce._catalog_work_active(ready)
        connection.request.assert_called_with("GET", "/acceptance/catalog-state",
                                             headers={"Authorization": "Bearer local-acceptance-placeholder"})

    def test_expired_task_deadline_never_reads_catalog(self):
        with patch("reproduce._model_metadata_once") as fetch, patch("reproduce.time.monotonic", return_value=10):
            with self.assertRaisesRegex(ValueError, "^catalog_startup_budget_exhausted$"):
                reproduce.model_metadata({}, "codex", "lazy", deadline=10)
        fetch.assert_not_called()

    def test_pending_native_discovery_requeries_actual_catalog(self):
        metadata = {"slug": "fixture", "context_window": 1000}
        evidence = []
        ready = {"profile": {"executor": "antigravity"}}
        with patch("reproduce._model_metadata_once", side_effect=[ValueError("catalog_model_unavailable"), metadata]) as fetch, \
                patch("reproduce._catalog_work_active", return_value=1), \
                patch("reproduce.time.monotonic", return_value=10), patch("reproduce.time.sleep") as wait:
            self.assertEqual(reproduce.model_metadata(ready, "codex", "lazy", deadline=11, observations=evidence), metadata)
        self.assertEqual(fetch.call_count, 2)
        self.assertEqual(evidence[0]["active_auxiliary_work"], 1)
        self.assertTrue(evidence[1]["available"])
        wait.assert_called_once()

    def test_settled_discovery_rechecks_publication_before_rejecting(self):
        ready = {"profile": {"executor": "antigravity"}}
        with patch("reproduce._model_metadata_once", side_effect=[ValueError("catalog_model_unavailable"), {"slug": "fixture"}]), \
                patch("reproduce._catalog_work_active", return_value=0), \
                patch("reproduce.time.monotonic", return_value=10), patch("reproduce.time.sleep") as wait:
            self.assertEqual(reproduce.model_metadata(ready, "claude", "lazy", deadline=11), {"slug": "fixture"})
        wait.assert_not_called()

    def test_settled_missing_model_does_not_loop(self):
        ready = {"profile": {"executor": "antigravity"}}
        with patch("reproduce._model_metadata_once", side_effect=ValueError("catalog_model_unavailable")) as fetch, \
                patch("reproduce._catalog_work_active", return_value=0), \
                patch("reproduce.time.monotonic", return_value=10), patch("reproduce.time.sleep") as wait:
            with self.assertRaisesRegex(ValueError, "^catalog_model_unavailable$"):
                reproduce.model_metadata(ready, "claude", "lazy", deadline=11)
        self.assertEqual(fetch.call_count, 2)
        wait.assert_not_called()

    def test_mode_errors_and_other_providers_never_wait(self):
        for executor, reason in (("antigravity", "catalog_profile_not_admitted"), ("codex", "catalog_model_unavailable")):
            with self.subTest(executor=executor), \
                    patch("reproduce._model_metadata_once", side_effect=ValueError(reason)), \
                    patch("reproduce._catalog_work_active") as state, \
                    patch("reproduce.time.monotonic", return_value=10), patch("reproduce.time.sleep") as wait:
                with self.assertRaisesRegex(ValueError, "^" + reason + "$"):
                    reproduce.model_metadata({"profile": {"executor": executor}}, "codex", "lazy", deadline=11)
                state.assert_not_called()
                wait.assert_not_called()

    def test_task_budget_exhaustion_stops_pending_discovery(self):
        with patch("reproduce._model_metadata_once", side_effect=ValueError("catalog_model_unavailable")) as fetch, \
                patch("reproduce._catalog_work_active", return_value=1), \
                patch("reproduce.time.monotonic", side_effect=[10, 10, 10.6, 10.6]), patch("reproduce.time.sleep") as wait:
            with self.assertRaisesRegex(ValueError, "^catalog_startup_budget_exhausted$"):
                reproduce.model_metadata({"profile": {"executor": "antigravity"}}, "codex", "lazy", deadline=10.5)
        self.assertEqual(fetch.call_count, 1)
        wait.assert_not_called()

if __name__ == "__main__":
    unittest.main()
