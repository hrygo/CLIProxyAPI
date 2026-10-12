"""Inspect native-client first requests against a local HTTP fixture only."""

import argparse
import json
from pathlib import Path
import shutil
import sys
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from catalog_fixture import build_catalog, canonical_bytes
from client_adapters import VERSIONS, prepare_isolated_client, synthetic_codex_model, write_private


def inspect_tools(client: str, mode: str, tools: list[dict]) -> dict:
    if client not in VERSIONS or mode not in {"eager", "lazy"}:
        raise ValueError("unknown probe client or mode")
    expected = sum(tool["name"] != "fixture_mutation" for tool in build_catalog())
    if client == "codex":
        namespace = next((tool for tool in tools if tool.get("name") == "mcp__fixture"), {})
        fixture_count = len(namespace.get("tools", []))
        search = any(tool.get("type") == "tool_search" for tool in tools)
        valid = fixture_count == expected and not search if mode == "eager" else fixture_count == 0 and search
    else:
        fixture_count = sum(tool.get("function", {}).get("name", "").startswith("fixture_") for tool in tools)
        search = any(tool.get("function", {}).get("name") == "execute" for tool in tools)
        valid = fixture_count == expected and not search if mode == "eager" else fixture_count == 0 and search and len(tools) == 1
    return {"fixture_declarations": fixture_count, "discovery_entry": search,
            "mode_observed": bool(valid), "tool_definition_bytes": len(canonical_bytes(tools))}


def probe(root: Path, client: str, mode: str, process_seconds: float = 20) -> dict:
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            if size > 16 * 1024 * 1024:
                self.send_error(413)
                return
            requests.append({"path": self.path, "body": json.loads(self.rfile.read(size))})
            # Non-retriable failure deliberately stops inference after capture.
            self.send_response(400)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":{"message":"intentional local probe stop","type":"fixture"}}')

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        prepared = prepare_isolated_client(client, mode, {
            "run_dir": root / (client + "-" + mode), "client_version": VERSIONS[client],
            "executable": shutil.which(client) or client, "fixture_python": sys.executable,
            "model": "fixture-model", "model_metadata": synthetic_codex_model("fixture-model"),
            "base_url": f"http://127.0.0.1:{server.server_port}/v1", "nonce": uuid.uuid4().hex,
            "max_dispatches": 8, "max_result_bytes": 65536},
            {"client_denied": [], "service_denied": ["fixture_mutation"]})
        result = prepared.run_task({"prompt": "Use the fixture to read alpha remaining budget and verification code.",
                                    "max_seconds": process_seconds, "max_output_bytes": 1000000})
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    write_private(prepared.run_dir / "requests.json", json.dumps(requests))
    task_request = next((request["body"] for request in requests if "tools" in request["body"]), None)
    observation = inspect_tools(client, mode, task_request["tools"]) if task_request else {
        "mode_observed": False, "fixture_declarations": None, "tool_definition_bytes": None}
    return {"client": client, "client_version": VERSIONS[client], "mode": mode,
            "requests_received": len(requests), "upstream_attempts_started": None,
            "requests_without_tools": sum("tools" not in request["body"] for request in requests),
            "run_dir": str(prepared.run_dir), "catalog_hash": prepared.catalog_hash,
            "permission_hash": prepared.permission_hash, "provider_input_tokens": None,
            "task_success_verified": False, "scope": "local-first-request-only", **result, **observation}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    results = []
    for client in VERSIONS:
        for mode in ("eager", "lazy"):
            result = probe(root, client, mode)
            results.append(result)
            print(json.dumps(result), flush=True)
    write_private(root / "summary.json", json.dumps({"gate_status": "unverified", "cases": results}, indent=2))
    if not all(result["mode_observed"] for result in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
