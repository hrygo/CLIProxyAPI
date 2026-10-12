"""Conservative diagnostic bounds must never certify tiny or selected samples."""

from copy import deepcopy
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from analyze_pairs import analyze_pairs, binomial_upper, median_lower
from run_acceptance import CaseJournal
from test_acceptance import BOUNDARIES, CHECKS, record, spec
from test_usage import request, response_usage


class PairAnalysisTests(unittest.TestCase):
    def data(self, count=3, *, repeats=1, losses=(), missing=(), unfinished=(), unknown=False):
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", "run-1")
            for template in range(count):
                for repeat in range(repeats):
                    for mode in ("eager", "lazy"):
                        case = spec(mode, f"pair-{template}-{repeat}", f"template-{template}")
                        if unknown:
                            case.scope["region_id"] = "unknown"
                        journal.start_case(case)
                        raw = None if (template, mode) in missing else response_usage(200 if mode == "eager" else 100)
                        record(journal, case.case_id, request(case.case_id, raw))
                        if (template, mode) not in unfinished:
                            checks = {**CHECKS, "format": False} if mode == "lazy" and template in losses else CHECKS
                            journal.finish_case(case.case_id, checks=checks, boundary_counts=BOUNDARIES,
                                                elapsed_ms=10, failure_class="model")
            return deepcopy(journal.events)

    def test_exact_bound_matches_nist_example_and_zero_loss_formula(self):
        self.assertAlmostEqual(binomial_upper(4, 20), 0.401029, delta=1e-6)
        self.assertAlmostEqual(binomial_upper(1, 2), .95 ** .5, places=12)
        self.assertAlmostEqual(binomial_upper(2, 3), .95 ** (1 / 3), places=12)
        self.assertAlmostEqual(binomial_upper(0, 3), 1 - 0.05 ** (1 / 3), places=12)
        self.assertEqual(binomial_upper(3, 3), 1)
        self.assertGreater(binomial_upper(0, 148), 0.02)
        self.assertLess(binomial_upper(0, 149), 0.02)

    def test_invalid_counts_and_probabilities_fail_closed(self):
        for failures, count, alpha in ((True, 3, .05), (0, False, .05), (-1, 3, .05),
                                       (4, 3, .05), (0, 0, .05), (0, 3, True),
                                       (0, 3, 0), (0, 3, 1), (0, 3, float("nan"))):
            with self.subTest(values=(failures, count, alpha)), self.assertRaises(ValueError):
                binomial_upper(failures, count, alpha)

    def test_three_clusters_have_no_finite_median_lower_bound(self):
        self.assertIsNone(median_lower([.5, .6, .7]))
        self.assertEqual(median_lower([.1, .2, .3, .4, .5]), .1)
        self.assertEqual(median_lower(list(range(10))), 1)
        self.assertIsNone(median_lower([]))

    def test_nonfinite_or_boolean_benefits_are_rejected(self):
        for value in (True, float("nan"), float("inf"), "0.5"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                median_lower([value] * 5)

    def test_zero_losses_are_not_a_zero_width_quality_interval(self):
        group = analyze_pairs(self.data())["groups"][0]
        self.assertEqual(group["quality"]["cluster_mean_difference"], 0)
        self.assertAlmostEqual(group["quality"]["conditional_lower_95"], -.63159685, places=7)
        self.assertEqual(group["gate_status"], "unverified")
        self.assertEqual(group["benefits"]["total_input_tokens"]["observed_cluster_median"], .5)
        self.assertIsNone(group["benefits"]["total_input_tokens"]["conditional_lower_95"])

    def test_repeats_do_not_manufacture_independent_clusters(self):
        one = analyze_pairs(self.data())["groups"][0]
        repeated = analyze_pairs(self.data(repeats=20))["groups"][0]
        self.assertEqual(repeated["started_cases"], 120)
        self.assertEqual(repeated["matched_pairs"], 60)
        self.assertEqual(repeated["template_clusters"], 3)
        self.assertEqual(repeated["quality"]["conditional_lower_95"], one["quality"]["conditional_lower_95"])
        self.assertIsNone(repeated["benefits"]["total_input_tokens"]["conditional_lower_95"])

    def test_failed_and_unfinished_tasks_remain_in_success_denominators(self):
        report = analyze_pairs(self.data(losses=(0,), unfinished=((1, "lazy"),)))
        group = report["groups"][0]
        self.assertEqual(report["started_cases"], 6)
        self.assertEqual(report["unfinished_cases"], 1)
        self.assertEqual(group["success_by_mode"], {"eager": 3, "lazy": 1})
        self.assertEqual(group["quality"]["regression_clusters"], 2)
        self.assertEqual(group["benefits"]["total_input_tokens"]["missing_pairs"], 1)
        self.assertIsNone(group["benefits"]["total_input_tokens"]["observed_cluster_median"])

    def test_missing_cost_cannot_be_replaced_by_known_subset(self):
        group = analyze_pairs(self.data(missing=((1, "lazy"),)))["groups"][0]
        metric = group["benefits"]["total_input_tokens"]
        self.assertEqual(metric["known_pairs"], 2)
        self.assertEqual(metric["missing_pairs"], 1)
        self.assertIsNone(metric["observed_cluster_median"])
        self.assertIsNone(metric["conditional_lower_95"])

    def test_unknown_scope_has_descriptive_values_but_no_certification(self):
        group = analyze_pairs(self.data(unknown=True))["groups"][0]
        self.assertEqual(group["unknown_scope_pairs"], 3)
        self.assertEqual(group["matched_pairs"], 3)
        self.assertEqual(group["benefits"]["first_input_tokens"]["observed_cluster_median"], .5)
        self.assertEqual(group["gate_status"], "unverified")
        self.assertIn("unknown_scope", group["unverified_reasons"])

    def test_unpaired_and_scope_mismatched_cases_block_aggregate_bounds(self):
        events = self.data()
        events = [e for e in events if e.get("case_id") != "pair-2-0-lazy"]
        for i, event in enumerate(events):
            event["sequence"] = i
        group = analyze_pairs(events)["groups"][0]
        self.assertEqual(group["started_cases"], 5)
        self.assertEqual(group["unmatched_pairs"], 1)
        self.assertEqual(group["template_clusters"], 3)
        self.assertEqual(group["matched_template_clusters"], 2)
        self.assertEqual(group["journaled_request_attempts"], 5)
        self.assertIsNone(group["quality"]["conditional_lower_95"])
        events = self.data()
        for event in events:
            if event["phase"] == "case_started" and event["spec"]["mode"] == "lazy":
                event["spec"]["prompt_hash"] = "2" * 64
        group = analyze_pairs(events)["groups"][0]
        self.assertEqual(group["unmatched_pairs"], 3)
        self.assertIsNone(group["benefits"]["total_input_tokens"]["observed_cluster_median"])

    def test_distinct_route_scopes_are_never_pooled(self):
        events = self.data()
        for event in events:
            if event["phase"] == "case_started" and event["spec"]["template_id"] == "template-2":
                event["spec"]["scope"]["provider"] = "other-provider"
        groups = analyze_pairs(events)["groups"]
        self.assertEqual(sorted(g["started_cases"] for g in groups), [2, 4])
        self.assertEqual(sorted(g["template_clusters"] for g in groups), [1, 2])

    def test_negative_benefits_and_failed_cases_are_included(self):
        events = self.data(count=5, losses=range(5))
        for event in events:
            if event["phase"] == "request_finished" and event["case_id"].endswith("lazy"):
                event["request"]["raw_usage"] = response_usage(300)
        group = analyze_pairs(events)["groups"][0]
        self.assertEqual(group["success_by_mode"]["lazy"], 0)
        self.assertEqual(group["quality"]["conditional_lower_95"], -1)
        self.assertEqual(group["benefits"]["total_input_tokens"]["observed_cluster_median"], -.5)
        self.assertEqual(group["benefits"]["total_input_tokens"]["conditional_lower_95"], -.5)

    def test_cli_rejects_malformed_journal_without_traceback_or_certificate(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "invalid.jsonl"
            path.write_text("{invalid}\n")
            result = subprocess.run([sys.executable, str(Path(__file__).resolve().parents[1] / "analyze_pairs.py"),
                                     "--journal", str(path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn('"error": "invalid_evidence"', result.stdout)
            self.assertNotIn("Traceback", result.stderr)

    def test_cli_validates_supplied_plan_instead_of_ignoring_it(self):
        import json
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "cases.jsonl"
            path.write_text("".join(json.dumps(event) + "\n" for event in self.data()))
            plan = Path(temporary) / "invalid-plan.json"
            plan.write_text("{}\n")
            result = subprocess.run([sys.executable, str(Path(__file__).resolve().parents[1] / "analyze_pairs.py"),
                                     "--journal", str(path), "--plan", str(plan)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn('"error": "invalid_evidence"', result.stdout)
            self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
