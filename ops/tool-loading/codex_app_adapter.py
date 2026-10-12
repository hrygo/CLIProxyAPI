"""Isolated Codex 0.160.0 app-server profile using its generated v2 contract."""

from __future__ import annotations

import json
import os
import selectors
import signal
import subprocess
import time


class AppTurn:
    def __init__(self, workspace, model, prompt):
        self.workspace, self.model, self.prompt = str(workspace), model, prompt
        self.thread_id = self.turn_id = self.terminal_status = None
        self.interrupt_sent = self.interrupt_ack = False
        self.expected_reply = None
        self.final_texts = []

    def start(self):
        self.expected_reply = 1
        return {"id": 1, "method": "initialize", "params": {
            "clientInfo": {"name": "tool-loading-acceptance", "version": "1"},
            "capabilities": {"experimentalApi": True}}}

    def accept(self, event):
        if not isinstance(event, dict):
            raise ValueError("invalid app-server event")
        if "method" in event and "id" in event:
            # No automatic approvals or responses to unrecognized server requests.
            return [{"id": event["id"], "error": {"code": -32601, "message": "Unsupported acceptance request"}}]
        if "error" in event:
            raise ValueError("app-server RPC error: " + str(event["error"])[:500])
        if "id" in event:
            if type(event["id"]) is not int or event["id"] != self.expected_reply:
                raise ValueError("unexpected app-server response")
            result = event.get("result", {})
            if event["id"] == 1:
                self.expected_reply = 2
                return [{"method": "initialized"}, {"id": 2, "method": "thread/start", "params": {
                    "model": self.model, "modelProvider": "acceptance", "cwd": self.workspace,
                    "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": True}}]
            if event["id"] == 2:
                thread_id = result["thread"]["id"]
                if not isinstance(thread_id, str) or not thread_id:
                    raise ValueError("invalid app-server thread identity")
                self.thread_id, self.expected_reply = thread_id, 3
                return [{"id": 3, "method": "turn/start", "params": {"threadId": self.thread_id,
                    "input": [{"type": "text", "text": self.prompt}]}}]
            if event["id"] == 3:
                turn_id = result["turn"]["id"]
                if not isinstance(turn_id, str) or not turn_id:
                    raise ValueError("invalid app-server turn identity")
                self.turn_id, self.expected_reply = turn_id, None
            if event["id"] == 4:
                if not self.interrupt_sent:
                    raise ValueError("unsolicited interrupt acknowledgement")
                self.interrupt_ack = True
                self.expected_reply = None
            return []
        params = event.get("params", {})
        if event.get("method") == "turn/completed":
            turn = params["turn"]
            if not self.thread_id or not self.turn_id or params["threadId"] != self.thread_id or turn["id"] != self.turn_id:
                raise ValueError("app-server terminal identity mismatch")
            if turn["status"] not in {"completed", "interrupted", "failed"}:
                raise ValueError("invalid app-server terminal status")
            self.terminal_status = turn["status"]
        if event.get("method") == "item/completed" and params.get("threadId") == self.thread_id and params.get("turnId") == self.turn_id:
            item = params.get("item", {})
            if item.get("type") == "agentMessage":
                self.final_texts.append(item.get("text", ""))
        return []

    def cancel(self):
        if not self.thread_id or not self.turn_id or self.interrupt_sent or self.terminal_status:
            return []
        self.interrupt_sent = True
        self.expected_reply = 4
        return [{"id": 4, "method": "turn/interrupt", "params": {"threadId": self.thread_id, "turnId": self.turn_id}}]

    @property
    def cancel_verified(self):
        return self.interrupt_sent and self.interrupt_ack and self.terminal_status == "interrupted"


def run_app_server_task(prepared, case_spec, *, cancel_event=None):
    """Protocol cancellation precedes process cleanup; exit is not task success."""
    if (prepared.client_id, prepared.client_version) != ("codex", "0.160.0"):
        raise ValueError("unsupported app-server profile")
    seconds, limit = case_spec["max_seconds"], case_spec["max_output_bytes"]
    if (type(seconds) not in {int, float} or not 0 < seconds < float("inf")
            or type(limit) is not int or limit < 1 or not isinstance(case_spec["prompt"], str)):
        raise ValueError("invalid app-server process limits")
    metadata = json.loads(prepared.catalog_path.read_text())["models"][0]
    command = [prepared.command[0], "app-server", "--listen", "stdio://"]
    for override in prepared.config_path.read_text().splitlines():
        command += ["-c", override]
    state = AppTurn(prepared.run_dir / "workspace", metadata["slug"], case_spec["prompt"])
    started, observed, stored = time.monotonic(), 0, 0
    termination, failure, buffered = "process_budget", None, b""
    outputs = [prepared.run_dir / "stdout.jsonl", prepared.run_dir / "stderr.log"]
    selector = selectors.DefaultSelector()
    handles, process = [], None
    rpc_log = prepared.run_dir / "app-rpc-sent.jsonl"

    try:
        for path in [*outputs, rpc_log]:
            handle = path.open("xb")
            os.chmod(path, 0o600)
            handles.append(handle)
        process = subprocess.Popen(command, cwd=state.workspace, env=prepared.env,
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        for stream, handle in zip((process.stdout, process.stderr), handles):
            os.set_blocking(stream.fileno(), False)
            selector.register(stream, selectors.EVENT_READ, handle)

        def send(message):
            encoded = (json.dumps(message) + "\n").encode()
            handles[2].write(encoded)
            handles[2].flush()
            process.stdin.write(encoded)
            process.stdin.flush()

        send(state.start())
        while time.monotonic() - started < seconds:
            if cancel_event is not None and cancel_event.is_set():
                for message in state.cancel():
                    send(message)
            if state.terminal_status:
                if not state.interrupt_sent:
                    termination = "completed" if state.terminal_status == "completed" else "protocol_failed"
                    break
                if state.cancel_verified:
                    events = prepared.collect_boundary_events()
                    if (events and events[-1]["active_count"] == 0
                            and any(item["phase"] == "cancelled" for item in events)
                            and any(item["phase"] == "released" for item in events)):
                        termination = "cancelled"
                        break
            if process.poll() is not None and not selector.get_map():
                termination = "unexpected_exit"
                break
            for key, _ in selector.select(0.02):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                observed += len(chunk)
                accepted = chunk[:max(0, limit - stored)]
                key.data.write(accepted)
                stored += len(accepted)
                if observed > limit:
                    termination = "output_budget"
                    break
                if key.fileobj is process.stdout:
                    buffered += chunk
                    while b"\n" in buffered:
                        line, buffered = buffered.split(b"\n", 1)
                        for message in state.accept(json.loads(line)):
                            send(message)
            if termination == "output_budget":
                break
    except (ValueError, KeyError, BrokenPipeError) as error:
        termination, failure = "protocol_error", str(error)[:500]
    finally:
        if process is not None:
            process.stdin.close()
            try:
                process.wait(timeout=1)
            except subprocess.TimeoutExpired:
                pass
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
            "client_output_profile": "codex-app-server-v2", "protocol_error": failure,
            "protocol_terminal_verified": state.terminal_status in {"completed", "interrupted", "failed"},
            "protocol_terminal_status": state.terminal_status, "interrupt_acknowledged": state.interrupt_ack,
            "interrupt_verified": state.cancel_verified, "final_texts": state.final_texts,
            "task_success_verified": False}
