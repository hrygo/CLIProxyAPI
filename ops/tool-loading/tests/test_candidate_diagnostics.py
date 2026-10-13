"""Only trusted diagnostic categories can leave the isolated child boundary."""

import json
from contextlib import contextmanager
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class CandidateDiagnosticsTests(unittest.TestCase):
    def test_actual_child_failure_preserves_only_safe_diagnostics_and_exit_receipt(self):
        from live_gateway import candidate_process, CandidateFailure
        failures = [{"stage": "credentials", "reason": "binding_content_mismatch"}]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config = root / "child", root / "config"
            config.write_text("synthetic-config")
            binary.write_text("#!/usr/bin/env python3\nimport json,sys\nsys.stderr.write(json.dumps(" + repr(
                {"version": 1, "scope": "isolated-candidate-error", "failures": failures}) + ")+'\\n')\nsys.exit(1)\n")
            os.chmod(binary, 0o700)
            profile = {"client_model": "fixture", "upstream_model": "fixture",
                       "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            with self.assertRaises(CandidateFailure) as failure:
                with candidate_process(binary, config, root, profile):
                    self.fail("failed child must never enter client execution")
            self.assertEqual(failure.exception.diagnostics, failures)
            self.assertEqual(json.loads((root / "gateway.failure.json").read_text())["failures"], failures)
            self.assertEqual(json.loads((root / "gateway.exit.json").read_text()),
                             {"exit_code": 1, "source_config_unchanged": True})

    def test_case_summary_retains_startup_failure_and_superseded_catalog_reason(self):
        from live_gateway import CandidateFailure
        from live_clients import CatalogFailure, run_case
        from run_acceptance import CaseJournal
        failure = [{"stage": "credentials", "reason": "binding_content_mismatch"}]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for p in (binary, config, executable):
                p.write_bytes(b"synthetic-file")
            profile = {"client_model": "fixture", "upstream_model": "fixture",
                       "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            @contextmanager
            def candidate(*args, **kwargs):
                try:
                    yield {}, root / "missing-capture"
                finally:
                    raise CandidateFailure(failure)
            journal = CaseJournal(root / "cases.jsonl", "safe-diagnostic-test")
            with patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.candidate_process", candidate), \
                    patch("live_clients.fetch_model_metadata", side_effect=CatalogFailure("tool_mode_mismatch")):
                result = run_case(root, journal, binary, config, profile, "codex", "lazy", "a" * 40, "ep_" + "b" * 32, 90)
            self.assertFalse(result["safe_task_success"])
            self.assertEqual(result["candidate_diagnostics"], failure)
            self.assertEqual(result["catalog_diagnostic"], {"reason": "tool_mode_mismatch"})
            self.assertEqual(sum(event["phase"] == "case_started" for event in journal.events), 1)
            self.assertEqual(sum(event["phase"] == "case_finished" for event in journal.events), 1)

    def test_safe_child_envelope_is_bounded_and_rejects_raw_details(self):
        from live_gateway import read_candidate_failure
        good = {"version": 1, "scope": "isolated-candidate-error",
                "failures": [{"stage": "credentials", "reason": "binding_content_mismatch"}]}
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "stderr"
            path.write_text(json.dumps(good) + "\n")
            self.assertEqual(read_candidate_failure(path), good["failures"])
            for bad in ("private-token", "x" * 65537, json.dumps({**good, "extra": "private-token"}),
                        json.dumps({**good, "version": True}), json.dumps({**good, "failures": []}),
                        json.dumps({**good, "failures": [{"stage": "credentials", "reason": "private-token"}]}),
                        json.dumps({**good, "failures": good["failures"] * 9}),
                        json.dumps(good) + "\n" + json.dumps(good),
                        json.dumps(good).replace('"version": 1', '"version": 0, "version": 1')):
                path.write_text(bad)
                self.assertIsNone(read_candidate_failure(path))

    def test_catalog_errors_keep_specific_mode_and_search_admission_causes(self):
        from live_clients import CatalogFailure, select_model_metadata
        body = {"models": [{"slug": "fixture", "context_window": 1000,
                            "tool_mode": "code_mode_only", "supports_search_tool": True}]}
        with self.assertRaises(CatalogFailure) as mismatch:
            select_model_metadata(body, "fixture", "codex", "eager")
        self.assertEqual(mismatch.exception.reason, "tool_mode_mismatch")
        body["models"][0]["tool_mode"] = "direct"
        body["models"][0]["supports_search_tool"] = False
        with self.assertRaises(CatalogFailure) as mismatch:
            select_model_metadata(body, "fixture", "codex", "lazy")
        self.assertEqual(mismatch.exception.reason, "search_not_admitted")


if __name__ == "__main__":
    unittest.main()
