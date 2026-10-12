"""Append-only case accounting and offline replay; live client adapters are separate."""

from __future__ import annotations

import argparse
from copy import deepcopy
from dataclasses import asdict, dataclass
import json
import os
from pathlib import Path
import re
from typing import Any

from jsonschema import Draft202012Validator

from normalize_usage import CONTRACTS, aggregate_usage

IDENTIFIER = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:+/@()-]{0,159}$")
HASH = re.compile(r"^[0-9a-f]{64}$")
FAILURES = {"unknown", "framework", "provider", "protocol", "model", "permission", "budget", "cancelled"}
CHECK_FIELDS = {"answer", "dispatch", "nonce", "code", "format", "terminal"}
BOUNDARY_FIELDS = {"mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"}
METRICS = ("first_input_tokens", "peak_input_tokens", "total_input_tokens")
SCHEMA = json.loads((Path(__file__).with_name("manifest.schema.json")).read_text())
SCOPE_VALIDATOR = Draft202012Validator({"$ref": "#/$defs/scope", "$defs": SCHEMA["$defs"]})


@dataclass
class CaseSpec:
    case_id: str
    pair_id: str
    template_id: str
    mode: str
    cache_condition: str
    scope: dict[str, str]
    prompt_hash: str
    fixture_seed: str
    limits: dict[str, int]
    gold: dict[str, bool]


def _identifier(value: Any) -> bool:
    return isinstance(value, str) and IDENTIFIER.fullmatch(value) is not None and "://" not in value


def _validate_spec(value: dict[str, Any]) -> None:
    if set(value) != set(CaseSpec.__dataclass_fields__):
        raise ValueError("case specification fields do not match the contract")
    if any(not _identifier(value[key]) for key in ("case_id", "pair_id", "template_id", "fixture_seed")):
        raise ValueError("case identifiers are invalid")
    if value["mode"] not in ("eager", "lazy") or value["cache_condition"] not in ("cold", "warm", "observed", "unknown"):
        raise ValueError("mode and cache condition must be explicit")
    if not isinstance(value["prompt_hash"], str) or not HASH.fullmatch(value["prompt_hash"]):
        raise ValueError("a prompt fingerprint is required")
    if not SCOPE_VALIDATOR.is_valid(value["scope"]):
        raise ValueError("evidence scope does not match the manifest contract")
    limits = value["limits"]
    if not isinstance(limits, dict) or set(limits) != {"max_requests", "max_dispatches", "task_seconds"} or any(type(count) is not int or not 1 <= count <= 2**31 - 1 for count in limits.values()):
        raise ValueError("positive task limits are required")
    gold = value["gold"]
    if not isinstance(gold, dict) or set(gold) != {"requires_dispatch", "requires_nonce", "requires_code", "format_required"} or any(type(check) is not bool for check in gold.values()):
        raise ValueError("task gold requirements are invalid")


def _safe_request(request: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(request, dict) or not _identifier(request.get("request_id")) or request.get("usage_kind") != "request-total":
        raise ValueError("request attempts need an ID and consolidated request-total usage")
    contract = request.get("contract")
    contract = contract if contract in CONTRACTS else "unknown"
    paths = {
        "openai-responses-v1": ("input_tokens", "output_tokens", "total_tokens", "input_tokens_details.cached_tokens", "input_tokens_details.cache_write_tokens", "output_tokens_details.reasoning_tokens"),
        "openai-chat-v1": ("prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details.cached_tokens", "prompt_tokens_details.cache_write_tokens", "completion_tokens_details.reasoning_tokens"),
        "anthropic-messages-v1": ("input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_1h_input_tokens", "output_tokens_details.thinking_tokens"),
        "gemini-generate-v1": ("promptTokenCount", "cachedContentTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount", "toolUsePromptTokenCount"),
    }
    raw = request.get("raw_usage")
    safe = None
    if isinstance(raw, dict) and contract in CONTRACTS:
        safe = {}
        for path in paths[contract]:
            original: Any = raw
            keys = path.split(".")
            target = safe
            for index, key in enumerate(keys):
                if not isinstance(original, dict) or key not in original:
                    break
                original = original[key]
                if index == len(keys) - 1:
                    # Retain counters/null, never arbitrary malformed text.
                    target[key] = original if original is None or type(original) in (int, bool) else "invalid"
                elif not isinstance(original, dict):
                    target[key] = None if original is None else "invalid"
                    break
                else:
                    target = target.setdefault(key, {})
    elif raw is not None and contract in CONTRACTS:
        safe = "invalid"
    return {"request_id": request["request_id"], "contract": contract,
            "usage_kind": "request-total", "raw_usage": safe}


def _validate_finish(event: dict[str, Any]) -> None:
    checks, boundaries = event["checks"], event["boundary_counts"]
    if not isinstance(checks, dict) or set(checks) != CHECK_FIELDS or any(value is not None and type(value) is not bool for value in checks.values()):
        raise ValueError("completion checks need explicit boolean or unknown values")
    if not isinstance(boundaries, dict) or set(boundaries) != BOUNDARY_FIELDS or any(value is not None and (type(value) is not int or not 0 <= value <= 2**63 - 1) for value in boundaries.values()):
        raise ValueError("execution boundaries need explicit counts or unknown values")
    if event["failure_class"] not in FAILURES:
        raise ValueError("unknown failure classification")
    elapsed = event["elapsed_ms"]
    if elapsed is not None and (type(elapsed) is not int or not 0 <= elapsed <= 2**63 - 1):
        raise ValueError("elapsed time must be nonnegative or unknown")


def _replay(events: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    cases: dict[str, dict[str, Any]] = {}
    pairs = set()
    run_id = None
    gateway_revision = None
    for sequence, event in enumerate(events):
        if not isinstance(event, dict) or event.get("sequence") != sequence or type(event.get("sequence")) is not int or not _identifier(event.get("run_id")):
            raise ValueError("event sequence/run identity is invalid")
        run_id = event["run_id"] if run_id is None else run_id
        if event["run_id"] != run_id:
            raise ValueError("multiple runs cannot share a journal")
        phase = event.get("phase")
        common = {"sequence", "run_id", "phase", "case_id"}
        fields = {"case_started": {"spec"}, "request_started": {"request"}, "request_finished": {"request"},
                  "case_finished": {"checks", "boundary_counts", "elapsed_ms", "failure_class"}}
        if phase not in fields or set(event) != common | fields[phase]:
            raise ValueError("event fields do not match the allowlist")
        case_id = event["case_id"]
        if not _identifier(case_id):
            raise ValueError("invalid case identity")
        if phase == "case_started":
            spec = event["spec"]
            _validate_spec(spec)
            revision = spec["scope"]["gateway_revision"]
            gateway_revision = revision if gateway_revision is None else gateway_revision
            if revision != gateway_revision:
                raise ValueError("a new gateway revision requires a separate run")
            pair = (spec["pair_id"], spec["mode"])
            if case_id != spec["case_id"] or case_id in cases or pair in pairs:
                raise ValueError("case/pair-mode identity was already started")
            pairs.add(pair)
            cases[case_id] = {"spec": deepcopy(spec), "requests": [], "settled": set(), "finished": None}
        else:
            if case_id not in cases or cases[case_id]["finished"] is not None:
                raise ValueError("event must belong to an active case")
            case = cases[case_id]
            if phase in ("request_started", "request_finished"):
                request = event["request"]
                if _safe_request(request) != request:
                    raise ValueError("request contains unexportable data")
                previous = next((item for item in case["requests"] if item["request_id"] == request["request_id"]), None)
                if phase == "request_started":
                    if previous is not None or request["raw_usage"] is not None:
                        raise ValueError("request start is duplicated or already has usage")
                    case["requests"].append(deepcopy(request))
                else:
                    if previous is None or request["request_id"] in case["settled"] or previous["contract"] != request["contract"]:
                        raise ValueError("request finish needs a matching unfinished start")
                    previous.update(deepcopy(request))
                    case["settled"].add(request["request_id"])
            else:
                _validate_finish(event)
                case["finished"] = deepcopy(event)
    return cases


class CaseJournal:
    """Single-writer durable journal; record starts before launching any task."""

    def __init__(self, path: Path, run_id: str):
        if not _identifier(run_id):
            raise ValueError("invalid run identity")
        self.path, self.run_id = path, run_id
        self.events: list[dict[str, Any]] = []
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(descriptor)

    def _append(self, phase: str, case_id: str, **payload: Any) -> None:
        event = {"sequence": len(self.events), "run_id": self.run_id, "phase": phase, "case_id": case_id, **payload}
        _replay(self.events + [event])
        document = json.dumps(event, sort_keys=True, separators=(",", ":"), allow_nan=False).encode() + b"\n"
        if len(document) > 1048576:
            raise ValueError("event exceeds the evidence size budget")
        with self.path.open("ab") as stream:
            stream.write(document)
            stream.flush()
            os.fsync(stream.fileno())
        self.events.append(deepcopy(event))

    def start_case(self, spec: CaseSpec) -> None:
        self._append("case_started", spec.case_id, spec=asdict(spec))

    def record_request(self, case_id: str, request: dict[str, Any]) -> None:
        self._append("request_finished", case_id, request=_safe_request(request))

    def start_request(self, case_id: str, request_id: str, contract: str) -> None:
        self._append("request_started", case_id, request=_safe_request({
            "request_id": request_id, "contract": contract, "usage_kind": "request-total", "raw_usage": None}))

    def finish_case(self, case_id: str, *, checks: dict[str, bool | None], boundary_counts: dict[str, int | None],
                    elapsed_ms: int | None, failure_class: str = "unknown") -> None:
        self._append("case_finished", case_id, checks=checks, boundary_counts=boundary_counts,
                     elapsed_ms=elapsed_ms, failure_class=failure_class)


def read_journal(path: Path) -> list[dict[str, Any]]:
    with path.open("rb") as stream:
        content = stream.read(67108865)
    if len(content) > 67108864 or (content and not content.endswith(b"\n")):
        raise ValueError("journal is oversized or truncated")
    try:
        events = [json.loads(line) for line in content.splitlines()]
    except (UnicodeDecodeError, ValueError) as error:
        raise ValueError("journal contains invalid JSON records") from error
    _replay(events)
    return events


def _result(case: dict[str, Any]) -> dict[str, Any]:
    spec, finished = case["spec"], case["finished"]
    usage = aggregate_usage(case["requests"])
    result = {"spec": spec, "usage": usage, "safe_task_success": False,
              "finished": finished is not None, "failure_class": "unfinished", "elapsed_ms": None,
              "checks": None, "boundary_counts": None}
    if finished is None:
        return result
    checks, counts, gold = finished["checks"], finished["boundary_counts"], spec["gold"]
    mandatory = {"answer", "terminal"}
    mandatory.update(field for field, requirement in (("dispatch", "requires_dispatch"), ("nonce", "requires_nonce"), ("code", "requires_code"), ("format", "format_required")) if gold[requirement])
    permission = counts["forbidden_dispatch"] == 0 and counts["forbidden_effect"] == 0
    released = counts["active"] == 0
    dispatch = counts["dispatch"] is not None and (counts["dispatch"] > 0 if gold["requires_dispatch"] else counts["dispatch"] == 0)
    consistent = counts["dispatch"] is not None and counts["mcp_attempts"] is not None and counts["dispatch"] <= counts["mcp_attempts"]
    elapsed = finished["elapsed_ms"]
    budget = (elapsed is not None and elapsed <= spec["limits"]["task_seconds"] * 1000
              and usage["request_count"] <= spec["limits"]["max_requests"]
              and counts["dispatch"] is not None and counts["dispatch"] <= spec["limits"]["max_dispatches"])
    settled = len(case["settled"]) == len(case["requests"])
    success = all(checks[field] is True for field in mandatory) and permission and released and dispatch and consistent and budget and settled
    failure = "none" if success else finished["failure_class"]
    if not permission:
        failure = "permission" if counts["forbidden_dispatch"] or counts["forbidden_effect"] else "unknown"
    elif not budget:
        failure = "budget" if elapsed is not None and counts["dispatch"] is not None else "unknown"
    result.update(safe_task_success=success, failure_class=failure, elapsed_ms=elapsed,
                  checks=checks, boundary_counts=counts)
    return result


def summarize(events: list[dict[str, Any]]) -> dict[str, Any]:
    results = [_result(case) for case in _replay(events).values()]
    failures: dict[str, int] = {}
    pair_cases: dict[str, dict[str, Any]] = {}
    groups: dict[str, dict[str, Any]] = {}
    for result in results:
        if not result["safe_task_success"]:
            failure = result["failure_class"]
            failures[failure] = failures.get(failure, 0) + 1
        spec = result["spec"]
        pair_cases.setdefault(spec["pair_id"], {})[spec["mode"]] = result
        identity = {"scope": spec["scope"], "mode": spec["mode"], "cache_condition": spec["cache_condition"]}
        group_id = json.dumps(identity, sort_keys=True)
        group = groups.setdefault(group_id, {**deepcopy(identity), "started_cases": 0, "safe_successes": 0,
                                            "unfinished_cases": 0, "request_attempts": 0, "template_ids": set(),
                                            "failure_counts": {}, "elapsed_ms": []})
        group["started_cases"] += 1
        group["safe_successes"] += result["safe_task_success"]
        group["unfinished_cases"] += not result["finished"]
        group["request_attempts"] += result["usage"]["request_count"]
        group["template_ids"].add(spec["template_id"])
        group["elapsed_ms"].append(result["elapsed_ms"])
        if not result["safe_task_success"]:
            group["failure_counts"][result["failure_class"]] = group["failure_counts"].get(result["failure_class"], 0) + 1
    pairs = []
    for pair_id, modes in pair_cases.items():
        comparable = set(modes) == {"eager", "lazy"}
        reason = "incomplete_pair" if not comparable else "matched"
        if comparable:
            left, right = modes["eager"]["spec"], modes["lazy"]["spec"]
            left_scope = {key: value for key, value in left["scope"].items() if key != "client_mode"}
            right_scope = {key: value for key, value in right["scope"].items() if key != "client_mode"}
            fields = ("template_id", "cache_condition", "prompt_hash", "fixture_seed", "limits", "gold")
            comparable = left_scope == right_scope and all(left[field] == right[field] for field in fields)
            reason = "matched" if comparable else "scope_mismatch"
            if comparable and ("unknown" in left["scope"].values() or "unknown" in right["scope"].values() or left["cache_condition"] == "unknown"):
                comparable, reason = False, "unknown_scope"
        savings = dict.fromkeys(METRICS)
        if comparable:
            for metric in METRICS:
                eager, lazy = modes["eager"]["usage"][metric], modes["lazy"]["usage"][metric]
                if eager is not None and eager > 0 and lazy is not None:
                    savings[metric] = 1 - lazy / eager
        pairs.append({"pair_id": pair_id, "template_id": next(iter(modes.values()))["spec"]["template_id"],
                      "comparable": comparable, "reason": reason, "savings": savings,
                      "safe_success": {mode: result["safe_task_success"] for mode, result in modes.items()}})
    for group in groups.values():
        group["template_clusters"] = len(group.pop("template_ids"))
    return {"schema_version": 1, "run_id": events[0]["run_id"] if events else "unknown",
            "started_cases": len(results), "safe_successes": sum(result["safe_task_success"] for result in results),
            "unfinished_cases": sum(not result["finished"] for result in results), "failure_counts": failures,
            "template_clusters": len({result["spec"]["template_id"] for result in results}),
            "cases": results, "groups": list(groups.values()), "pairs": pairs, "gate_status": "unverified"}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--replay", type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(summarize(read_journal(args.replay)), sort_keys=True, allow_nan=False))


if __name__ == "__main__":
    main()
