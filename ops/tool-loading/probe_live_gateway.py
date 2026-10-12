"""Verify the live bootstrap using only a synthetic configuration/upstream."""

import argparse
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading
from urllib.parse import urlsplit

from client_adapters import write_private
from http_attempts import read_http_attempts
from live_gateway import candidate_process


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            received.append(request)
            response = {"id": "fixture", "object": "chat.completion", "model": "fixture-model",
                        "choices": [{"index": 0, "message": {"role": "assistant", "content": "verified bootstrap fixture"}, "finish_reason": "stop"}],
                        "usage": {"prompt_tokens": 31, "completion_tokens": 2, "total_tokens": 33}}
            body = json.dumps(response).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    source = root / "source-fixture.json"
    config = {"config-version": 8, "server": {"host": "127.0.0.1", "port": 8317}, "access": {"api-keys": ["synthetic-source-key"]},
              "api-keys": {"openai-compatibility": [{"name": "fixture", "base-url": f"http://127.0.0.1:{server.server_port}/v1",
                           "keys": [{"api-key": "synthetic-upstream-only"}], "models": [{"name": "fixture-model", "alias": "fixture-alias"}]}]}}
    write_private(source, json.dumps(config))
    profile = {"client_model": "fixture-alias", "upstream_model": "fixture-model", "executor": "openai-compatible-fixture", "contract": "openai-chat-v1"}
    try:
        with candidate_process(args.gateway_binary, source, root, profile) as (ready, capture):
            url = urlsplit(ready["base_url"])
            connection = http.client.HTTPConnection(url.hostname, url.port)
            try:
                connection.request("POST", "/v1/chat/completions", json.dumps({"model": "fixture-alias", "stream": False,
                                   "messages": [{"role": "user", "content": "synthetic bootstrap request"}]}),
                                   {"Authorization": "Bearer local-acceptance-placeholder", "Content-Type": "application/json"})
                response = connection.getresponse()
                body = json.loads(response.read())
            finally:
                connection.close()
            write_private(root / "response.json", json.dumps({"status": response.status, "body": body}))
            if response.status != 200 or body["choices"][0]["message"]["content"] != "verified bootstrap fixture":
                raise ValueError("synthetic candidate did not execute")
        attempts = read_http_attempts(capture)
        if len(attempts) != 1 or len(received) != 1 or attempts[0]["executor_identity"]["executor"] != profile["executor"]:
            raise ValueError("actual bootstrap attempt evidence missing")
        write_private(root / "summary.json", json.dumps({"gate_status": "unverified", "scope": "synthetic-bootstrap-fixture",
                      "requests": len(received), "attempts": len(attempts), "exit": json.loads((root / "gateway.exit.json").read_text())}, indent=2))
        print("Synthetic isolated SDK bootstrap passed: one actual attempt, unchanged source, clean exit.")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    main()
