"""Verify opt-in HTTP artifacts and import every attempt into a started case.

Transport EOF never certifies a protocol terminal or a successful coding task.
Private bodies stay local; CaseJournal applies its usage-field export allowlist.
"""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import stat
from typing import Any

from normalize_usage import CONTRACTS
from run_acceptance import CaseJournal
from antigravity_wire import responses as antigravity_responses, terminal as antigravity_terminal

MAX_BODY = 64 * 1024 * 1024
MAX_JOURNAL = 16 * 1024 * 1024
OUTCOMES = {"eof", "no_body", "closed", "close_error", "read_error", "transport_error", "capture_error"}


def _pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("duplicate evidence JSON field")
        result[key] = value
    return result


def _json(body):
    return json.loads(body, object_pairs_hook=_pairs)


def _read_file(directory: Path, name: str, limit: int) -> bytes:
    # Only fixed filenames from the recorder are accepted; never follow symlinks.
    path = directory / name
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode):
        raise ValueError("evidence must be a regular file")
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(descriptor, "rb") as stream:
        opened = os.fstat(stream.fileno())
        if not stat.S_ISREG(opened.st_mode) or (before.st_dev, before.st_ino) != (opened.st_dev, opened.st_ino):
            raise ValueError("HTTP artifact changed while opening")
        content = stream.read(limit + 1)
    if len(content) > limit:
        raise ValueError("evidence exceeds the size budget")
    return content


def _artifact(directory, value, expected):
    if (not isinstance(value, dict) or set(value) != {"path", "sha256", "bytes", "complete"}
            or value["path"] != expected or type(value["bytes"]) is not int
            or not 0 <= value["bytes"] <= MAX_BODY or type(value["complete"]) is not bool
            or not isinstance(value["sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", value["sha256"])):
        raise ValueError("invalid HTTP body artifact")
    body = _read_file(directory, expected, MAX_BODY)
    if len(body) != value["bytes"] or hashlib.sha256(body).hexdigest() != value["sha256"]:
        raise ValueError("HTTP artifact digest or size mismatch")
    return body


def read_admission_diagnostics(directory: Path, *, max_attempts: int, request_count: int) -> dict | None:
    """Read a sealed local admission snapshot; old/missing snapshots stay unknown."""
    if (type(max_attempts) is not int or not 1 <= max_attempts <= 10000
            or type(request_count) is not int or not 0 <= request_count <= max_attempts):
        raise ValueError("invalid admission comparison")
    try:
        state = _json(_read_file(Path(directory), "admission.json", 4096))
    except FileNotFoundError:
        return None
    integers = {"version", "max_attempts", "reserved_attempts", "budget_rejections", "closed_rejections", "failed_rejections", "active_bodies"}
    booleans = {"journal_closed", "journal_failed"}
    if (not isinstance(state, dict) or set(state) != integers | booleans
            or any(type(state[key]) is not int or not 0 <= state[key] <= 2**63 - 1 for key in integers)
            or any(type(state[key]) is not bool for key in booleans)
            or state["version"] != 1 or state["max_attempts"] != max_attempts
            or not request_count <= state["reserved_attempts"] <= max_attempts
            or not state["journal_failed"] and state["reserved_attempts"] != request_count
            or not state["journal_closed"] or state["active_bodies"] != 0
            or state["budget_rejections"] > 0 and state["reserved_attempts"] != max_attempts
            or state["failed_rejections"] > 0 and not state["journal_failed"]):
        raise ValueError("invalid or inconsistent admission diagnostic")
    return state


def _usage(contract, body):
    issues = []
    if contract == "antigravity-generate-v1":
        try:
            values = antigravity_responses(body, _json, transport_complete=True)
        except (ValueError, UnicodeDecodeError):
            return None, ["malformed_usage_source"]
        # The distinct contract requires the response wrapper; only then does
        # its inner usage share GenerateContent counter semantics.
        body = json.dumps(values).encode()
        contract = "gemini-generate-v1"
    try:
        document = _json(body)
        values = document if isinstance(document, list) else [document]
    except (ValueError, UnicodeDecodeError):
        values = []
        for frame in body.replace(b"\r\n", b"\n").split(b"\n\n"):
            data = b"\n".join(line[5:].lstrip(b" ") for line in frame.splitlines() if line.startswith(b"data:"))
            if not data or data == b"[DONE]":
                continue
            try:
                values.append(_json(data))
            except (ValueError, UnicodeDecodeError):
                issues.append("malformed_usage_source")
        if not values:
            issues.append("missing_usage_source")
    snapshots = []
    for value in values:
        if not isinstance(value, dict):
            issues.append("malformed_usage_source")
            continue
        if contract == "gemini-generate-v1":
            usage = value.get("usageMetadata")
        elif contract == "openai-responses-v1" and isinstance(value.get("response"), dict):
            usage = value["response"].get("usage")
        elif contract == "anthropic-messages-v1" and value.get("type") == "message_start" and isinstance(value.get("message"), dict):
            usage = value["message"].get("usage")
        else:
            usage = value.get("usage")
        if usage is not None:
            if not isinstance(usage, dict):
                issues.append("malformed_usage_source")
            else:
                snapshots.append(usage)
    if issues:
        return None, sorted(set(issues))
    if not snapshots:
        return None, ["missing_usage"]
    if contract in ("openai-responses-v1", "openai-chat-v1"):
        if any(snapshot != snapshots[0] for snapshot in snapshots):
            return None, ["conflicting_usage_snapshots"]
        return snapshots[-1], []
    # Anthropic emits input/cache counts at message_start and cumulative output
    # at message_delta. Gemini's usageMetadata is a cumulative request snapshot.
    merged = {}
    cumulative = ({"output_tokens"} if contract == "anthropic-messages-v1" else
                  {"candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount"})
    for snapshot in snapshots:
        for key, value in snapshot.items():
            if key in merged and merged[key] != value:
                if key not in cumulative or type(value) is not int or type(merged[key]) is not int or value < merged[key]:
                    return None, ["conflicting_usage_snapshots"]
            merged[key] = value
    return merged, []


def _closed_protocol_terminal(contract, body):
    """Prove a self-delimiting terminal, never manufacture transport EOF.

    Closed-response metering is currently scoped to Chat/Responses SSE. Other
    wire contracts remain unknown until their independent terminal proof exists.
    """
    if contract == "antigravity-generate-v1":
        return antigravity_terminal(body, _json, success=False)
    if contract not in {"openai-chat-v1", "openai-responses-v1"}:
        return False
    try:
        body.decode("utf-8")
    except UnicodeDecodeError:
        return False
    body = body.replace(b"\r\n", b"\n")
    if b"\r" in body or not body.endswith(b"\n\n"):
        return False
    payloads = []
    for frame in body.split(b"\n\n"):
        data = []
        event = None
        for line in frame.split(b"\n"):
            if not line or line.startswith(b":"):
                continue
            field, _, value = line.partition(b":")
            if field not in {b"data", b"event", b"id", b"retry"}:
                return False
            if field == b"data":
                data.append(value.removeprefix(b" "))
            elif field == b"event":
                if event is not None:
                    return False
                event = value.removeprefix(b" ")
            elif field == b"id" and b"\0" in value:
                return False
            elif field == b"retry" and not value.removeprefix(b" ").isdigit():
                return False
        if data:
            raw = b"\n".join(data)
            if raw == b"[DONE]":
                if event not in (None, b"", b"message"):
                    return False
                payloads.append("done")
            else:
                try:
                    value = _json(raw)
                except (ValueError, UnicodeDecodeError):
                    return False
                if not isinstance(value, dict):
                    return False
                if contract == "openai-chat-v1":
                    if event not in (None, b"", b"message"):
                        return False
                elif event not in (None, b"", b"message") and event.decode("utf-8") != value.get("type"):
                    return False
                payloads.append(value)
        elif event not in (None, b"", b"message"):
            return False
    if not payloads:
        return False
    if contract == "openai-chat-v1":
        if payloads[-1] != "done" or "done" in payloads[:-1]:
            return False
        observed, finished = set(), set()
        identities = set()
        reasons = {"stop", "length", "tool_calls", "function_call", "content_filter"}
        for value in payloads[:-1]:
            if "error" in value or not isinstance(value.get("choices"), list):
                return False
            if "id" in value:
                if not isinstance(value["id"], str) or not value["id"]:
                    return False
                identities.add(value["id"])
                if len(identities) > 1:
                    return False
            for choice in value["choices"]:
                if not isinstance(choice, dict) or type(choice.get("index")) is not int or choice["index"] < 0:
                    return False
                index, reason = choice["index"], choice.get("finish_reason")
                if index in finished:
                    return False
                observed.add(index)
                if reason is not None:
                    if not isinstance(reason, str) or reason not in reasons:
                        return False
                    finished.add(index)
        return bool(observed and observed == finished)
    if payloads[-1] == "done":
        payloads.pop()
    if not payloads or any(not isinstance(value, dict) for value in payloads):
        return False
    statuses = {"response.completed": "completed", "response.failed": "failed", "response.incomplete": "incomplete"}
    final = payloads[-1]
    response = final.get("response")
    if (not isinstance(final.get("type"), str) or final["type"] not in statuses or not isinstance(response, dict)
            or response.get("status") != statuses[final["type"]]
            or not isinstance(response.get("id"), str) or not response["id"]):
        return False
    for value in payloads[:-1]:
        if not isinstance(value.get("type"), str) or value["type"] in statuses or value["type"] == "error":
            return False
        parent = value.get("response")
        if isinstance(parent, dict) and parent.get("id") not in (None, response["id"]):
            return False
        if value.get("response_id") not in (None, response["id"]):
            return False
    return True


def read_http_attempts(directory: Path) -> list[dict[str, Any]]:
    """Require intact journals; absent finishes remain unknown, never zero-cost."""
    directory = Path(directory)
    content = _read_file(directory, "attempts.jsonl", MAX_JOURNAL)
    if content and not content.endswith(b"\n"):
        raise ValueError("HTTP journal has a truncated record")
    records = {}
    for sequence, line in enumerate(content.splitlines(), 1):
        event = _json(line)
        common = {"version", "sequence", "event", "request_id", "contract"}
        if (not isinstance(event, dict) or type(event.get("sequence")) is not int or event["sequence"] != sequence
                or type(event.get("version")) is not int or event["version"] not in {1, 2} or event.get("contract") not in CONTRACTS):
            raise ValueError("invalid HTTP journal identity or contract")
        request_id = event.get("request_id")
        if not isinstance(request_id, str) or not re.fullmatch(r"request-[0-9]{6}", request_id):
            raise ValueError("invalid HTTP attempt ID")
        if event.get("event") == "request-started":
            extra = {"executor_identity"} if event["version"] == 2 else set()
            if set(event) != common | {"request"} | extra or request_id != f"request-{len(records) + 1:06d}":
                raise ValueError("invalid or duplicated HTTP start")
            identity = event.get("executor_identity")
            if identity is not None:
                if (not isinstance(identity, dict) or set(identity) != {"executor", "model_sha256", "auth_sha256"}
                        or not isinstance(identity["executor"], str) or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", identity["executor"])
                        or any(not isinstance(identity[key], str) or identity[key] != "" and not re.fullmatch(r"[0-9a-f]{64}", identity[key])
                               for key in ("model_sha256", "auth_sha256"))):
                    raise ValueError("invalid actual executor identity")
            _artifact(directory, event["request"], request_id + ".body")
            if not event["request"]["complete"]:
                raise ValueError("HTTP request evidence must be complete before dispatch")
            records[request_id] = {"request_id": request_id, "contract": event["contract"],
                                   "usage_kind": "request-total", "raw_usage": None, "finished": False,
                                   "transport_outcome": "unknown", "executor_identity": identity,
                                   "journal_version": event["version"], "transport_capture_complete": None,
                                   "http_status_code": None,
                                   "usage_evidence": "unknown", "issues": ["unfinished_http_attempt"]}
        elif event.get("event") == "request-finished":
            if (set(event) != common | {"outcome", "status_code", "observed_bytes", "response"}
                    or request_id not in records or records[request_id]["finished"]
                    or event["version"] != records[request_id]["journal_version"]
                    or event["contract"] != records[request_id]["contract"]
                    or not isinstance(event["outcome"], str) or event["outcome"] not in OUTCOMES or type(event["status_code"]) is not int
                    or event["status_code"] != 0 and not 100 <= event["status_code"] <= 599
                    or type(event["observed_bytes"]) is not int or not 0 <= event["observed_bytes"] <= 2**63 - 1):
                raise ValueError("invalid HTTP finish")
            body = _artifact(directory, event["response"], "response-" + request_id[8:] + ".body")
            if event["observed_bytes"] < len(body):
                raise ValueError("capture contains more bytes than upstream delivered")
            complete = event["response"]["complete"]
            if complete and (event["outcome"] not in {"eof", "no_body"} or event["observed_bytes"] != len(body)):
                raise ValueError("invalid complete-response assertion")
            terminal = (not complete and event["outcome"] == "closed" and 200 <= event["status_code"] < 300
                        and event["observed_bytes"] == len(body) and _closed_protocol_terminal(event["contract"], body))
            usage, issues = _usage(event["contract"], body) if complete or terminal else (None, ["incomplete_response_capture"])
            basis = ("transport-complete" if complete else "protocol-terminal") if usage is not None else "unknown"
            records[request_id].update(raw_usage=usage, finished=True, transport_outcome=event["outcome"], issues=issues,
                                      http_status_code=event["status_code"],
                                      transport_capture_complete=complete, usage_evidence=basis)
        else:
            raise ValueError("unknown HTTP journal event")
    return list(records.values())


def import_http_attempts(journal: CaseJournal, case_id: str, directory: Path, *, expected_executor: str | None = None) -> list[dict[str, Any]]:
    """Import a sealed snapshot into an already-started, not-yet-finished case.

    The upstream journal supplies the pre-dispatch evidence. This post-run import
    does not pretend the Python record itself was written before network I/O.
    Reimporting the same snapshot is rejected by CaseJournal's unique-ID rules.
    """
    records = read_http_attempts(directory)
    if expected_executor is not None:
        if not isinstance(expected_executor, str) or not expected_executor:
            raise ValueError("invalid expected executor")
        for record in records:
            identity = record["executor_identity"]
            if (identity is None or identity["executor"] != expected_executor
                    or not identity["model_sha256"] or not identity["auth_sha256"]):
                raise ValueError("actual executor does not match the pinned evidence scope")
    for record in records:
        journal.start_request(case_id, record["request_id"], record["contract"])
        if record["finished"]:
            journal.record_request(case_id, record)
    return records
