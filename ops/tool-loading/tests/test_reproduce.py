"""Focused checks for native result binding and manifest admission."""
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from reproduce import client_result, validate_manifest
from reproduce import wire_observations
from catalog_fixture import build_catalog


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


if __name__ == "__main__":
    unittest.main()
