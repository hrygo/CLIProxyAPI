"""Response diagnostics are typed local evidence, never child error prose."""
import json
from pathlib import Path
import sys
import tempfile
import unittest
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from response_evidence import read_response_diagnostics, response_failure_class

def empty_response():
    return {"version":1,"requests_seen":1,"active_requests":0,"sealed":True,"failed":False,"failures":[]}

class ResponseEvidenceTests(unittest.TestCase):
    def test_missing_and_sealed_diagnostics(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            self.assertIsNone(read_response_diagnostics(root))
            valid=empty_response()
            path=root/"response-failures.json"
            path.write_text(json.dumps(valid))
            self.assertEqual(read_response_diagnostics(root),valid)
            for change in ({"version":True},{"version":2},{"sealed":False},{"failed":True},{"active_requests":1},{"requests_seen":257},{"message":"private"}):
                path.write_text(json.dumps({**valid,**change}))
                with self.assertRaises(ValueError):read_response_diagnostics(root)
            path.write_text('{"version":1,"version":1}')
            with self.assertRaises(ValueError):read_response_diagnostics(root)
            path.unlink();path.symlink_to(root/"target")
            with self.assertRaises(ValueError):read_response_diagnostics(root)

    def test_fixed_response_phases_reasons_and_request_identity(self):
        failure={"request_sequence":1,"phase":"stream_feed","reason":"upstream_contract","status_code":502}
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);path=root/"response-failures.json"
            valid={**empty_response(),"failures":[failure]}
            for phase in ("stream_feed","stream_finish","response_restore"):
                path.write_text(json.dumps({**valid,"failures":[{**failure,"phase":phase}]}))
                self.assertEqual(response_failure_class(read_response_diagnostics(root)),"protocol")
            for change in ({"phase":"prepare"},{"request_sequence":2},{"request_sequence":True},{"reason":"private"},{"status_code":200},{"status_code":True},{"payload":"private"}):
                path.write_text(json.dumps({**valid,"failures":[{**failure,**change}]}))
                with self.assertRaises(ValueError):read_response_diagnostics(root)
            path.write_text(json.dumps({**valid,"failures":[failure,failure]}))
            with self.assertRaises(ValueError):read_response_diagnostics(root)
            self.assertIsNone(response_failure_class(None))
            self.assertIsNone(response_failure_class(empty_response()))
            self.assertEqual(response_failure_class({**valid,"failures":[{**failure,"reason":"unknown"}]}),"framework")
            self.assertEqual(response_failure_class({**valid,"failures":[{**failure,"reason":"attempt_budget","status_code":413}]}),"budget")
