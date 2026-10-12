"""Native clients -> real isolated gateway -> scripted upstream -> actual MCP.

This checks the evidence chain, not live Provider quality or token savings.
Every started case is journaled before launching its gateway/client.
"""

from copy import deepcopy
import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import select
import shutil
import signal
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit
import uuid

from catalog_fixture import build_catalog, digest
from client_adapters import ENV_KEYS, VERSIONS, prepare_isolated_client, synthetic_codex_model, write_private
from http_attempts import import_http_attempts
from run_acceptance import CaseJournal, CaseSpec, read_journal, summarize
from scripted_clients import chat_chunks, client_result, response_events, tool_receipt

CONTRACTS = {"fixture-chat": "openai-chat-v1", "codex": "openai-responses-v1"}
PROMPT = "Use the fixture to read alpha remaining budget and verification code."


def declarations(request):
    found = []

    def walk(tools, namespace=None):
        for tool in tools:
            if tool.get("type") == "namespace":
                walk(tool.get("tools", []), tool.get("name"))
            elif tool.get("type") == "function":
                function = tool.get("function", tool)
                found.append({"name": function.get("name"), "namespace": namespace, "description": function.get("description", "")})
    walk(request.get("tools", []))
    return found


class BridgeModel:
    def __init__(self, client, mode, executor):
        if client not in VERSIONS or mode not in {"eager", "lazy"} or executor not in CONTRACTS:
            raise ValueError("unknown gateway fixture profile")
        self.client, self.mode, self.executor = client, mode, executor
        self.round = 0
        self.receipt = None
        self.discovered_names = set()
        self.invocation_emitted = self.search_result_has_target = False

    def _receipt(self, request, call_id):
        receipt = tool_receipt("opencode" if self.executor == "fixture-chat" else "codex", request, call_id)
        if self.client == "codex" and receipt:
            # Translation changes the container, not the Codex result's text
            # wrapper. Decode that original wrapper using the existing adapter.
            receipt = tool_receipt("codex", {"input": [{"type": "function_call_output", "call_id": call_id, "output": receipt["raw"]}]}, call_id)
        return receipt

    def _message(self, text):
        return {"type": "message", "id": "msg_final", "role": "assistant", "status": "completed",
                "content": [{"type": "output_text", "text": text, "annotations": []}]}

    def next(self, request):
        stage = self.round
        self.round += 1
        tools = declarations(request)

        def call(target, arguments, call_id, *, suffix=False, promoted=False):
            hits = [item for item in tools if item["name"] == target or suffix and isinstance(item["name"], str) and item["name"].endswith("_" + target)]
            if promoted:
                hits = [item for item in tools if item["name"] in self.discovered_names and "Authoritative ledger lookup." in item["description"]]
            if len(hits) != 1:
                return self._message("fixture target signature unavailable or ambiguous")
            item = {"type": "function_call", "id": "fc_" + call_id, "call_id": call_id,
                    "name": hits[0]["name"], "arguments": arguments}
            if hits[0]["namespace"]:
                item["namespace"] = hits[0]["namespace"]
            return item

        if self.mode == "lazy" and stage == 0:
            query = "ledger budget remaining alpha verification code"
            if self.client == "codex":
                return call("tool_search", {"query": query, "limit": 8}, "search")
            return call("execute", {"code": 'return await tools.$codemode.search({query:' + json.dumps(query) + ',limit:8});'}, "search")
        if stage == (1 if self.mode == "lazy" else 0):
            if self.mode == "lazy":
                search = self._receipt(request, "search")
                if self.client == "codex" and search:
                    try:
                        manifest = json.loads(search["raw"]) if isinstance(search["raw"], str) else search["raw"]
                        self.discovered_names = {item["name"] for item in manifest.get("tools", []) if isinstance(item, dict) and isinstance(item.get("name"), str)}
                    except (ValueError, AttributeError):
                        self.discovered_names = set()
                    self.search_result_has_target = any(item["name"] in self.discovered_names and "Authoritative ledger lookup." in item["description"] for item in tools)
                else:
                    self.search_result_has_target = bool(search and "ledger_budget_lookup" in json.dumps(search))
                if not self.search_result_has_target:
                    return self._message("fixture discovery did not return the target")
            item = (call("execute", {"code": 'return await tools.fixture.ledger_budget_lookup({project:"alpha"});'}, "invoke")
                    if (self.client, self.mode) == ("opencode", "lazy") else
                    call("ledger_budget_lookup", {"project": "alpha"}, "invoke", suffix=True,
                         promoted=(self.client, self.mode) == ("codex", "lazy")))
            self.invocation_emitted = item["type"] == "function_call"
            return item
        self.receipt = self._receipt(request, "invoke")
        text = ("fixture final: " + str(self.receipt["remaining_budget"]) + " " + self.receipt["verification_code"]
                if self.receipt and "verification_code" in self.receipt else "fixture final: no verified tool result")
        return self._message(text)


def validate_gateway_ready(ready, pid, executor):
    keys = {"version", "scope", "pid", "base_url", "executor", "contract", "fixture_policy_sha256"}
    if not isinstance(ready, dict) or set(ready) != keys:
        raise ValueError("invalid gateway readiness shape")
    address = urlsplit(ready["base_url"])
    if (type(ready["version"]) is not int or ready["version"] != 1 or ready["scope"] != "synthetic-loopback-gateway"
            or type(ready["pid"]) is not int or ready["pid"] != pid or ready["executor"] != executor
            or ready["contract"] != CONTRACTS[executor] or address.scheme != "http" or address.hostname != "127.0.0.1"
            or address.username or address.password or address.query or address.fragment or address.path != "/v1"
            or address.port is None or not re.fullmatch(r"[0-9a-f]{64}", ready["fixture_policy_sha256"])):
        raise ValueError("gateway readiness differs from the actual process/profile")


def upstream_events(model, request, response_id):
    # Some executors add built-in declarations to title requests. Only actual
    # function declarations identify this fixture's tool-bearing task requests.
    item = model.next(request) if declarations(request) else model._message("Local fixture title")
    wire_item = deepcopy(item)
    if model.executor == "codex":
        if wire_item["type"] == "function_call":
            wire_item["arguments"] = json.dumps(wire_item["arguments"])
        events = response_events(wire_item, response_id)
        events[-1]["response"]["usage"] = {"input_tokens": 31, "output_tokens": 2, "total_tokens": 33}
    else:
        events = chat_chunks(wire_item)
        events[-1]["usage"] = {"prompt_tokens": 31, "completion_tokens": 2, "total_tokens": 33}
    # Scripted counters exercise metering only; these are not live tokens.
    return events


@contextmanager
def gateway_process(binary, root, case_id, upstream, executor):
    state = root / ("gateway-" + case_id)
    with (root / ("gateway-" + case_id + ".stderr.log")).open("xb") as stderr:
        os.chmod(stderr.name, 0o600)
        env = {key: os.environ[key] for key in ENV_KEYS if key in os.environ}
        process = subprocess.Popen([str(binary), "--upstream-url", upstream, "--executor", executor,
                                    "--state-dir", str(state), "--max-attempts", "16"],
                                   env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=stderr, start_new_session=True)
        try:
            if not select.select([process.stdout], [], [], 10)[0]:
                raise ValueError("gateway startup budget exhausted")
            line = process.stdout.readline(8193)
            if len(line) > 8192 or not line.endswith(b"\n"):
                raise ValueError("gateway readiness missing or oversized")
            ready = json.loads(line)
            validate_gateway_ready(ready, process.pid, executor)
            if process.poll() is not None:
                raise ValueError("gateway stopped during startup")
            write_private(root / ("gateway-" + case_id + ".ready.json"), json.dumps(ready))
            yield ready, state
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            process.stdout.close()
            write_private(root / ("gateway-" + case_id + ".exit.json"), json.dumps({"exit_code": process.returncode}))


def case_spec(client, mode, executor):
    permissions = {"client_denied": [], "service_denied": ["fixture_mutation"]}
    scope = {key: "unknown" for key in ("gateway_revision", "policy_generation", "client_revision", "endpoint_id")}
    scope.update(client=client, client_version=VERSIONS[client], client_mode=("direct" if mode == "eager" else
                 "tool-search" if client == "codex" else "code-mode"), source_format="codex" if client == "codex" else "openai",
                 provider=executor, model="fixture-model", upstream_format="openai" if executor == "fixture-chat" else "codex",
                 transport="http-sse", catalog_hash=digest(build_catalog()), fixture_hash=hashlib.sha256(Path(__file__).with_name("catalog_fixture.py").read_bytes()).hexdigest(),
                 permission_hash=digest(permissions), region_id="synthetic-loopback")
    pair = client + "-" + executor
    return CaseSpec(case_id=pair + "-" + mode, pair_id=pair, template_id="ledger-lookup", mode=mode,
                    cache_condition="unknown", scope=scope, prompt_hash=hashlib.sha256(PROMPT.encode()).hexdigest(),
                    fixture_seed="synthetic-gateway-1", limits={"max_requests": 16, "max_dispatches": 8, "task_seconds": 20},
                    gold={"requires_dispatch": True, "requires_nonce": True, "requires_code": False, "format_required": True})


def run_case(root, journal, binary, client, mode, executor):
    spec = case_spec(client, mode, executor)
    journal.start_case(spec)
    model = BridgeModel(client, mode, executor)
    upstream_dir = root / ("upstream-" + spec.case_id)
    upstream_dir.mkdir(mode=0o700)
    received, lock = [], threading.Lock()
    state, prepared, process_result = None, None, None
    checks = dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"))
    boundaries = dict.fromkeys(("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"))
    failure = "framework"
    issues = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 <= size <= 1048576:
                self.send_error(413)
                return
            request = json.loads(self.rfile.read(size))
            with lock:
                received.append(deepcopy(request))
                write_private(upstream_dir / f"request-{len(received):02}.json", json.dumps(request))
                if len(received) > 16:
                    self.send_error(429)
                    return
                events = upstream_events(model, request, "resp_" + str(len(received)))
                body = "".join("data: " + json.dumps(event) + "\n\n" for event in events)
                if executor == "fixture-chat":
                    body += "data: [DONE]\n\n"
                write_private(upstream_dir / f"response-{len(received):02}.sse", body)
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
    try:
        with gateway_process(binary, root, spec.case_id, f"http://127.0.0.1:{server.server_port}/v1", executor) as (ready, state):
            prepared = prepare_isolated_client(client, mode, {
                "run_dir": root / spec.case_id, "client_version": VERSIONS[client], "executable": shutil.which(client) or client,
                "fixture_python": sys.executable, "model": "fixture-model", "model_metadata": synthetic_codex_model("fixture-model"),
                "base_url": ready["base_url"], "nonce": nonce, "max_dispatches": 8, "max_result_bytes": 65536},
                {"client_denied": [], "service_denied": ["fixture_mutation"]})
            process_result = prepared.run_task({"prompt": PROMPT, "max_seconds": 20, "max_output_bytes": 1000000})
        events = prepared.collect_boundary_events()
        if not events:
            raise ValueError("MCP boundary evidence missing")
        observed = client_result(client, Path(process_result["stdout_path"]).read_text())
        dispatches = sum(event["phase"] == "dispatch" for event in events)
        receipt_nonce = model.receipt.get("verification_code") if model.receipt else None
        final_nonce = any("fixture final: 424242 " + nonce in text for text in observed["final_texts"])
        checks.update(answer=final_nonce, dispatch=dispatches == 1, nonce=receipt_nonce == nonce and final_nonce,
                      format=observed["parse_complete"], terminal=observed["legal_terminal"])
        boundaries.update(mcp_attempts=sum(event["phase"] == "request" for event in events), dispatch=dispatches,
                          forbidden_dispatch=sum(event["phase"] == "dispatch" and event["tool"] == "fixture_mutation" for event in events),
                          forbidden_effect=max(event["effect_count"] for event in events), active=events[-1]["active_count"])
        if (not model.invocation_emitted or mode == "lazy" and not model.search_result_has_target
                or process_result["termination"] != "exited" or process_result["exit_code"] != 0):
            checks["answer"] = False
        failure = "none" if all(checks[key] for key in ("answer", "dispatch", "nonce", "format", "terminal")) else "model"
    except (OSError, ValueError, subprocess.SubprocessError):
        issues.append("client_or_gateway_probe_failed")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    attempts = []
    if state is not None:
        try:
            attempts = import_http_attempts(journal, spec.case_id, state / "capture", expected_executor=executor)
            if len(attempts) < len(received):
                issues.append("missing_observed_upstream_attempts")
                checks["answer"] = False
                failure = "framework"
        except (OSError, ValueError):
            issues.append("gateway_evidence_import_failed")
            checks["answer"] = False
            failure = "framework"
    gateway_exit = root / ("gateway-" + spec.case_id + ".exit.json")
    if not gateway_exit.exists() or json.loads(gateway_exit.read_text())["exit_code"] != 0:
        issues.append("gateway_did_not_close_cleanly")
        checks["answer"] = False
        failure = "framework"
    journal.finish_case(spec.case_id, checks=checks, boundary_counts=boundaries,
                        elapsed_ms=int(process_result["elapsed_seconds"] * 1000) if process_result else None,
                        failure_class="unknown" if failure == "none" else failure)
    replayed = next(case for case in summarize(journal.events)["cases"] if case["spec"]["case_id"] == spec.case_id)
    verified = replayed["safe_task_success"] and not issues
    if failure == "none" and not verified:
        failure = replayed["failure_class"]
    summary = {"case_id": spec.case_id, "client": client, "mode": mode, "executor": executor,
               "scope": "native-client-scripted-gateway", "requests_received": len(received), "upstream_attempts": len(attempts),
               "checks": checks, "boundary_counts": boundaries, "search_result_has_target": model.search_result_has_target,
               "failure_class": failure, "issues": issues, "protocol_loop_verified": verified,
               "metering_source": "scripted-fixture", "provider_input_tokens": None, "gate_status": "unverified"}
    write_private(root / (spec.case_id + ".summary.json"), json.dumps(summary, indent=2))
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--clients", nargs="+", choices=list(VERSIONS), default=list(VERSIONS))
    parser.add_argument("--modes", nargs="+", choices=["eager", "lazy"], default=["eager", "lazy"])
    parser.add_argument("--executor", choices=list(CONTRACTS), default="fixture-chat")
    args = parser.parse_args()
    root, binary = args.output_dir.resolve(), args.gateway_binary.resolve()
    if not binary.is_file():
        parser.error("gateway binary does not exist")
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    write_private(root / "gateway-binary-sha256.txt", hashlib.sha256(binary.read_bytes()).hexdigest() + "\n")
    journal = CaseJournal(root / "cases.jsonl", "native-gateway-" + uuid.uuid4().hex)
    results = []
    for client in args.clients:
        for mode in args.modes:
            result = run_case(root, journal, binary, client, mode, args.executor)
            results.append(result)
            print(json.dumps(result), flush=True)
    write_private(root / "replay.json", json.dumps(summarize(read_journal(journal.path)), indent=2))
    write_private(root / "summary.json", json.dumps({"gate_status": "unverified", "cases": results}, indent=2))
    if not all(result["protocol_loop_verified"] for result in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
