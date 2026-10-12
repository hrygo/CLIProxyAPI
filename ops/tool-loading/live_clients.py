"""Bounded real-Provider calibration, with every started task retained.

This runner does not certify a route or implement statistical acceptance.
Only synthetic MCP data is sent; operator configuration remains read only.
"""

import argparse
from copy import deepcopy
from dataclasses import asdict
import hashlib
import http.client
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
from urllib.parse import urlsplit
import uuid

from catalog_fixture import build_catalog, digest
from batch_plan import build_plan, write_plan, fingerprint_materials, verify_materials, BatchBudget, audit_plan
from client_adapters import VERSIONS, prepare_isolated_client, write_private
from http_attempts import import_http_attempts
from live_gateway import candidate_process
from run_acceptance import CaseJournal, CaseSpec, read_journal, summarize
from scripted_clients import client_result
from task_scenarios import SCENARIOS, get_scenario, answer_matches, dispatch_matches, intermediate_receipt_replayed

PROMPT = get_scenario("lookup").prompt
PERMISSIONS = {"client_denied": [], "service_denied": ["fixture_mutation"]}


def endpoint_id(value):
    if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}", value) is None:
        raise ValueError("complete configured endpoint digest is required")
    return "ep_" + value[:32]


def select_model_metadata(body, model, client, mode):
    if not isinstance(body, dict) or not isinstance(body.get("models"), list):
        raise ValueError("actual Codex catalog is required")
    selected = [item for item in body["models"] if isinstance(item, dict) and item.get("slug") == model]
    if len(selected) != 1 or type(selected[0].get("context_window")) is not int or selected[0]["context_window"] < 1:
        raise ValueError("selected actual model metadata unavailable or ambiguous")
    metadata = selected[0]
    if client == "codex" and (metadata.get("tool_mode") not in (None, "direct") or
            mode == "lazy" and metadata.get("supports_search_tool") is not True):
        raise ValueError("actual catalog does not admit the requested Codex search profile")
    return deepcopy(metadata)


def fetch_model_metadata(ready, model, client, mode):
    address = urlsplit(ready["base_url"])
    connection = http.client.HTTPConnection(address.hostname, address.port, timeout=5)
    try:
        connection.request("GET", "/v1/models?client_version=" + VERSIONS["codex"],
                           headers={"Authorization": "Bearer local-acceptance-placeholder"})
        response = connection.getresponse()
        body = response.read(4 * 1024 * 1024 + 1)
        if response.status != 200 or len(body) > 4 * 1024 * 1024:
            raise ValueError("bounded local model catalog query failed")
        return select_model_metadata(json.loads(body), model, client, mode)
    finally:
        connection.close()


def result_checks(client, raw, nonce, *, scenario="lookup"):
    observed = client_result(client, raw)
    values = []
    plain_json = False
    for text in observed["final_texts"][-1:]:
        try:
            values.append(json.loads(text))
            plain_json = True
        except (ValueError, TypeError):
            # Receipt use and hard JSON formatting are independent checks.
            # This does not admit an earlier message or relax the format gate.
            if isinstance(text, str) and text.startswith("```json\n") and text.rstrip().endswith("```"):
                try:
                    values.append(json.loads(text.rstrip()[8:-3]))
                except ValueError:
                    pass
    answer = values[-1] if values and isinstance(values[-1], dict) else {}
    matched, receipt, exact = answer_matches(scenario, answer, nonce)
    return {"answer": matched, "dispatch": None, "nonce": receipt, "code": None,
            "format": bool(observed["parse_complete"] and plain_json and exact), "terminal": observed["legal_terminal"]}


def chat_terminal(body):
    try:
        values = [json.loads(body)]
    except (ValueError, UnicodeDecodeError):
        values = []
        for frame in body.replace(b"\r\n", b"\n").split(b"\n\n"):
            data = b"\n".join(line[5:].lstrip() for line in frame.splitlines() if line.startswith(b"data:"))
            if data and data != b"[DONE]":
                try:
                    values.append(json.loads(data))
                except (ValueError, UnicodeDecodeError):
                    return False
    reasons = []
    for value in values:
        if not isinstance(value, dict) or not isinstance(value.get("choices", []), list):
            return False
        for choice in value.get("choices", []):
            if not isinstance(choice, dict):
                return False
            if choice.get("finish_reason") is not None:
                reasons.append(choice["finish_reason"])
    return bool(reasons and all(isinstance(reason, str) and reason in {"stop", "tool_calls", "function_call"} for reason in reasons))


def make_case_spec(config, profile, client, mode, revision, endpoint, seconds, *, scenario="lookup"):
    task = get_scenario(scenario)
    pair = client + "-" + profile["client_model"]
    if scenario != "lookup":
        pair += "-" + scenario
    case_id = pair + "-" + mode
    executable = Path(shutil.which(client) or client).resolve()
    scope = dict(gateway_revision=revision, policy_generation=hashlib.sha256(config.read_bytes()).hexdigest(),
                 client=client, client_version=VERSIONS[client], client_revision=hashlib.sha256(executable.read_bytes()).hexdigest(),
                 client_mode="direct" if mode == "eager" else "tool-search" if client == "codex" else "code-mode",
                 source_format="codex" if client == "codex" else "openai", provider=profile["executor"], model=profile["upstream_model"],
                 upstream_format="openai", endpoint_id=endpoint, transport="http-sse", region_id="unknown",
                 catalog_hash=digest(build_catalog()), fixture_hash=hashlib.sha256(Path(__file__).with_name("catalog_fixture.py").read_bytes()).hexdigest(),
                 permission_hash=digest(PERMISSIONS))
    return CaseSpec(case_id, pair, task.template_id, mode, "unknown", scope, hashlib.sha256(task.prompt.encode()).hexdigest(),
                    "live-calibration-2", {"max_requests": 8, "max_dispatches": 8, "task_seconds": seconds}, task.gold)


def run_case(root, journal, binary, config, profile, client, mode, revision, endpoint, seconds, *,
             specification=None, locked_plan=None, material_paths=None, scenario="lookup"):
    task = get_scenario(scenario)
    started = time.monotonic()
    spec = specification or make_case_spec(config, profile, client, mode, revision, endpoint, seconds, scenario=scenario)
    case_id = spec.case_id
    journal.start_case(spec)
    case_root = root / case_id
    case_root.mkdir(mode=0o700)
    checks = dict.fromkeys(("answer", "dispatch", "nonce", "code", "format", "terminal"))
    boundaries = dict.fromkeys(("mcp_attempts", "dispatch", "forbidden_dispatch", "forbidden_effect", "active"))
    capture, prepared, process = None, None, None
    issues, attempts = [], []
    failure, effective, declaration_bytes = "framework", False, []
    stage = "candidate_start"
    try:
        if locked_plan is not None:
            stage = "material_validation"
            verify_materials(locked_plan, material_paths)
            stage = "scope_validation"
            if asdict(spec) != asdict(make_case_spec(config, profile, client, mode, revision, endpoint, seconds, scenario=scenario)):
                raise ValueError("actual task inputs differ from predeclared case")
        stage = "candidate_start"
        with candidate_process(binary, config, case_root, profile, max_attempts=spec.limits["max_requests"]) as (ready, capture):
            stage = "catalog_validation"
            metadata = fetch_model_metadata(ready, profile["client_model"], client, mode)
            write_private(case_root / "actual-model.json", json.dumps(metadata, indent=2))
            nonce = uuid.uuid4().hex
            stage = "client_preparation"
            executable = Path(shutil.which(client) or client).resolve()
            prepared = prepare_isolated_client(client, mode, {
                "run_dir": case_root / "client", "client_version": VERSIONS[client], "executable": str(executable),
                "fixture_python": sys.executable, "model": profile["client_model"], "model_metadata": metadata,
                "base_url": ready["base_url"], "nonce": nonce, "max_dispatches": spec.limits["max_dispatches"], "max_result_bytes": 65536}, PERMISSIONS)
            remaining = seconds - (time.monotonic() - started)
            if remaining <= 0:
                raise ValueError("task budget exhausted before client launch")
            stage = "client_execution"
            process = prepared.run_task({"prompt": task.prompt, "max_seconds": remaining, "max_output_bytes": 2 * 1024 * 1024})
            checks = result_checks(client, Path(process["stdout_path"]).read_text(), nonce, scenario=scenario)
            events = prepared.collect_boundary_events()
            if events:
                boundaries.update(mcp_attempts=sum(event["phase"] == "request" for event in events),
                    dispatch=sum(event["phase"] == "dispatch" for event in events),
                    forbidden_dispatch=sum(event["phase"] == "dispatch" and event["tool"] == "fixture_mutation" for event in events),
                    forbidden_effect=max(event["effect_count"] for event in events), active=events[-1]["active_count"])
                checks["dispatch"] = dispatch_matches(scenario, events)
                if not task.requires_dispatch:
                    checks["answer"] = bool(checks["answer"] and checks["dispatch"])
            failure = "model"
            if process["termination"] != "exited" or process["exit_code"] != 0:
                checks["answer"] = False
                failure = "budget" if process["termination"] in {"process_budget", "output_budget"} else "framework"
            stage = "candidate_cleanup"
        if locked_plan is not None:
            stage = "material_revalidation"
            verify_materials(locked_plan, material_paths)
    except (OSError, ValueError, subprocess.SubprocessError):
        checks["answer"] = False
        issues.append(stage + "_failure")
        failure = "framework"
    if capture is not None:
        try:
            attempts = import_http_attempts(journal, case_id, capture, expected_executor=profile["executor"])
            bodies = [json.loads((capture / (attempt["request_id"] + ".body")).read_bytes()) for attempt in attempts]
            if scenario == "multitool" and prepared is not None and not intermediate_receipt_replayed(bodies, nonce):
                checks["dispatch"] = False
                issues.append("intermediate_receipt_not_replayed")
            declarations = [body.get("tools", []) for body in bodies if body.get("tools")]
            declaration_bytes = [len(json.dumps(tools, separators=(",", ":")).encode()) for tools in declarations]
            if declarations:
                first = json.dumps(declarations[0])
                effective = (len(declarations[0]) >= 60 if mode == "eager" else len(declarations[0]) < 60 and
                             ("tool_search" in first if client == "codex" else '"execute"' in first))
            if not attempts or not effective:
                checks["answer"] = False
                issues.append("effective_tool_mode_not_observed")
            terminals = [chat_terminal((capture / ("response-" + attempt["request_id"][8:] + ".body")).read_bytes()) for attempt in attempts]
            checks["terminal"] = bool(checks["terminal"] and terminals and all(terminals))
        except (OSError, ValueError):
            checks["answer"] = False
            issues.append("attempt_evidence_import_failed")
            failure = "framework"
    else:
        issues.append("no_gateway_attempt_capture")
    journal.finish_case(case_id, checks=checks, boundary_counts=boundaries,
                        elapsed_ms=int((time.monotonic() - started) * 1000), failure_class=failure)
    case = next(value for value in summarize(journal.events)["cases"] if value["spec"]["case_id"] == case_id)
    result = {"case_id": case_id, "scope": "real-provider-calibration", "safe_task_success": case["safe_task_success"] and not issues,
              "checks": checks, "boundary_counts": boundaries, "upstream_attempts": len(attempts), "issues": issues,
              "effective_mode_observed": effective, "declaration_bytes": declaration_bytes, "usage": case["usage"],
              "failure_class": case["failure_class"], "gate_status": "unverified"}
    write_private(case_root / "summary.json", json.dumps(result, indent=2))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("gateway-binary", "config", "output-dir"):
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ("gateway-revision", "endpoint-sha256", "client-model", "upstream-model", "executor"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--clients", nargs="+", choices=list(VERSIONS), default=list(VERSIONS))
    parser.add_argument("--modes", nargs="+", choices=["eager", "lazy"], default=["eager", "lazy"])
    parser.add_argument("--scenarios", nargs="+", choices=list(SCENARIOS), default=["lookup"])
    parser.add_argument("--task-seconds", type=int, default=90)
    parser.add_argument("--schedule-seed", default="calibration-order-1")
    parser.add_argument("--batch-max-requests", type=int)
    parser.add_argument("--batch-max-dispatches", type=int)
    args = parser.parse_args()
    if not 10 <= args.task_seconds <= 300 or not args.executor.startswith("openai-compatible-"):
        parser.error("bounded Chat calibration only")
    if any(len(set(values)) != len(values) for values in (args.clients, args.modes, args.scenarios)):
        parser.error("duplicate client, mode, or scenario")
    paired = set(args.modes) == {"eager", "lazy"}
    if not paired and (args.batch_max_requests is not None or args.batch_max_dispatches is not None):
        parser.error("batch reservations require paired modes")
    try:
        endpoint = endpoint_id(args.endpoint_sha256)
    except ValueError:
        parser.error("complete configured endpoint digest is required")
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    run_id = "live-calibration-" + uuid.uuid4().hex
    journal = CaseJournal(root / "cases.jsonl", run_id)
    profile = {"client_model": args.client_model, "upstream_model": args.upstream_model,
               "executor": args.executor, "contract": "openai-chat-v1"}
    results = []
    locked_plan, material_paths, budget = None, None, None
    stop_reason = "interrupted" if paired else "diagnostic-only"
    try:
        cases = [make_case_spec(args.config.resolve(), profile, client, mode, args.gateway_revision, endpoint, args.task_seconds, scenario=scenario)
                 for client in args.clients for scenario in args.scenarios for mode in args.modes]
        if paired:
            material_paths = {"gateway-binary": args.gateway_binary.resolve(), "source-config": args.config.resolve(),
                              "harness-python": Path(sys.executable).resolve()}
            material_paths.update({"client-" + client: Path(shutil.which(client) or client).resolve() for client in args.clients})
            material_paths.update({"harness-" + path.name.replace(".", "-"): path for path in Path(__file__).resolve().parent.iterdir()
                                   if path.is_file() and path.suffix in {".py", ".json", ".txt"}})
            locked_plan = build_plan(run_id, cases, fingerprint_materials(material_paths), args.schedule_seed,
                len(cases) * 8 if args.batch_max_requests is None else args.batch_max_requests,
                len(cases) * 8 if args.batch_max_dispatches is None else args.batch_max_dispatches)
            write_plan(root / "batch-plan.json", locked_plan)
            journal.lock_plan(locked_plan["plan_sha256"])
            budget = BatchBudget(locked_plan)
        while cases:
            declaration = budget.admit_next() if budget is not None else asdict(cases.pop(0))
            if declaration is None:
                stop_reason = budget.stop_reason
                break
            selected = CaseSpec(**declaration)
            scenario = next(name for name, task in SCENARIOS.items() if task.template_id == selected.template_id)
            result = run_case(root, journal, args.gateway_binary.resolve(), args.config.resolve(), profile,
                              selected.scope["client"], selected.mode, args.gateway_revision, endpoint, args.task_seconds,
                              specification=selected, locked_plan=locked_plan, material_paths=material_paths, scenario=scenario)
            results.append(result)
            print(json.dumps(result), flush=True)
            if budget is not None:
                audit_plan(locked_plan, journal.events)
                unknown = any(issue in {"attempt_evidence_import_failed", "no_gateway_attempt_capture"} for issue in result["issues"])
                budget.settle(selected.case_id, requests=None if unknown else result["upstream_attempts"],
                              dispatches=result["boundary_counts"]["dispatch"])
    finally:
        durable_events = read_journal(journal.path)
        replay = summarize(durable_events)
        write_private(root / "replay.json", json.dumps(replay, indent=2))
        if locked_plan is not None:
            write_private(root / "batch-audit.json", json.dumps(audit_plan(locked_plan, durable_events), indent=2))
        write_private(root / "summary.json", json.dumps({"scope": "real-provider-calibration", "gate_status": "unverified",
            "batch_stop_reason": stop_reason, "batch_plan_sha256": None if locked_plan is None else locked_plan["plan_sha256"],
            "cases": results}, indent=2))
    if not results or paired and stop_reason != "complete" or not all(case["safe_task_success"] for case in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
