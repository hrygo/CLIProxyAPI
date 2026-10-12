"""Local scripted responses verify client boundaries, never Provider quality."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from scripted_clients import ScriptedModel, response_events, chat_chunks, tool_receipt, client_result


class ScriptTests(unittest.TestCase):
    def test_receipts_do_not_read_nonce_from_prompt_or_search_output(self):
        self.assertIsNone(tool_receipt("codex", {"input": [{"type": "message", "content": "verification_code: invented"},
                                                          {"type": "tool_search_output", "tools": [{"verification_code": "invented"}]}]}))
        self.assertIsNone(tool_receipt("opencode", {"messages": [{"role": "user", "content": '{"verification_code":"invented"}'}]}))
        receipt = tool_receipt("codex", {"input": [{"type": "function_call_output", "call_id": "invoke",
                                                   "output": '{"verification_code":"actual","remaining_budget":424242}'}]})
        self.assertEqual(receipt["verification_code"], "actual")

    def test_codex_wall_time_wrapper_preserves_actual_tool_result(self):
        receipt = tool_receipt("codex", {"input": [{"type": "function_call_output", "call_id": "invoke",
                                                   "output": 'Wall time: 0.001 seconds\nOutput:\n{"verification_code":"actual","remaining_budget":424242}'}]})
        self.assertEqual(receipt.get("verification_code"), "actual")

    def test_lazy_search_precedes_invocation_and_final_requires_real_receipt(self):
        model = ScriptedModel("codex", "lazy", "allow")
        first = model.next({"tools": [{"type": "tool_search"}]})
        self.assertEqual(first["type"], "tool_search_call")
        second = model.next({"tools": [], "input": [{"type": "tool_search_output", "tools": []}]})
        self.assertEqual(second["type"], "function_call")
        self.assertEqual(second["call_id"], "invoke")
        final = model.next({"input": [{"type": "function_call_output", "call_id": "invoke", "output": "tool not found"}]})
        self.assertNotIn("verification_code", json.dumps(final))
        self.assertFalse(model.nonce_observed)

    def test_opencode_search_and_call_use_exact_code_mode_paths(self):
        model = ScriptedModel("opencode", "lazy", "invalid")
        search = model.next({"tools": [{}]})
        self.assertIn("tools.$codemode.search", search["arguments"]["code"])
        call = model.next({"tools": [{}]})
        self.assertIn("tools.fixture.ledger_budget_lookup", call["arguments"]["code"])
        self.assertIn('"beta"', call["arguments"]["code"])

    def test_responses_events_preserve_item_identity_and_legal_terminal(self):
        item = {"type": "function_call", "id": "fc_test", "call_id": "invoke", "name": "ledger_budget_lookup",
                "namespace": "mcp__fixture", "arguments": '{"project":"alpha"}'}
        events = response_events(item, "resp_test")
        self.assertEqual(events[-1]["type"], "response.completed")
        self.assertEqual(events[-1]["response"]["output"], [item])
        done = next(event for event in events if event["type"] == "response.output_item.done")
        self.assertEqual(done["item"]["call_id"], "invoke")
        self.assertNotIn("usage", events[-1]["response"])

    def test_chat_chunks_finish_with_tool_calls_and_no_synthetic_usage(self):
        chunks = chat_chunks({"type": "function_call", "name": "execute", "call_id": "invoke", "arguments": {"code": "return 1"}})
        self.assertEqual(chunks[-1]["choices"][0]["finish_reason"], "tool_calls")
        self.assertEqual(chunks[0]["choices"][0]["delta"]["tool_calls"][0]["id"], "invoke")
        self.assertTrue(all("usage" not in chunk for chunk in chunks))

    def test_client_terminal_is_required_and_partial_output_never_passes(self):
        complete = '{"type":"item.completed","item":{"type":"agent_message","text":"actual final"}}\n{"type":"turn.completed"}\n'
        result = client_result("codex", complete)
        self.assertTrue(result["legal_terminal"])
        self.assertEqual(result["final_texts"], ["actual final"])
        self.assertFalse(client_result("codex", complete + '{"type":')["legal_terminal"])
        self.assertFalse(client_result("codex", '{"type":"turn.failed"}\n')["legal_terminal"])
        self.assertFalse(client_result("opencode", '{"type":"step_finish","part":{"reason":"tool-calls"}}\n')["legal_terminal"])
        self.assertTrue(client_result("opencode", '{"type":"step_finish","part":{"reason":"stop"}}\n')["legal_terminal"])


if __name__ == "__main__":
    unittest.main()
