"""Scripted Claude decisions require the actual loaded schema and MCP receipts."""

import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
from contextlib import redirect_stdout
import io

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from claude_client_probe import ScriptedClaudeModel, inspect_claude_tools, claude_result


class ClaudeProbeTests(unittest.TestCase):
    def test_every_probe_start_including_framework_failure_remains_in_summary(self):
        from claude_client_probe import main
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "new-probes"
            argv = ["probe", "--output-dir", str(root), "--gateway-binary", str(Path(temporary) / "binary")]
            with patch.object(sys, "argv", argv), patch("claude_client_probe.probe", side_effect=[ValueError("private detail"), *[{"passed": True}] * 3]), redirect_stdout(io.StringIO()):
                with self.assertRaises(SystemExit):
                    main()
            summary = json.loads((root / "summary.json").read_text())
            events = [json.loads(line) for line in (root / "probe-events.jsonl").read_text().splitlines()]
            self.assertEqual(len(summary["cases"]), 4)
            self.assertEqual(sum(event["event"] == "started" for event in events), 4)
            self.assertEqual(sum(event["event"] == "finished" for event in events), 4)
            self.assertEqual(summary["cases"][0]["exception_type"], "ValueError")
            self.assertNotIn("private detail", json.dumps(summary))

    def declarations(self):
        return [{"type": "function", "function": {"name": name, "parameters": {"type": "object"}}}
                for name in ("ToolSearch", "mcp__fixture__ledger_budget_lookup", "mcp__fixture__ledger_budget_confirm")]

    def test_bridge_discovery_requires_bound_manifest_before_invocation(self):
        model = ScriptedClaudeModel("lazy", "chat")
        first = model.next({"tools": self.declarations()[:1]})
        self.assertEqual(first["name"], "ToolSearch")
        second = model.next({"tools": self.declarations(), "messages": [{"role": "tool",
            "tool_call_id": "search", "content": json.dumps({"loaded_tools": [{"name": "mcp__fixture__ledger_budget_lookup"},
                {"name": "mcp__fixture__ledger_budget_confirm"}]})}]})
        self.assertEqual(second["name"], "mcp__fixture__ledger_budget_lookup")
        self.assertTrue(model.discovery_verified)
        third = model.next({"tools": self.declarations(), "messages": [{"role": "tool", "tool_call_id": "lookup",
            "content": '{"remaining_budget":424242,"verification_code":"actual-receipt"}\n\n<system-reminder>extra</system-reminder>'}]})
        self.assertEqual(third["arguments"]["verification_code"], "actual-receipt")
        fourth = model.next({"messages": [{"role": "tool", "tool_call_id": "confirm", "content": '{"confirmation_code":"actual-confirmation"}'}]})
        self.assertIn("actual-confirmation", fourth["content"][0]["text"])
        self.assertTrue(model.receipts_verified)

    def test_missing_search_manifest_or_schema_never_invents_a_call(self):
        model = ScriptedClaudeModel("lazy", "chat")
        model.next({"tools": self.declarations()[:1]})
        result = model.next({"tools": self.declarations(), "messages": []})
        self.assertEqual(result["type"], "message")
        self.assertFalse(model.discovery_verified)
        self.assertTrue(model.issues)

    def test_claude_terminal_requires_no_error_and_complete_jsonl(self):
        text = json.dumps({"type": "result", "subtype": "success", "is_error": False, "result": "answer"}) + "\n"
        self.assertEqual(claude_result(text), {"terminal": True, "text": "answer"})
        self.assertFalse(claude_result(text.rstrip())["terminal"])
        self.assertFalse(claude_result(text.replace('false', 'true'))["terminal"])
        self.assertFalse(claude_result(text.replace('success', 'error_during_execution'))["terminal"])

    def test_lazy_observation_rejects_eager_declarations_and_unknown_builtins(self):
        tools = [{"name": "ToolSearch", "input_schema": {"type": "object"}},
                 {"name": "DeferredToolPlaceholder", "defer_loading": True, "input_schema": {"type": "object"}}]
        self.assertTrue(inspect_claude_tools("lazy", tools)["mode_observed"])
        for extra in ("Bash", "mcp__fixture__ledger_budget_lookup"):
            self.assertFalse(inspect_claude_tools("lazy", tools + [{"name": extra}])["mode_observed"])


if __name__ == "__main__":
    unittest.main()
