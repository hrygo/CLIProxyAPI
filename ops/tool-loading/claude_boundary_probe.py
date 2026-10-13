"""Pinned Claude Code permission, argument, revocation and cancellation probes.

Uses scripted loopback models and actual MCP dispatch. Provider quality, input
usage and billing remain unverified. Every started probe stays in its journal.
"""
import argparse
import json
from pathlib import Path
import shutil
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from client_adapters import CLAUDE_VERSION, prepare_isolated_client, synthetic_codex_model, write_private
from claude_client_probe import anthropic_events, claude_result

def probe(root, mode, scenario, *, max_seconds=25):
    if mode not in {"eager", "lazy"} or scenario not in {"client-denied", "invalid", "revoked", "cancel"}:
        raise ValueError("unsupported boundary profile")
    root = Path(root).resolve()
    run = root / (mode + "-" + scenario)
    run.mkdir(mode=0o700)
    target = "fixture_wait" if scenario == "cancel" else "ledger_budget_lookup"
    name = "mcp__fixture__" + target
    requests = []
    emitted, error_replayed, discovery = False, False, mode == "eager"
    prepared = None
    cancel, stop = threading.Event(), threading.Event()

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            nonlocal emitted, error_replayed, discovery
            if self.path not in {"/v1/messages", "/v1/messages?beta=true"}:
                self.send_error(404)
                return
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= 1048576 or len(requests) >= 8:
                self.send_error(429)
                return
            body = json.loads(self.rfile.read(size))
            stage = len(requests)
            requests.append(body)
            if mode == "lazy" and stage == 0:
                item = {"type": "function_call", "name": "ToolSearch", "call_id": "search",
                        "arguments": {"query": "select:" + name, "max_results": 1}}
            elif stage == int(mode == "lazy"):
                definitions = {tool.get("name") for tool in body.get("tools", [])}
                discovery = discovery or name in definitions
                if scenario == "revoked":
                    temporary = prepared.run_dir / "permission-next.json"
                    write_private(temporary, json.dumps({"generation": 2, "denied": ["fixture_mutation", target]}))
                    temporary.replace(prepared.run_dir / "permissions.json")
                    write_private(run / "revoked.json", json.dumps({"generation": 2, "before_call_id": "invoke"}))
                emitted = True
                item = {"type": "function_call", "name": name, "call_id": "invoke",
                        "arguments": {"project": "beta" if scenario == "invalid" else "alpha"}}
            else:
                for message in body.get("messages", []):
                    if message.get("role") == "user" and isinstance(message.get("content"), list):
                        error_replayed = error_replayed or any(part.get("type") == "tool_result"
                            and part.get("tool_use_id") == "invoke" and part.get("is_error") is True
                            for part in message["content"] if isinstance(part, dict))
                item = {"type": "message", "content": [{"type": "output_text", "text": "Boundary fixture finished."}]}
            encoded = "".join("event: " + kind + "\ndata: " + json.dumps(value) + "\n\n"
                              for kind, value in anthropic_events(item, body["model"], len(requests))).encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)
        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    observer = None
    try:
        prepared = prepare_isolated_client("claude", mode, {
            "run_dir": run / "client", "client_version": CLAUDE_VERSION,
            "executable": shutil.which("claude"), "fixture_python": sys.executable,
            "model": "claude-sonnet-4-5", "model_metadata": synthetic_codex_model("claude-sonnet-4-5"),
            "base_url": f"http://127.0.0.1:{server.server_port}/v1", "nonce": uuid.uuid4().hex,
            "max_dispatches": 8, "max_result_bytes": 65536},
            {"client_denied": [target] if scenario == "client-denied" else [], "service_denied": ["fixture_mutation"]})
        if scenario == "cancel":
            def observe():
                while not stop.wait(.01):
                    try:
                        events = prepared.collect_boundary_events()
                    except ValueError:
                        continue
                    dispatch = next((entry for entry in events if entry["phase"] == "dispatch" and entry["tool"] == target), None)
                    if dispatch:
                        write_private(run / "cancel-requested.json", json.dumps({"after_mcp_sequence": dispatch["sequence"]}))
                        cancel.set()
                        return
            observer = threading.Thread(target=observe, daemon=True)
            observer.start()
        process = prepared.run_task({"prompt": "Use the fixture to read alpha budget.", "max_seconds": max_seconds,
                                     "max_output_bytes": 1048576}, cancel_event=cancel)
    finally:
        stop.set()
        if observer:
            observer.join()
        server.shutdown()
        server.server_close()
        worker.join()
        write_private(run / "requests.json", json.dumps(requests))
    events = prepared.collect_boundary_events()
    dispatches = sum(entry["phase"] == "dispatch" for entry in events)
    effects = max((entry["effect_count"] for entry in events), default=0)
    active = events[-1]["active_count"] if events else None
    rejected = [entry.get("reason") for entry in events if entry["phase"] == "rejected"]
    terminal = claude_result(Path(process["stdout_path"]).read_text())["terminal"]
    passed = bool(events and emitted and effects == 0 and active == 0 and not process["output_truncated"])
    if scenario == "cancel":
        passed = passed and discovery and dispatches == 1 and process["termination"] == "cancelled" and not process["forced_process_cleanup"]
        passed = passed and any(entry["phase"] == "cancelled" for entry in events) and any(entry["phase"] == "released" for entry in events)
    else:
        passed = passed and dispatches == 0 and error_replayed and terminal and process["exit_code"] == 0
        if scenario == "client-denied":
            passed = passed and not any(entry["phase"] == "request" for entry in events)
        else:
            passed = passed and discovery and ("invalid_arguments" if scenario == "invalid" else "permission_denied") in rejected
    result = {"mode": mode, "scenario": scenario, "passed": bool(passed), "requests": len(requests),
              "dispatches": dispatches, "effects": effects, "active": active, "rejected": rejected,
              "error_replayed": error_replayed, "discovery": discovery, "terminal": terminal,
              "process": process, "gate_status": "unverified", "scope": "scripted-native-client-mcp-boundary"}
    write_private(run / "summary.json", json.dumps(result, indent=2))
    return result

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--task-seconds", type=int, default=25)
    args = parser.parse_args()
    if not 5 <= args.task_seconds <= 60:
        parser.error("bounded task budget required")
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    journal = root / "probe-events.jsonl"
    write_private(journal, "")
    summaries = []
    def event(value):
        with journal.open("a") as stream:
            stream.write(json.dumps(value) + "\n")
            stream.flush()
    try:
        for scenario in ("client-denied", "invalid", "revoked", "cancel"):
            for mode in ("eager", "lazy"):
                event({"event": "started", "mode": mode, "scenario": scenario})
                try:
                    result = probe(root, mode, scenario, max_seconds=args.task_seconds)
                except Exception as failure:
                    result = {"mode": mode, "scenario": scenario, "passed": False,
                              "exception_type": type(failure).__name__}
                summaries.append(result)
                event({"event": "finished", **result})
                print(json.dumps({key: value for key, value in result.items() if key != "process"}), flush=True)
    finally:
        write_private(root / "summary.json", json.dumps({"gate_status": "unverified", "cases": summaries}, indent=2))
    if not all(value["passed"] for value in summaries):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
