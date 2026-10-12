"""Isolated OpenCode 1.19.0 HTTP abort and native structured-output profiles."""

import http.client
import json
import os
import re
import selectors
import signal
import subprocess
import threading
import time

from jsonschema import Draft202012Validator


def structured_reply(session_id, statuses, message, schema):
    """Validate the response to this fresh session's single prompt request."""
    if not isinstance(statuses, dict) or not isinstance(message, dict):
        return None
    if statuses.get(session_id, {}).get("type", "idle") != "idle":
        return None
    info = message.get("info", {})
    value = info.get("structured")
    if (info.get("sessionID") != session_id or info.get("role") != "assistant" or not info.get("parentID")
            or info.get("error") is not None or info.get("finish") not in {"stop", "tool-calls"}
            or type(info.get("time", {}).get("completed")) is not int
            or not isinstance(value, dict) or not Draft202012Validator(schema).is_valid(value)):
        return None
    calls = [part for part in message.get("parts", []) if part.get("type") == "tool" and part.get("tool") == "StructuredOutput"]
    if len(calls) != 1 or not calls[0].get("callID"):
        return None
    state = calls[0].get("state", {})
    if state.get("status") != "completed" or state.get("input") != value:
        return None
    return value


def structured_terminal(session_id, statuses, messages, schema, prompt):
    if not isinstance(messages, list) or any(not isinstance(message, dict) for message in messages):
        return None
    users = [message for message in messages if message.get("info", {}).get("sessionID") == session_id
             and message["info"].get("role") == "user"]
    if len(users) != 1 or not any(part.get("type") == "text" and part.get("text") == prompt
                                  for part in users[0].get("parts", [])):
        return None
    user_id = users[0]["info"].get("id")
    assistants = [message for message in messages if message.get("info", {}).get("sessionID") == session_id
                  and message["info"].get("role") == "assistant"]
    if not assistants:
        return None
    message = assistants[-1]
    if not user_id or message["info"].get("parentID") != user_id:
        return None
    return structured_reply(session_id, statuses, message, schema)


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
    schema = case_spec.get("output_schema")
    title = case_spec.get("session_title")
    if schema is not None:
        from client_adapters import validate_output_schema, validate_session_title
        validate_output_schema(schema)
        if title is not None:
            validate_session_title(title)
    started, observed, stored = time.monotonic(), 0, 0
    outputs = [prepared.run_dir / "stdout.log", prepared.run_dir / "stderr.log"]
    handles, process = [], None
    selector = selectors.DefaultSelector()
    termination, failure, boot = "process_budget", None, b""
    port = session_id = None
    abort_ack, terminal, forced_cleanup = False, False, False
    statuses, messages, structured = {}, [], None
    workspace = str(prepared.run_dir / "workspace")
    selected_model = json.loads(prepared.config_path.read_text())["model"]
    provider_id, model_id = selected_model.split("/", 1)
    capture_lock = threading.Lock()
    closing, prompt_done = threading.Event(), threading.Event()
    prompt_result, prompt_thread = {}, None

    def request(method, path, body=None, *, await_prompt=False):
        nonlocal observed, stored
        payload = json.dumps(body).encode() if body is not None else None
        with capture_lock:
            handles[2].write((json.dumps({"phase": "request_started", "method": method, "path": path, "body": body}) + "\n").encode())
            handles[2].flush()
        remaining = max(0.001, seconds - (time.monotonic() - started))
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=remaining if await_prompt else min(1, remaining))
        try:
            connection.request(method, path, body=payload, headers={"Content-Type": "application/json", "x-opencode-directory": workspace})
            response = connection.getresponse()
            data = response.read(limit + 1)
            value = json.loads(data) if data else None
            with capture_lock:
                observed += len(data)
                accepted = data[:max(0, limit - stored)]
                stored += len(accepted)
                if observed > limit:
                    raise ValueError("OpenCode HTTP output budget")
                if not closing.is_set():
                    handles[2].write((json.dumps({"phase": "request_finished", "method": method, "path": path,
                                               "status": response.status, "body": value}) + "\n").encode())
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
                with capture_lock:
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
                session_body = {"title": "Local cancellation acceptance"} if schema is None else ({"title": title} if title is not None else {})
                session_id = request("POST", "/session", session_body)["id"]
                prompt_body = {
                    "model": {"providerID": provider_id, "modelID": model_id},
                    "parts": [{"type": "text", "text": case_spec["prompt"]}]}
                if schema is not None:
                    prompt_body["format"] = {"type": "json_schema", "schema": schema, "retryCount": 0}
                if schema is None:
                    request("POST", f"/session/{session_id}/prompt_async", prompt_body)
                else:
                    def send_prompt():
                        try:
                            prompt_result["message"] = request("POST", f"/session/{session_id}/message", prompt_body, await_prompt=True)
                        except Exception as error:
                            prompt_result["error"] = error
                        finally:
                            prompt_done.set()
                    prompt_thread = threading.Thread(target=send_prompt, daemon=True)
                    prompt_thread.start()
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
            elif schema is not None and prompt_done.is_set():
                if "error" in prompt_result:
                    raise ValueError("OpenCode prompt request failed") from prompt_result["error"]
                statuses = request("GET", "/session/status")
                if statuses.get(session_id, {}).get("type", "idle") == "idle":
                    message = prompt_result["message"]
                    structured = structured_reply(session_id, statuses, message, schema)
                    if structured is not None:
                        terminal, termination = True, "exited"
                        break
                    failure, termination = "opencode_session_output_invalid", "protocol_error"
                    break
    except (ValueError, KeyError, TypeError, AttributeError, OSError, http.client.HTTPException) as error:
        failure = str(error)[:500]
        termination = "output_budget" if "output budget" in failure else "protocol_error"
    finally:
        closing.set()
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
                        forced_cleanup = True
            process.wait()
            process.stdout.close()
            process.stderr.close()
        if prompt_thread is not None:
            prompt_thread.join(timeout=1)
            if prompt_thread.is_alive():
                forced_cleanup, terminal, structured = True, False, None
                termination, failure = "protocol_error", "opencode_prompt_thread_unreleased"
        selector.close()
        for handle in handles:
            handle.close()
    return {"exit_code": None if process is None else process.returncode, "termination": termination,
            "elapsed_seconds": time.monotonic() - started, "stored_output_bytes": stored,
            "observed_output_bytes": observed, "output_truncated": observed > limit,
            "stdout_path": str(outputs[0]), "stderr_path": str(outputs[1]),
            "client_output_profile": "opencode-http-session-v1", "protocol_error": failure,
            "protocol_terminal_verified": terminal, "protocol_terminal_status": ("completed" if structured is not None else "interrupted") if terminal else None,
            "interrupt_acknowledged": abort_ack, "interrupt_verified": bool(abort_ack and terminal and structured is None),
            "forced_process_cleanup": forced_cleanup,
            "final_texts": [], "structured_output": structured, "task_success_verified": False}
