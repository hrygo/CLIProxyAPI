"""Reproduce official-client eager/lazy coding pairs through an isolated SDK gateway."""
import argparse
from copy import deepcopy
import hashlib
import http.client
import json
import os
from pathlib import Path
import tempfile
import time
from urllib.parse import urlsplit
import uuid
from jsonschema import Draft202012Validator

from clients import ADAPTER_VERSIONS, prepare_isolated_client, write_private
from catalog_fixture import digest
from coding_tasks import CodingProject, TASKS
from codex_code_mode import metadata_admits_mode, observe_code_mode, observe_code_discovery
from code_mode_wire import normalize_code_mode_wire
from evidence import _json, _read_file, read_http_attempts, read_admission_diagnostics
from gateway import candidate_process, validate_profile, verify_gateway_revision, CandidateFailure
from native_wire import declarations, successful_terminal
from normalize_usage import aggregate_usage, normalize_usage
from report import numeric_usage, paired_summary

SCHEMA = {"type": "object", "properties": {"passed": {"type": "boolean"},
          "source_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
          "verification_code": {"type": "string"}}, "required": ["passed", "source_sha256", "verification_code"],
          "additionalProperties": False}
ENV_KEYS = ("PATH", "TMPDIR", "LANG", "LC_ALL", "SYSTEMROOT", "WINDIR")


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def save(path, value):
    """Replace only this run's owned summary; initial artifacts use exclusive creation."""
    tmp = path.with_suffix(path.suffix + ".new")
    write_private(tmp, json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n")
    os.replace(tmp, path)


def _model_metadata_once(ready, client, mode):
    address = urlsplit(ready["base_url"])
    conn = http.client.HTTPConnection(address.hostname, address.port, timeout=5)
    try:
        conn.request("GET", "/v1/models?client_version=" + ADAPTER_VERSIONS["codex"],
                     headers={"Authorization": "Bearer local-acceptance-placeholder"})
        response = conn.getresponse()
        raw = response.read(4 * 1024 * 1024 + 1)
        if response.status != 200 or len(raw) > 4 * 1024 * 1024:
            raise ValueError("catalog_unavailable")
        models = _json(raw).get("models", [])
        selected = [x for x in models if isinstance(x, dict) and x.get("slug") == ready["profile"]["client_model"]]
        if len(selected) != 1:
            raise ValueError("catalog_model_unavailable")
        value = deepcopy(selected[0])
        if client == "codex" and (not metadata_admits_mode(value, "code_mode_only")
                or mode == "lazy" and value.get("supports_search_tool") is not True):
            raise ValueError("catalog_profile_not_admitted")
        return value
    finally:
        conn.close()


def _catalog_work_active(ready):
    address = urlsplit(ready["base_url"])
    conn = http.client.HTTPConnection(address.hostname, address.port, timeout=0.5)
    try:
        conn.request("GET", "/acceptance/catalog-state",
                     headers={"Authorization": "Bearer local-acceptance-placeholder"})
        response = conn.getresponse()
        raw = response.read(8193)
        body = _json(raw)
        if (response.status != 200 or len(raw) > 8192 or not isinstance(body, dict)
                or set(body) != {"pid", "ready_nonce", "active_auxiliary_work"}
                or type(body["pid"]) is not int or body["pid"] != ready["pid"]
                or body["ready_nonce"] != ready["ready_nonce"]
                or type(body["active_auxiliary_work"]) is not int or body["active_auxiliary_work"] < 0):
            raise ValueError("catalog_readiness_unverified")
        return body["active_auxiliary_work"]
    except (OSError, ValueError, http.client.HTTPException):
        raise ValueError("catalog_readiness_unverified") from None
    finally:
        conn.close()


def model_metadata(ready, client, mode, *, deadline=None, observations=None):
    if deadline is None:
        return _model_metadata_once(ready, client, mode)
    observations = observations if observations is not None else []
    started = time.monotonic()
    while True:
        if time.monotonic() >= deadline:
            raise ValueError("catalog_startup_budget_exhausted")
        try:
            metadata = _model_metadata_once(ready, client, mode)
            observations.append({"elapsed_ms": round((time.monotonic()-started)*1000), "available": True})
            return metadata
        except ValueError as error:
            if str(error) != "catalog_model_unavailable" or ready.get("profile", {}).get("executor") != "antigravity":
                raise
            active = _catalog_work_active(ready)
            observations.append({"elapsed_ms": round((time.monotonic()-started)*1000), "available": False,
                                 "active_auxiliary_work": active})
            if active == 0:
                # Discovery can publish between the catalog and state reads.
                metadata = _model_metadata_once(ready, client, mode)
                observations.append({"elapsed_ms": round((time.monotonic()-started)*1000), "available": True})
                return metadata
            remaining = deadline-time.monotonic()
            if remaining <= 0:
                raise ValueError("catalog_startup_budget_exhausted") from None
            time.sleep(min(0.05, remaining))

def client_result(prepared):
    raw = Path(prepared.run_dir / "stdout.jsonl").read_bytes()
    if not raw or not raw.endswith(b"\n"):
        return False, None
    events = [_json(x) for x in raw.splitlines()]
    if not all(isinstance(x, dict) for x in events):
        return False, None
    last = events[-1]
    if prepared.client_id == "codex":
        texts = [x["item"].get("text", "") for x in events if x.get("type") == "item.completed"
                 and isinstance(x.get("item"), dict) and x["item"].get("type") == "agent_message"]
        value = _json(texts[-1]) if texts else None
        return last.get("type") == "turn.completed", value
    value = last.get("structured_output")
    calls = [block for event in events if event.get("type") == "assistant"
             and isinstance(event.get("message"), dict) for block in event["message"].get("content", [])
             if isinstance(block, dict) and block.get("type") == "tool_use" and block.get("name") == "StructuredOutput"]
    legal = (last.get("type") == "result" and last.get("subtype") == "success" and last.get("is_error") is False
             and sum(x.get("type") == "result" for x in events) == 1
             and not any(x.get("type") == "assistant" and x.get("error") for x in events))
    identifiers = [x.get("id") for x in calls]
    bound = (bool(calls) and all(isinstance(x, str) and x for x in identifiers)
             and len(set(identifiers)) == len(identifiers) and calls[-1].get("input") == value)
    return bool(legal and bound), value


def wire_observations(capture, requests, client, mode, task):
    bodies, rows = [], []
    for request in requests:
        request_id = request["request_id"]
        if request["contract"] == "antigravity-auxiliary-v1":
            continue
        body = _json(_read_file(capture, request_id + ".body", 64 * 1024 * 1024))
        bodies.append(body)
        tools = declarations(request["contract"], body)
        names = [(x.get("function") or x).get("name", "") for x in tools]
        rows.append({"request_id": request_id, "fixture_declarations": sum("mcp__fixture" in x for x in names)})
    if not bodies:
        return False, rows, None
    if client == "codex":
        contract = next(x["contract"] for x in requests if x["contract"] != "antigravity-auxiliary-v1")
        tools = declarations(contract, bodies[0])
        observed = observe_code_mode(tools, mode, coding_task=task, wire_contract=contract,
                                     client_denied=["fixture_mutation"], service_denied=["fixture_mutation"])
        view = normalize_code_mode_wire(bodies, contract)
        observed["wire_history_integrity"] = view["integrity"]
        discovery = observe_code_discovery(view["bodies"], coding_task=task) if mode == "lazy" else None
        required = {"coding_task_read", "coding_source_replace", "coding_tests_run"}
        loaded = bool(discovery and discovery["discovery_result_observed"]
                      and discovery["history_identity_consistent"]
                      and not required.intersection(discovery["invocations_before_schema"])
                      and required <= set(discovery["loaded_fixture_schema_tools"])
                      and required <= set(discovery["observed_fixture_invocations"]))
        effective = observed["effective"] and view["integrity"] and (mode == "eager" or loaded)
        return effective, rows, {"mode": observed, "discovery": discovery}
    counts = [row["fixture_declarations"] for row in rows]
    effective = bool(counts[0] >= 70 if mode == "eager" else counts[0] == 0 and any(x >= 3 for x in counts[1:]))
    return effective, rows, None


def run_case(root, binary, config, route, mode, manifest, max_attempts):
    client, task, profile = route["client"], route["task"], route["profile"]
    result = {"client": client, "model": profile["client_model"], "task": task, "mode": mode,
              "status": "started", "safe_task_success": False, "effective_mode": False,
              "usage": None, "physical_attempts": None, "per_request": [], "checks": {},
              "failure_class": "unknown", "issues": []}
    root.mkdir(mode=0o700)
    started = time.monotonic()
    deadline = started + manifest["seconds"]
    prepared = raw_result = None
    nonce = uuid.uuid4().hex
    capture = root / "gateway-state/capture"
    try:
        with candidate_process(binary, config, root, profile, max_attempts=max_attempts) as (ready, capture):
            result["catalog_readiness"] = []
            metadata = model_metadata(ready, client, mode, deadline=deadline, observations=result["catalog_readiness"])
            args = {"run_dir": root / "client", "client_version": ADAPTER_VERSIONS[client],
                    "executable": manifest[client], "model": profile["client_model"], "base_url": ready["base_url"],
                    "fixture_python": manifest["fixture_python"], "model_metadata": metadata, "nonce": nonce,
                    "max_dispatches": 16, "max_result_bytes": 65536, "coding_task": task, "output_schema": SCHEMA}
            if client == "codex":
                args["codex_tool_mode"] = "code_mode_only"
            prepared = prepare_isolated_client(client, mode, args,
                       {"client_denied": ["fixture_mutation"], "service_denied": ["fixture_mutation"]})
            prompt = ("Discover the fixture tools for reading this coding task, replacing its source, and running its tests. "
                      "Read requirements via coding_task_read, repair module.py via coding_source_replace, then call "
                      "coding_tests_run. Follow the returned bounded Python language contract. Only use fixture tools, "
                      "never shell, filesystem, network, images or unrelated tools. Your final JSON must contain exactly "
                      "passed, source_sha256 and verification_code copied from the successful actual test result. "
                      "Do not claim success without those tool results.")
            if route.get("notify") is True and client == "codex":
                prompt += (" In each exec invocation performing fixture read, replace or tests, call notify('PROGRESS') "
                           "before the fixture call and text the real result after it in the same invocation.")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ValueError("catalog_startup_budget_exhausted")
            raw_result = prepared.run_task({"prompt": prompt, "max_seconds": remaining, "max_output_bytes": 2**20})
            result["process"] = {key: raw_result[key] for key in
                                 ("exit_code", "termination", "output_truncated", "forced_process_cleanup")}
            result["catalog_sha256"] = prepared.catalog_hash
        requests = read_http_attempts(capture)
        admission = read_admission_diagnostics(capture, max_attempts=max_attempts, request_count=len(requests))
        if admission is None:
            raise ValueError("admission_unverified")
        expected_model = hashlib.sha256(profile["upstream_model"].encode()).hexdigest()
        ready = _json((root / "gateway.ready.json").read_bytes())
        for request in requests:
            identity = request.get("executor_identity") or {}
            if (identity.get("executor") != profile["executor"]
                    or identity.get("auth_sha256") not in ready["selected_auth_ids"]
                    or request["contract"] != "antigravity-auxiliary-v1" and identity.get("model_sha256") != expected_model):
                raise ValueError("actual_route_identity_mismatch")
        result["physical_attempts"] = len(requests)
        result["usage"] = aggregate_usage(requests)
        result["usage"].pop("requests", None)
        for request in requests:
            result["per_request"].append({"request_id": request["request_id"], "contract": request["contract"],
                "http_status_code": request["http_status_code"], "finished": request["finished"],
                "transport_outcome": request["transport_outcome"], "usage_evidence": request["usage_evidence"],
                "issues": request["issues"], "raw_usage_numeric": numeric_usage(request["raw_usage"]),
                "tokens": normalize_usage(request["contract"], request["raw_usage"])["tokens"]})
        effective, wire, discovery = wire_observations(capture, requests, client, mode, task)
        result.update(effective_mode=effective, wire=wire, code_mode=discovery)
        events = prepared.collect_boundary_events()
        completed = [x for x in events if x["phase"] == "completed"]
        last_event = events[-1] if events else None
        score = CodingProject(prepared.run_dir / "workspace", task).score()
        result["code_outcome"] = score
        legal, answer = client_result(prepared)
        tests = [x for x in completed if x["tool"] == "coding_tests_run"
                 and x.get("source_sha256") == score["source_sha256"] and x.get("tests_passed") is True]
        expected = {"passed": True, "source_sha256": score["source_sha256"], "verification_code": nonce}
        inference = [x for x in requests if x["contract"] != "antigravity-auxiliary-v1"]
        def completed_response(request):
            return successful_terminal(request["contract"],
                _read_file(capture, "response-" + request["request_id"][8:] + ".body", 64 * 1024 * 1024),
                transport_complete=request["transport_capture_complete"])
        terminal = bool(inference and inference[-1]["finished"]
                        and 200 <= inference[-1]["http_status_code"] < 300 and completed_response(inference[-1])
                        and all(x["finished"] and (400 <= x["http_status_code"] < 600
                                or 200 <= x["http_status_code"] < 300 and completed_response(x)) for x in inference))
        forbidden = [x for x in events if x["phase"] == "dispatch" and x["tool"] == "fixture_mutation"]
        result["boundary"] = {"mcp_dispatches": sum(x["phase"] == "dispatch" for x in events),
                              "forbidden_dispatches": len(forbidden),
                              "fixture_effects": last_event["effect_count"] if last_event else None,
                              "active": last_event["active_count"] if last_event else None}
        result["checks"] = {"process": raw_result["exit_code"] == 0 and raw_result["termination"] == "exited"
                             and raw_result["output_truncated"] is False,
                            "client_terminal": legal, "upstream_terminal": bool(requests) and terminal,
                            "mode": effective, "code": score["passed"] and score["changed"] and bool(tests),
                            "result_replay": Draft202012Validator(SCHEMA).is_valid(answer) and answer == expected,
                            "required_tools": {"coding_task_read", "coding_source_replace", "coding_tests_run"} <= {x["tool"] for x in completed},
                            "boundary": not forbidden and bool(last_event) and last_event["active_count"] == 0 and last_event["effect_count"] == 0}
        result["safe_task_success"] = all(result["checks"].values())
        statuses = [x["http_status_code"] for x in requests]
        result["failure_class"] = ("none" if result["safe_task_success"] else "provider_quota" if 429 in statuses
            else "budget" if raw_result["termination"].endswith("budget")
            else "protocol_or_client" if not legal or not terminal else "model_or_mode")
    except CandidateFailure as error:
        result["issues"] = error.diagnostics
        result["failure_class"] = "configuration_or_credentials"
        # These typed phases return before SDK startup and before any recorder.
        if not (root / "gateway-state").exists() and all(x["stage"] in {"arguments", "configuration", "credentials"}
                                                        for x in error.diagnostics):
            result["physical_attempts"] = 0
    except (ValueError, OSError, KeyError) as error:
        result["issues"] = [{"reason": str(error) if isinstance(error, ValueError) and str(error).startswith("catalog_")
                              else type(error).__name__}]
        result["failure_class"] = ("budget" if result["issues"][0]["reason"] == "catalog_startup_budget_exhausted"
                                   else "client_profile" if result["issues"][0]["reason"].startswith("catalog_") else "evidence_or_client")
        # Preserve every attempt even when client startup or result parsing fails.
        if capture.exists():
            try:
                attempts = read_http_attempts(capture)
                result["physical_attempts"] = len(attempts)
                result["usage"] = aggregate_usage(attempts)
                result["usage"].pop("requests", None)
            except (ValueError, OSError):
                pass
    finally:
        if not result["per_request"] and capture.exists():
            try:
                attempts = read_http_attempts(capture)
                admission = read_admission_diagnostics(capture, max_attempts=max_attempts, request_count=len(attempts))
                if admission is not None:
                    result["physical_attempts"] = len(attempts)
                    result["usage"] = aggregate_usage(attempts)
                    result["usage"].pop("requests", None)
                    result["per_request"] = [{"request_id": x["request_id"], "contract": x["contract"],
                        "http_status_code": x["http_status_code"], "finished": x["finished"],
                        "transport_outcome": x["transport_outcome"], "usage_evidence": x["usage_evidence"],
                        "issues": x["issues"], "raw_usage_numeric": numeric_usage(x["raw_usage"]),
                        "tokens": normalize_usage(x["contract"], x["raw_usage"])["tokens"]} for x in attempts]
            except (ValueError, OSError):
                pass
        result["status"] = "finished"
        result["elapsed_ms"] = round((time.monotonic() - started) * 1000)
        save(root / "result.json", result)
    return result


def validate_manifest(value):
    required = {"version", "gateway_revision", "config", "codex", "claude", "fixture_python", "seconds",
                "max_attempts", "max_physical_attempts", "routes"}
    if not isinstance(value, dict) or set(value) != required or type(value["version"]) is not int or value["version"] != 1:
        raise ValueError("invalid manifest keys or version")
    if type(value["seconds"]) is not int or not 1 <= value["seconds"] <= 180:
        raise ValueError("invalid case time budget")
    if type(value["max_attempts"]) is not int or not 1 <= value["max_attempts"] <= 32:
        raise ValueError("invalid per-case attempt budget")
    if type(value["max_physical_attempts"]) is not int or not 1 <= value["max_physical_attempts"] <= 256:
        raise ValueError("invalid run attempt budget")
    routes = value["routes"]
    if not isinstance(routes, list) or not 1 <= len(routes) <= 12:
        raise ValueError("invalid pair count")
    seen = set()
    for route in routes:
        if (not isinstance(route, dict) or set(route) - {"notify"} != {"client", "task", "profile", "modes"}
                or route["client"] not in ADAPTER_VERSIONS or route["task"] not in TASKS
                or route["modes"] not in (["eager", "lazy"], ["lazy", "eager"])
                or "notify" in route and type(route["notify"]) is not bool):
            raise ValueError("invalid explicit pair")
        validate_profile(route["profile"])
        key = (route["client"], route["task"], route["profile"]["client_model"])
        if key in seen:
            raise ValueError("duplicate pair")
        seen.add(key)
    for key in ("config", "codex", "claude", "fixture_python"):
        if not Path(value[key]).is_absolute() or not Path(value[key]).is_file():
            raise ValueError("manifest paths must identify existing absolute files")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, help="New directory outside the source repository; defaults to system tmp")
    args = parser.parse_args()
    manifest = _json(args.manifest.read_bytes())
    validate_manifest(manifest)
    repo = Path(__file__).resolve().parents[2]
    output = args.output.absolute() if args.output else Path(tempfile.mkdtemp(prefix="cliproxyapi-performance-"))
    if output.resolve().is_relative_to(repo):
        parser.error("run artifacts must stay outside the source repository")
    if args.output:
        output.mkdir(mode=0o700, parents=True, exist_ok=False)
    revision = manifest["gateway_revision"]
    binary_sha = verify_gateway_revision(args.binary, revision)
    materials = {key: sha(manifest[key]) for key in ("config", "codex", "claude", "fixture_python")}
    materials["binary"] = binary_sha
    write_private(output / "frozen-manifest.json", json.dumps(manifest, indent=2))
    write_private(output / "materials.json", json.dumps(materials, indent=2))
    cases, spent, stop = [], 0, None
    with (output / "cases.jsonl").open("x") as journal:
        os.chmod(journal.name, 0o600)
        try:
            for route in manifest["routes"]:
                for mode in route["modes"]:
                    remaining = manifest["max_physical_attempts"] - spent
                    if remaining <= 0:
                        stop = "run_attempt_budget"
                        break
                    if any(sha(manifest[key]) != expected for key, expected in materials.items() if key != "binary") or sha(args.binary) != binary_sha:
                        stop = "material_drift"
                        break
                    spec = {"client": route["client"], "model": route["profile"]["client_model"],
                            "task": route["task"], "mode": mode}
                    journal.write(json.dumps({"phase": "case_started", **spec}) + "\n")
                    journal.flush()
                    case_root = output / f"case-{len(cases)+1:02d}"
                    cases.append({**spec, "status": "started", "safe_task_success": False,
                                  "effective_mode": False, "usage": None, "physical_attempts": None})
                    result = run_case(case_root, args.binary, Path(manifest["config"]), route, mode, manifest,
                                      min(manifest["max_attempts"], remaining))
                    cases[-1] = result
                    journal.write(json.dumps({"phase": "case_finished", **result}) + "\n")
                    journal.flush()
                    save(output / "report.json", {"gateway_revision": revision, "cases": cases, **paired_summary(cases)})
                    print(json.dumps({**spec, "safe_task_success": result["safe_task_success"],
                                      "failure_class": result["failure_class"], "physical_attempts": result["physical_attempts"]}), flush=True)
                    if result["physical_attempts"] is None:
                        stop = "accounting_unknown"
                        break
                    spent += result["physical_attempts"]
                    boundary = result.get("boundary") or {}
                    if boundary.get("forbidden_dispatches", 0) or boundary.get("fixture_effects", 0) or boundary.get("active", 0):
                        stop = "execution_boundary"
                        break
                if stop:
                    break
        except BaseException:
            stop = "interrupted_or_incomplete_case"
            raise
        finally:
            preservation = {key: sha(manifest[key]) == expected for key, expected in materials.items() if key != "binary"}
            preservation["binary"] = sha(args.binary) == binary_sha
            save(output / "report.json", {"gateway_revision": revision, "planned": len(manifest["routes"]) * 2,
                 "cases": cases, "known_physical_attempts": spent,
                 "physical_attempts": spent if all(x.get("physical_attempts") is not None for x in cases) else None,
                 "stop_reason": stop, "materials_unchanged": preservation,
                 **paired_summary(cases)})
    print(json.dumps({"output": str(output), "started": len(cases), "stop_reason": stop}), flush=True)
    return 0 if not stop and len(cases) == len(manifest["routes"]) * 2 and all(x["safe_task_success"] for x in cases) else 1


if __name__ == "__main__":
    raise SystemExit(main())
