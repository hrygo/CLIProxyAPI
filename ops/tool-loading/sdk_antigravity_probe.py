"""Exercise an isolated SDK candidate against synthetic loopback Antigravity.

No operator config, OAuth account, Coding Agent, or real Provider is used.
Successful fixtures prove integration only, never live certification or savings.
"""

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading
import time
from urllib.parse import urlsplit
import uuid

from client_adapters import write_private
from http_attempts import read_http_attempts, read_admission_diagnostics, read_auxiliary_work_diagnostics
from live_gateway import candidate_process
from normalize_usage import aggregate_usage
from task_scenarios import intermediate_receipt_replayed, confirmation_code
from antigravity_wire import terminal as native_terminal


def run_probe(binary, root):
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    outcomes = []
    for stream in (False, True):
        case = root / ("sse" if stream else "buffered")
        case.mkdir(mode=0o700)
        nonce = uuid.uuid4().hex
        received = []
        lock = threading.Lock()
        credits_ready = threading.Event()

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                with lock:
                    received.append((self.path, body))
                    index = sum("generateContent" in path or "streamGenerateContent" in path for path, _ in received)
                if self.path == "/v1internal:loadCodeAssist":
                    payload = {"currentTier": {"id": "free-tier"}}
                    self.respond(json.dumps(payload).encode(), "application/json")
                    credits_ready.set()
                    return
                if self.path == "/v1internal:fetchAvailableModels":
                    self.respond(b'{"models":{"gemini-3-flash":{}}}', "application/json")
                    return
                if self.path not in ("/v1internal:generateContent", "/v1internal:streamGenerateContent?alt=sse"):
                    self.send_error(404)
                    return
                # Synchronize the fixture, so shutdown cannot race the query.
                if not credits_ready.wait(5):
                    self.send_error(500, "auxiliary query did not arrive")
                    return
                part = ({"functionCall": {"name": "fixture_lookup", "args": {"project": "fixture"}}} if index == 1 else
                        {"functionCall": {"name": "fixture_confirm", "args": {"project": "fixture", "verification_code": nonce}}} if index == 2 else
                        {"text": json.dumps({"confirmation_code": confirmation_code(nonce)})})
                payload = {"response": {"responseId": "synthetic-" + str(index), "modelVersion": "gemini-3-flash",
                           "candidates": [{"index": 0, "content": {"role": "model", "parts": [part]}, "finishReason": "STOP"}],
                           "usageMetadata": {"promptTokenCount": 100, "candidatesTokenCount": 10, "totalTokenCount": 110}}}
                data = json.dumps(payload).encode()
                if "streamGenerateContent" in self.path:
                    self.respond(b"data: " + data + b"\n\n", "text/event-stream")
                else:
                    self.respond(data, "application/json")

            def respond(self, data, content_type):
                self.send_response(200)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
                self.wfile.flush()

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        auth_dir = case / "synthetic-auths"
        auth_dir.mkdir(mode=0o700)
        credential = {"type": "antigravity", "access_token": "synthetic-access-token", "refresh_token": "synthetic-refresh-never-used",
                      "project_id": "synthetic-project", "expired": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
                      "base_url": f"http://127.0.0.1:{server.server_port}",
                      "model_aliases": [{"name": "gemini-3-flash", "alias": "fixture-alias"}]}
        credential_data = json.dumps(credential).encode()
        write_private(auth_dir / "selected.json", credential_data.decode())
        binding = {"name_sha256": hashlib.sha256(b"selected.json").hexdigest(), "content_sha256": hashlib.sha256(credential_data).hexdigest()}
        profile = {"client_model": "fixture-alias", "upstream_model": "gemini-3-flash", "executor": "antigravity",
                   "contract": "antigravity-generate-v1", "auth_files": [binding]}
        source = case / "source-fixture.json"
        write_private(source, json.dumps({"config-version": 8, "server": {"host": "127.0.0.1", "port": 8317},
                      "oauth": {"auth-dir": str(auth_dir.resolve()), "providers": {"antigravity": {"antigravity-credits": True}}},
                      "requests": {"responses-tools": {"enabled": True}}, "access": {"api-keys": ["synthetic-source"]}}))
        history = [{"role": "user", "content": "Execute lookup then confirmation using the returned receipt."}]
        tools = [{"type": "function", "name": name, "parameters": {"type": "object", "properties": {
                 "project": {"type": "string"}, **({"verification_code": {"type": "string"}} if name == "fixture_confirm" else {})},
                 "required": ["project"] + (["verification_code"] if name == "fixture_confirm" else [])}}
                 for name in ("fixture_lookup", "fixture_confirm")]
        try:
            with candidate_process(binary, source, case, profile, max_attempts=5) as (ready, capture):
                address = urlsplit(ready["base_url"])
                # Bounded local registration probe; never an upstream timeout.
                deadline = time.monotonic() + 5
                while True:
                    connection = http.client.HTTPConnection(address.hostname, address.port)
                    try:
                        connection.request("GET", "/v1/models", headers={"Authorization": "Bearer local-acceptance-placeholder"})
                        response = connection.getresponse()
                        catalog = json.loads(response.read())
                    finally:
                        connection.close()
                    if any(item.get("id") == "fixture-alias" for item in catalog.get("data", [])):
                        break
                    assert time.monotonic() < deadline, "actual SDK model registration did not complete"
                    threading.Event().wait(0.01)
                for index in range(3):
                    connection = http.client.HTTPConnection(address.hostname, address.port)
                    try:
                        connection.request("POST", "/v1/responses", json.dumps({"model": "fixture-alias", "stream": stream,
                                           "input": history, "tools": tools}),
                                           {"Authorization": "Bearer local-acceptance-placeholder", "Content-Type": "application/json"})
                        response = connection.getresponse()
                        data = response.read()
                        write_private(case / f"client-response-{index}.txt", data.decode())
                        assert response.status == 200, f"SDK fixture returned {response.status}"
                    finally:
                        connection.close()
                    if stream:
                        frames = [json.loads(line[6:]) for line in data.splitlines() if line.startswith(b"data: {")]
                        terminal = [frame["response"] for frame in frames if frame.get("type") == "response.completed"]
                        assert len(terminal) == 1, "missing or duplicate client terminal"
                        output = terminal[0]["output"]
                    else:
                        document = json.loads(data)
                        assert document["status"] == "completed", "invalid buffered client terminal"
                        output = document["output"]
                    if index < 2:
                        calls = [item for item in output if item.get("type") == "function_call"]
                        assert len(calls) == 1 and calls[0]["name"] == ("fixture_lookup" if index == 0 else "fixture_confirm")
                        arguments = json.loads(calls[0]["arguments"])
                        assert arguments == ({"project": "fixture"} if index == 0 else {"project": "fixture", "verification_code": nonce})
                        history.extend(output)
                        receipt = {"verification_code": nonce} if index == 0 else {"confirmation_code": confirmation_code(nonce)}
                        history.append({"type": "function_call_output", "call_id": calls[0]["call_id"], "output": json.dumps(receipt)})
                    else:
                        assert confirmation_code(nonce) in json.dumps(output)
            assert hashlib.sha256((auth_dir / "selected.json").read_bytes()).hexdigest() == binding["content_sha256"]
            attempts = read_http_attempts(capture)
            admission = read_admission_diagnostics(capture, max_attempts=5, request_count=len(attempts))
            work = read_auxiliary_work_diagnostics(capture)
            model = [attempt for attempt in attempts if attempt["contract"] == "antigravity-generate-v1"]
            auxiliary = [attempt for attempt in attempts if attempt["contract"] == "antigravity-auxiliary-v1"]
            assert len(attempts) == len(received) == 5 and len(model) == 3 and len(auxiliary) == 2
            assert {item["operation"] for item in auxiliary} == {"credits_query", "model_discovery"}
            assert all(item["raw_usage"] is None for item in auxiliary)
            assert work["started"] == work["completed"] and work["started"] >= 2 and work["active"] == 0 and work["sealed"]
            assert intermediate_receipt_replayed([body for path, body in received if "GenerateContent" in path or "generateContent" in path], nonce)
            usage = aggregate_usage(attempts)
            assert usage["total_input_tokens"] == 300
            for item in model:
                path = capture / ("response-" + item["request_id"][8:] + ".body")
                assert native_terminal(path.read_bytes(), json.loads, transport_complete=item["transport_capture_complete"]), "invalid native terminal"
            outcomes.append({"case": case.name, "physical_requests": len(attempts), "model_requests": len(model),
                             "auxiliary_requests": len(auxiliary), "auxiliary_work": work, "admission": admission,
                             "result_replay_verified": True, "native_terminals_verified": True,
                             "synthetic_total_input_tokens": usage["total_input_tokens"], "source_credential_unchanged": True})
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
    write_private(root / "summary.json", json.dumps({"scope": "synthetic-sdk-loopback-only", "gate_status": "unverified", "cases": outcomes}, indent=2))
    return outcomes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(run_probe(args.gateway_binary.resolve(), args.output_dir.resolve())))


if __name__ == "__main__":
    main()
