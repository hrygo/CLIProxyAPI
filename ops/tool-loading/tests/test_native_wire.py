"""Native route wire views must prove declarations and legal terminals."""
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from native_wire import wire_contract, declarations, declaration_bytes, successful_terminal


class NativeWireTests(unittest.TestCase):
    def test_explicit_supported_contracts_never_guess_unknown_routes(self):
        self.assertEqual(wire_contract("meta"),"openai-responses-v1")
        self.assertEqual(wire_contract("codex"),"openai-responses-v1")
        self.assertEqual(wire_contract("openai-compatible-fixture"),"openai-chat-v1")
        for provider in ("unknown","aistudio","gemini-cli"):
            with self.assertRaises(ValueError): wire_contract(provider)

    def test_namespace_schema_count_and_lite_additional_tools_are_visible(self):
        body={"tools":[{"type":"namespace","name":"fixture","tools":[{"type":"function","name":"one"},{"type":"function","name":"two"}]}]}
        self.assertEqual(len(declarations("openai-responses-v1",body)),2)
        self.assertEqual(declaration_bytes("openai-responses-v1",body),len(json.dumps(body["tools"],separators=(",", ":")).encode()))
        body={"input":[{"type":"tool_search_output","additional_tools":[{"type":"namespace","name":"fixture","tools":[{"type":"function","name":"three"}]}]}]}
        self.assertEqual(declarations("openai-responses-v1",body)[0]["name"],"three")
        self.assertEqual(declaration_bytes("openai-responses-v1",body),len(json.dumps(body["input"][0]["additional_tools"],separators=(",", ":")).encode()))
        with self.assertRaises(ValueError): declarations("openai-responses-v1",{"tools":[{"type":"namespace","tools":"bad"}]})

    def test_responses_complete_success_is_separate_from_failed_terminal(self):
        def sse(status):
            kind={"completed":"response.completed","failed":"response.failed","incomplete":"response.incomplete"}[status]
            return ("event: "+kind+"\ndata: "+json.dumps({"type":kind,"response":{"id":"r","status":status}})+"\n\n").encode()
        self.assertTrue(successful_terminal("openai-responses-v1",sse("completed")))
        self.assertFalse(successful_terminal("openai-responses-v1",sse("failed")))
        self.assertFalse(successful_terminal("openai-responses-v1",sse("incomplete")))
        self.assertFalse(successful_terminal("openai-responses-v1",b"data: [DONE]\n\n"))
        self.assertFalse(successful_terminal("openai-responses-v1",sse("completed")[:-1]))
        self.assertTrue(successful_terminal("openai-responses-v1",json.dumps({"id":"r","status":"completed","output":[]}).encode()))
        self.assertTrue(successful_terminal("openai-responses-v1",json.dumps({"id":"r","status":"completed","output":[],"error":None}).encode()))
        self.assertFalse(successful_terminal("openai-responses-v1",json.dumps({"id":"r","status":"incomplete","output":[]}).encode()))
        error = {"id":"r","status":"completed","output":[],"error":{"code":"server_error"}}
        self.assertFalse(successful_terminal("openai-responses-v1",json.dumps(error).encode()))
        self.assertFalse(successful_terminal("openai-responses-v1",("data: "+json.dumps({"type":"response.completed","response":error})+"\n\n").encode()))

    def test_native_terminal_rejects_duplicate_trailing_error_and_conflicting_ids(self):
        complete=b'data: {"type":"response.completed","response":{"id":"r","status":"completed"}}\n\n'
        self.assertFalse(successful_terminal("openai-responses-v1",complete+complete))
        self.assertFalse(successful_terminal("openai-responses-v1",complete+b'data: {"type":"error"}\n\n'))
        self.assertFalse(successful_terminal("openai-responses-v1",b'data: {"type":"response.created","response":{"id":"other"}}\n\n'+complete))
        self.assertFalse(successful_terminal("openai-responses-v1",b'data: {"type":"response.completed","response":{"id":"r","status":"completed","status":"failed"}}\n\n'))

    def test_chat_stream_cannot_pass_truncated_frames_or_conflicting_identities(self):
        complete = b'data: {"id":"a","choices":[{"index":0,"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'
        self.assertTrue(successful_terminal("openai-chat-v1", complete))
        for body in (complete[:-1], complete.replace(b'data: [DONE]\n\n', b''), complete + complete,
                     b'data: {"id":"other","choices":[{"index":0,"finish_reason":null}]}\n\n' + complete):
            with self.subTest(body=body):
                self.assertFalse(successful_terminal("openai-chat-v1", body))


if __name__=="__main__": unittest.main()
