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
        body={"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"fixture","tools":[{"type":"function","name":"three"}]}]}]}
        self.assertEqual(declarations("openai-responses-v1",body)[0]["name"],"three")
        self.assertEqual(declaration_bytes("openai-responses-v1",body),len(json.dumps(body["input"][0]["tools"],separators=(",", ":")).encode()))
        with self.assertRaises(ValueError): declarations("openai-responses-v1",{"tools":[{"type":"namespace","tools":"bad"}]})

    def test_actual_lite_and_search_history_declarations_are_metered(self):
        leaf = {"type": "function", "name": "confirm", "parameters": {"type": "object"}}
        namespace = {"type": "namespace", "name": "fixture", "description": "Fixture tools", "tools": [leaf]}
        body = {"tools": [leaf], "input": [
            {"type": "additional_tools", "tools": [namespace]},
            {"type": "tool_search_output", "tools": [namespace]},
            {"type": "function_call_output", "tools": [leaf], "output": "ordinary result"},
            {"type": "tool_search_output", "additional_tools": [leaf]},
        ]}
        self.assertEqual(declarations("openai-responses-v1", body), [leaf, leaf, leaf])
        containers = ([leaf], [namespace], [namespace])
        self.assertEqual(declaration_bytes("openai-responses-v1", body),
                         sum(len(json.dumps(value, separators=(",", ":")).encode()) for value in containers))
        # Repeated transport occurrences must remain visible, even if the gateway
        # later deduplicates the active callable set. Chat does not have this history.
        self.assertEqual(declarations("openai-chat-v1", body), [leaf])

    def test_malformed_recognized_history_containers_cannot_disappear(self):
        for item in ({"type": "additional_tools"},
                     {"type": "additional_tools", "tools": "invalid"},
                     {"type": "tool_search_output", "tools": None},
                     {"type": "tool_search_output", "tools": [None]}):
            with self.subTest(item=item):
                with self.assertRaises(ValueError):
                    declarations("openai-responses-v1", {"input": [item]})
                with self.assertRaises(ValueError):
                    declaration_bytes("openai-responses-v1", {"input": [item]})
        self.assertEqual(declarations("openai-responses-v1", {"input": [
            {"type": "additional_tools", "tools": []}, {"type": "tool_search_output"},
        ]}), [])

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

    def test_antigravity_wrapped_declarations_include_both_supported_spellings(self):
        self.assertEqual(wire_contract("antigravity"), "antigravity-generate-v1")
        one = {"name": "lookup", "parameters": {"type": "OBJECT"}}
        two = {"name": "confirm", "parameters": {"type": "OBJECT"}}
        tools = [{"functionDeclarations": [one]}, {"function_declarations": [two]}, {"googleSearch": {}}]
        body = {"request": {"tools": tools}, "tools": [{"name": "unrelated-wrapper-field"}]}
        self.assertEqual(declarations("antigravity-generate-v1", body), [one, two])
        self.assertEqual(declaration_bytes("antigravity-generate-v1", body),
                         len(json.dumps(tools, separators=(",", ":")).encode()))
        self.assertEqual(declarations("antigravity-generate-v1", {"request": {"tools": [
            {"functionDeclarations": [one], "function_declarations": [two]}]}}), [one, two])
        for malformed in ({"tools": tools}, {"request": None},
                          {"request": {"tools": "invalid"}},
                          {"request": {"tools": [{"functionDeclarations": None}]}}):
            with self.subTest(malformed=malformed), self.assertRaises(ValueError):
                declarations("antigravity-generate-v1", malformed)

    def test_antigravity_explicit_terminal_requires_consistent_wrapped_candidates(self):
        def sse(inner):
            return ("data: " + json.dumps({"response": inner}) + "\n\n").encode()
        final = {"responseId": "r", "candidates": [{"index": 0, "finishReason": "STOP"}],
                 "usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 3}}
        self.assertTrue(successful_terminal("antigravity-generate-v1", sse(final)))
        self.assertTrue(successful_terminal("antigravity-generate-v1", json.dumps({"response": final}).encode()))
        for bad in (sse({**final, "candidates": [{"finishReason": "MAX_TOKENS"}]}),
                    sse({**final, "error": {"code": 500}}), sse(final) + sse(final),
                    sse({"responseId": "other", "candidates": [{"content": {"parts": [{"text": "partial"}]}}]}) + sse(final),
                    sse(final) + b'data: {"error":{"code":500}}\n\n',
                    json.dumps(final).encode(), sse(final)[:-1],
                    sse({**final, "candidates": [{"index": True, "finishReason": "STOP"}]}),
                    sse({**final, "candidates": [{"finishReason": "FUTURE_UNKNOWN_REASON"}]})):
            with self.subTest(body=bad):
                self.assertFalse(successful_terminal("antigravity-generate-v1", bad))

    def test_antigravity_missing_finish_needs_actual_complete_transport_and_usage(self):
        inner = {"responseId": "r", "candidates": [{"content": {"parts": [
            {"functionCall": {"name": "lookup", "args": {}}}]}}],
            "usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 3}}
        body = ("data: " + json.dumps({"response": inner}) + "\n").encode()
        self.assertFalse(successful_terminal("antigravity-generate-v1", body))
        self.assertTrue(successful_terminal("antigravity-generate-v1", body, transport_complete=True))
        for bad in ({**inner, "usageMetadata": None}, {**inner, "candidates": []},
                    {**inner, "candidates": [{"content": {"parts": []}}]},
                    {**inner, "usageMetadata": {"promptTokenCount": True, "candidatesTokenCount": 3}}):
            with self.subTest(inner=bad):
                payload = ("data: " + json.dumps({"response": bad}) + "\n").encode()
                self.assertFalse(successful_terminal("antigravity-generate-v1", payload, transport_complete=True))

    def test_antigravity_all_candidates_finish_and_failed_terminal_is_meterable(self):
        from http_attempts import _closed_protocol_terminal
        def sse(candidates):
            return ("data: " + json.dumps({"response": {"responseId": "r", "candidates": candidates}}) + "\n\n").encode()
        both = sse([{"index": 0, "finishReason": "STOP"}, {"index": 1, "finishReason": "STOP"}])
        self.assertTrue(successful_terminal("antigravity-generate-v1", both))
        self.assertFalse(successful_terminal("antigravity-generate-v1", sse([
            {"index": 0, "finishReason": "STOP"}, {"index": 1, "content": {"parts": [{"text": "partial"}]}}])))
        failed = sse([{"finishReason": "MAX_TOKENS"}])
        self.assertFalse(successful_terminal("antigravity-generate-v1", failed))
        self.assertTrue(_closed_protocol_terminal("antigravity-generate-v1", failed))
        self.assertFalse(_closed_protocol_terminal("antigravity-generate-v1", sse([{"finishReason": "FUTURE_UNKNOWN_REASON"}])))


if __name__=="__main__": unittest.main()
