"""Native Claude output must bind the final result to an actual tool invocation."""

from copy import deepcopy
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from task_scenarios import output_schema


class ClaudeStructuredTests(unittest.TestCase):
    def events(self, value=None):
        value = value or {"remaining_budget": 424242, "verification_code": "actual-receipt"}
        return [{"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "output-1",
                    "name": "StructuredOutput", "input": deepcopy(value)}]}},
                {"type": "result", "subtype": "success", "is_error": False, "result": "",
                 "structured_output": deepcopy(value)}]

    def observe(self, events):
        from scripted_clients import claude_structured_result
        return claude_structured_result("".join(json.dumps(event) + "\n" for event in events), output_schema("lookup"))

    def test_final_structured_object_requires_matching_native_invocation(self):
        events = self.events()
        observed = self.observe(events)
        self.assertTrue(observed["legal_terminal"])
        self.assertTrue(observed["schema_valid"])
        self.assertTrue(observed["invocation_verified"])
        self.assertEqual(observed["structured_output"], events[-1]["structured_output"])
        for mutate in (lambda e: e.pop(0),
                       lambda e: e[0]["message"]["content"][0].update(name="Other"),
                       lambda e: e[0]["message"]["content"][0].update(id=""),
                       lambda e: e[0]["message"]["content"][0]["input"].update(verification_code="guessed")):
            changed = deepcopy(events)
            mutate(changed)
            self.assertFalse(self.observe(changed)["invocation_verified"])

    def test_bad_schema_error_terminal_and_truncated_stream_fail_closed(self):
        from scripted_clients import claude_structured_result
        good = self.events()
        for changed in ({"is_error": True}, {"subtype": "error_max_structured_output_retries"},
                        {"structured_output": {"remaining_budget": True, "verification_code": "actual-receipt"}},
                        {"structured_output": {"remaining_budget": 424242, "verification_code": "actual-receipt", "extra": 1}}):
            events = deepcopy(good)
            events[-1].update(changed)
            observed = self.observe(events)
            self.assertFalse(observed["legal_terminal"] and observed["schema_valid"] and observed["invocation_verified"])
        raw = "".join(json.dumps(event) + "\n" for event in good)
        for bad in (raw.rstrip(), '42\n' + raw, raw + '{"type":',
                    '{"type":"assistant","error":"invalid_request"}\n' + raw,
                    raw.replace('"remaining_budget": 424242', '"remaining_budget": 1e999'),
                    raw.replace('"remaining_budget": 424242', '"remaining_budget": 0, "remaining_budget": 424242')):
            self.assertFalse(claude_structured_result(bad, output_schema("lookup"))["legal_terminal"])

    def test_latest_native_invocation_wins_without_reusing_earlier_gold(self):
        events = self.events()
        earlier = deepcopy(events[0])
        earlier["message"]["content"][0]["id"] = "output-old"
        events[0]["message"]["content"][0]["input"]["verification_code"] = "wrong-final"
        self.assertFalse(self.observe([earlier, *events])["invocation_verified"])
        retry = deepcopy(earlier)
        retry["message"]["content"][0]["input"] = {"remaining_budget": "invalid"}
        self.assertTrue(self.observe([retry, *self.events()])["invocation_verified"])
        duplicate = self.events()
        self.assertFalse(self.observe([deepcopy(duplicate[0]), *duplicate])["invocation_verified"])


if __name__ == "__main__":
    unittest.main()
