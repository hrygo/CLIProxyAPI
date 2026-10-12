"""Physical auxiliary attempts remain counted without fabricated token usage."""
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from normalize_usage import aggregate_usage
from run_acceptance import CaseJournal, summarize, read_journal
from test_acceptance import spec, CHECKS, BOUNDARIES
from test_http_attempts import artifact
from http_attempts import import_http_attempts, read_http_attempts, read_auxiliary_work_diagnostics


def generate():
    return {"request_id": "request-000002", "contract": "antigravity-generate-v1", "usage_kind": "request-total",
            "raw_usage": {"promptTokenCount": 20, "candidatesTokenCount": 3, "thoughtsTokenCount": 2,
                          "totalTokenCount": 25, "private": "synthetic-private-payload"}}


def auxiliary():
    return {"request_id": "request-000001", "contract": "antigravity-auxiliary-v1", "usage_kind": "request-total",
            "raw_usage": None, "role": "auxiliary", "operation": "credits_query"}


class AuxiliaryAccountingTests(unittest.TestCase):
    def test_auxiliary_only_and_failed_model_never_fabricate_zero_input(self):
        usage = aggregate_usage([auxiliary()])
        self.assertEqual(usage["request_count"], 1)
        self.assertEqual(usage["model_request_count"], 0)
        for field in ("first_input_tokens", "peak_input_tokens", "total_input_tokens", "total_output_tokens"):
            self.assertIsNone(usage[field])
        failed = {**generate(), "raw_usage": None}
        usage = aggregate_usage([auxiliary(), failed])
        self.assertEqual(usage["request_count"], 2)
        self.assertEqual(usage["unknown_input_requests"], 1)
        self.assertEqual(usage["unknown_auxiliary_cost_requests"], 1)
        self.assertIsNone(usage["total_input_tokens"])
        self.assertIsNone(usage["peak_input_tokens"])

    def test_auxiliary_work_seal_requires_consistent_settlement(self):
        state = {"version": 1, "started": 3, "completed": 3, "rejected": 0, "active": 0, "sealed": True}
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.assertIsNone(read_auxiliary_work_diagnostics(root))
            path = root / "auxiliary-work.json"
            path.write_text(json.dumps(state))
            self.assertEqual(read_auxiliary_work_diagnostics(root), state)
            for changed in ({"active": 1}, {"completed": 2}, {"sealed": False}, {"rejected": 1},
                            {"started": True}, {"private": "secret"}, {"version": 2}):
                path.write_text(json.dumps({**state, **changed}))
                with self.subTest(changed=changed), self.assertRaises(ValueError):
                    read_auxiliary_work_diagnostics(root)
            path.write_text('{"version":1,"version":1}')
            with self.assertRaises(ValueError):
                read_auxiliary_work_diagnostics(root)
            path.unlink()
            target = root / "target.json"
            target.write_text(json.dumps(state))
            path.symlink_to(target)
            with self.assertRaises(ValueError):
                read_auxiliary_work_diagnostics(root)

    def write_http(self, root, *, changed=None, unfinished=False):
        events = []
        identity = {"executor": "antigravity", "auth_sha256": "a" * 64, "model_sha256": "", "operation": "credits_query"}
        if changed:
            identity.update(changed)
        for number, (contract, body, actual) in enumerate([
                ("antigravity-auxiliary-v1", b'{"usage":{"input_tokens":999},"private":"synthetic-secret"}', identity),
                ("antigravity-generate-v1", b'{"response":{"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":25}}}',
                 {"executor": "antigravity", "model_sha256": "b" * 64, "auth_sha256": "a" * 64})], 1):
            rid = f"request-{number:06d}"
            events.append({"version": 2, "sequence": len(events) + 1, "event": "request-started", "request_id": rid,
                           "contract": contract, "executor_identity": actual, "request": artifact(root, rid + ".body", b'{}')})
            if not unfinished or number != 1:
                events.append({"version": 2, "sequence": len(events) + 1, "event": "request-finished", "request_id": rid,
                               "contract": contract, "status_code": 200, "observed_bytes": len(body), "outcome": "eof",
                               "response": artifact(root, f"response-{number:06d}.body", body)})
        (root / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))

    def test_actual_http_auxiliary_import_preserves_budget_usage_and_unfinished_status(self):
        for unfinished in (False, True):
            with self.subTest(unfinished=unfinished), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.write_http(root, unfinished=unfinished)
                journal = CaseJournal(root / "cases.jsonl", "mixed-http")
                case = spec()
                journal.start_case(case)
                attempts = import_http_attempts(journal, case.case_id, root, expected_executor="antigravity")
                self.assertEqual(attempts[0]["role"], "auxiliary")
                self.assertEqual(attempts[0]["operation"], "credits_query")
                self.assertIsNone(attempts[0]["raw_usage"])
                journal.finish_case(case.case_id, checks=CHECKS, boundary_counts=BOUNDARIES, elapsed_ms=100)
                result = summarize(read_journal(journal.path))["cases"][0]
                self.assertEqual(result["usage"]["request_count"], 2)
                self.assertEqual(result["usage"]["total_input_tokens"], 20)
                self.assertEqual(result["safe_task_success"], not unfinished)
                self.assertNotIn("synthetic-secret", journal.path.read_text())

    def test_http_auxiliary_requires_exact_actual_identity(self):
        for changed in ({"executor": "gemini"}, {"auth_sha256": ""}, {"model_sha256": "b" * 64},
                        {"operation": "private-payload"}, {"operation": []}, {"private": "secret"}):
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.write_http(root, changed=changed)
                with self.assertRaises(ValueError):
                    read_http_attempts(root)

    def test_wrapped_usage_survives_the_complete_case_journal_export(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", "antigravity-export")
            case = spec()
            journal.start_case(case)
            value = generate()
            journal.start_request(case.case_id, value["request_id"], value["contract"])
            journal.record_request(case.case_id, value)
            journal.finish_case(case.case_id, checks=CHECKS, boundary_counts=BOUNDARIES, elapsed_ms=100)
            summary = summarize(read_journal(journal.path))
            self.assertEqual(summary["cases"][0]["usage"]["total_input_tokens"], 20)
            self.assertNotIn("synthetic-private-payload", journal.path.read_text())

    def test_mixed_attempts_retain_all_physical_budget_and_unknown_auxiliary_bill(self):
        values = [auxiliary(), generate()]
        usage = aggregate_usage(values)
        self.assertEqual(usage["request_count"], 2)
        self.assertEqual(usage["model_request_count"], 1)
        self.assertEqual(usage["auxiliary_request_count"], 1)
        self.assertEqual(usage["unknown_auxiliary_cost_requests"], 1)
        self.assertEqual(usage["first_input_tokens"], 20)
        self.assertEqual(usage["peak_input_tokens"], 20)
        self.assertEqual(usage["total_input_tokens"], 20)
        self.assertIsNone(usage["requests"][0]["tokens"]["input_total"])
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", "antigravity-mixed")
            case = spec()
            case.limits["max_requests"] = 1
            journal.start_case(case)
            for value in values:
                arguments = {key: value[key] for key in ("role", "operation") if key in value}
                journal.start_request(case.case_id, value["request_id"], value["contract"], **arguments)
                journal.record_request(case.case_id, value)
            journal.finish_case(case.case_id, checks=CHECKS, boundary_counts=BOUNDARIES, elapsed_ms=100)
            result = summarize(read_journal(journal.path))["cases"][0]
            self.assertFalse(result["safe_task_success"])
            self.assertEqual(result["failure_class"], "budget")
            self.assertEqual(result["usage"]["request_count"], 2)

    def test_untrusted_roles_operations_and_auxiliary_usage_are_rejected(self):
        for changed in ({"contract": "openai-chat-v1"}, {"operation": "private-payload"}, {"operation": []},
                        {"role": "inference"}, {"raw_usage": {"input_tokens": 0}}):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                aggregate_usage([{**auxiliary(), **changed}])
        with self.assertRaises(ValueError):
            aggregate_usage([{**generate(), "role": "auxiliary", "operation": "credits_query"}])

    def test_auxiliary_identity_cannot_change_between_start_and_finish(self):
        with tempfile.TemporaryDirectory() as temporary:
            journal = CaseJournal(Path(temporary) / "cases.jsonl", "antigravity-identity")
            case = spec()
            journal.start_case(case)
            value = auxiliary()
            journal.start_request(case.case_id, value["request_id"], value["contract"], role="auxiliary", operation="credits_query")
            with self.assertRaises(ValueError):
                journal.record_request(case.case_id, {**value, "operation": "project_discovery"})


if __name__ == "__main__":
    unittest.main()
