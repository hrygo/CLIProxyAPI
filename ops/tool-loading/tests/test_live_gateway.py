import copy
from contextlib import contextmanager, redirect_stdout
import hashlib
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import sys
import threading
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from live_gateway import _ready_http, validate_ready, parse_credential_bindings, validate_profile, main


class LiveGatewayTests(unittest.TestCase):
    def test_startup_cli_forwards_explicit_native_file_binding(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "run"
            observed = []
            @contextmanager
            def candidate(binary, config, root, profile):
                observed.append(profile)
                yield {}, root / "capture"
            argv = ["live_gateway.py", "--gateway-binary", "unused", "--config", "unused", "--output-dir", str(output),
                    "--client-model", "fixture", "--upstream-model", "fixture", "--executor", "codex",
                    "--contract", "openai-responses-v1", "--auth-file-binding", "a" * 64 + ":" + "b" * 64]
            with patch("sys.argv", argv), patch("live_gateway.candidate_process", candidate), redirect_stdout(io.StringIO()):
                main()
            self.assertEqual(observed[0]["auth_files"], [{"name_sha256": "a" * 64, "content_sha256": "b" * 64}])

    def test_explicit_native_bindings_are_bounded_and_cannot_select_chat_credentials(self):
        value = "a" * 64 + ":" + "b" * 64
        bindings = parse_credential_bindings([value], "codex")
        self.assertEqual(bindings, [{"name_sha256": "a" * 64, "content_sha256": "b" * 64}])
        profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "codex",
                   "contract": "openai-responses-v1", "auth_files": bindings}
        validate_profile(profile)
        for values, executor in (([value, value], "codex"), ([value] * 9, "codex"),
                                 ([value.upper()], "codex"), (["bad"], "codex"), ([value], "openai-compatible-fixture")):
            with self.subTest(values=values, executor=executor), self.assertRaises(ValueError):
                parse_credential_bindings(values, executor)
        for change in ({"auth_files": []}, {"auth_files": [{"name_sha256": "a" * 64}]}, {"auth_files": "bad"},
                       {"executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                validate_profile({**profile, **change})

    def test_http_readiness_requires_instance_nonce_and_pid(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"pid":123,"ready_nonce":"actual-instance"}')

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            ready = {"base_url": f"http://127.0.0.1:{server.server_port}/v1", "pid": 123, "ready_nonce": "actual-instance"}
            self.assertTrue(_ready_http(ready))
            self.assertFalse(_ready_http({**ready, "pid": 124}))
            self.assertFalse(_ready_http({**ready, "ready_nonce": "different-instance"}))
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_readiness_pins_child_profile_config_and_loopback(self):
        profile = {"client_model": "fixture", "upstream_model": "fixture-model", "executor": "codex", "contract": "openai-responses-v1"}
        ready = {"version": 1, "scope": "isolated-live-candidate", "pid": 123, "base_url": "http://127.0.0.1:18419/v1",
                 "ready_nonce": "actual-instance", "profile": profile, "source_config_sha256": "a" * 64,
                 "source_policy_sha256": "b" * 64, "isolation_version": 4, "selected_auth_ids_sha256": hashlib.sha256(('["' + "c" * 64 + '"]').encode()).hexdigest(),
                 "selected_auth_ids": ["c" * 64], "selected_auth_count": 1, "credential_refresh_disabled": True}
        validate_ready(ready, 123, 18419, profile, "a" * 64)
        for field, value in (("pid", 124), ("source_config_sha256", "d" * 64), ("selected_auth_count", 0),
                             ("base_url", "http://example.com:18419/v1"), ("base_url", "http://127.0.0.1:18420/v1"),
                             ("ready_nonce", ""), ("isolation_version", True), ("credential_refresh_disabled", False), ("selected_auth_ids_sha256", "d" * 64)):
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                validate_ready({**ready, field: value}, 123, 18419, profile, "a" * 64)
        mutated = copy.deepcopy(ready)
        mutated["profile"]["executor"] = "other"
        with self.assertRaises(ValueError):
            validate_ready(mutated, 123, 18419, profile, "a" * 64)


if __name__ == "__main__":
    unittest.main()
