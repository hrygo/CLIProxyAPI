import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from live_clients import select_model_metadata, result_checks, chat_terminal, endpoint_id


class LiveClientTests(unittest.TestCase):
    def test_endpoint_identifier_is_derived_from_complete_digest(self):
        self.assertEqual(endpoint_id("a" * 64), "ep_" + "a" * 32)
        for value in ("a" * 33, "unknown", "A" * 64, None):
            with self.assertRaises(ValueError):
                endpoint_id(value)

    def test_real_catalog_required_without_synthetic_fallback(self):
        model = {"slug": "actual-alias", "tool_mode": "direct", "context_window": 200000, "supports_search_tool": True}
        self.assertEqual(select_model_metadata({"models": [model]}, "actual-alias", "codex", "lazy"), model)
        self.assertEqual(select_model_metadata({"models": [{**model, "tool_mode": None}]}, "actual-alias", "codex", "lazy")["tool_mode"], None)
        for body in ({"data": [{"id": "actual-alias"}]}, {"models": []}, {"models": [model, model]}):
            with self.assertRaises(ValueError):
                select_model_metadata(body, "actual-alias", "codex", "lazy")
        with self.assertRaises(ValueError):
            select_model_metadata({"models": [{**model, "supports_search_tool": False}]}, "actual-alias", "codex", "lazy")

    def test_gold_uses_actual_final_receipt_and_client_terminal(self):
        def output(text, terminal=True):
            events = [{"type": "item.completed", "item": {"type": "agent_message", "text": text}}]
            if terminal:
                events.append({"type": "turn.completed"})
            return "".join(json.dumps(event) + "\n" for event in events)
        answer = json.dumps({"remaining_budget": 424242, "verification_code": "actual-receipt"})
        self.assertTrue(result_checks("codex", output(answer), "actual-receipt")["answer"])
        self.assertFalse(result_checks("codex", output(answer), "different")["nonce"])
        self.assertFalse(result_checks("codex", output(answer, False), "actual-receipt")["terminal"])
        self.assertFalse(result_checks("codex", output("guessed 424242 actual-receipt"), "actual-receipt")["answer"])
        fenced = result_checks("codex", output("```json\n" + answer + "\n```"), "actual-receipt")
        self.assertTrue(fenced["answer"])
        self.assertTrue(fenced["nonce"])
        self.assertFalse(fenced["format"])
        earlier = {"type": "item.completed", "item": {"type": "agent_message", "text": answer}}
        self.assertFalse(result_checks("codex", json.dumps(earlier) + "\n" + output("wrong final answer"), "actual-receipt")["answer"])

    def test_eof_and_done_do_not_substitute_for_chat_finish_reason(self):
        self.assertFalse(chat_terminal(b'data: [DONE]\n\n'))
        self.assertFalse(chat_terminal(b'data: {"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}\n\n'))
        self.assertTrue(chat_terminal(b'data: {"choices":[{"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n'))
        self.assertFalse(chat_terminal(b'data: {"choices":[{"finish_reason":"length"}]}\n\n'))
        self.assertFalse(chat_terminal(b'{"choices":null}'))
        self.assertFalse(chat_terminal(b'{"choices":[{"finish_reason":{}}]}'))


if __name__ == "__main__":
    unittest.main()
