"""Regression coverage for complete paired performance accounting."""
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from report import paired_summary, numeric_usage


def case(mode, value, success=True, effective=True):
    return {"client": "codex", "model": "fixture", "task": "budget", "mode": mode,
            "safe_task_success": success, "effective_mode": effective,
            "usage": {"first_input_tokens": value, "peak_input_tokens": value,
                      "total_input_tokens": value, "total_output_tokens": 5},
            "elapsed_ms": 10, "status": "finished"}


class ReportTests(unittest.TestCase):
    def test_keeps_failures_and_negative_benefit(self):
        summary = paired_summary([case("eager", 100), case("lazy", 110, success=False)])
        self.assertEqual(summary["started"], 2)
        self.assertEqual(summary["safe_successes"], 1)
        self.assertEqual(summary["pairs"][0]["total_input_reduction"], -0.1)
        self.assertFalse(summary["pairs"][0]["both_successful"])

    def test_unknown_total_is_not_partial_sum_or_zero(self):
        summary = paired_summary([case("eager", 100), case("lazy", None)])
        self.assertIsNone(summary["pairs"][0]["total_input_reduction"])

    def test_unpaired_and_incomplete_cases_remain_in_denominator(self):
        lazy = case("lazy", 50)
        lazy["status"] = "started"
        summary = paired_summary([case("eager", 100), lazy])
        self.assertEqual(summary["started"], 2)
        self.assertEqual(summary["finished"], 1)
        self.assertEqual(summary["safe_successes"], 1)
        self.assertIsNone(summary["pairs"][0]["total_input_reduction"])

    def test_requested_mode_is_insufficient_for_comparison(self):
        summary = paired_summary([case("eager", 100), case("lazy", 50, effective=False)])
        self.assertFalse(summary["pairs"][0]["comparable"])
        self.assertIsNone(summary["pairs"][0]["total_input_reduction"])

    def test_duplicate_case_mode_is_rejected(self):
        with self.assertRaises(ValueError):
            paired_summary([case("eager", 100), case("eager", 100)])

    def test_raw_usage_export_is_numeric_and_allowlisted(self):
        value = numeric_usage({"input_tokens": 3, "output_tokens": True, "total_tokens": "4",
                               "input_tokens_details": {"cached_tokens": 0, "secret": "private"},
                               "auth": "private"})
        self.assertEqual(value, {"input_tokens": 3, "input_tokens_details": {"cached_tokens": 0}})


if __name__ == "__main__":
    unittest.main()
