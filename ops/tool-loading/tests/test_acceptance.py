"""Intent-to-treat accounting; synthetic records are never live certificates."""

from copy import deepcopy
import json
from pathlib import Path
import sys
import subprocess
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from run_acceptance import CaseJournal, CaseSpec, read_journal, summarize
from validate_evidence import validate_evidence
from test_manifest_schema import fixture_manifest
from test_usage import request, response_usage


def spec(mode="eager", pair_id="pair-1", template="template-1"):
    scope = fixture_manifest()["scope"]
    scope["client_mode"] = "direct" if mode == "eager" else "tool-search"
    return CaseSpec(case_id=pair_id + "-" + mode, pair_id=pair_id, template_id=template,
                    mode=mode, cache_condition="observed", scope=scope,
                    prompt_hash="1" * 64, fixture_seed="fixture-seed-1",
                    limits={"max_requests": 8, "max_dispatches": 8, "task_seconds": 60},
                    gold={"requires_dispatch": True, "requires_nonce": True,
                          "requires_code": False, "format_required": True})


CHECKS = {"answer": True, "dispatch": True, "nonce": True, "code": None,
          "format": True, "terminal": True}
BOUNDARIES = {"mcp_attempts": 1, "dispatch": 1, "forbidden_dispatch": 0,
              "forbidden_effect": 0, "active": 0}


def record(journal, case_id, value):
    journal.start_request(case_id, value["request_id"], value["contract"])
    journal.record_request(case_id, value)


class AcceptanceTests(unittest.TestCase):
    def test_repeat_read_oracle_does_not_bypass_locked_dispatch_budget(self):
        from task_scenarios import dispatch_matches
        for count, expected in ((8,True),(9,False)):
            with self.subTest(dispatches=count), tempfile.TemporaryDirectory() as temporary:
                events=[{"phase":phase,"tool":"ledger_budget_lookup"}
                        for _ in range(count) for phase in ("dispatch","completed","released")]
                self.assertTrue(dispatch_matches("lookup",events))
                journal=self.journal(temporary)
                case=spec()
                journal.start_case(case)
                record(journal,case.case_id,request("lookup-request",response_usage()))
                self.finish(journal,case,boundaries={**BOUNDARIES,"mcp_attempts":count,"dispatch":count})
                result=summarize(read_journal(journal.path))["cases"][0]
                self.assertEqual(result["safe_task_success"],expected)
                if not expected:
                    self.assertEqual(result["failure_class"],"budget")

    def journal(self, temporary):
        return CaseJournal(Path(temporary) / "events.jsonl", "run-1")

    def finish(self, journal, case, *, checks=None, boundaries=None, failure="unknown"):
        journal.finish_case(case.case_id, checks=CHECKS if checks is None else checks,
                            boundary_counts=BOUNDARIES if boundaries is None else boundaries,
                            elapsed_ms=10, failure_class=failure)

    def test_request_started_without_usage_remains_in_cost_and_request_denominators(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            record(journal, case.case_id, request("known", response_usage()))
            journal.start_request(case.case_id, "interrupted-retry", "openai-responses-v1")
            self.finish(journal, case)
            result = summarize(read_journal(journal.path))["cases"][0]
            self.assertEqual(result["usage"]["request_count"], 2)
            self.assertIsNone(result["usage"]["total_input_tokens"])
            self.assertEqual(result["usage"]["known_total_input_tokens"], 100)
            self.assertFalse(result["safe_task_success"])

    def test_sanitization_retains_malformed_cache_ttl_as_invalid_evidence(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            raw = {"input_tokens": 50, "cache_read_input_tokens": 30,
                   "cache_creation_input_tokens": 20, "output_tokens": 10, "cache_creation": "fixture-secret"}
            record(journal, case.case_id, request("invalid-cache", raw, "anthropic-messages-v1"))
            self.finish(journal, case)
            report = summarize(read_journal(journal.path))
            self.assertIsNone(report["cases"][0]["usage"]["total_input_tokens"])
            self.assertNotIn("fixture-secret", journal.path.read_text())

    def test_started_unfinished_and_failed_cases_stay_in_denominator(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            first, second = spec(), spec("lazy")
            journal.start_case(first)
            record(journal, first.case_id, request("failed-first", response_usage()))
            self.finish(journal, first, checks={**CHECKS, "terminal": False}, failure="protocol")
            journal.start_case(second)
            record(journal, second.case_id, request("unfinished", None))
            report = summarize(read_journal(journal.path))
            self.assertEqual(report["started_cases"], 2)
            self.assertEqual(report["safe_successes"], 0)
            self.assertEqual(report["unfinished_cases"], 1)
            self.assertEqual(report["failure_counts"], {"protocol": 1, "unfinished": 1})
            self.assertIsNone(report["cases"][1]["usage"]["total_input_tokens"])
            self.assertEqual(report["gate_status"], "unverified")

    def test_retry_cost_is_preserved_even_when_later_task_succeeds(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            record(journal, case.case_id, request("failed", response_usage()))
            record(journal, case.case_id, request("success", response_usage(200, 20)))
            self.finish(journal, case)
            result = summarize(read_journal(journal.path))["cases"][0]
            self.assertTrue(result["safe_task_success"])
            self.assertEqual(result["usage"]["request_count"], 2)
            self.assertEqual(result["usage"]["total_input_tokens"], 300)

    def test_forbidden_dispatch_or_effect_overrides_reported_success(self):
        for field in ("forbidden_dispatch", "forbidden_effect", "active"):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                journal = self.journal(temporary)
                case = spec()
                journal.start_case(case)
                self.finish(journal, case, boundaries={**BOUNDARIES, field: 1})
                self.assertFalse(summarize(read_journal(journal.path))["cases"][0]["safe_task_success"])

    def test_hard_format_nonce_and_dispatch_requirements_cannot_be_waived(self):
        for field in ("format", "nonce", "dispatch", "terminal"):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                journal = self.journal(temporary)
                case = spec()
                journal.start_case(case)
                self.finish(journal, case, checks={**CHECKS, field: None})
                self.assertFalse(summarize(read_journal(journal.path))["cases"][0]["safe_task_success"])

    def test_missing_boundaries_are_unknown_and_no_dispatch_is_not_success(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            self.finish(journal, case, boundaries={**BOUNDARIES, "dispatch": 0})
            self.assertFalse(summarize(read_journal(journal.path))["cases"][0]["safe_task_success"])

    def test_budget_overrun_keeps_every_request_and_fails_task(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            case.limits["max_requests"] = 1
            journal.start_case(case)
            for index in range(2):
                record(journal, case.case_id, request(str(index), response_usage()))
            self.finish(journal, case)
            result = summarize(read_journal(journal.path))["cases"][0]
            self.assertFalse(result["safe_task_success"])
            self.assertEqual(result["failure_class"], "budget")
            self.assertEqual(result["usage"]["total_input_tokens"], 200)

    def test_pair_scope_and_template_cluster_are_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            for mode, count in (("eager", 200), ("lazy", 100)):
                case = spec(mode)
                journal.start_case(case)
                record(journal, case.case_id, request(mode, response_usage(count)))
                self.finish(journal, case)
            report = summarize(read_journal(journal.path))
            self.assertEqual(report["template_clusters"], 1)
            pair = report["pairs"][0]
            self.assertTrue(pair["comparable"])
            self.assertEqual(pair["savings"]["total_input_tokens"], 0.5)
            self.assertEqual(pair["template_id"], "template-1")
            self.assertEqual(report["gate_status"], "unverified")

    def test_route_and_mode_groups_do_not_pool_different_providers(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            for pair_id, provider in (("pair-1", "fixture"), ("pair-2", "other-provider")):
                case = spec(pair_id=pair_id)
                case.scope["provider"] = provider
                journal.start_case(case)
                self.finish(journal, case)
            report = summarize(read_journal(journal.path))
            self.assertEqual(len(report["groups"]), 2)
            self.assertEqual({group["scope"]["provider"] for group in report["groups"]}, {"fixture", "other-provider"})
            self.assertTrue(all(group["started_cases"] == 1 for group in report["groups"]))

    def test_new_gateway_revision_requires_new_run_before_task_start(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            journal.start_case(spec())
            changed = spec("lazy")
            changed.scope["gateway_revision"] = "2" * 40
            with self.assertRaises(ValueError):
                journal.start_case(changed)
            self.assertEqual(summarize(read_journal(journal.path))["started_cases"], 1)

    def test_mismatched_revision_permissions_or_cache_never_make_pair(self):
        for container, field in (("scope", "client_revision"), ("scope", "permission_hash"), ("root", "cache_condition")):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                journal = self.journal(temporary)
                for mode in ("eager", "lazy"):
                    case = spec(mode)
                    if mode == "lazy":
                        if container == "scope":
                            case.scope[field] = "2" * 64
                        else:
                            case.cache_condition = "warm"
                    journal.start_case(case)
                    self.finish(journal, case)
                pair = summarize(read_journal(journal.path))["pairs"][0]
                self.assertFalse(pair["comparable"])
                self.assertTrue(all(value is None for value in pair["savings"].values()))

    def test_duplicate_case_pair_request_finish_and_existing_file_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            with self.assertRaises(ValueError):
                journal.start_case(case)
            other = deepcopy(case)
            other.case_id = "another-case"
            with self.assertRaises(ValueError):
                journal.start_case(other)
            value = request("request", response_usage())
            record(journal, case.case_id, value)
            with self.assertRaises(ValueError):
                journal.record_request(case.case_id, value)
            self.finish(journal, case)
            with self.assertRaises(ValueError):
                self.finish(journal, case)
            with self.assertRaises(FileExistsError):
                CaseJournal(journal.path, "run-2")

    def test_truncated_reordered_or_forged_journal_is_not_valid_evidence(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            journal.start_case(spec())
            content = journal.path.read_text()
            journal.path.write_text(content.rstrip("\n"))
            with self.assertRaises(ValueError):
                read_journal(journal.path)
            event = json.loads(content)
            event["sequence"] = 4
            journal.path.write_text(json.dumps(event) + "\n")
            with self.assertRaises(ValueError):
                read_journal(journal.path)

    def test_unrecognized_fields_are_rejected_and_usage_export_is_allowlisted(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            case.scope["api_key"] = "fixture-secret"
            with self.assertRaises(ValueError):
                journal.start_case(case)
            del case.scope["api_key"]
            journal.start_case(case)
            raw = response_usage()
            raw["credential"] = "fixture-secret"
            value = request("request", raw)
            value["authorization"] = "fixture-secret"
            record(journal, case.case_id, value)
            self.finish(journal, case)
            self.assertNotIn("fixture-secret", journal.path.read_text())
            self.assertNotIn("fixture-secret", str(summarize(read_journal(journal.path))))

    def test_cli_replay_and_validator_preserve_failures_without_certification(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = self.journal(temporary)
            case = spec()
            journal.start_case(case)
            record(journal, case.case_id, request("failed", None))
            self.finish(journal, case, checks={**CHECKS, "terminal": False}, failure="protocol")
            root = Path(__file__).resolve().parents[1]
            replay = subprocess.run([sys.executable, str(root / "run_acceptance.py"), "--replay", str(journal.path)], capture_output=True, text=True)
            self.assertEqual(replay.returncode, 0, replay.stderr)
            report = json.loads(replay.stdout)
            self.assertEqual(report["failure_counts"], {"protocol": 1})
            self.assertIsNone(report["cases"][0]["usage"]["total_input_tokens"])
            validation = validate_evidence(journal.path)
            self.assertTrue(validation["journal_valid"])
            self.assertEqual(validation["usage_incomplete_cases"], 1)
            self.assertEqual(validation["gate_status"], "unverified")
            journal.path.write_text("broken\n")
            invalid = subprocess.run([sys.executable, str(root / "validate_evidence.py"), "--journal", str(journal.path)], capture_output=True, text=True)
            self.assertEqual(invalid.returncode, 2)
            self.assertEqual(json.loads(invalid.stdout), {"journal_valid": False, "error": "invalid_evidence"})


if __name__ == "__main__":
    unittest.main()
