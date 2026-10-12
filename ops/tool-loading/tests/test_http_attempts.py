"""Request evidence must retain failures and verify private body artifacts."""

from copy import deepcopy
import hashlib
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from http_attempts import read_http_attempts, import_http_attempts
from run_acceptance import CaseJournal, summarize, read_journal
from test_acceptance import spec, CHECKS, BOUNDARIES


def artifact(directory, name, body, complete=True):
    (directory / name).write_bytes(body)
    return {"path": name, "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest(), "complete": complete}


def evidence(directory, bodies=(None, b'{"usage":{"input_tokens":100,"output_tokens":5}}'), contract="openai-responses-v1"):
    events = []
    for number, body in enumerate(bodies, 1):
        request_id = f"request-{number:06d}"
        events.append({"sequence": len(events) + 1, "version": 1, "event": "request-started",
                       "request_id": request_id, "contract": contract,
                       "request": artifact(directory, request_id + ".body", b'{"model":"fixture"}')})
        if body is not None:
            events.append({"sequence": len(events) + 1, "version": 1, "event": "request-finished",
                           "request_id": request_id, "contract": contract, "status_code": 200,
                           "outcome": "eof", "observed_bytes": len(body),
                           "response": artifact(directory, f"response-{number:06d}.body", body)})
    path = directory / "attempts.jsonl"
    path.write_text("".join(json.dumps(event) + "\n" for event in events))
    return events


class HTTPAttemptsTests(unittest.TestCase):
    def test_unfinished_and_known_retry_import_without_zero_filling(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            evidence(directory)
            journal = CaseJournal(directory / "cases.jsonl", "run-fixture")
            case = spec()
            journal.start_case(case)
            records = import_http_attempts(journal, case.case_id, directory)
            self.assertEqual(len(records), 2)
            self.assertFalse(records[0]["finished"])
            self.assertIsNone(records[0]["raw_usage"])
            journal.finish_case(case.case_id, checks=CHECKS, boundary_counts=BOUNDARIES,
                                elapsed_ms=12, failure_class="unknown")
            result = summarize(read_journal(journal.path))["cases"][0]
            self.assertEqual(result["usage"]["request_count"], 2)
            self.assertEqual(result["usage"]["known_total_input_tokens"], 100)
            self.assertIsNone(result["usage"]["total_input_tokens"])
            self.assertFalse(result["safe_task_success"])

    def test_tamper_path_symlink_duplicate_finish_and_sequence_rejected(self):
        for kind in ("tamper", "path", "symlink", "duplicate", "sequence", "extra"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                events = evidence(directory, bodies=(b'{"usage":{"input_tokens":10}}',))
                if kind == "tamper":
                    (directory / "request-000001.body").write_bytes(b"tamper")
                elif kind == "path":
                    events[0]["request"]["path"] = "../outside.body"
                elif kind == "symlink":
                    original = directory / "request-000001.body"
                    original.rename(directory / "target.body")
                    original.symlink_to(directory / "target.body")
                elif kind == "duplicate":
                    events.append({**deepcopy(events[-1]), "sequence": 3})
                elif kind == "sequence":
                    events[0]["sequence"] = True
                else:
                    events[0]["url"] = "private-source"
                (directory / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))
                with self.assertRaises(ValueError):
                    read_http_attempts(directory)

    def test_partial_capture_does_not_supply_usage_or_fake_terminal(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            events = evidence(directory, bodies=(b'{"usage":{"input_tokens":100,"output_tokens":5}}',))
            events[-1]["response"]["complete"] = False
            events[-1]["observed_bytes"] += 10
            (directory / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))
            record = read_http_attempts(directory)[0]
            self.assertIsNone(record["raw_usage"])
            self.assertNotIn("terminal", record)
            self.assertIn("incomplete_response_capture", record["issues"])

    def test_stream_usage_snapshots_are_consolidated_not_summed(self):
        examples = [
            ("gemini-generate-v1", b'data: {"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1}}\n\ndata: {"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":3}}\n\n', {"promptTokenCount": 20, "candidatesTokenCount": 3}),
            ("anthropic-messages-v1", b'data: {"type":"message_start","message":{"usage":{"input_tokens":20,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}\n\ndata: {"type":"message_delta","usage":{"output_tokens":3}}\n\n', {"input_tokens": 20, "output_tokens": 3, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}),
            ("openai-chat-v1", b'data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":3}}\n\ndata: [DONE]\n\n', {"prompt_tokens": 20, "completion_tokens": 3}),
            ("openai-responses-v1", b'data: {"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":3}}}\n\n', {"input_tokens": 20, "output_tokens": 3}),
        ]
        for contract, body, expected in examples:
            with self.subTest(contract=contract), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                evidence(directory, bodies=(body,), contract=contract)
                self.assertEqual(read_http_attempts(directory)[0]["raw_usage"], expected)

    def test_conflicting_non_cumulative_usage_is_unknown(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            body = b'data: {"usage":{"prompt_tokens":20,"completion_tokens":3}}\n\ndata: {"usage":{"prompt_tokens":21,"completion_tokens":3}}\n\n'
            evidence(directory, bodies=(body,), contract="openai-chat-v1")
            record = read_http_attempts(directory)[0]
            self.assertIsNone(record["raw_usage"])
            self.assertIn("conflicting_usage_snapshots", record["issues"])


if __name__ == "__main__":
    unittest.main()
