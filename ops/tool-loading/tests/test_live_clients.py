import json
import io
from contextlib import redirect_stdout, contextmanager
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from live_clients import select_model_metadata, result_checks, chat_terminal, endpoint_id, make_case_spec, run_case, main, http_failure_class
from batch_plan import build_plan, fingerprint_materials
from run_acceptance import CaseJournal, summarize
from test_preparation_evidence import empty_preparation


class LiveClientTests(unittest.TestCase):
    def test_claude_scope_is_explicit_without_expanding_legacy_default_clients(self):
        from client_adapters import VERSIONS, CLAUDE_VERSION
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, executable = root / "config", root / "claude"
            config.write_bytes(b"synthetic-config")
            executable.write_bytes(b"synthetic-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                lazy = make_case_spec(config, profile, "claude", "lazy", "a" * 40, "ep_" + "b" * 32, 90)
                eager = make_case_spec(config, profile, "claude", "eager", "a" * 40, "ep_" + "b" * 32, 90)
                with self.assertRaises(ValueError):
                    make_case_spec(config, profile, "claude", "lazy", "a" * 40, "ep_" + "b" * 32, 90, structured_output=True)
            self.assertEqual(lazy.scope["client_version"], CLAUDE_VERSION)
            self.assertEqual(lazy.scope["source_format"], "claude")
            self.assertEqual(lazy.scope["client_mode"], "tool-search")
            self.assertEqual(eager.scope["client_mode"], "direct")
            self.assertEqual(set(VERSIONS), {"codex", "opencode"})

    def test_claude_final_result_requires_success_and_no_assistant_error(self):
        answer = json.dumps({"remaining_budget": 424242, "verification_code": "fresh"})
        result = {"type": "result", "subtype": "success", "is_error": False, "result": answer}
        raw = json.dumps(result) + "\n"
        checks = result_checks("claude", raw, "fresh")
        self.assertTrue(all(checks[name] for name in ("answer", "nonce", "format", "terminal")))
        for bad in (raw.rstrip(), json.dumps({**result, "is_error": True}) + "\n",
                    json.dumps({**result, "subtype": "error_max_turns"}) + "\n",
                    json.dumps({"type": "assistant", "error": "invalid_request"}) + "\n" + raw,
                    '42\n' + raw, raw + '{"type":"system"}\n'):
            self.assertFalse(result_checks("claude", bad, "fresh")["terminal"])
        earlier = json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": answer}]}}) + "\n"
        self.assertFalse(result_checks("claude", earlier + json.dumps({**result, "result": "wrong"}) + "\n", "fresh")["answer"])

    def test_claude_effective_mode_is_observed_on_translated_wire(self):
        from live_clients import effective_tool_mode
        search = {"type": "function", "function": {"name": "ToolSearch", "parameters": {"type": "object"}}}
        self.assertTrue(effective_tool_mode("claude", "lazy", [search]))
        self.assertFalse(effective_tool_mode("claude", "lazy", [{"type": "function", "function": {"name": "Other"}}]))
        self.assertFalse(effective_tool_mode("claude", "lazy", [search] * 60))
        self.assertTrue(effective_tool_mode("claude", "eager", [{"name": f"tool_{i}"} for i in range(67)]))

    def test_claude_structured_output_rejected_before_run_directory(self):
        from contextlib import redirect_stderr
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "absent"
            argv = ["live_clients.py", "--gateway-binary", "unused", "--config", "unused", "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "openai-compatible-fixture", "--clients", "claude", "--structured-output"]
            with patch("live_clients.sys.argv", argv), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 2)
            self.assertFalse(output.exists())

    def test_auxiliary_capture_is_budgeted_without_becoming_model_terminal_or_schema(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        from test_auxiliary_accounting import AuxiliaryAccountingTests
        from test_http_attempts import artifact
        from test_acceptance import CHECKS
        from catalog_fixture import digest, build_catalog
        from live_clients import PERMISSIONS
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            executable, config, stdout = (root / name for name in ("client", "config", "stdout"))
            executable.write_bytes(b"synthetic-client")
            config.write_bytes(b"synthetic-config")
            stdout.write_text("{}\n")
            capture = root / "capture"
            capture.mkdir()
            AuxiliaryAccountingTests().write_http(capture)
            events = [json.loads(line) for line in (capture / "attempts.jsonl").read_text().splitlines()]
            request = {"request": {"tools": [{"functionDeclarations": [{"name": f"fixture_{i}"} for i in range(64)]}]}}
            events[2]["request"] = artifact(capture, "request-000002.body", json.dumps(request).encode())
            response = {"response": {"responseId": "fixture-response", "candidates": [{"index": 0, "finishReason": "STOP",
                "content": {"parts": [{"text": "unavailable"}]}}], "usageMetadata": {"promptTokenCount": 20,
                "candidatesTokenCount": 3, "thoughtsTokenCount": 2, "totalTokenCount": 25}}}
            body = json.dumps(response).encode()
            events[3].update(response=artifact(capture, "response-000002.body", body), observed_bytes=len(body))
            (capture / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))
            (capture / "preparation.json").write_text(json.dumps(empty_preparation()))
            (capture / "auxiliary-work.json").write_text(json.dumps({"version": 1, "started": 1,
                "completed": 1, "rejected": 0, "active": 0, "sealed": True}))
            (capture / "admission.json").write_text(json.dumps({"version": 1, "max_attempts": 8,
                "reserved_attempts": 2, "budget_rejections": 0, "closed_rejections": 0, "failed_rejections": 0,
                "active_bodies": 0, "journal_closed": True, "journal_failed": False}))
            @contextmanager
            def candidate(*args, **kwargs):
                yield {"base_url": "http://127.0.0.1:1"}, capture
            prepared = SimpleNamespace(permission_hash=digest(PERMISSIONS), catalog_hash=digest(build_catalog()),
                run_task=Mock(return_value={"stdout_path": str(stdout), "termination": "exited", "exit_code": 0}),
                collect_boundary_events=Mock(return_value=[{"phase": "catalog", "active_count": 0, "effect_count": 0}]))
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "antigravity", "contract": "antigravity-generate-v1"}
            journal = CaseJournal(root / "cases.jsonl", "synthetic-auxiliary-model")
            with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process", candidate), \
                    patch("live_clients.fetch_model_metadata", return_value={}), patch("live_clients.prepare_isolated_client", return_value=prepared), \
                    patch("live_clients.result_checks", return_value=dict(CHECKS)):
                result = run_case(root, journal, executable, config, profile, "codex", "eager", "a" * 40,
                                  "ep_" + "b" * 32, 90, scenario="unavailable")
            self.assertEqual(result["issues"], [])
            self.assertTrue(result["safe_task_success"])
            self.assertEqual(result["upstream_attempts"], 2)
            self.assertEqual(result["usage"]["model_request_count"], 1)
            self.assertEqual(result["usage"]["auxiliary_request_count"], 1)
            self.assertEqual(result["usage"]["total_input_tokens"], 20)
            self.assertEqual(len(result["declaration_bytes"]), 1)

    def test_run_case_records_local_denials_without_adding_http_attempts(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        from test_http_attempts import evidence
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            executable, config, stdout = (root / name for name in ("client", "config", "stdout"))
            executable.write_bytes(b"synthetic-client")
            config.write_bytes(b"synthetic-config")
            stdout.write_text("{}\n")
            capture = root / "capture"
            capture.mkdir()
            body = b'{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":1,"total_tokens":21}}'
            events = evidence(capture, bodies=(body,) * 8, contract="openai-chat-v1")
            for event in events:
                event["version"] = 2
                if event["event"] == "request-started":
                    event["executor_identity"] = {"executor": "openai-compatible-fixture", "model_sha256": "b" * 64, "auth_sha256": "c" * 64}
            (capture / "attempts.jsonl").write_text("".join(json.dumps(event) + "\n" for event in events))
            (capture / "admission.json").write_text(json.dumps({"version": 1, "max_attempts": 8,
                "reserved_attempts": 8, "budget_rejections": 3, "closed_rejections": 0, "failed_rejections": 0,
                "active_bodies": 0, "journal_closed": True, "journal_failed": False}))
            @contextmanager
            def candidate(*args, **kwargs):
                yield {"base_url": "http://127.0.0.1:1"}, capture
            from catalog_fixture import digest, build_catalog
            from live_clients import PERMISSIONS
            prepared = SimpleNamespace(permission_hash=digest(PERMISSIONS), catalog_hash=digest(build_catalog()),
                                       run_task=Mock(return_value={"stdout_path": str(stdout), "termination": "exited", "exit_code": 1}),
                                       collect_boundary_events=Mock(return_value=[{"phase": "catalog", "active_count": 0, "effect_count": 0}]))
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            journal = CaseJournal(root / "cases.jsonl", "run-admission-budget")
            with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process", candidate), \
                    patch("live_clients.fetch_model_metadata", return_value={}), patch("live_clients.prepare_isolated_client", return_value=prepared):
                result = run_case(root, journal, executable, config, profile, "codex", "eager", "a" * 40, "ep_" + "b" * 32, 90)
            self.assertEqual(result["failure_class"], "budget")
            self.assertEqual(result["upstream_attempts"], 8)
            self.assertEqual(result["usage"]["total_input_tokens"], 160)
            self.assertEqual(result["admission_diagnostics"]["budget_rejections"], 3)
            self.assertIn("http_attempt_budget_exhausted", result["issues"])
            self.assertEqual(summarize(journal.events)["started_cases"], 1)
            self.assertFalse(result["safe_task_success"])
            # A preparation rejection must not require an outbound HTTP error
            # or a physical-attempt admission denial to be attributed locally.
            state = json.loads((capture / "admission.json").read_text())
            state["budget_rejections"] = 0
            (capture / "admission.json").write_text(json.dumps(state))
            (capture / "preparation.json").write_text(json.dumps({**empty_preparation(), "failures": [
                {"request_sequence": 1, "phase": "prepare", "reason": "declaration_budget", "status_code": 413}]}))
            second_root = root / "local-preparation"
            second_root.mkdir()
            second_journal = CaseJournal(second_root / "cases.jsonl", "run-preparation-budget")
            with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process", candidate), \
                    patch("live_clients.fetch_model_metadata", return_value={}), patch("live_clients.prepare_isolated_client", return_value=prepared):
                second = run_case(second_root, second_journal, executable, config, profile, "codex", "eager", "a" * 40, "ep_" + "b" * 32, 90)
            self.assertEqual(second["failure_class"], "budget")
            self.assertEqual(second["upstream_attempts"], 8)
            self.assertEqual(second["usage"]["total_input_tokens"], 160)
            self.assertIn("responses_tools_prepare_rejected", second["issues"])

    def test_http_failure_class_separates_budget_evidence_and_provider(self):
        state = {"budget_rejections": 0, "closed_rejections": 0, "failed_rejections": 0, "journal_failed": False}
        records = [{"http_status_code": 429}]
        self.assertEqual(http_failure_class(state, records, empty_preparation()), "provider")
        self.assertEqual(http_failure_class({**state, "budget_rejections": 1}, records, empty_preparation()), "budget")
        for changed in ({"closed_rejections": 1}, {"failed_rejections": 1}, {"journal_failed": True}):
            self.assertEqual(http_failure_class({**state, **changed}, records, empty_preparation()), "framework")
        self.assertEqual(http_failure_class(None, records, empty_preparation()), "framework")
        self.assertIsNone(http_failure_class(state, [{"http_status_code": 200}] * 8, empty_preparation()))
        self.assertEqual(http_failure_class(state, [{"http_status_code": 200}], None), "framework")
        preparation = {**empty_preparation(), "failures": [{"request_sequence": 1, "phase": "prepare", "reason": "declaration_budget", "status_code": 413}]}
        self.assertEqual(http_failure_class(state, [{"http_status_code": 200}], preparation), "budget")

    def test_native_structured_response_needs_actual_terminal_and_exact_gold(self):
        result = {"client_output_profile": "opencode-http-session-v1", "protocol_terminal_verified": True,
                  "protocol_terminal_status": "completed",
                  "structured_output": {"remaining_budget": 424242, "verification_code": "fresh"}}
        self.assertTrue(result_checks("opencode", "", "fresh", native_result=result)["format"])
        self.assertTrue(result_checks("opencode", "", "fresh", native_result=result)["answer"])
        for changed in ({"protocol_terminal_verified": False}, {"protocol_terminal_status": "interrupted"},
                        {"client_output_profile": "other"}, {"structured_output": {"remaining_budget": True, "verification_code": "fresh"}},
                        {"structured_output": {"remaining_budget": 424242, "verification_code": "old"}}):
            with self.subTest(changed=changed):
                self.assertFalse(result_checks("opencode", "", "fresh", native_result={**result, **changed})["answer"])
        self.assertFalse(result_checks("codex", "", "fresh", native_result=result)["format"])

    def test_locked_output_profile_drift_fails_before_candidate_launch(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable): path.write_bytes(b"synthetic-file")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                cases = [make_case_spec(config, profile, "codex", mode, "a" * 40, "ep_" + "b" * 32, 90,
                                       structured_output=True) for mode in ("eager", "lazy")]
            files = {"gateway-binary": binary, "source-config": config, "client-codex": executable}
            plan = build_plan("run-output-locked", cases, fingerprint_materials(files), "seed", 16, 16)
            selected = next(case for case in cases if case.case_id == plan["schedule"][0])
            journal = CaseJournal(root / "cases.jsonl", plan["run_id"])
            with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process") as launch:
                result = run_case(root, journal, binary, config, profile, "codex", selected.mode, "a" * 40,
                    "ep_" + "b" * 32, 90, specification=selected, locked_plan=plan, material_paths=files, structured_output=False)
            launch.assert_not_called()
            self.assertIn("scope_validation_failure", result["issues"])
            self.assertFalse(result["safe_task_success"])
            self.assertEqual(summarize(journal.events)["started_cases"], 1)

    def test_locked_title_drift_fails_before_candidate_launch(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable): path.write_bytes(b"synthetic-file")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                cases = [make_case_spec(config, profile, "opencode", mode, "a" * 40, "ep_" + "b" * 32, 90,
                                       opencode_title="Locked title") for mode in ("eager", "lazy")]
            files = {"gateway-binary": binary, "source-config": config, "client-opencode": executable}
            plan = build_plan("run-title-locked", cases, fingerprint_materials(files), "seed", 16, 16)
            spec = next(case for case in cases if case.case_id == plan["schedule"][0])
            journal = CaseJournal(root / "cases.jsonl", plan["run_id"])
            with patch("live_clients.shutil.which", return_value=str(executable)), patch("live_clients.candidate_process") as launch:
                result = run_case(root, journal, binary, config, profile, "opencode", spec.mode, "a" * 40,
                    "ep_" + "b" * 32, 90, specification=spec, locked_plan=plan, material_paths=files, opencode_title="Changed title")
            launch.assert_not_called()
            self.assertIn("scope_validation_failure", result["issues"])
            self.assertFalse(result["safe_task_success"])
            self.assertEqual(summarize(journal.events)["started_cases"], 1)

    def test_main_predeclares_all_three_scenarios_and_runs_matching_gold(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable): path.write_bytes(b"synthetic-file")
            output, observed = root / "run", []
            def task(root, journal, *args, specification, scenario, **kwargs):
                from task_scenarios import get_scenario
                self.assertEqual(specification.template_id, get_scenario(scenario).template_id)
                observed.append((scenario, specification.mode))
                journal.start_case(specification)
                checks = {field: False for field in ("answer", "dispatch", "nonce", "code", "format", "terminal")}
                counts = {field: 0 for field in ("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active")}
                journal.finish_case(specification.case_id, checks=checks, boundary_counts=counts, elapsed_ms=1, failure_class="framework")
                return {"safe_task_success": False, "upstream_attempts": 0, "issues": ["no_gateway_attempt_capture"], "boundary_counts": counts}
            argv = ["live_clients.py", "--gateway-binary", str(binary), "--config", str(config), "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "openai-compatible-fixture", "--clients", "codex",
                    "--scenarios", "lookup", "unavailable", "multitool"]
            with patch("live_clients.sys.argv", argv), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.run_case", side_effect=task), redirect_stdout(io.StringIO()), self.assertRaises(SystemExit):
                main()
            self.assertEqual(set(observed), {(scenario, mode) for scenario in ("lookup", "unavailable", "multitool") for mode in ("eager", "lazy")})
            plan = json.loads((output / "batch-plan.json").read_text())
            self.assertEqual(len(plan["cases"]), 6)
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["started_cases"], 6)
            self.assertEqual(audit["safe_successes"], 0)
            self.assertEqual(json.loads((output / "summary.json").read_text())["batch_stop_reason"], "complete")

    def test_explicit_claude_batch_locks_both_modes_and_retains_started_failures(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable):
                path.write_bytes(b"synthetic-file")
            output, observed = root / "run", []
            def task(root, journal, *args, specification, locked_plan, **kwargs):
                self.assertEqual(specification.scope["client"], "claude")
                self.assertEqual(len(locked_plan["cases"]), 2)
                observed.append(specification.mode)
                journal.start_case(specification)
                counts = {field: 0 for field in ("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active")}
                journal.finish_case(specification.case_id, checks=dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"), False),
                                    boundary_counts=counts, elapsed_ms=1, failure_class="framework")
                return {"safe_task_success": False, "upstream_attempts": 0, "issues": [], "boundary_counts": counts}
            argv = ["live_clients.py", "--gateway-binary", str(binary), "--config", str(config), "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "openai-compatible-fixture", "--clients", "claude"]
            with patch("live_clients.sys.argv", argv), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.run_case", side_effect=task), redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 1)
            self.assertEqual(set(observed), {"eager", "lazy"})
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["started_cases"], 2)
            self.assertEqual(audit["safe_successes"], 0)
            self.assertEqual(audit["unfinished_cases"], 0)

    def test_client_preparation_keeps_executable_after_spec_extraction(self):
        from types import SimpleNamespace
        from unittest.mock import Mock
        from test_http_attempts import evidence
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            executable, config = root / "client", root / "config"
            executable.write_bytes(b"synthetic-client")
            config.write_bytes(b"synthetic-config")
            stdout = root / "stdout"
            stdout.write_text("{}\n")
            capture = root / "capture"
            capture.mkdir()
            evidence(capture, bodies=(b'{"choices":[{"finish_reason":"stop"}]}',), contract="openai-chat-v1")
            @contextmanager
            def candidate(*args, **kwargs):
                yield {"base_url": "http://127.0.0.1:1"}, capture
            from catalog_fixture import digest, build_catalog
            from live_clients import PERMISSIONS
            prepared = SimpleNamespace(permission_hash=digest(PERMISSIONS), catalog_hash=digest(build_catalog()),
                                       run_task=Mock(return_value={"stdout_path": str(stdout), "termination": "exited", "exit_code": 0}),
                                       collect_boundary_events=Mock(return_value=[]))
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            journal = CaseJournal(root / "cases.jsonl", "run-client-preparation")
            with patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.candidate_process", candidate), patch("live_clients.fetch_model_metadata", return_value={}), \
                    patch("live_clients.prepare_isolated_client", return_value=prepared) as prepare:
                run_case(root, journal, executable, config, profile, "codex", "eager", "a" * 40, "ep_" + "b" * 32, 90)
            self.assertEqual(prepare.call_args.args[2]["executable"], str(executable.resolve()))
            self.assertEqual(summarize(journal.events)["started_cases"], 1)
            self.assertEqual(summarize(journal.events)["unfinished_cases"], 0)

    def test_main_writes_plan_before_launch_and_stops_without_unknown_refunds(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable): path.write_bytes(b"synthetic-file")
            output = root / "run"
            calls = []
            def task(root, journal, *args, specification, locked_plan, material_paths, **kwargs):
                self.assertTrue((root / "batch-plan.json").exists())
                self.assertEqual(specification.case_id, locked_plan["schedule"][len(calls)])
                calls.append(specification.case_id)
                journal.start_case(specification)
                checks = dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"))
                boundaries = dict.fromkeys(("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"))
                journal.finish_case(specification.case_id, checks=checks, boundary_counts=boundaries,
                                    elapsed_ms=1, failure_class="framework")
                return {"case_id": specification.case_id, "safe_task_success": False, "upstream_attempts": 0,
                        "issues": ["no_gateway_attempt_capture"], "boundary_counts": boundaries}
            argv = ["live_clients.py", "--gateway-binary", str(binary), "--config", str(config), "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "openai-compatible-fixture", "--clients", "codex",
                    "--batch-max-requests", "8"]
            with patch("live_clients.sys.argv", argv), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.run_case", side_effect=task), redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 1)
            self.assertEqual(len(calls), 1)
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(summary["batch_stop_reason"], "request_budget")
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["started_cases"], 1)
            self.assertEqual(audit["planned_not_started"], 1)
            self.assertIsNone(audit["total_dispatches"])
            self.assertIsNone(audit["request_attempts"])
            self.assertEqual(audit["journaled_request_attempts"], 0)

    def test_main_preserves_started_unfinished_task_on_unexpected_interruption(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            for path in (binary, config, executable): path.write_bytes(b"synthetic-file")
            output = root / "run"
            def task(root, journal, *args, specification, **kwargs):
                journal.start_case(specification)
                raise OSError("synthetic interruption")
            argv = ["live_clients.py", "--gateway-binary", str(binary), "--config", str(config), "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "openai-compatible-fixture", "--clients", "codex"]
            with patch("live_clients.sys.argv", argv), patch("live_clients.shutil.which", return_value=str(executable)), \
                    patch("live_clients.run_case", side_effect=task), self.assertRaises(OSError):
                main()
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual(summary["batch_stop_reason"], "interrupted")
            audit = json.loads((output / "batch-audit.json").read_text())
            self.assertEqual(audit["started_cases"], 1)
            self.assertEqual(audit["unfinished_cases"], 1)
            self.assertEqual(audit["safe_successes"], 0)

    def test_locked_material_drift_counts_started_failure_before_gateway_launch(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary, config, executable = (root / name for name in ("gateway", "config", "client"))
            binary.write_bytes(b"synthetic")
            config.write_bytes(b"synthetic-config")
            executable.write_bytes(b"synthetic-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                specs = [make_case_spec(config, profile, "codex", mode, "a" * 40, "ep_" + "b" * 32, 90) for mode in ("eager", "lazy")]
            files = {"gateway-binary": binary, "source-config": config, "client-codex": executable}
            plan = build_plan("run-locked", specs, fingerprint_materials(files), "seed", 16, 16)
            selected = next(case for case in specs if case.case_id == plan["schedule"][0])
            config.write_bytes(b"changed-config")
            journal = CaseJournal(root / "cases.jsonl", plan["run_id"])
            with patch("live_clients.candidate_process") as launch:
                result = run_case(root, journal, binary, config, profile, "codex", selected.mode, "a" * 40,
                                  "ep_" + "b" * 32, 90, specification=selected, locked_plan=plan, material_paths=files)
            launch.assert_not_called()
            self.assertFalse(result["safe_task_success"])
            self.assertIn("material_validation_failure", result["issues"])
            report = summarize(journal.events)
            self.assertEqual(report["started_cases"], 1)
            self.assertEqual(report["unfinished_cases"], 0)
            self.assertEqual(report["safe_successes"], 0)
            self.assertIsNone(report["cases"][0]["usage"]["total_input_tokens"])

    def test_endpoint_identifier_is_derived_from_complete_digest(self):
        self.assertEqual(endpoint_id("a" * 64), "ep_" + "a" * 32)
        for value in ("a" * 33, "unknown", "A" * 64, None):
            with self.assertRaises(ValueError):
                endpoint_id(value)

    def test_invalid_native_credential_binding_fails_before_creating_run_artifacts(self):
        import io
        from contextlib import redirect_stderr
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "absent-run"
            argv = ["live_clients.py", "--gateway-binary", "unused", "--config", "unused", "--output-dir", str(output),
                    "--gateway-revision", "a" * 40, "--endpoint-sha256", "b" * 64, "--client-model", "fixture",
                    "--upstream-model", "fixture", "--executor", "codex", "--auth-file-binding", "bad"]
            with patch("live_clients.sys.argv", argv), patch("live_clients.run_case") as launch, \
                    redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as exit:
                main()
            self.assertEqual(exit.exception.code, 2)
            self.assertFalse(output.exists())
            launch.assert_not_called()

    def test_real_catalog_required_without_synthetic_fallback(self):
        model = {"slug": "actual-alias", "tool_mode": "direct", "context_window": 200000, "supports_search_tool": True}
        self.assertEqual(select_model_metadata({"models": [model]}, "actual-alias", "codex", "lazy"), model)
        self.assertEqual(select_model_metadata({"models": [{**model, "tool_mode": None}]}, "actual-alias", "codex", "lazy")["tool_mode"], None)
        for body in ({"data": [{"id": "actual-alias"}]}, {"models": []}, {"models": [model, model]}):
            with self.assertRaises(ValueError):
                select_model_metadata(body, "actual-alias", "codex", "lazy")
        with self.assertRaises(ValueError):
            select_model_metadata({"models": [{**model, "supports_search_tool": False}]}, "actual-alias", "codex", "lazy")

    def test_gold_uses_actual_final_receipt_and_client_terminal(self):
        def output(text, terminal=True):
            events = [{"type": "item.completed", "item": {"type": "agent_message", "text": text}}]
            if terminal:
                events.append({"type": "turn.completed"})
            return "".join(json.dumps(event) + "\n" for event in events)
        answer = json.dumps({"remaining_budget": 424242, "verification_code": "actual-receipt"})
        self.assertTrue(result_checks("codex", output(answer), "actual-receipt")["answer"])
        self.assertFalse(result_checks("codex", output(answer), "different")["nonce"])
        self.assertFalse(result_checks("codex", output(answer, False), "actual-receipt")["terminal"])
        self.assertFalse(result_checks("codex", output("guessed 424242 actual-receipt"), "actual-receipt")["answer"])
        fenced = result_checks("codex", output("```json\n" + answer + "\n```"), "actual-receipt")
        self.assertTrue(fenced["answer"])
        self.assertTrue(fenced["nonce"])
        self.assertFalse(fenced["format"])
        earlier = {"type": "item.completed", "item": {"type": "agent_message", "text": answer}}
        self.assertFalse(result_checks("codex", json.dumps(earlier) + "\n" + output("wrong final answer"), "actual-receipt")["answer"])

    def test_eof_and_done_do_not_substitute_for_chat_finish_reason(self):
        self.assertFalse(chat_terminal(b'data: [DONE]\n\n'))
        self.assertFalse(chat_terminal(b'data: {"choices":[{"delta":{"content":"partial"},"finish_reason":null}]}\n\n'))
        self.assertTrue(chat_terminal(b'data: {"choices":[{"index":0,"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n'))
        self.assertFalse(chat_terminal(b'data: {"choices":[{"finish_reason":"length"}]}\n\n'))
        self.assertFalse(chat_terminal(b'{"choices":null}'))
        self.assertFalse(chat_terminal(b'{"choices":[{"finish_reason":{}}]}'))


if __name__ == "__main__":
    unittest.main()
