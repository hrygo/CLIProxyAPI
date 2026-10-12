"""Only sealed typed local failures may classify preparation errors."""
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from preparation_evidence import read_preparation_diagnostics, preparation_failure_class


def empty_preparation():
    return {"version": 1, "requests_seen": 1, "active_requests": 0,
            "sealed": True, "failed": False, "failures": []}


class PreparationEvidenceTests(unittest.TestCase):
    def test_missing_and_unfinished_evidence_cannot_be_positive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertIsNone(read_preparation_diagnostics(root))
            path = root / "preparation.json"
            valid = empty_preparation()
            path.write_text(json.dumps(valid))
            self.assertEqual(read_preparation_diagnostics(root), valid)
            for change in ({"version": True}, {"version": 2}, {"active_requests": 1},
                           {"sealed": False}, {"failed": True}, {"requests_seen": 257},
                           {"requests_seen": True}, {"payload": "private"}):
                with self.subTest(change=change):
                    path.write_text(json.dumps({**valid, **change}))
                    with self.assertRaises(ValueError): read_preparation_diagnostics(root)
            path.write_text('{"version":1,"version":1}')
            with self.assertRaises(ValueError): read_preparation_diagnostics(root)
            path.unlink()
            path.symlink_to(root / "other")
            with self.assertRaises(ValueError): read_preparation_diagnostics(root)

    def test_fixed_typed_failure_shape_and_budget_classification(self):
        failure = {"request_sequence": 1, "phase": "prepare", "reason": "declaration_budget", "status_code": 413}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "preparation.json"
            valid = {**empty_preparation(), "failures": [failure]}
            path.write_text(json.dumps(valid))
            observed = read_preparation_diagnostics(root)
            self.assertEqual(preparation_failure_class(observed), "budget")
            self.assertIsNone(preparation_failure_class(empty_preparation()))
            self.assertEqual(preparation_failure_class(None), "framework")
            for change in ({"request_sequence": 2}, {"request_sequence": True}, {"phase": "upstream"},
                           {"status_code": True}, {"status_code": 200}, {"reason": "private receipt"},
                           {"message": "private receipt"}):
                with self.subTest(change=change):
                    path.write_text(json.dumps({**valid, "failures": [{**failure, **change}]}))
                    with self.assertRaises(ValueError): read_preparation_diagnostics(root)
            self.assertEqual(preparation_failure_class({**valid, "failures": [{**failure, "reason": "json_syntax", "status_code": 400}]}), "protocol")
            self.assertEqual(preparation_failure_class({**valid, "failures": [{**failure, "reason": "unknown", "status_code": 500}]}), "framework")


if __name__ == "__main__": unittest.main()
