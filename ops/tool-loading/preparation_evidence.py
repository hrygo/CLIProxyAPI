"""Read fixed typed local preparation diagnostics, never client error prose."""
from pathlib import Path

from http_attempts import _json, _read_file

REASONS = {"json_syntax", "history_link", "unsupported_protocol", "unsupported_grammar",
           "opaque_history", "declaration_budget", "attempt_budget", "shared_capacity",
           "upstream_contract", "schema_policy", "custom_forced_call", "ambiguous_identity",
           "invalid_custom_input", "invalid_configuration", "unknown"}
BUDGET_STATUS = {"declaration_budget": 413, "attempt_budget": 413, "shared_capacity": 429}


def read_preparation_diagnostics(directory):
    try:
        value = _json(_read_file(Path(directory), "preparation.json", 65536))
    except FileNotFoundError:
        return None
    if (not isinstance(value, dict) or set(value) != {"version", "requests_seen", "active_requests", "sealed", "failed", "failures"}
            or type(value["version"]) is not int or value["version"] != 1
            or type(value["requests_seen"]) is not int or not 0 <= value["requests_seen"] <= 256
            or type(value["active_requests"]) is not int or value["active_requests"] != 0
            or value["sealed"] is not True or value["failed"] is not False
            or not isinstance(value["failures"], list) or len(value["failures"]) > 256):
        raise ValueError("invalid or unsealed preparation diagnostic")
    for failure in value["failures"]:
        if (not isinstance(failure, dict) or set(failure) != {"request_sequence", "phase", "reason", "status_code"}
                or type(failure["request_sequence"]) is not int or not 1 <= failure["request_sequence"] <= value["requests_seen"]
                or failure["phase"] != "prepare" or not isinstance(failure["reason"], str) or failure["reason"] not in REASONS
                or type(failure["status_code"]) is not int or not 400 <= failure["status_code"] <= 599):
            raise ValueError("invalid typed preparation failure")
    return value


def preparation_failure_class(diagnostics):
    if diagnostics is None:
        return "framework"
    failures = diagnostics["failures"]
    if any(failure["reason"] == "unknown" for failure in failures):
        return "framework"
    if any(BUDGET_STATUS.get(failure["reason"]) == failure["status_code"] for failure in failures):
        return "budget"
    return "protocol" if failures else None
