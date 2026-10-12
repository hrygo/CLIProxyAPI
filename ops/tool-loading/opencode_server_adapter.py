"""Isolated OpenCode 1.19.0 HTTP session-abort profile, not CLI SIGINT."""

import http.client
import json
import os
import re
import selectors
import signal
import subprocess
import time


def abort_terminal(session_id, acknowledged, statuses, messages):
    if not acknowledged or statuses.get(session_id, {}).get("type", "idle") != "idle":
        return False
    return any(message.get("info", {}).get("sessionID") == session_id
               and message["info"].get("role") == "assistant"
               and message["info"].get("error", {}).get("name") == "MessageAbortedError"
               for message in messages)


def run_server_task(prepared, case_spec, *, cancel_event=None):
    if (prepared.client_id, prepared.client_version) != ("opencode", "1.19.0"):
        raise ValueError("unsupported OpenCode server profile")
    seconds, limit = case_spec["max_seconds"], case_spec["max_output_bytes"]
    if (type(seconds) not in {int, float} or not 0 < seconds < float("inf")
            or type(limit) is not int or limit < 1 or not isinstance(case_spec["prompt"], str)):
        raise ValueError("invalid OpenCode process limits")
    started, observed, stored = time.monotonic(), 0, 0
    outputs = [prepared.run_dir / "stdout.log", prepared.run_dir / "stderr.log"]
    handles, process = [], None
    selector = selectors.DefaultSelector()
    termination, failure, boot = "process_budget", None, b""
    port = session_id = None
    abort_ack, terminal = False, False
    statuses, messages = {}, []
    workspace = str(prepared.run_dir / "workspace")
    selected_model = json.loads(prepared.config_path.read_text())["model"]
    provider_id, model_id = selected_model.split("/", 1)

    def request(method, path, body=None):
        nonlocal observed, stored
        payload = json.dumps(body).encode() if body is not None else None
        handles[2].write((json.dumps({"phase": "request_started", "method": method, "path": path, "body": body}) + "\n").encode())
        handles[2].flush()
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=min(1, max(0.001, seconds - (time.monotonic() - started))))
        try:
            connection.request(method, path, body=payload, headers={"Content-Type": "application/json", "x-opencode-directory": workspace})
            response = connection.getresponse()
            data = response.read(max(0, limit - stored) + 1)
            observed += len(data)
            accepted = data[:max(0, limit - stored)]
            stored += len(accepted)
            if observed > limit:
                raise ValueError("OpenCode HTTP output budget")
            value = json.loads(data) if data else None
            handles[2].write((json.dumps({"phase": "request_finished", "status": response.status, "body": value}) + "\n").encode())
            handles[2].flush()
            if not 200 <= response.status < 300:
                raise ValueError("OpenCode API status " + str(response.status))
            return value
        finally:
            connection.close()

    try:
        for path in [*outputs, prepared.run_dir / "server-http.jsonl"]:
            handle = path.open("xb")
            os.chmod(path, 0o600)
            handles.append(handle)
        command = [prepared.command[0], "serve", "--pure", "--hostname", "127.0.0.1", "--port", "0"]
        process = subprocess.Popen(command, cwd=workspace, env=prepared.env,
                                   stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        for stream, handle in zip((process.stdout, process.stderr), handles):
            os.set_blocking(stream.fileno(), False)
            selector.register(stream, selectors.EVENT_READ, handle)
        while time.monotonic() - started < seconds:
            if process.poll() is not None:
                termination = "unexpected_exit"
                break
            for key, _ in selector.select(0.02):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                observed += len(chunk)
                accepted = chunk[:max(0, limit - stored)]
                stored += len(accepted)
                key.data.write(accepted)
                if observed > limit:
                    raise ValueError("OpenCode process output budget")
                if key.fileobj is process.stdout and port is None:
                    boot += chunk
                    match = re.search(rb"opencode server listening on http://127\.0\.0\.1:(\d+)", boot)
                    if match:
                        port = int(match.group(1))
            if port is None:
                continue
            if session_id is None:
                session_id = request("POST", "/session", {"title": "Local cancellation acceptance"})["id"]
                request("POST", f"/session/{session_id}/prompt_async", {
                    "model": {"providerID": provider_id, "modelID": model_id},
                    "parts": [{"type": "text", "text": case_spec["prompt"]}]})
            if cancel_event is not None and cancel_event.is_set() and not abort_ack:
                abort_ack = request("POST", f"/session/{session_id}/abort") is True
                if not abort_ack:
                    raise ValueError("OpenCode abort not acknowledged")
            if abort_ack:
                statuses = request("GET", "/session/status")
                messages = request("GET", f"/session/{session_id}/message")
                terminal = abort_terminal(session_id, abort_ack, statuses, messages)
                events = prepared.collect_boundary_events()
                if (terminal and events and events[-1]["active_count"] == 0
                        and any(item["phase"] == "cancelled" for item in events)
                        and any(item["phase"] == "released" for item in events)):
                    termination = "cancelled"
                    break
    except (ValueError, KeyError, OSError, http.client.HTTPException) as error:
        failure = str(error)[:500]
        termination = "output_budget" if "output budget" in failure else "protocol_error"
    finally:
        if process is not None:
            for sig in (signal.SIGTERM, signal.SIGKILL):
                try:
                    os.killpg(process.pid, sig)
                except ProcessLookupError:
                    pass
                if sig == signal.SIGTERM:
                    try:
                        process.wait(timeout=1)
                    except subprocess.TimeoutExpired:
                        pass
            process.wait()
            process.stdout.close()
            process.stderr.close()
        selector.close()
        for handle in handles:
            handle.close()
    return {"exit_code": process.returncode, "termination": termination,
            "elapsed_seconds": time.monotonic() - started, "stored_output_bytes": stored,
            "observed_output_bytes": observed, "output_truncated": observed > limit,
            "stdout_path": str(outputs[0]), "stderr_path": str(outputs[1]),
            "client_output_profile": "opencode-http-session-v1", "protocol_error": failure,
            "protocol_terminal_verified": terminal, "protocol_terminal_status": "interrupted" if terminal else None,
            "interrupt_acknowledged": abort_ack, "interrupt_verified": terminal,
            "final_texts": [], "task_success_verified": False}
