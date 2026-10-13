"""Registered task variation must not erase exact evidence or inflate clusters."""

from copy import deepcopy
from dataclasses import asdict
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from batch_plan import _digest, build_plan
from run_acceptance import CaseJournal
from task_registry import analyze_registered, build_registry, read_registry, write_registry
from test_acceptance import BOUNDARIES, CHECKS, record, spec
from test_usage import request, response_usage


THRESHOLDS = {"quality_margin": .02, "first_input_reduction": .45,
              "peak_input_reduction": .45, "total_input_reduction": .10}


def declarations(count=5, repeats=1):
    cases, members = [], []
    for task in range(count):
        for repeat in range(repeats):
            pair_id = f"pair-{task}-{repeat}"
            for mode in ("eager", "lazy"):
                case = spec(mode, pair_id, f"template-{task}")
                for field in ("fixture_hash", "permission_hash", "catalog_hash"):
                    case.scope[field] = hashlib.sha256(f"{task}-{field}".encode()).hexdigest()
                cases.append(asdict(case))
            members.append({"pair_id": pair_id, "route_id": "route-1", "task_id": f"task-{task}",
                            "family_id": "repository", "cluster_id": f"cluster-{task}",
                            "repeat_id": f"repeat-{repeat}"})
    return cases, members


class TaskRegistryTests(unittest.TestCase):
    def setup_run(self, count=5, repeats=1, *, loss=(), missing_usage=(), unknown=False, stop=None, unfinished=()):
        cases, members = declarations(count, repeats)
        if unknown:
            for case in cases:
                case["scope"]["region_id"] = "unknown"
        registry = build_registry("experiment-1", cases, members, "frame".encode().hex().ljust(64, "0"),
                                  "seed-1", THRESHOLDS)
        plan = build_plan("run-1", cases, {"task-registry": _digest(registry),
                          "task-frame": registry["task_frame_sha256"]}, "seed-1", 10000, 10000)
        with tempfile.TemporaryDirectory() as directory:
            journal = CaseJournal(Path(directory) / "cases.jsonl", "run-1")
            journal.lock_plan(plan["plan_sha256"])
            by_id = {case["case_id"]: case for case in cases}
            from run_acceptance import CaseSpec
            for index, identity in enumerate(plan["schedule"]):
                if stop is not None and index >= stop:
                    break
                case = CaseSpec(**by_id[identity])
                journal.start_case(case)
                usage = None if case.pair_id in missing_usage and case.mode == "lazy" else response_usage(200 if case.mode == "eager" else 100)
                record(journal, identity, request(identity, usage))
                checks = {**CHECKS, "format": False} if case.pair_id in loss and case.mode == "lazy" else CHECKS
                if identity not in unfinished:
                    journal.finish_case(identity, checks=checks, boundary_counts=BOUNDARIES,
                                        elapsed_ms=10, failure_class="model")
            return registry, plan, deepcopy(journal.events)

    def test_registered_different_exact_fixtures_form_one_route_without_hash_loss(self):
        registry, plan, events = self.setup_run()
        result = analyze_registered(events, plan, registry)
        group = result["routes"][0]
        self.assertEqual(len(result["routes"]), 1)
        self.assertEqual(group["registered_clusters"], 5)
        self.assertEqual(group["started_cases"], 10)
        self.assertEqual(group["benefits"]["peak_input_tokens"]["conditional_lower_95"], .5)
        self.assertEqual(group["gate_status"], "unverified")
        self.assertEqual(len({member["case_sha256"]["eager"] for member in registry["members"]}), 5)
        self.assertEqual(result["case_set_sha256"], registry["case_set_sha256"])

    def test_repeated_variants_do_not_create_extra_clusters(self):
        registry, plan, events = self.setup_run(count=3, repeats=10)
        group = analyze_registered(events, plan, registry)["routes"][0]
        self.assertEqual(group["started_cases"], 60)
        self.assertEqual(group["registered_clusters"], 3)
        self.assertAlmostEqual(group["quality"]["conditional_lower_95"], -.6315968501359613)
        self.assertIsNone(group["benefits"]["total_input_tokens"]["conditional_lower_95"])

    def test_failure_unknown_usage_and_unknown_scope_remain_visible(self):
        registry, plan, events = self.setup_run(loss=("pair-0-0",), missing_usage=("pair-1-0",), unknown=True)
        result = analyze_registered(events, plan, registry)
        group = result["routes"][0]
        self.assertEqual(result["safe_successes"], 9)
        self.assertEqual(group["success_by_mode"], {"eager": 5, "lazy": 4})
        self.assertEqual(group["quality"]["regression_clusters"], 1)
        self.assertEqual(group["benefits"]["total_input_tokens"]["missing_pairs"], 1)
        self.assertIsNone(group["benefits"]["total_input_tokens"]["observed_cluster_median"])
        self.assertIn("unknown_scope", group["unverified_reasons"])

    def test_not_started_pairs_block_bounds_without_entering_started_denominator(self):
        registry, plan, events = self.setup_run(stop=3)
        result = analyze_registered(events, plan, registry)
        self.assertEqual(result["started_cases"], 3)
        self.assertEqual(result["planned_not_started"], 7)
        self.assertEqual(len(result["routes"][0]["pairs"]), 5)
        self.assertIsNone(result["routes"][0]["quality"]["conditional_lower_95"])

    def test_zero_starts_and_unfinished_case_are_not_omitted(self):
        registry, plan, events = self.setup_run(stop=0)
        result = analyze_registered(events, plan, registry)
        self.assertEqual(result["started_cases"], 0)
        self.assertEqual(result["planned_not_started"], 10)
        self.assertIsNone(result["routes"][0]["quality"]["conditional_lower_95"])
        registry, plan, events = self.setup_run(unfinished=("pair-0-0-lazy",))
        group = analyze_registered(events, plan, registry)["routes"][0]
        self.assertEqual(group["unfinished_cases"], 1)
        self.assertEqual(group["success_by_mode"]["lazy"], 4)
        self.assertEqual(group["quality"]["regression_clusters"], 1)
        self.assertEqual(group["benefits"]["peak_input_tokens"]["missing_pairs"], 1)

    def test_registry_must_be_bound_before_first_case(self):
        registry, plan, events = self.setup_run()
        plan = build_plan("run-1", plan["cases"], {"other": "a" * 64}, "seed-1", 10000, 10000)
        events[0]["plan_sha256"] = plan["plan_sha256"]
        with self.assertRaises(ValueError):
            analyze_registered(events, plan, registry)

    def test_posthoc_registry_edit_and_exact_case_drift_are_rejected(self):
        registry, plan, events = self.setup_run()
        tampered = deepcopy(registry)
        tampered["members"][0]["cluster_id"] = "selected-cluster"
        with self.assertRaises(ValueError):
            analyze_registered(events, plan, tampered)
        for event in events:
            if event["phase"] == "case_started":
                event["spec"]["scope"]["fixture_hash"] = "a" * 64
                break
        with self.assertRaises(ValueError):
            analyze_registered(events, plan, registry)

    def test_route_identity_cannot_pool_different_provider_or_client_mode(self):
        for field in ("provider", "client_mode", "policy_generation"):
            cases, members = declarations(2)
            for case in cases[2:]:
                case["scope"][field] = "9" * 64 if field == "policy_generation" else "other"
            with self.subTest(field=field), self.assertRaises(ValueError):
                build_registry("experiment-1", cases, members, "a" * 64, "seed-1", THRESHOLDS)

    def test_one_fixture_cannot_be_split_into_independent_clusters(self):
        cases, members = declarations(1, 2)
        members[1]["cluster_id"] = "invented-independent-cluster"
        with self.assertRaises(ValueError):
            build_registry("experiment-1", cases, members, "a" * 64, "seed-1", THRESHOLDS)

    def test_all_pairs_required_duplicate_or_unregistered_members_rejected(self):
        cases, members = declarations(2)
        for selected in (members[:1], members + [members[0]], [{**members[0], "pair_id": "not-a-pair"}, members[1]]):
            with self.subTest(members=selected), self.assertRaises(ValueError):
                build_registry("experiment-1", cases, selected, "a" * 64, "seed-1", THRESHOLDS)

    def test_thresholds_and_sampling_frame_must_be_explicit(self):
        cases, members = declarations(1)
        for thresholds in ({}, {**THRESHOLDS, "quality_margin": True}, {**THRESHOLDS, "total_input_reduction": float("nan")}):
            with self.subTest(thresholds=thresholds), self.assertRaises(ValueError):
                build_registry("experiment-1", cases, members, "a" * 64, "seed-1", thresholds)

    def test_canonical_private_file_matches_material_hash_and_cannot_overwrite(self):
        from batch_plan import fingerprint_materials
        registry, _, _ = self.setup_run()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "registry.json"
            write_registry(path, registry)
            self.assertEqual(read_registry(path), registry)
            self.assertEqual(fingerprint_materials({"task-registry": path}), {"task-registry": _digest(registry)})
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                write_registry(path, registry)

    def test_noncanonical_duplicate_nonfinite_and_symlink_registry_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "invalid.json"
            for content in ('{"a":1,"a":2}', '{"a":NaN}', '{ "a": 1 }'):
                path.write_text(content)
                with self.subTest(content=content), self.assertRaises(ValueError):
                    read_registry(path)
            link = Path(directory) / "link.json"
            link.symlink_to(path)
            with self.assertRaises(OSError):
                read_registry(link)

    def test_missing_task_frame_binding_and_boolean_version_rejected(self):
        registry, plan, events = self.setup_run()
        changed = deepcopy(registry)
        changed["schema_version"] = True
        with self.assertRaises(ValueError):
            analyze_registered(events, plan, changed)
        altered = build_plan("run-1", plan["cases"], {"task-registry": _digest(registry)}, "seed-1", 10000, 10000)
        events[0]["plan_sha256"] = altered["plan_sha256"]
        with self.assertRaises(ValueError):
            analyze_registered(events, altered, registry)

    def test_duplicate_route_aliases_and_cluster_family_changes_rejected(self):
        cases, members = declarations(2)
        members[1]["route_id"] = "selected-route"
        with self.assertRaises(ValueError):
            build_registry("experiment-1", cases, members, "a" * 64, "seed-1", THRESHOLDS)
        members[1]["route_id"] = "route-1"
        members[1]["cluster_id"] = members[0]["cluster_id"]
        members[1]["family_id"] = "unrelated-family"
        with self.assertRaises(ValueError):
            build_registry("experiment-1", cases, members, "a" * 64, "seed-1", THRESHOLDS)

    def test_cli_rejects_invalid_journal_without_traceback(self):
        with tempfile.TemporaryDirectory() as directory:
            registry, plan, _ = self.setup_run()
            registry_path = Path(directory) / "registry.json"
            write_registry(registry_path, registry)
            plan_path = Path(directory) / "plan.json"
            plan_path.write_text(json.dumps(plan))
            journal_path = Path(directory) / "cases.jsonl"
            journal_path.write_text("{invalid}\n")
            result = subprocess.run([sys.executable, str(Path(__file__).resolve().parents[1] / "task_registry.py"),
                                     "--journal", str(journal_path), "--plan", str(plan_path),
                                     "--registry", str(registry_path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertEqual(json.loads(result.stdout), {"error": "invalid_evidence", "gate_status": "unverified"})
            self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
