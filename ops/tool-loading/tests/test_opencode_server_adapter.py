"""A successful abort HTTP response alone never proves cancelled execution."""

from pathlib import Path
from copy import deepcopy
import json
import os
import sys
import tempfile
import textwrap
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from opencode_server_adapter import abort_terminal, structured_terminal, structured_reply, run_server_task
from task_scenarios import output_schema


class AbortTerminalTests(unittest.TestCase):
    def test_completed_native_session_does_not_certify_interruption(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "workspace").mkdir()
            config = root / "config.json"
            config.write_text(json.dumps({"model": "fixture/fixture-model"}))
            executable = root / "fake-opencode"
            message = self.structured_messages()[-1]
            executable.write_text("#!/usr/bin/env python3\n" + textwrap.dedent("""
                from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
                import json
                class Handler(BaseHTTPRequestHandler):
                    def do_POST(self):
                        self.rfile.read(int(self.headers.get('Content-Length', '0')))
                        value = {'id':'ses_fixture'} if self.path == '/session' else MESSAGE
                        self.send_response(200)
                        self.end_headers()
                        self.wfile.write(json.dumps(value).encode())
                    def do_GET(self):
                        self.send_response(200)
                        self.end_headers()
                        self.wfile.write(b'{}')
                    def log_message(self, *args): pass
                server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
                print('opencode server listening on http://127.0.0.1:' + str(server.server_port), flush=True)
                server.serve_forever()
            """).replace("MESSAGE", repr(message)))
            executable.chmod(0o700)
            prepared = SimpleNamespace(client_id="opencode", client_version="1.19.0", run_dir=root,
                config_path=config, command=[str(executable)], env={"PATH": os.environ["PATH"]})
            result = run_server_task(prepared, {"prompt": "fixture prompt", "max_seconds": 5,
                "max_output_bytes": 1048576, "output_schema": output_schema("lookup"), "session_title": "Local fixture"})
            self.assertEqual(result["termination"], "exited")
            self.assertTrue(result["protocol_terminal_verified"])
            self.assertFalse(result["interrupt_acknowledged"])
            self.assertFalse(result["interrupt_verified"])
            self.assertFalse(result["forced_process_cleanup"])

    def test_synchronous_reply_needs_matching_session_and_completed_output_call(self):
        message = self.structured_messages()[-1]
        self.assertEqual(structured_reply("ses_fixture", {}, message, output_schema("lookup")), message["info"]["structured"])
        for value in ("another-session", ""):
            self.assertIsNone(structured_reply(value, {}, message, output_schema("lookup")))
        message["info"]["parentID"] = ""
        self.assertIsNone(structured_reply("ses_fixture", {}, message, output_schema("lookup")))

    def structured_messages(self):
        value = {"remaining_budget": 424242, "verification_code": "fresh"}
        return [{"info": {"id": "user-1", "sessionID": "ses_fixture", "role": "user"},
                 "parts": [{"type": "text", "text": "fixture prompt"}]},
                {"info": {"id": "assistant-1", "parentID": "user-1", "sessionID": "ses_fixture", "role": "assistant",
                          "finish": "tool-calls", "time": {"completed": 123}, "structured": value},
                 "parts": [{"type": "tool", "tool": "StructuredOutput", "callID": "call-1",
                            "state": {"status": "completed", "input": deepcopy(value)}}]}]

    def test_structured_terminal_requires_native_completed_call_and_idle_state(self):
        args = ("ses_fixture", {}, self.structured_messages(), output_schema("lookup"), "fixture prompt")
        self.assertEqual(structured_terminal(*args), {"remaining_budget": 424242, "verification_code": "fresh"})
        self.assertIsNone(structured_terminal("ses_fixture", {"ses_fixture": {"type": "busy"}}, *args[2:]))
        self.assertIsNone(structured_terminal("other-session", *args[1:]))
        self.assertIsNone(structured_terminal(*args[:-1], "other prompt"))

    def test_structured_document_alone_or_error_or_stale_call_is_not_success(self):
        for field, value in (("finish", "length"), ("error", {"name": "MessageAbortedError"}),
                             ("parentID", "old-user"), ("time", {}), ("structured", {"remaining_budget": True, "verification_code": "fresh"})):
            messages = self.structured_messages()
            messages[-1]["info"][field] = value
            with self.subTest(field=field):
                self.assertIsNone(structured_terminal("ses_fixture", {}, messages, output_schema("lookup"), "fixture prompt"))
        for change in ("missing-call", "running", "different-input", "duplicate-call", "later-error"):
            messages = self.structured_messages()
            if change == "missing-call": messages[-1]["parts"] = []
            if change == "running": messages[-1]["parts"][0]["state"]["status"] = "running"
            if change == "different-input": messages[-1]["parts"][0]["state"]["input"]["verification_code"] = "other"
            if change == "duplicate-call": messages[-1]["parts"].append(deepcopy(messages[-1]["parts"][0]))
            if change == "later-error": messages.append({"info": {"sessionID": "ses_fixture", "role": "assistant", "parentID": "user-1", "error": {"name": "APIError"}}, "parts": []})
            with self.subTest(change=change):
                self.assertIsNone(structured_terminal("ses_fixture", {}, messages, output_schema("lookup"), "fixture prompt"))

    def test_requires_actual_session_error_ack_and_idle_state(self):
        message = {"info": {"sessionID": "ses_fixture", "role": "assistant",
                            "error": {"name": "MessageAbortedError", "data": {"message": "Aborted"}}}}
        self.assertFalse(abort_terminal("ses_fixture", True, {}, []))
        self.assertFalse(abort_terminal("ses_fixture", False, {}, [message]))
        self.assertFalse(abort_terminal("ses_fixture", True, {"ses_fixture": {"type": "busy"}}, [message]))
        self.assertFalse(abort_terminal("another_session", True, {}, [message]))
        self.assertTrue(abort_terminal("ses_fixture", True, {}, [message]))


if __name__ == "__main__":
    unittest.main()
