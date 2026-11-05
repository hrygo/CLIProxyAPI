"""Request-level metering: unknown usage must not become free failed work."""

from copy import deepcopy
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from normalize_usage import aggregate_usage, estimate_cost, normalize_usage


def response_usage(input_tokens=100, output_tokens=10):
    return {"input_tokens": input_tokens, "output_tokens": output_tokens,
            "input_tokens_details": {"cached_tokens": 30, "cache_write_tokens": 20},
            "output_tokens_details": {"reasoning_tokens": 4},
            "total_tokens": input_tokens + output_tokens}


def request(request_id, usage, contract="openai-responses-v1"):
    return {"request_id": request_id, "contract": contract,
            "usage_kind": "request-total", "raw_usage": usage}


class UsageTests(unittest.TestCase):
    def test_openai_cache_is_a_subset_and_reasoning_is_not_added_twice(self):
        raw = response_usage()
        before = deepcopy(raw)
        value = normalize_usage("openai-responses-v1", raw)
        self.assertEqual(value["tokens"]["input_total"], 100)
        self.assertEqual(value["tokens"]["input_uncached"], 50)
        self.assertEqual(value["tokens"]["output_total"], 10)
        self.assertEqual(value["tokens"]["reasoning_output"], 4)
        self.assertEqual(value["input_semantics"], "inclusive")
        self.assertEqual(value["issues"], [])
        self.assertEqual(raw, before)

    def test_chat_uses_its_own_wire_fields(self):
        value = normalize_usage("openai-chat-v1", {
            "prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110,
            "prompt_tokens_details": {"cached_tokens": 30, "cache_write_tokens": 0},
            "completion_tokens_details": {"reasoning_tokens": 4}})
        self.assertEqual(value["tokens"]["input_uncached"], 70)
        self.assertEqual(value["tokens"]["output_total"], 10)

    def test_anthropic_input_excludes_both_cache_buckets(self):
        value = normalize_usage("anthropic-messages-v1", {
            "input_tokens": 50, "cache_read_input_tokens": 30,
            "cache_creation_input_tokens": 20, "output_tokens": 10,
            "cache_creation": {"ephemeral_5m_input_tokens": 5, "ephemeral_1h_input_tokens": 15}})
        self.assertEqual(value["tokens"]["input_total"], 100)
        self.assertEqual(value["tokens"]["input_uncached"], 50)
        self.assertEqual(value["input_semantics"], "exclusive")
        self.assertEqual(value["cache_write_by_ttl"], {"5m": 5, "1h": 15})

    def test_gemini_cache_overlaps_prompt_but_thoughts_are_separate(self):
        value = normalize_usage("gemini-generate-v1", {
            "promptTokenCount": 100, "cachedContentTokenCount": 30,
            "candidatesTokenCount": 10, "thoughtsTokenCount": 4,
            "totalTokenCount": 114, "toolUsePromptTokenCount": 3})
        self.assertEqual(value["tokens"]["input_total"], 100)
        self.assertEqual(value["tokens"]["output_total"], 14)
        self.assertEqual(value["tokens"]["tool_prompt_tokens"], 3)
        self.assertIsNone(value["tokens"]["cache_write"])
        self.assertIsNone(value["tokens"]["input_uncached"])

    def test_missing_usage_and_unknown_contract_never_guess(self):
        for raw in (None, {}, {"input_tokens": 100}):
            value = normalize_usage("anthropic-messages-v1", raw)
            self.assertIsNone(value["tokens"]["input_total"])
            self.assertIn("input_total", value["missing_fields"])
        value = normalize_usage("muse", response_usage())
        self.assertTrue(all(count is None for count in value["tokens"].values()))
        self.assertIn("unknown_contract", value["issues"])

    def test_absent_cache_write_is_unknown_even_with_known_input_total(self):
        raw = response_usage()
        del raw["input_tokens_details"]["cache_write_tokens"]
        value = normalize_usage("openai-responses-v1", raw)
        self.assertEqual(value["tokens"]["input_total"], 100)
        self.assertIsNone(value["tokens"]["cache_write"])
        self.assertIsNone(value["tokens"]["input_uncached"])

    def test_invalid_counters_are_rejected_without_coercion(self):
        for count in (-1, True, "100", 1.5, float("nan")):
            raw = response_usage()
            raw["input_tokens"] = count
            value = normalize_usage("openai-responses-v1", raw)
            self.assertIsNone(value["tokens"]["input_total"])
            self.assertIn("invalid_counter:input_tokens", value["issues"])

    def test_overlapping_impossible_cache_and_wrong_total_are_not_billable(self):
        raw = response_usage()
        raw["input_tokens_details"]["cached_tokens"] = 90
        value = normalize_usage("openai-responses-v1", raw)
        self.assertIn("cache_exceeds_input", value["issues"])
        self.assertIsNone(value["tokens"]["input_uncached"])
        raw = response_usage()
        raw["total_tokens"] = 999
        value = normalize_usage("openai-responses-v1", raw)
        self.assertIn("inconsistent_total", value["issues"])
        self.assertIsNone(estimate_cost(value, {}, fixed_cost="0")["cost"])

    def test_all_request_attempts_count_including_retry_and_failure(self):
        records = [request("first", response_usage()), request("retry", response_usage(200, 20))]
        records[0]["outcome"] = "failed"
        value = aggregate_usage(records)
        self.assertEqual(value["request_count"], 2)
        self.assertEqual(value["first_input_tokens"], 100)
        self.assertEqual(value["peak_input_tokens"], 200)
        self.assertEqual(value["total_input_tokens"], 300)
        self.assertEqual(value["total_output_tokens"], 30)

    def test_missing_retry_usage_invalidates_total_and_peak_not_known_subtotal(self):
        value = aggregate_usage([request("first", response_usage()), request("retry", None)])
        self.assertEqual(value["first_input_tokens"], 100)
        self.assertIsNone(value["peak_input_tokens"])
        self.assertIsNone(value["total_input_tokens"])
        self.assertEqual(value["known_total_input_tokens"], 100)
        self.assertEqual(value["unknown_input_requests"], 1)
        empty = aggregate_usage([])
        self.assertIsNone(empty["total_input_tokens"])
        self.assertIsNone(empty["known_total_input_tokens"])

    def test_duplicate_or_snapshot_usage_cannot_be_summed(self):
        record = request("same", response_usage())
        with self.assertRaises(ValueError):
            aggregate_usage([record, deepcopy(record)])
        record["usage_kind"] = "stream-snapshot"
        with self.assertRaises(ValueError):
            aggregate_usage([record])

    def test_contradictory_usage_cannot_substantiate_aggregate_savings(self):
        raw = response_usage()
        raw["total_tokens"] = 999
        value = aggregate_usage([request("inconsistent", raw)])
        self.assertIsNone(value["total_input_tokens"])
        self.assertIsNone(value["peak_input_tokens"])
        self.assertEqual(value["invalid_usage_requests"], 1)

    def test_cost_needs_explicit_prices_currency_and_fixed_charges(self):
        value = normalize_usage("openai-responses-v1", response_usage())
        prices = {"currency": "USD", "price_source": "fixture-price",
                  "input_uncached": "2", "cache_read": "0.5", "cache_write": "3", "output_total": "10"}
        cost = estimate_cost(value, prices, fixed_cost="0")
        self.assertEqual(cost["cost"], "0.000275")
        self.assertTrue(cost["complete"])
        self.assertIsNone(estimate_cost(value, prices)["cost"])
        del prices["cache_write"]
        partial = estimate_cost(value, prices, fixed_cost="0")
        self.assertFalse(partial["complete"])
        self.assertIsNone(partial["cost"])
        self.assertEqual(partial["known_subtotal"], "0.000215")

    def test_ttl_write_buckets_require_matching_prices(self):
        value = normalize_usage("anthropic-messages-v1", {
            "input_tokens": 0, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 20,
            "cache_creation": {"ephemeral_5m_input_tokens": 5, "ephemeral_1h_input_tokens": 15}, "output_tokens": 0})
        prices = {"currency": "USD", "price_source": "fixture-price", "cache_write": "1"}
        self.assertFalse(estimate_cost(value, prices, fixed_cost="0")["complete"])
        prices.update(cache_write_5m="1", cache_write_1h="2")
        self.assertEqual(estimate_cost(value, prices, fixed_cost="0")["cost"], "0.000035")

    def test_raw_extra_fields_never_reach_normalized_export(self):
        raw = response_usage()
        raw["api_key"] = "fixture-private-value"
        value = normalize_usage("openai-responses-v1", raw)
        self.assertNotIn("fixture-private-value", str(value))
        self.assertNotIn("api_key", str(value))


if __name__ == "__main__":
    unittest.main()
