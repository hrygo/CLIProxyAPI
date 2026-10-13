"""Drive native client/MCP boundaries with scripted local model responses.

This is a deterministic client probe, not a real Provider or task benchmark.
The scripted model learns the nonce only from an actual tool-result receipt.
"""

from copy import deepcopy
import argparse
import json
import math
from pathlib import Path
import shutil
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from jsonschema import Draft202012Validator

from client_adapters import VERSIONS, prepare_isolated_client, synthetic_codex_model, write_private
from codex_app_adapter import run_app_server_task
from opencode_server_adapter import run_server_task


def _value(value, depth=0):
    if depth > 12:
        return None
    if isinstance(value, str):
        try:
            return _value(json.loads(value), depth + 1)
        except ValueError:
            return None
    if isinstance(value, dict):
        if isinstance(value.get("verification_code"), str) and type(value.get("remaining_budget")) is int:
            return {"verification_code": value["verification_code"], "remaining_budget": value["remaining_budget"]}
        values = value.values()
    elif isinstance(value, list):
        values = value
    else:
        return None
    for child in values:
        found = _value(child, depth + 1)
        if found:
            return found
    return None


def tool_receipt(client, request, call_id="invoke"):
    items = request.get("input", []) if client == "codex" else request.get("messages", [])
    for item in reversed(items):
        if client == "codex":
            if item.get("type") not in {"function_call_output", "custom_tool_call_output"} or item.get("call_id") != call_id:
                continue
            output = item.get("output")
            decoded_output = output.partition("\nOutput:\n")[2] if isinstance(output, str) and "\nOutput:\n" in output else output
        else:
            if item.get("role") != "tool" or item.get("tool_call_id") != call_id:
                continue
            output = item.get("content")
            decoded_output = output
        return {"raw": output, **(_value(decoded_output) or {})}
    return None


class ScriptedModel:
    def __init__(self, client, mode, scenario, *, codex_tool_mode=None):
        from codex_code_mode import requested_codex_mode
        self.codex_tool_mode = requested_codex_mode(client, codex_tool_mode)
        if client not in VERSIONS or mode not in {"eager", "lazy"} or scenario not in {"allow", "client-denied", "service-denied", "invalid", "revoked", "cancel", "loop", "dispatch-budget", "large-result"}:
            raise ValueError("unknown scripted profile")
        if scenario == "service-denied" and self.codex_tool_mode != "code_mode_only":
            raise ValueError("service-denied requires the explicit Codex Code Mode profile")
        if scenario in {"loop", "dispatch-budget", "large-result"} and (client, mode) != ("opencode", "lazy"):
            raise ValueError("this execution probe requires OpenCode Code Mode")
        self.client, self.mode, self.scenario = client, mode, scenario
        self.round = 0
        self.nonce_observed = self.invocation_emitted = False
        self.receipt = None
        self.search_result_has_target = False
        self.target = "fixture_wait" if scenario == "cancel" else "fixture_mutation" if scenario == "service-denied" else "ledger_budget_lookup"

    def next(self, request):
        stage = self.round
        self.round += 1
        if self.mode == "lazy" and stage == 0:
            query = "fixture wait until cancelled" if self.scenario == "cancel" else "fixture mutation increment synthetic counter" if self.scenario == "service-denied" else "ledger budget remaining alpha verification code"
            if self.client == "codex":
                if self.codex_tool_mode == "code_mode_only":
                    return {"type": "custom_tool_call", "id": "custom_search", "call_id": "search",
                            "name": "exec", "namespace": "functions",
                            "input": 'text(ALL_TOOLS.filter(t => t.name === '+json.dumps("mcp__fixture__"+self.target)+'));'}
                return {"type": "tool_search_call", "id": "tsc_search", "call_id": "search", "execution": "client",
                        "status": "completed", "arguments": {"query": query, "limit": 8}}
            return {"type": "function_call", "name": "execute", "call_id": "search",
                    "arguments": {"code": 'return await tools.$codemode.search({query:' + json.dumps(query) + ',limit:8});'}}
        if stage == (1 if self.mode == "lazy" else 0):
            if self.mode == "lazy":
                if self.client == "codex":
                    search_result = tool_receipt(self.client, request, "search") if self.codex_tool_mode == "code_mode_only" else next((item for item in request.get("input", [])
                                          if item.get("type") == "tool_search_output" and item.get("call_id") == "search"), None)
                else:
                    search_result = tool_receipt(self.client, request, "search")
                self.search_result_has_target = bool(search_result and self.target in json.dumps(search_result))
            self.invocation_emitted = True
            arguments = {"project": "beta" if self.scenario == "invalid" else "alpha"}
            if self.client == "codex":
                if self.codex_tool_mode == "code_mode_only":
                    return {"type": "custom_tool_call", "id": "custom_invoke", "call_id": "invoke",
                            "name": "exec", "namespace": "functions",
                            "input": "text(await tools.mcp__fixture__"+self.target+"("+json.dumps(arguments)+"));"}
                return {"type": "function_call", "id": "fc_invoke", "call_id": "invoke", "name": self.target,
                        "namespace": "mcp__fixture", "arguments": json.dumps(arguments)}
            if self.mode == "lazy":
                code = "return await tools.fixture." + self.target + "(" + json.dumps(arguments) + ");"
                if self.scenario == "loop":
                    code = "while (true) {}"
                elif self.scenario == "dispatch-budget":
                    code = 'const results=[]; for(let i=0;i<12;i++){results.push(await tools.fixture.ledger_budget_lookup({project:"alpha"}));} return results;'
                elif self.scenario == "large-result":
                    code = 'return await tools.fixture.fixture_large_result({project:"alpha",size:1048576});'
                return {"type": "function_call", "name": "execute", "call_id": "invoke",
                        "arguments": {"code": code}}
            return {"type": "function_call", "name": "fixture_" + self.target, "call_id": "invoke", "arguments": arguments}
        self.receipt = tool_receipt(self.client, request)
        self.nonce_observed = bool(self.receipt and "verification_code" in self.receipt)
        text = ("fixture final: " + str(self.receipt["remaining_budget"]) + " " + self.receipt["verification_code"]
                if self.nonce_observed else "fixture final: invocation rejected or result unavailable")
        return {"type": "message", "id": "msg_final", "role": "assistant", "status": "completed", "channel": "final",
                "content": [{"type": "output_text", "text": text, "annotations": []}]}


def response_events(item, response_id):
    initial = {"id": response_id, "object": "response", "created_at": 0, "model": "fixture-model", "status": "in_progress", "output": []}
    added = deepcopy(item)
    if item["type"] == "function_call":
        added["arguments"] = ""
    if item["type"] == "custom_tool_call":
        added["input"] = ""
    if item["type"] == "message":
        added["content"] = []
    events = [{"type": "response.created", "response": initial},
              {"type": "response.output_item.added", "output_index": 0, "item": added}]
    if item["type"] == "function_call":
        events.append({"type": "response.function_call_arguments.delta", "output_index": 0,
                       "item_id": item["id"], "delta": item["arguments"]})
        events.append({"type": "response.function_call_arguments.done", "output_index": 0,
                       "item_id": item["id"], "arguments": item["arguments"]})
    elif item["type"] == "custom_tool_call":
        events.append({"type": "response.custom_tool_call_input.delta", "output_index": 0,
                       "item_id": item["id"], "delta": item["input"]})
        events.append({"type": "response.custom_tool_call_input.done", "output_index": 0,
                       "item_id": item["id"], "input": item["input"]})
    elif item["type"] == "message":
        part = item["content"][0]
        refs = {"output_index": 0, "content_index": 0, "item_id": item["id"]}
        events.extend([{"type": "response.content_part.added", **refs, "part": {**part, "text": ""}},
                       {"type": "response.output_text.delta", **refs, "delta": part["text"]},
                       {"type": "response.output_text.done", **refs, "text": part["text"]},
                       {"type": "response.content_part.done", **refs, "part": part}])
    events.extend([{"type": "response.output_item.done", "output_index": 0, "item": item},
                   {"type": "response.completed", "response": {**initial, "status": "completed", "output": [item]}}])
    return [{**event, "sequence_number": index} for index, event in enumerate(events)]


def chat_chunks(item):
    if item["type"] == "function_call":
        delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": item["call_id"], "type": "function",
                 "function": {"name": item["name"], "arguments": json.dumps(item["arguments"])}}]}
        finish = "tool_calls"
    else:
        delta = {"role": "assistant", "content": item["content"][0]["text"]}
        finish = "stop"
    return [{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 0, "model": "fixture-model",
             "choices": [{"index": 0, "delta": payload, "finish_reason": reason}]}
            for payload, reason in ((delta, None), ({}, finish))]


def client_result(client, raw):
    texts, events = [], []
    complete = bool(raw) and raw.endswith("\n")
    for line in raw.splitlines():
        try:
            event = json.loads(line)
            if not isinstance(event, dict):
                complete = False
                continue
            events.append(event)
        except ValueError:
            complete = False
            continue
        if client == "codex" and event.get("type") == "item.completed" and event.get("item", {}).get("type") == "agent_message":
            texts.append(event["item"].get("text", ""))
        if client == "opencode" and event.get("type") == "text":
            texts.append(event.get("part", {}).get("text", ""))
    last = events[-1] if events else {}
    if client == "claude":
        terminal = (last.get("type") == "result" and last.get("subtype") == "success"
                    and last.get("is_error") is False and isinstance(last.get("result"), str)
                    and not any(event.get("type") == "assistant" and event.get("error") for event in events))
        return {"final_texts": [last["result"]] if complete and terminal else [],
                "legal_terminal": bool(complete and terminal), "parse_complete": complete}
    terminal = (last.get("type") == "turn.completed" if client == "codex"
                else last.get("type") == "step_finish" and last.get("part", {}).get("reason") == "stop")
    return {"final_texts": texts, "legal_terminal": bool(complete and terminal), "parse_complete": complete}


def claude_structured_result(raw, schema):
    """Bind a strict native result to its latest StructuredOutput tool use."""
    invalid = {"legal_terminal": False, "schema_valid": False,
               "invocation_verified": False, "structured_output": None}
    if not raw or not raw.endswith("\n"):
        return invalid

    def unique_object(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("duplicate JSON member")
            value[key] = item
        return value

    def reject_constant(value):
        raise ValueError("non-finite JSON constant")

    def finite_float(value):
        number = float(value)
        if not math.isfinite(number):
            raise ValueError("non-finite JSON number")
        return number

    try:
        events = [json.loads(line, object_pairs_hook=unique_object, parse_constant=reject_constant, parse_float=finite_float)
                  for line in raw.splitlines()]
    except (ValueError, RecursionError):
        return invalid
    if any(not isinstance(event, dict) for event in events):
        return invalid
    last = events[-1]
    legal = (last.get("type") == "result" and last.get("subtype") == "success"
             and last.get("is_error") is False
             and sum(event.get("type") == "result" for event in events) == 1
             and not any(event.get("type") == "assistant" and event.get("error") for event in events))
    value = last.get("structured_output")
    calls = []
    for event in events:
        message = event.get("message")
        content = message.get("content") if isinstance(message, dict) else None
        if event.get("type") == "assistant" and isinstance(content, list):
            calls.extend(block for block in content if isinstance(block, dict)
                         and block.get("type") == "tool_use" and block.get("name") == "StructuredOutput")
    identifiers = [call.get("id") for call in calls]
    bound = (bool(calls) and all(isinstance(value_id, str) and value_id for value_id in identifiers)
             and len(set(identifiers)) == len(identifiers) and isinstance(value, dict)
             and isinstance(calls[-1].get("input"), dict)
             and json.dumps(calls[-1]["input"], sort_keys=True, allow_nan=False)
             == json.dumps(value, sort_keys=True, allow_nan=False))
    valid = isinstance(value, dict) and Draft202012Validator(schema).is_valid(value)
    return {"legal_terminal": bool(legal), "schema_valid": valid,
            "invocation_verified": bool(legal and bound), "structured_output": value if legal else None}


def run_case(root, client, mode, scenario, *, max_seconds=20, transport="cli", codex_tool_mode=None):
    if (transport not in {"cli", "codex-app-server", "opencode-server"}
            or (transport == "codex-app-server" and client != "codex")
            or (transport == "opencode-server" and (client != "opencode" or scenario != "cancel"))):
        raise ValueError("unsupported scripted client transport")
    model = ScriptedModel(client, mode, scenario, codex_tool_mode=codex_tool_mode)
    requests, responses = [], []
    lock = threading.Lock()
    prepared = None

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 <= size <= 16 * 1024 * 1024:
                self.send_error(413)
                return
            request = json.loads(self.rfile.read(size))
            with lock:
                requests.append({"path": self.path, "body": request})
                write_private(prepared.run_dir / f"request-{len(requests):02}.json", json.dumps(request))
                if len(requests) > 16:
                    self.send_error(400, "scripted request budget")
                    return
                if client == "opencode" and "tools" not in request:
                    item = {"type": "message", "content": [{"text": "Local fixture title"}]}
                else:
                    item = model.next(request)
                    if scenario == "revoked" and item["type"] in {"function_call", "custom_tool_call"} and item["call_id"] == "invoke":
                        temporary = prepared.run_dir / "permissions-next.json"
                        write_private(temporary, json.dumps({"generation": 2, "denied": ["fixture_mutation", "ledger_budget_lookup"]}))
                        temporary.replace(prepared.run_dir / "permissions.json")
                        write_private(prepared.run_dir / "revocation.json", json.dumps({"request_received": len(requests), "call_id": item["call_id"], "generation": 2}))
                responses.append(item)
            events = response_events(item, "resp_" + str(len(requests))) if client == "codex" else chat_chunks(item)
            body = "".join("data: " + json.dumps(event) + "\n\n" for event in events)
            if client == "opencode":
                body += "data: [DONE]\n\n"
            encoded = body.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    nonce = uuid.uuid4().hex
    cancel_event, stop_observer = threading.Event(), threading.Event()
    observer = None
    try:
        metadata = synthetic_codex_model("fixture-model")
        if codex_tool_mode == "code_mode_only":
            metadata["tool_mode"] = "code_mode_only"
        inputs = {
            "run_dir": root / (f"{client}-{mode}-{scenario}" + ("-code-mode-only" if codex_tool_mode == "code_mode_only" else "")), "client_version": VERSIONS[client],
            "executable": shutil.which(client) or client, "fixture_python": sys.executable,
            "model": "fixture-model", "model_metadata": metadata,
            "base_url": f"http://127.0.0.1:{server.server_port}/v1", "nonce": nonce,
            "max_dispatches": 8, "max_result_bytes": 65536}
        if codex_tool_mode is not None:
            inputs["codex_tool_mode"] = codex_tool_mode
        prepared = prepare_isolated_client(client, mode, inputs,
            {"client_denied": ["ledger_budget_lookup"] if scenario == "client-denied" else [], "service_denied": ["fixture_mutation"]})
        write_private(prepared.run_dir / "case-started.json", json.dumps({"client": client, "version": VERSIONS[client], "mode": mode, "scenario": scenario, "transport": transport}))
        if scenario == "cancel":
            def observe_dispatch():
                while not stop_observer.wait(0.01):
                    try:
                        events = prepared.collect_boundary_events()
                    except ValueError:
                        continue
                    dispatched = next((event for event in events if event["phase"] == "dispatch" and event["tool"] == "fixture_wait"), None)
                    if dispatched:
                        write_private(prepared.run_dir / "cancel-requested.json", json.dumps({"after_mcp_sequence": dispatched["sequence"]}))
                        cancel_event.set()
                        return
            observer = threading.Thread(target=observe_dispatch, daemon=True)
            observer.start()
        if transport == "cli":
            runner = prepared.run_task
        else:
            server_runner = run_app_server_task if transport == "codex-app-server" else run_server_task
            runner = lambda spec, **kwargs: server_runner(prepared, spec, **kwargs)
        result = runner({"prompt": "Use the fixture to read alpha remaining budget and verification code.",
                         "max_seconds": max_seconds, "max_output_bytes": 1000000}, cancel_event=cancel_event)
    finally:
        stop_observer.set()
        if observer:
            observer.join()
        server.shutdown()
        server.server_close()
        thread.join()
    events = prepared.collect_boundary_events()
    write_private(prepared.run_dir / "responses.json", json.dumps(responses))
    dispatches = sum(event["phase"] == "dispatch" for event in events)
    effects = max((event["effect_count"] for event in events), default=0)
    active = events[-1]["active_count"] if events else 0
    clean_exit = result["exit_code"] == 0 and result["termination"] == "exited"
    receipt_nonce = model.receipt.get("verification_code") if model.receipt else None
    if transport == "cli":
        observed = client_result(client, Path(result["stdout_path"]).read_text())
    else:
        clean_exit = result["termination"] == "completed" and result["protocol_terminal_status"] == "completed"
        observed = {"final_texts": result["final_texts"], "legal_terminal": result["protocol_terminal_verified"]}
    service_allowed = any(event["phase"] == "catalog" and event.get("ledger_allowed") is True for event in events)
    verified = (service_allowed and clean_exit and observed["legal_terminal"] and model.invocation_emitted
                and bool(model.receipt) and effects == active == 0)
    if scenario == "allow":
        verified = (verified and dispatches == 1 and receipt_nonce == nonce
                    and any("fixture final: 424242 " + nonce in text for text in observed["final_texts"])
                    and (mode == "eager" or model.search_result_has_target))
    elif scenario in {"client-denied", "service-denied", "invalid", "revoked"}:
        verified = verified and dispatches == 0 and receipt_nonce is None
        if scenario == "client-denied":
            verified = verified and not any(event["phase"] == "request" for event in events)
        if scenario == "revoked":
            revocation = prepared.run_dir / "revocation.json"
            verified = (verified and revocation.exists()
                        and json.loads(revocation.read_text()).get("call_id") == "invoke"
                        and any(event.get("reason") == "permission_denied" for event in events)
                        and (mode == "eager" or model.search_result_has_target))
        if scenario == "invalid":
            verified = verified and any(event.get("reason") == "invalid_arguments" for event in events)
        if scenario == "service-denied":
            from codex_code_mode import observe_hidden_tool_rejection
            service_rejected = any(event.get("reason") == "permission_denied" and event.get("tool") == "fixture_mutation" for event in events)
            client_rejected = (not any(event["phase"] == "request" for event in events)
                               and observe_hidden_tool_rejection([request["body"] for request in requests], "fixture_mutation"))
            verified = verified and (service_rejected or client_rejected)
    elif scenario == "cancel":
        verified = (service_allowed and model.invocation_emitted and result["termination"] == "cancelled"
                    and dispatches == 1 and effects == active == 0
                    and any(event["phase"] == "cancelled" for event in events)
                    and any(event["phase"] == "released" for event in events))
    elif scenario == "loop":
        verified = (verified and dispatches == 0 and any(word in json.dumps(model.receipt).lower() for word in ("budget exceeded", "limit exceeded")))
    elif scenario == "dispatch-budget":
        verified = (verified and dispatches == 8 and any(event.get("reason") == "dispatch_budget" for event in events))
    elif scenario == "large-result":
        verified = (verified and dispatches == 1 and any(event.get("reason") == "result_budget" for event in events))
    code_observation = None
    if codex_tool_mode == "code_mode_only":
        from codex_code_mode import observe_code_mode, observe_code_discovery
        from native_wire import declarations
        code_observation = observe_code_mode(declarations("openai-responses-v1", requests[0]["body"]), mode,
            client_denied=["ledger_budget_lookup"] if scenario == "client-denied" else [],
            service_denied=["fixture_mutation"]) if requests else {"effective":False}
        if mode == "lazy":
            code_observation.update(observe_code_discovery([request["body"] for request in requests]))
            if scenario == "allow":
                verified = verified and model.target in code_observation["loaded_fixture_schema_tools"]
            verified = (verified and code_observation["discovery_result_observed"]
                        and code_observation["history_identity_consistent"]
                        and not (set(code_observation["invocations_before_schema"])
                                 & {event["tool"] for event in events if event["phase"] == "dispatch"}))
        verified = verified and code_observation["effective"]
    summary = {"client": client, "client_version": VERSIONS[client], "mode": mode, "scenario": scenario,
               "codex_tool_mode": codex_tool_mode,
               "code_mode_observation": code_observation,
               "scope": "scripted-local-client-boundary", "transport": transport,
               "requests_received": len(requests), "mcp_dispatches": dispatches,
               "mcp_requests": sum(event["phase"] == "request" for event in events), "effects": effects,
               "active_final": active, "nonce_backfilled": receipt_nonce == nonce, "boundary_verified": bool(verified),
               "search_result_has_target": model.search_result_has_target if mode == "lazy" else None,
               "legal_client_terminal": observed["legal_terminal"],
               "service_initially_allowed_target": service_allowed,
               "cancellation_observed": any(event["phase"] == "cancelled" for event in events),
               "protocol_cancel_notifications": sum(event["phase"] == "protocol_cancel_received" for event in events),
               "provider_input_tokens": None, "gate_status": "unverified", "run_dir": str(prepared.run_dir), **result}
    write_private(prepared.run_dir / "summary.json", json.dumps(summary, indent=2))
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--clients", nargs="+", choices=list(VERSIONS), default=list(VERSIONS))
    parser.add_argument("--modes", nargs="+", choices=["eager", "lazy"], default=["eager", "lazy"])
    parser.add_argument("--codex-tool-mode", choices=["direct", "code_mode_only"])
    parser.add_argument("--transport", choices=["cli", "codex-app-server", "opencode-server"], default="cli")
    parser.add_argument("--cases", nargs="+", choices=["allow", "client-denied", "service-denied", "invalid", "revoked", "cancel", "loop", "dispatch-budget", "large-result"], default=["allow", "client-denied", "invalid", "revoked"])
    args = parser.parse_args()
    if args.codex_tool_mode is not None and args.clients != ["codex"]:
        parser.error("explicit Codex mode requires --clients codex")
    if args.transport == "codex-app-server" and args.clients != ["codex"]:
        parser.error("codex-app-server requires --clients codex")
    if args.transport == "opencode-server" and (args.clients != ["opencode"] or args.cases != ["cancel"]):
        parser.error("opencode-server requires --clients opencode --cases cancel")
    for client in args.clients:
        for mode in args.modes:
            for scenario in args.cases:
                ScriptedModel(client, mode, scenario, codex_tool_mode=args.codex_tool_mode)
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    results = []
    for client in args.clients:
        for mode in args.modes:
            for scenario in args.cases:
                result = run_case(args.output_dir.resolve(), client, mode, scenario, transport=args.transport, codex_tool_mode=args.codex_tool_mode)
                results.append(result)
                print(json.dumps(result), flush=True)
    write_private(args.output_dir / "summary.json", json.dumps({"gate_status": "unverified", "cases": results}, indent=2))
    if not all(result["boundary_verified"] for result in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
