"""Predeclared order, material drift and conservative batch budgets."""

from copy import deepcopy
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from batch_plan import build_plan, validate_plan, fingerprint_materials, verify_materials, BatchBudget, audit_plan, write_plan, read_plan
from run_acceptance import CaseJournal
from test_acceptance import spec, CHECKS, BOUNDARIES


def matrix(routes=10, templates=3):
    cases = []
    for route in range(routes):
        for template in range(templates):
            for mode in ("eager", "lazy"):
                case = spec(mode, f"route-{route}-template-{template}", f"template-{template}")
                case.scope["model"] = f"model-{route}"
                cases.append(case)
    return cases


def plan(cases=None, **changes):
    cases = matrix() if cases is None else cases
    return build_plan("run-plan", cases, {"gateway-binary": "a" * 64, "source-config": "b" * 64},
                      "schedule-seed-1", changes.get("requests", len(cases) * 8), changes.get("dispatches", len(cases) * 8))


class BatchPlanTests(unittest.TestCase):
    def test_retroactive_plan_without_durable_lock_is_rejected(self):
        from run_acceptance import CaseSpec
        value = plan(matrix(1, 1))
        declarations = {case["case_id"]:case for case in value["cases"]}
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", value["run_id"])
            journal.start_case(CaseSpec(**declarations[value["schedule"][0]]))
            with self.assertRaises(ValueError): audit_plan(value, journal.events)

    def test_sixty_slots_are_deterministic_balanced_within_each_route(self):
        value = plan()
        self.assertEqual(value, plan(list(reversed(matrix()))))
        self.assertEqual(len(value["schedule"]), 60)
        by_id = {case["case_id"]: case for case in value["cases"]}
        first_modes = {}
        for index in range(0, len(value["schedule"]), 2):
            first, second = (by_id[key] for key in value["schedule"][index:index + 2])
            self.assertEqual(first["pair_id"], second["pair_id"])
            self.assertNotEqual(first["mode"], second["mode"])
            first_modes.setdefault(first["scope"]["model"], []).append(first["mode"])
        for modes in first_modes.values():
            self.assertLessEqual(abs(modes.count("eager") - modes.count("lazy")), 1)
        all_modes = [mode for modes in first_modes.values() for mode in modes]
        self.assertEqual(all_modes.count("eager"), all_modes.count("lazy"))
        self.assertEqual(value["gate_status"], "unverified")

    def test_pair_scope_prompt_gold_and_limits_cannot_drift(self):
        for field in ("scope", "prompt_hash", "gold", "limits", "fixture_seed", "cache_condition"):
            with self.subTest(field=field):
                cases = matrix(1, 1)
                if field == "scope": cases[1].scope["policy_generation"] = "c" * 64
                elif field == "gold": cases[1].gold["requires_nonce"] = False
                elif field == "limits": cases[1].limits["max_requests"] = 7
                elif field == "prompt_hash": cases[1].prompt_hash = "2" * 64
                elif field == "cache_condition": cases[1].cache_condition = "cold"
                else: cases[1].fixture_seed = "other-seed"
                with self.assertRaises(ValueError): plan(cases)

    def test_duplicate_missing_modes_and_mixed_revision_are_rejected(self):
        for cases in (matrix(1, 1)[:1], matrix(1, 1) + matrix(1, 1)):
            with self.assertRaises(ValueError): plan(cases)
        cases = matrix(2, 1)
        cases[-1].scope["gateway_revision"] = "d" * 40
        with self.assertRaises(ValueError): plan(cases)

    def test_mutated_plan_hash_schedule_and_export_fields_fail_closed(self):
        value = plan()
        for mutation in ("hash", "schedule", "scope", "secret"):
            with self.subTest(mutation=mutation):
                bad = deepcopy(value)
                if mutation == "hash": bad["plan_sha256"] = "0" * 64
                elif mutation == "schedule": bad["schedule"].reverse()
                elif mutation == "scope": bad["cases"][0]["scope"]["model"] = "different"
                else: bad["base_url"] = "https://private.invalid"
                with self.assertRaises(ValueError): validate_plan(bad)

    def test_unknown_scope_is_retained_without_certification(self):
        cases = matrix(1, 1)
        for case in cases: case.scope["region_id"] = "unknown"
        value = plan(cases)
        validate_plan(value)
        self.assertEqual(value["cases"][0]["scope"]["region_id"], "unknown")
        self.assertEqual(value["gate_status"], "unverified")

    def test_material_drift_reuse_symlink_and_extra_material_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "binary"
            config = root / "config"
            binary.write_bytes(b"synthetic-binary")
            config.write_bytes(b"synthetic-private-key")
            paths = {"gateway-binary": binary, "source-config": config}
            fingerprints = fingerprint_materials(paths)
            self.assertNotIn("synthetic-private-key", json.dumps(fingerprints))
            value = build_plan("run-plan", matrix(1, 1), fingerprints, "seed", 16, 16)
            verify_materials(value, paths)
            write_plan(root / "plan.json", value)
            self.assertEqual(read_plan(root / "plan.json"), value)
            self.assertEqual((root / "plan.json").stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError): write_plan(root / "plan.json", value)
            config.write_bytes(b"changed")
            with self.assertRaises(ValueError): verify_materials(value, paths)
            alias = root / "alias"
            alias.symlink_to(binary)
            with self.assertRaises(ValueError): fingerprint_materials({"gateway-binary": alias})
            with self.assertRaises(ValueError): verify_materials(value, {**paths, "extra": binary})

    def test_request_and_dispatch_reservation_prevent_unbounded_batch(self):
        value = plan(matrix(1, 2), requests=10, dispatches=10)
        budget = BatchBudget(value)
        first = budget.admit_next()
        self.assertIsNotNone(first)
        with self.assertRaises(ValueError): budget.admit_next()
        budget.settle(first["case_id"], requests=3, dispatches=1)
        self.assertIsNone(budget.admit_next())
        self.assertEqual(budget.stop_reason, "request_budget")
        self.assertEqual(budget.remaining_requests, 7)

    def test_unknown_attempt_count_does_not_refund_reserved_requests(self):
        budget = BatchBudget(plan(matrix(1, 1), requests=16, dispatches=16))
        first = budget.admit_next()
        budget.settle(first["case_id"], requests=None, dispatches=None)
        self.assertEqual(budget.remaining_requests, 8)
        self.assertEqual(budget.remaining_dispatches, 8)
        second = budget.admit_next()
        with self.assertRaises(ValueError): budget.settle(second["case_id"], requests=9, dispatches=1)
        with self.assertRaises(ValueError): budget.settle("wrong-case", requests=0, dispatches=0)
        budget.settle(second["case_id"], requests=8, dispatches=8)
        self.assertIsNone(budget.admit_next())
        self.assertEqual(budget.stop_reason, "complete")

    def test_audit_retains_failed_and_unfinished_cases_and_requires_exact_order(self):
        value = plan(matrix(1, 1))
        cases = {case["case_id"]: case for case in value["cases"]}
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", value["run_id"])
            journal.lock_plan(value["plan_sha256"])
            first = value["schedule"][0]
            from run_acceptance import CaseSpec
            journal.start_case(CaseSpec(**cases[first]))
            journal.start_request(first, "request-1", "openai-responses-v1")
            report = audit_plan(value, journal.events)
            self.assertEqual(report["planned_not_started"], 1)
            self.assertEqual(report["unfinished_cases"], 1)
            self.assertIsNone(report["total_dispatches"])
            journal.finish_case(first, checks={**CHECKS, "answer": False}, boundary_counts=BOUNDARIES,
                                elapsed_ms=1, failure_class="model")
            report = audit_plan(value, journal.events)
            self.assertEqual(report["safe_successes"], 0)
            self.assertEqual(report["request_attempts"], 1)
            self.assertEqual(report["gate_status"], "unverified")
            altered = deepcopy(journal.events)
            next(event for event in altered if event["phase"] == "case_started")["spec"]["prompt_hash"] = "0" * 64
            with self.assertRaises(ValueError): audit_plan(value, altered)
            with self.assertRaises(ValueError): audit_plan({**value, "run_id": "other-run"}, journal.events)

    def test_invalid_budget_boolean_material_hash_and_revision_are_rejected(self):
        for changes in ({"requests": True}, {"dispatches": 0}):
            with self.assertRaises(ValueError): plan(matrix(1, 1), **changes)
        with self.assertRaises(ValueError): build_plan("run", matrix(1, 1), {"source-config": "unknown"}, "seed", 16, 16)
        cases = matrix(1, 1)
        for case in cases: case.scope["gateway_revision"] = "HEAD"
        with self.assertRaises(ValueError): plan(cases)

    def test_plan_reader_rejects_duplicate_keys_nonfinite_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = root / "plan.json"
            for raw in ('{"schema_version":1,"schema_version":1}', '{"schema_version":NaN}'):
                path.write_text(raw)
                with self.assertRaises(ValueError): read_plan(path)
            alias = root / "alias"
            alias.symlink_to(path)
            with self.assertRaises(ValueError): read_plan(alias)

    def test_durable_lock_cannot_be_added_late_or_repeated_or_changed(self):
        from run_acceptance import CaseSpec
        value = plan(matrix(1, 1))
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            journal = CaseJournal(root / "locked.jsonl", value["run_id"])
            journal.lock_plan(value["plan_sha256"])
            with self.assertRaises(ValueError): journal.lock_plan(value["plan_sha256"])
            other = plan(matrix(1, 1), requests=15)
            with self.assertRaises(ValueError): audit_plan(other, journal.events)
            late = CaseJournal(root / "late.jsonl", value["run_id"])
            declaration = next(case for case in value["cases"] if case["case_id"] == value["schedule"][0])
            late.start_case(CaseSpec(**declaration))
            with self.assertRaises(ValueError): late.lock_plan(value["plan_sha256"])
            self.assertEqual(len(late.events), 1)

    def test_validator_binds_journal_to_plan_and_distinguishes_binding_error(self):
        from validate_evidence import validate_evidence
        from run_acceptance import CaseSpec
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            value = plan(matrix(1, 1))
            write_plan(root / "plan.json", value)
            journal = CaseJournal(root / "cases.jsonl", value["run_id"])
            journal.lock_plan(value["plan_sha256"])
            declarations = {case["case_id"]:case for case in value["cases"]}
            journal.start_case(CaseSpec(**declarations[value["schedule"][0]]))
            bound = validate_evidence(journal.path, plan_path=root / "plan.json")
            self.assertTrue(bound["journal_valid"])
            self.assertTrue(bound["batch_plan_valid"])
            self.assertEqual(bound["batch_audit"]["unfinished_cases"], 1)
            (root / "plan.json").write_text("{}")
            bad = validate_evidence(journal.path, plan_path=root / "plan.json")
            self.assertTrue(bad["journal_valid"])
            self.assertFalse(bad["batch_plan_valid"])
            self.assertEqual(bad["batch_error"], "invalid_batch_binding")


if __name__ == "__main__":
    unittest.main()
