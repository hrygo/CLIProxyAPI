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


def _usage(contract, body):
    issues = []
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
                                   "journal_version": event["version"], "issues": ["unfinished_http_attempt"]}
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
            usage, issues = _usage(event["contract"], body) if complete else (None, ["incomplete_response_capture"])
            records[request_id].update(raw_usage=usage, finished=True, transport_outcome=event["outcome"], issues=issues)
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
