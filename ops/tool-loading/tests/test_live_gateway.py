import copy
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import sys
import threading
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from live_gateway import _ready_http, validate_ready


class LiveGatewayTests(unittest.TestCase):
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
                 "source_policy_sha256": "b" * 64, "isolation_version": 3, "selected_auth_ids_sha256": hashlib.sha256(('["' + "c" * 64 + '"]').encode()).hexdigest(),
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
