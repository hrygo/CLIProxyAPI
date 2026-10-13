"""Pinned Claude Code/MCP protocol probes, using scripted loopback models only.

Synthetic usage never measures Provider context savings or billing. Every run
gets a fresh private client, MCP service, receipts, and optional gateway process.
"""

from contextlib import nullcontext
import argparse
import json
from pathlib import Path
import shutil
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib

from catalog_fixture import build_catalog, canonical_bytes, confirmation_code
from client_adapters import CLAUDE_VERSION, prepare_isolated_client, synthetic_codex_model, write_private
from http_attempts import read_http_attempts, read_admission_diagnostics, read_auxiliary_work_diagnostics
from live_gateway import candidate_process
from scripted_clients import chat_chunks, client_result, claude_structured_result
from task_scenarios import output_schema

LOOKUP = "mcp__fixture__ledger_budget_lookup"
CONFIRM = "mcp__fixture__ledger_budget_confirm"


def inspect_claude_tools(mode, tools, *, structured_output=False):
    names = [tool.get("name") for tool in tools]
    expected = {"mcp__fixture__" + tool["name"] for tool in build_catalog() if tool["name"] != "fixture_mutation"}
    if structured_output:
        expected.add("StructuredOutput")
    lazy_names = {"ToolSearch", "DeferredToolPlaceholder"} | ({"StructuredOutput"} if structured_output else set())
    valid = (set(names) == expected and len(names) == len(expected) and all(not tool.get("defer_loading") for tool in tools)) if mode == "eager" else (
        len(names) == len(lazy_names) and set(names) == lazy_names
        and next(tool for tool in tools if tool.get("name") == "DeferredToolPlaceholder").get("defer_loading") is True)
    return {"mode_observed": valid, "declaration_count": len(tools), "first_declaration_bytes": len(canonical_bytes(tools))}


def claude_result(raw):
    observed = client_result("claude", raw)
    return {"terminal": observed["legal_terminal"],
            "text": observed["final_texts"][-1] if observed["final_texts"] else ""}


def _result(body, contract, call_id):
    for message in reversed(body.get("messages", [])):
        if contract == "chat":
            if message.get("role") == "tool" and message.get("tool_call_id") == call_id:
                return message.get("content")
        elif message.get("role") == "user" and isinstance(message.get("content"), list):
            for part in message["content"]:
                if part.get("type") == "tool_result" and part.get("tool_use_id") == call_id and not part.get("is_error"):
                    return part.get("content")
    return None


def _object(value):
    if not isinstance(value, str):
        return value if isinstance(value, dict) else None
    try:
        decoded, _ = json.JSONDecoder().raw_decode(value.lstrip())
    except ValueError:
        return None
    return decoded if isinstance(decoded, dict) else None


class ScriptedClaudeModel:
    def __init__(self, mode, contract, *, structured_output=False):
        self.mode, self.contract, self.round = mode, contract, 0
        self.structured_output = structured_output
        self.discovery_verified = mode == "eager"
        self.receipts_verified = False
        self.issues = []
        self.lookup = None

    def next(self, body):
        definitions = {tool.get("function", {}).get("name"): tool.get("function", {}).get("parameters")
                       for tool in body.get("tools", [])} if self.contract == "chat" else {
                           tool.get("name"): tool.get("input_schema") for tool in body.get("tools", [])}
        stage = self.round
        self.round += 1
        if stage == 0 and self.mode == "lazy":
            return self.call("ToolSearch", "search", {"query": "select:" + LOOKUP + "," + CONFIRM, "max_results": 2}) if isinstance(definitions.get("ToolSearch"), dict) else self.failure("search_schema_missing")
        if stage == int(self.mode == "lazy"):
            if self.mode == "lazy":
                search = _result(body, self.contract, "search")
                names = {part.get("tool_name") for part in search if isinstance(part, dict) and part.get("type") == "tool_reference"} if isinstance(search, list) else {
                    item.get("name") for item in (_object(search) or {}).get("loaded_tools", []) if isinstance(item, dict)}
                self.discovery_verified = names == {LOOKUP, CONFIRM}
                if not self.discovery_verified:
                    return self.failure("discovery_receipt_missing")
            if not all(isinstance(definitions.get(name), dict) for name in (LOOKUP, CONFIRM)):
                return self.failure("loaded_schema_missing")
            return self.call(LOOKUP, "lookup", {"project": "alpha"})
        if stage == 1 + int(self.mode == "lazy"):
            self.lookup = _object(_result(body, self.contract, "lookup"))
            if not self.lookup or type(self.lookup.get("remaining_budget")) is not int or not isinstance(self.lookup.get("verification_code"), str):
                return self.failure("lookup_receipt_missing")
            if not isinstance(definitions.get(CONFIRM), dict):
                return self.failure("confirmation_schema_missing")
            return self.call(CONFIRM, "confirm", {"project": "alpha", "verification_code": self.lookup["verification_code"]})
        if stage == 2 + int(self.mode == "lazy"):
            confirmed = _object(_result(body, self.contract, "confirm"))
            if not confirmed or not isinstance(confirmed.get("confirmation_code"), str):
                return self.failure("confirmation_receipt_missing")
            self.receipts_verified = True
            answer = {
                "remaining_budget": self.lookup["remaining_budget"], "verification_code": self.lookup["verification_code"],
                "confirmation_code": confirmed["confirmation_code"]}
            if self.structured_output:
                if not isinstance(definitions.get("StructuredOutput"), dict):
                    return self.failure("structured_schema_missing")
                return self.call("StructuredOutput", "structured", answer)
            return {"type": "message", "content": [{"type": "output_text", "text": json.dumps(answer)}]}
        return self.failure("unexpected_extra_round")

    @staticmethod
    def call(name, call_id, arguments):
        return {"type": "function_call", "name": name, "call_id": call_id, "arguments": arguments}

    def failure(self, issue):
        self.issues.append(issue)
        return {"type": "message", "content": [{"type": "output_text", "text": "Local protocol probe could not verify the tool receipt."}]}


def anthropic_events(item, model, index):
    call = item["type"] == "function_call"
    block = {"type": "tool_use", "id": item["call_id"], "name": item["name"], "input": {}} if call else {"type": "text", "text": ""}
    delta = {"type": "input_json_delta", "partial_json": json.dumps(item["arguments"])} if call else {
        "type": "text_delta", "text": item["content"][0]["text"]}
    return [("message_start", {"type": "message_start", "message": {"id": "msg_fixture_" + str(index),
        "type": "message", "role": "assistant", "model": model, "content": [], "stop_reason": None,
        "stop_sequence": None, "usage": {"input_tokens": 100, "output_tokens": 1}}}),
        ("content_block_start", {"type": "content_block_start", "index": 0, "content_block": block}),
        ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": delta}),
        ("content_block_stop", {"type": "content_block_stop", "index": 0}),
        ("message_delta", {"type": "message_delta", "delta": {"stop_reason": "tool_use" if call else "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 10}}),
        ("message_stop", {"type": "message_stop"})]


def probe(root, mode, *, binary=None, structured_output=False):
    contract = "chat" if binary else "messages"
    case = Path(root) / (contract + "-" + mode + ("-native-json-schema" if structured_output else ""))
    case.mkdir(mode=0o700)
    requests = []
    model = ScriptedClaudeModel(mode, contract, structured_output=structured_output)
    nonce = uuid.uuid4().hex

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            if self.path not in {"/v1/messages?beta=true", "/v1/messages", "/v1/chat/completions"}:
                self.send_error(404)
                return
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= 1048576:
                self.send_error(413)
                return
            body = json.loads(self.rfile.read(size))
            requests.append({"path": self.path, "body": body, "beta": self.headers.get("anthropic-beta")})
            if len(requests) > 8:
                self.send_error(429)
                return
            item = model.next(body)
            if contract == "messages":
                encoded = "".join("event: " + name + "\ndata: " + json.dumps(event) + "\n\n"
                                  for name, event in anthropic_events(item, body["model"], len(requests))).encode()
            else:
                chunks = chat_chunks(item)
                chunks[-1]["usage"] = {"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110}
                encoded = ("".join("data: " + json.dumps(chunk) + "\n\n" for chunk in chunks) + "data: [DONE]\n\n").encode()
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
    gateway = nullcontext(({"base_url": f"http://127.0.0.1:{server.server_port}/v1"}, None))
    if binary:
        source = case / "source-fixture.json"
        write_private(source, json.dumps({"config-version": 8, "api-keys": {"openai-compatibility": [{
            "name": "claude-fixture", "base-url": f"http://127.0.0.1:{server.server_port}/v1", "request-retry": 0,
            "models": [{"name": "fixture-model", "alias": "claude-sonnet-4-5"}], "keys": [{"api-key": "synthetic-upstream-key"}]}]},
            "requests": {"responses-tools": {"enabled": True}}}))
        profile = {"client_model": "claude-sonnet-4-5", "upstream_model": "fixture-model",
                   "executor": "openai-compatible-claude-fixture", "contract": "openai-chat-v1"}
        gateway = candidate_process(binary, source, case, profile, max_attempts=8)
    try:
        with gateway as (ready, capture):
            client_inputs = {"run_dir": case / "client", "client_version": CLAUDE_VERSION,
                "executable": shutil.which("claude") or "claude", "fixture_python": sys.executable,
                "model": "claude-sonnet-4-5", "model_metadata": synthetic_codex_model("claude-sonnet-4-5"),
                "base_url": ready["base_url"], "nonce": nonce, "max_dispatches": 8, "max_result_bytes": 65536}
            if structured_output:
                client_inputs["output_schema"] = output_schema("multitool")
            prepared = prepare_isolated_client("claude", mode, client_inputs,
                {"client_denied": [], "service_denied": ["fixture_mutation"]})
            result = prepared.run_task({"prompt": "Read alpha remaining budget, then confirm its actual returned verification_code using the fixture. Return exact JSON with remaining_budget, verification_code, confirmation_code.",
                                       "max_seconds": 60, "max_output_bytes": 2000000})
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    write_private(case / "requests.json", json.dumps(requests))
    write_private(case / "process.json", json.dumps(result))
    events = prepared.collect_boundary_events()
    raw = Path(result["stdout_path"]).read_text()
    native = claude_structured_result(raw, output_schema("multitool")) if structured_output else None
    terminal = {"terminal": native["legal_terminal"], "text": ""} if native else claude_result(raw)
    answer = native["structured_output"] if native else _object(terminal["text"])
    dispatches = [event["tool"] for event in events if event["phase"] == "dispatch"]
    expected = {"remaining_budget": 424242, "verification_code": nonce, "confirmation_code": confirmation_code(nonce)}
    boundary_good = dispatches == ["ledger_budget_lookup", "ledger_budget_confirm"] and bool(events) and events[-1]["active_count"] == 0 and max(event["effect_count"] for event in events) == 0
    sizes = [len(canonical_bytes(request["body"].get("tools", []))) for request in requests]
    observation = inspect_claude_tools(mode, requests[0]["body"].get("tools", []), structured_output=structured_output) if requests and not binary else {}
    if binary and requests:
        first_names = {tool.get("function", {}).get("name") for tool in requests[0]["body"].get("tools", [])}
        expected_search = {"ToolSearch"} | ({"StructuredOutput"} if structured_output else set())
        observation["mode_observed"] = first_names == expected_search if mode == "lazy" else len(first_names) == 67 + int(structured_output) and "ToolSearch" not in first_names
    evidence = {}
    if capture:
        attempts = read_http_attempts(capture)
        admission = read_admission_diagnostics(capture, max_attempts=8, request_count=len(attempts))
        evidence = {"physical_http_attempts": len(attempts), "admission": admission,
                    "auxiliary_work": read_auxiliary_work_diagnostics(capture)}
        evidence["physical_attempts_match"] = len(attempts) == len(requests)
    good = (terminal["terminal"] and answer == expected and boundary_good and model.discovery_verified
            and model.receipts_verified and not model.issues and observation.get("mode_observed") and result["termination"] == "exited"
            and (not native or native["schema_valid"] and native["invocation_verified"])
            and result["exit_code"] == 0 and not result["output_truncated"] and (not capture or evidence.get("physical_attempts_match")))
    summary = {"client": "claude", "client_version": CLAUDE_VERSION, "mode": mode, "contract": contract,
        "scope": "synthetic-native-client-mcp-loopback", "gate_status": "protocol-verified" if good else "unverified",
        "passed": bool(good), "requests_received": len(requests), "mcp_dispatches": dispatches,
        "client_terminal": terminal["terminal"], "receipt_replay_verified": model.receipts_verified,
        "discovery_verified": model.discovery_verified, "declaration_bytes": sizes, "provider_input_tokens": None,
        "structured_output_profile": structured_output, "structured_output_verified": None if native is None else native["invocation_verified"] and native["schema_valid"],
        "billing_cost": None, "model_issues": model.issues, "catalog_sha256": prepared.catalog_hash,
        "permission_sha256": prepared.permission_hash,
        "client_executable_sha256": hashlib.sha256(Path(prepared.command[0]).read_bytes()).hexdigest(),
        **observation, **evidence}
    write_private(case / "summary.json", json.dumps(summary, indent=2))
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--structured-output", action="store_true", help="explicit native StructuredOutput protocol profile")
    args = parser.parse_args()
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    results = []
    journal = root / "probe-events.jsonl"
    write_private(journal, "")
    def record(event):
        with journal.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(event) + "\n")
            handle.flush()
    for binary in (None, args.gateway_binary.resolve()):
        for mode in ("eager", "lazy"):
            case_id = ("chat" if binary else "messages") + "-" + mode + ("-native-json-schema" if args.structured_output else "")
            record({"event": "started", "case_id": case_id, "sequence": len(results) * 2})
            try:
                result = probe(root, mode, binary=binary, structured_output=args.structured_output)
            except Exception as error:
                result = {"passed": False, "gate_status": "unverified", "failure_class": "framework",
                          "exception_type": type(error).__name__, "provider_input_tokens": None, "billing_cost": None}
            record({"event": "finished", "case_id": case_id, "sequence": len(results) * 2 + 1, "passed": result["passed"]})
            results.append(result)
            print(json.dumps(result), flush=True)
    write_private(root / "summary.json", json.dumps({"gate_status": "unverified", "scope": "scripted-loopback-only", "cases": results}, indent=2))
    if not all(result["passed"] for result in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
