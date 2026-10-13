"""Read sealed local response-conversion diagnostics, never error prose."""
from pathlib import Path
from http_attempts import _json, _read_file
from preparation_evidence import REASONS, preparation_failure_class

PHASES = {"response_restore", "stream_feed", "stream_finish"}

def read_response_diagnostics(directory):
    try:
        value = _json(_read_file(Path(directory), "response-failures.json", 65536))
    except FileNotFoundError:
        return None
    if (not isinstance(value, dict) or set(value) != {"version", "requests_seen", "active_requests", "sealed", "failed", "failures"}
            or type(value["version"]) is not int or value["version"] != 1
            or type(value["requests_seen"]) is not int or not 0 <= value["requests_seen"] <= 256
            or type(value["active_requests"]) is not int or value["active_requests"] != 0
            or value["sealed"] is not True or value["failed"] is not False
            or not isinstance(value["failures"], list) or len(value["failures"]) > 256):
        raise ValueError("invalid or unsealed response diagnostic")
    seen = set()
    for failure in value["failures"]:
        if (not isinstance(failure, dict) or set(failure) != {"request_sequence", "phase", "reason", "status_code"}
                or type(failure["request_sequence"]) is not int or not 1 <= failure["request_sequence"] <= value["requests_seen"]
                or failure["request_sequence"] in seen
                or not isinstance(failure["phase"], str) or failure["phase"] not in PHASES
                or not isinstance(failure["reason"], str) or failure["reason"] not in REASONS
                or type(failure["status_code"]) is not int or not 400 <= failure["status_code"] <= 599):
            raise ValueError("invalid typed response failure")
        seen.add(failure["request_sequence"])
    return value

def response_failure_class(diagnostics):
    if diagnostics is None:
        return None
    return preparation_failure_class(diagnostics)
