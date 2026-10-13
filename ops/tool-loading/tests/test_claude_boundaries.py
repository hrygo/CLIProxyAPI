import io
import json
from contextlib import redirect_stdout
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from claude_boundary_probe import main, probe


class ClaudeBoundaryTests(unittest.TestCase):
    def test_structured_boundary_profile_is_explicit_and_keeps_all_starts(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "new-probes"
            argv = ["probe", "--output-dir", str(root), "--structured-output"]
            with patch.object(sys, "argv", argv), patch("claude_boundary_probe.probe", return_value={"passed": True}) as run, redirect_stdout(io.StringIO()):
                main()
            self.assertEqual(run.call_count, 8)
            self.assertTrue(all(call.kwargs["structured_output"] is True for call in run.call_args_list))
            events = [json.loads(line) for line in (root / "probe-events.jsonl").read_text().splitlines()]
            self.assertEqual(sum(event["event"] == "started" for event in events), 8)

    def test_framework_failure_stays_started_and_does_not_skip_other_profiles(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "new-probes"
            argv = ["probe", "--output-dir", str(root)]
            with patch.object(sys, "argv", argv), patch("claude_boundary_probe.probe", side_effect=[ValueError("private detail"), *[{"passed": True}] * 7]), redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 1)
            events = [json.loads(line) for line in (root / "probe-events.jsonl").read_text().splitlines()]
            summary = json.loads((root / "summary.json").read_text())
            self.assertEqual(sum(value["event"] == "started" for value in events), 8)
            self.assertEqual(sum(value["event"] == "finished" for value in events), 8)
            self.assertEqual(len(summary["cases"]), 8)
            self.assertEqual(summary["cases"][0]["exception_type"], "ValueError")
            self.assertNotIn("private detail", json.dumps(summary))
            self.assertEqual(summary["gate_status"], "unverified")

    def test_unknown_boundary_profile_does_not_launch_client_or_create_case(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with patch("claude_boundary_probe.prepare_isolated_client") as launch, self.assertRaises(ValueError):
                probe(root, "lazy", "unknown")
            launch.assert_not_called()
            self.assertEqual(list(root.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
