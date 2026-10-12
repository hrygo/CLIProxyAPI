"""Version-specific configuration and process isolation, independent of Providers."""

from copy import deepcopy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from client_adapters import PreparedClient, prepare_isolated_client, synthetic_codex_model
from probe_clients import inspect_tools


class AdapterTests(unittest.TestCase):
    def catalog(self, root, client="codex"):
        return {"run_dir": Path(root) / "run", "client_version": "0.160.0" if client == "codex" else "1.19.0",
                "executable": sys.executable, "model": "fixture-model", "base_url": "http://127.0.0.1:12345/v1",
                "fixture_python": sys.executable, "model_metadata": synthetic_codex_model("fixture-model"),
                "nonce": "fresh-nonce", "max_dispatches": 8, "max_result_bytes": 65536}

    def prepare(self, client, mode, catalog, permissions=None):
        output = "codex-cli 0.160.0\n" if client == "codex" else "1.19.0\n"
        with patch("client_adapters.subprocess.run", return_value=subprocess.CompletedProcess([], 0, output, "")):
            return prepare_isolated_client(client, mode, catalog, permissions or {"client_denied": [], "service_denied": ["fixture_mutation"]})

    def test_codex_uses_isolated_home_and_real_catalog_gate_not_legacy_flag(self):
        with tempfile.TemporaryDirectory() as temporary:
            catalog = self.catalog(temporary)
            before = deepcopy(catalog)
            prepared = self.prepare("codex", "lazy", catalog)
            self.assertEqual(catalog, before)
            self.assertEqual(prepared.env["CODEX_HOME"], str(prepared.run_dir / "codex-home"))
            self.assertIn("--strict-config", prepared.command)
            self.assertIn("--ignore-user-config", prepared.command)
            model = json.loads(prepared.catalog_path.read_text())["models"][0]
            self.assertTrue(model["supports_search_tool"])
            self.assertEqual(model["tool_mode"], "direct")
            self.assertTrue(model["base_instructions"])
            self.assertNotIn("features.tool_search", prepared.config_path.read_text())
            self.assertEqual(prepared.expected_mode, "tool-search")

    def test_eager_and_lazy_keep_same_fixture_and_permission_catalog(self):
        with tempfile.TemporaryDirectory() as temporary:
            first = self.catalog(temporary)
            second = deepcopy(first)
            second["run_dir"] = Path(temporary) / "another-run"
            eager = self.prepare("codex", "eager", first)
            lazy = self.prepare("codex", "lazy", second)
            self.assertEqual(eager.permission_hash, lazy.permission_hash)
            eager_model = json.loads(eager.catalog_path.read_text())["models"][0]
            lazy_model = json.loads(lazy.catalog_path.read_text())["models"][0]
            self.assertFalse(eager_model.pop("supports_search_tool"))
            self.assertTrue(lazy_model.pop("supports_search_tool"))
            self.assertEqual(eager_model, lazy_model)

    def test_nullable_catalog_tool_mode_admits_explicit_direct_profile_without_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            catalog = self.catalog(temporary)
            catalog["model_metadata"]["tool_mode"] = None
            prepared = self.prepare("codex", "lazy", catalog)
            self.assertIsNone(catalog["model_metadata"]["tool_mode"])
            self.assertEqual(json.loads(prepared.catalog_path.read_text())["models"][0]["tool_mode"], "direct")

    def test_opencode_v1_19_uses_code_mode_and_denies_underlying_tool(self):
        with tempfile.TemporaryDirectory() as temporary:
            prepared = self.prepare("opencode", "lazy", self.catalog(temporary, "opencode"),
                                    {"client_denied": ["ledger_budget_lookup"], "service_denied": ["fixture_mutation"]})
            config = json.loads(prepared.config_path.read_text())
            self.assertEqual(prepared.env["OPENCODE_EXPERIMENTAL_CODE_MODE"], "true")
            self.assertEqual(config["permission"]["execute"], "allow")
            self.assertEqual(config["permission"]["fixture_ledger_budget_lookup"], "deny")
            self.assertEqual(config["permission"]["*"], "deny")
            self.assertEqual(config["plugin"], [])
            self.assertFalse(config["autoupdate"])
            self.assertEqual(prepared.expected_mode, "code-mode")

    def test_versions_unknown_modes_and_nonlocal_endpoints_fail_before_writing(self):
        for key, value in (("client_version", "2.0.0"), ("base_url", "https://fixture.invalid/v1")):
            with self.subTest(key=key), tempfile.TemporaryDirectory() as temporary:
                catalog = self.catalog(temporary)
                catalog[key] = value
                with self.assertRaises(ValueError):
                    self.prepare("codex", "lazy", catalog)
                self.assertFalse(catalog["run_dir"].exists())
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(ValueError):
                self.prepare("codex", "code-mode-only", self.catalog(temporary))
        with tempfile.TemporaryDirectory() as temporary:
            catalog = self.catalog(temporary)
            catalog["namespace_tools"] = True
            with self.assertRaises(ValueError):
                self.prepare("codex", "lazy", catalog)
            self.assertFalse(catalog["run_dir"].exists())

    def test_child_environment_drops_credentials_and_unrelated_agent_flags(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict("os.environ", {"OPENAI_API_KEY": "fixture-secret", "OPENCODE_EXPERIMENTAL": "true", "BENCH_GATEWAY_KEY": "fixture-secret"}):
            prepared = self.prepare("opencode", "eager", self.catalog(temporary, "opencode"))
            self.assertNotIn("OPENAI_API_KEY", prepared.env)
            self.assertNotIn("BENCH_GATEWAY_KEY", prepared.env)
            self.assertNotIn("OPENCODE_EXPERIMENTAL", prepared.env)
            self.assertEqual(prepared.env["OPENCODE_EXPERIMENTAL_CODE_MODE"], "false")
            self.assertNotIn("fixture-secret", prepared.config_path.read_text())

    def test_actual_version_and_selected_metadata_must_match_declared_scope(self):
        with tempfile.TemporaryDirectory() as temporary:
            catalog = self.catalog(temporary)
            with patch("client_adapters.subprocess.run", return_value=subprocess.CompletedProcess([], 0, "codex-cli 0.162.0\n", "")):
                with self.assertRaises(ValueError):
                    prepare_isolated_client("codex", "lazy", catalog, {"client_denied": [], "service_denied": []})
            self.assertFalse(catalog["run_dir"].exists())
            catalog["model_metadata"]["slug"] = "different-model"
            with self.assertRaises(ValueError):
                self.prepare("codex", "lazy", catalog)
            self.assertFalse(catalog["run_dir"].exists())

    def test_unknown_denied_tools_and_existing_run_are_not_silently_accepted(self):
        with tempfile.TemporaryDirectory() as temporary:
            catalog = self.catalog(temporary)
            with self.assertRaises(ValueError):
                self.prepare("codex", "lazy", catalog, {"client_denied": ["unknown-tool"], "service_denied": []})
            self.prepare("codex", "eager", catalog)
            with self.assertRaises(FileExistsError):
                self.prepare("codex", "lazy", catalog)

    def process_client(self, root, code):
        path = Path(root)
        return PreparedClient("fixture-process", "1", "direct", [sys.executable, "-c", code],
                              {}, path, path / "config", path / "catalog", path / "events", "", "")

    def test_run_task_records_exit_and_enforces_output_and_process_budget(self):
        for code, expected, limit in (("print('done')", "exited", 4096),
                                      ("print('x' * 10000)", "output_budget", 256),
                                      ("while True: pass", "process_budget", 4096)):
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as temporary:
                result = self.process_client(temporary, code).run_task(
                    {"prompt": "fixture", "max_seconds": 0.25, "max_output_bytes": limit})
                self.assertEqual(result["termination"], expected)
                self.assertLessEqual(result["stored_output_bytes"], limit)
                self.assertIsInstance(result["exit_code"], int)
                self.assertFalse(result["task_success_verified"])

    def test_cancel_is_distinct_and_boundary_events_reject_incomplete_evidence(self):
        with tempfile.TemporaryDirectory() as temporary:
            client = self.process_client(temporary, "while True: pass")
            cancelled = threading.Event()
            cancelled.set()
            result = client.run_task({"prompt": "fixture", "max_seconds": 5, "max_output_bytes": 4096},
                                     cancel_event=cancelled)
            self.assertEqual(result["termination"], "cancelled")
            client.events_path.write_text('{"sequence":0}\n')
            self.assertEqual(len(client.collect_boundary_events()), 1)
            client.events_path.write_text('{"sequence":2}\n')
            with self.assertRaises(ValueError):
                client.collect_boundary_events()
            client.events_path.write_text('{"sequence":false}\n')
            with self.assertRaises(ValueError):
                client.collect_boundary_events()
            client.events_path.write_text('{"sequence":0}')
            with self.assertRaises(ValueError):
                client.collect_boundary_events()

    def test_cancel_interrupts_main_client_and_retains_graceful_shutdown_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            code = ("import signal, pathlib, sys\n"
                    "def stop(*args):\n print('INTERRUPTED', flush=True)\n pathlib.Path('interrupted').write_text('yes')\n sys.exit(0)\n"
                    "signal.signal(signal.SIGINT, stop)\npathlib.Path('ready').write_text('yes')\nwhile True: pass\n")
            client = self.process_client(temporary, code)
            cancelled, observer_stop = threading.Event(), threading.Event()
            def observe():
                while not observer_stop.wait(0.005):
                    if (Path(temporary) / "ready").exists():
                        cancelled.set()
                        return
            observer = threading.Thread(target=observe)
            observer.start()
            try:
                result = client.run_task({"prompt": "fixture", "max_seconds": 3, "max_output_bytes": 4096}, cancel_event=cancelled)
            finally:
                observer_stop.set()
                observer.join()
            self.assertEqual(result["termination"], "cancelled")
            self.assertTrue((Path(temporary) / "interrupted").exists())
            self.assertIn("INTERRUPTED", Path(result["stdout_path"]).read_text())

    def test_mode_observation_requires_expected_fixture_declarations_and_entry(self):
        with self.assertRaises(ValueError):
            inspect_tools("unknown-client", "lazy", [])
        namespace = {"type": "namespace", "name": "mcp__fixture", "tools": [{}] * 67}
        search = {"type": "tool_search"}
        execute = {"type": "function", "function": {"name": "execute"}}
        self.assertTrue(inspect_tools("codex", "eager", [namespace])["mode_observed"])
        self.assertTrue(inspect_tools("codex", "lazy", [search])["mode_observed"])
        self.assertFalse(inspect_tools("codex", "lazy", [namespace, search])["mode_observed"])
        self.assertFalse(inspect_tools("codex", "lazy", [])["mode_observed"])
        self.assertTrue(inspect_tools("opencode", "lazy", [execute])["mode_observed"])
        self.assertFalse(inspect_tools("opencode", "lazy", [execute, {"function": {"name": "fixture_ledger_budget_lookup"}}])["mode_observed"])


if __name__ == "__main__":
    unittest.main()
