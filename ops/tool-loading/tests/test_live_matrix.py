"""One fixed matrix must declare every route and retain all started outcomes."""

from copy import deepcopy
from contextlib import redirect_stdout
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class LiveMatrixTests(unittest.TestCase):
    def test_manifest_reader_rejects_duplicate_members_and_symlinks(self):
        from live_matrix import read_declaration
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            good = root / "matrix.json"
            good.write_text(json.dumps(self.declaration()))
            self.assertEqual(read_declaration(good), self.declaration())
            alias = root / "linked.json"
            alias.symlink_to(good)
            with self.assertRaises((OSError, ValueError)):
                read_declaration(alias)
            good.write_text(good.read_text().replace('"schema_version": 1', '"schema_version": 0, "schema_version": 1'))
            with self.assertRaises(ValueError):
                read_declaration(good)

    def declaration(self):
        units = []
        for client, model, executor in (("codex", "model-a", "codex"), ("opencode", "model-b", "meta"),
                                        ("claude", "model-c", "openai-compatible-fixture")):
            units.append({"profile": {"client_model": model, "upstream_model": model, "executor": executor,
                                      "contract": "openai-chat-v1" if client == "claude" else "openai-responses-v1"},
                          "endpoint_sha256": "e" * 64, "client": client, "structured_output": client == "claude",
                          "opencode_title": "Fixed matrix" if client == "opencode" else None,
                          "scenarios": ["lookup", "unavailable", "multitool"]})
        return {"schema_version": 1, "units": units, "task_seconds": 90, "max_requests": 144,
                "max_dispatches": 144, "schedule_seed": "predeclared-matrix-1"}

    def test_invalid_declarations_are_rejected_without_launch(self):
        from live_matrix import validate_declaration
        good = self.declaration()
        validate_declaration(good)
        variants = []
        for change in ({"schema_version": True}, {"units": []}, {"max_requests": 0}, {"task_seconds": 0},
                       {"extra": "unknown"}):
            variants.append({**deepcopy(good), **change})
        for change in ({"client": "unknown"}, {"scenarios": ["lookup", "lookup"]}, {"structured_output": 1},
                       {"endpoint_sha256": "short"}, {"opencode_title": "Wrong client"},
                       {"profile": {**good["units"][0]["profile"], "contract": "openai-chat-v1"}}):
            changed = deepcopy(good)
            changed["units"][0].update(change)
            variants.append(changed)
        for value in variants:
            with self.subTest(value=value), self.assertRaises(ValueError):
                validate_declaration(value)

    def test_all_routes_are_locked_before_first_launch_and_failures_remain(self):
        from live_matrix import main
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable, declaration = (root / name for name in ("gateway", "config", "client", "matrix.json"))
            for p in (binary, config, executable):
                p.write_bytes(b"synthetic-file")
            declaration.write_text(json.dumps(self.declaration()))
            output, observed = root / "run", []

            def task(root, journal, binary, config, profile, client, mode, revision, endpoint, seconds,
                     *, specification, locked_plan, **kwargs):
                self.assertEqual(len(locked_plan["cases"]), 18)
                self.assertEqual({c["scope"]["client"] for c in locked_plan["cases"]}, {"codex", "opencode", "claude"})
                self.assertIn("matrix-declaration", locked_plan["materials"])
                self.assertTrue((root / "batch-plan.json").exists())
                observed.append((client, mode, specification.template_id))
                journal.start_case(specification)
                counts = dict.fromkeys(("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"), 0)
                journal.finish_case(specification.case_id,
                    checks=dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"), False),
                    boundary_counts=counts, elapsed_ms=1, failure_class="model")
                return {"case_id": specification.case_id, "safe_task_success": False, "upstream_attempts": 0,
                        "issues": [], "boundary_counts": counts}

            argv = ["matrix", "--declaration", str(declaration), "--gateway-binary", str(binary),
                    "--config", str(config), "--output-dir", str(output), "--gateway-revision", "a" * 40]
            with patch("live_matrix.sys.argv", argv), patch("live_matrix.verify_gateway_revision", return_value="2a3a895e6ffa476f024217471fd61316ab73ef74d8b2ede64aa3a44d5186af15"), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_matrix.shutil.which", return_value=str(executable)), \
                    patch("live_matrix.run_case", side_effect=task), redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 1)
            self.assertEqual(len(observed), 18)
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual((audit["planned_cases"], audit["started_cases"], audit["unfinished_cases"]), (18, 18, 0))
            self.assertEqual(audit["safe_successes"], 0)
            self.assertEqual(json.loads((output / "summary.json").read_text())["batch_stop_reason"], "complete")

    def test_duplicate_route_cases_fail_before_candidate_creation(self):
        from live_matrix import compile_cases
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, executable = root / "config", root / "client"
            config.write_bytes(b"synthetic-config")
            executable.write_bytes(b"synthetic-client")
            matrix = self.declaration()
            matrix["units"].append(deepcopy(matrix["units"][0]))
            with patch("live_clients.shutil.which", return_value=str(executable)), self.assertRaises(ValueError):
                compile_cases(matrix, config, "a" * 40)

    def test_unknown_attempt_counts_keep_reservation_and_stop_before_next_case(self):
        from live_matrix import main
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable, declaration = (root / name for name in ("gateway", "config", "client", "matrix.json"))
            for p in (binary, config, executable):
                p.write_bytes(b"synthetic-file")
            matrix = self.declaration()
            matrix["max_requests"] = 8
            declaration.write_text(json.dumps(matrix))
            output = root / "run"

            def task(root, journal, *args, specification, **kwargs):
                journal.start_case(specification)
                counts = dict.fromkeys(("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"), 0)
                journal.finish_case(specification.case_id,
                    checks=dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"), False),
                    boundary_counts=counts, elapsed_ms=1, failure_class="framework")
                return {"case_id": specification.case_id, "safe_task_success": False, "upstream_attempts": 0,
                        "issues": ["attempt_evidence_import_failed"], "boundary_counts": counts}

            argv = ["matrix", "--declaration", str(declaration), "--gateway-binary", str(binary),
                    "--config", str(config), "--output-dir", str(output), "--gateway-revision", "a" * 40]
            with patch("live_matrix.sys.argv", argv), patch("live_matrix.verify_gateway_revision", return_value="2a3a895e6ffa476f024217471fd61316ab73ef74d8b2ede64aa3a44d5186af15"), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_matrix.shutil.which", return_value=str(executable)), patch("live_matrix.run_case", side_effect=task) as launch, \
                    redirect_stdout(io.StringIO()), self.assertRaises(SystemExit):
                main()
            self.assertEqual(launch.call_count, 1)
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["planned_not_started"], 17)
            self.assertEqual(audit["started_cases"], 1)
            self.assertEqual(json.loads((output / "summary.json").read_text())["batch_stop_reason"], "request_budget")

    def test_unexpected_interruption_retains_started_unfinished_case(self):
        from live_matrix import main
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable, declaration = (root / name for name in ("gateway", "config", "client", "matrix.json"))
            for p in (binary, config, executable):
                p.write_bytes(b"synthetic-file")
            declaration.write_text(json.dumps(self.declaration()))
            output = root / "run"

            def task(root, journal, *args, specification, **kwargs):
                journal.start_case(specification)
                raise RuntimeError("unexpected local failure")

            argv = ["matrix", "--declaration", str(declaration), "--gateway-binary", str(binary),
                    "--config", str(config), "--output-dir", str(output), "--gateway-revision", "a" * 40]
            with patch("live_matrix.sys.argv", argv), patch("live_matrix.verify_gateway_revision", return_value="2a3a895e6ffa476f024217471fd61316ab73ef74d8b2ede64aa3a44d5186af15"), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_matrix.shutil.which", return_value=str(executable)), patch("live_matrix.run_case", side_effect=task), \
                    redirect_stdout(io.StringIO()), self.assertRaises(RuntimeError):
                main()
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["started_cases"], 1)
            self.assertEqual(audit["unfinished_cases"], 1)
            self.assertEqual(audit["safe_successes"], 0)
            self.assertEqual(json.loads((output / "summary.json").read_text())["batch_stop_reason"], "interrupted")


if __name__ == "__main__":
    unittest.main()
