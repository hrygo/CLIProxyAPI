"""Upstream decisions use real declarations/receipts, never fixture nonce state."""

import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from gateway_clients import BridgeModel, upstream_events, validate_gateway_ready


class GatewayClientTests(unittest.TestCase):
    def test_responses_fixture_usage_has_required_total(self):
        model = BridgeModel("codex", "eager", "codex")
        events = upstream_events(model, {"tools": [{"type": "function", "name": "ledger_budget_lookup"}]}, "resp_fixture")
        usage = events[-1]["response"]["usage"]
        self.assertEqual(usage["total_tokens"], usage["input_tokens"] + usage["output_tokens"])

    def test_title_with_builtin_tools_does_not_advance_tool_round(self):
        model = BridgeModel("opencode", "lazy", "codex")
        events = upstream_events(model, {"tools": [{"type": "image_generation"}]}, "resp_title")
        self.assertEqual(model.round, 0)
        self.assertEqual(events[-1]["response"]["output"][0]["content"][0]["text"], "Local fixture title")
        tool_events = upstream_events(model, {"tools": [{"type": "function", "name": "execute"}]}, "resp_tool")
        self.assertEqual(model.round, 1)
        self.assertEqual(tool_events[-1]["response"]["output"][0]["name"], "execute")

    def test_codex_bridge_search_and_alias_require_actual_receipts(self):
        model = BridgeModel("codex", "lazy", "fixture-chat")
        first = model.next({"tools": [{"type": "function", "function": {"name": "tool_search"}}]})
        self.assertEqual(first["name"], "tool_search")
        second = model.next({"tools": [{"type": "function", "function": {"name": "cts_fixture_alias", "description": "Authoritative ledger lookup."}}],
                             "messages": [{"role": "tool", "tool_call_id": "search", "content": json.dumps({"tools": [{"name": "cts_fixture_alias"}]})}]})
        self.assertEqual(second["name"], "cts_fixture_alias")
        self.assertTrue(model.search_result_has_target)
        final = model.next({"messages": [{"role": "tool", "tool_call_id": "invoke", "content": json.dumps({"remaining_budget": 424242, "verification_code": "actual-receipt"})}]})
        self.assertIn("actual-receipt", final["content"][0]["text"])

    def test_translated_codex_wall_time_result_is_decoded(self):
        model = BridgeModel("codex", "eager", "fixture-chat")
        model.next({"tools": [{"type": "function", "function": {"name": "mcp__fixture__ledger_budget_lookup"}}]})
        final = model.next({"messages": [{"role": "tool", "tool_call_id": "invoke", "content": 'Wall time: 0.1 seconds\nOutput:\n{"remaining_budget":424242,"verification_code":"actual-receipt"}'}]})
        self.assertIn("actual-receipt", final["content"][0]["text"])

    def test_missing_signature_does_not_fabricate_an_invocation(self):
        model = BridgeModel("codex", "eager", "fixture-chat")
        item = model.next({"tools": []})
        self.assertEqual(item["type"], "message")
        self.assertFalse(model.invocation_emitted)

    def test_ready_requires_actual_child_identity_and_loopback_contract(self):
        ready = {"version": 1, "scope": "synthetic-loopback-gateway", "pid": 123,
                 "base_url": "http://127.0.0.1:12345/v1", "executor": "fixture-chat",
                 "contract": "openai-chat-v1", "fixture_policy_sha256": "a" * 64}
        validate_gateway_ready(ready, 123, "fixture-chat")
        for key, value in (("pid", 456), ("base_url", "http://example.com/v1"), ("contract", "openai-responses-v1")):
            with self.subTest(key=key), self.assertRaises(ValueError):
                validate_gateway_ready({**ready, key: value}, 123, "fixture-chat")


if __name__ == "__main__":
    unittest.main()
