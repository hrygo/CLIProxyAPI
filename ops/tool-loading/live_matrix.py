"""Run every predeclared route on one revision with one budget and CaseJournal.

This matrix records bounded live evidence, not a statistical certificate. Native
credentials are selected only by explicit filename/content digest bindings.
"""

from copy import deepcopy
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import uuid

from batch_plan import BatchBudget, audit_plan, build_plan, fingerprint_materials, write_plan
from client_adapters import ADAPTER_VERSIONS, validate_session_title, write_private
from http_attempts import _json
from live_clients import endpoint_id, make_case_spec, run_case
from live_gateway import validate_profile
from native_wire import wire_contract
from run_acceptance import CaseJournal, CaseSpec, read_journal, summarize
from task_scenarios import SCENARIOS


def validate_declaration(value):
    fields = {"schema_version", "units", "task_seconds", "max_requests", "max_dispatches", "schedule_seed"}
    if (not isinstance(value, dict) or set(value) != fields or type(value["schema_version"]) is not int
            or value["schema_version"] != 1 or type(value["task_seconds"]) is not int
            or not 10 <= value["task_seconds"] <= 300 or not isinstance(value["units"], list)
            or not 1 <= len(value["units"]) <= 32 or not isinstance(value["schedule_seed"], str)
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}", value["schedule_seed"])
            or any(type(value[key]) is not int or not 1 <= value[key] <= 2**31 - 1
                   for key in ("max_requests", "max_dispatches"))):
        raise ValueError("invalid matrix declaration")
    for unit in value["units"]:
        if (not isinstance(unit, dict) or set(unit) != {"profile", "endpoint_sha256", "client", "structured_output", "opencode_title", "scenarios"}
                or not isinstance(unit["client"], str) or unit["client"] not in ADAPTER_VERSIONS
                or type(unit["structured_output"]) is not bool or not isinstance(unit["scenarios"], list)
                or not unit["scenarios"] or any(not isinstance(name, str) or name not in SCENARIOS for name in unit["scenarios"])
                or len(set(unit["scenarios"])) != len(unit["scenarios"])):
            raise ValueError("invalid matrix unit")
        validate_profile(unit["profile"])
        if unit["profile"]["contract"] != wire_contract(unit["profile"]["executor"]):
            raise ValueError("matrix wire contract does not match its executor")
        endpoint_id(unit["endpoint_sha256"])
        if unit["opencode_title"] is not None:
            if unit["client"] != "opencode":
                raise ValueError("matrix session title requires OpenCode")
            validate_session_title(unit["opencode_title"])


def read_declaration(path):
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or not 0 < before.st_size <= 1048576:
            raise ValueError("bounded regular matrix declaration required")
        raw = stream.read(1048577)
        after = os.fstat(stream.fileno())
    identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if (len(raw) != before.st_size or identity(before) != identity(after)
            or identity(after) != identity(os.stat(path, follow_symlinks=False))):
        raise ValueError("matrix declaration changed while reading")
    result = _json(raw)
    validate_declaration(result)
    return result


def compile_cases(declaration, config, revision):
    validate_declaration(declaration)
    contexts, cases = {}, []
    for unit in declaration["units"]:
        unit = deepcopy(unit)
        endpoint = endpoint_id(unit["endpoint_sha256"])
        for scenario in unit["scenarios"]:
            for mode in ("eager", "lazy"):
                spec = make_case_spec(config, unit["profile"], unit["client"], mode, revision, endpoint,
                    declaration["task_seconds"], scenario=scenario, opencode_title=unit["opencode_title"],
                    structured_output=unit["structured_output"])
                if spec.case_id in contexts:
                    raise ValueError("duplicate matrix case identity")
                cases.append(spec)
                contexts[spec.case_id] = {**unit, "scenario": scenario, "endpoint_id": endpoint}
    return cases, contexts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("declaration", "gateway-binary", "config", "output-dir"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--gateway-revision", required=True)
    args = parser.parse_args()
    try:
        declaration = read_declaration(args.declaration)
        cases, contexts = compile_cases(declaration, args.config.resolve(), args.gateway_revision)
        material_paths = {"gateway-binary": args.gateway_binary.resolve(), "source-config": args.config.resolve(),
                          "harness-python": Path(sys.executable).resolve(), "matrix-declaration": args.declaration}
        clients = {unit["client"] for unit in declaration["units"]}
        material_paths.update({"client-" + client: Path(shutil.which(client) or client).resolve() for client in clients})
        material_paths.update({"harness-" + path.name.replace(".", "-"): path for path in Path(__file__).resolve().parent.iterdir()
                               if path.is_file() and path.suffix in {".py", ".json", ".txt"}})
        run_id = "live-matrix-" + uuid.uuid4().hex
        plan = build_plan(run_id, cases, fingerprint_materials(material_paths), declaration["schedule_seed"],
                          declaration["max_requests"], declaration["max_dispatches"])
    except (OSError, ValueError):
        parser.error("matrix declaration or material preflight failed")
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    journal = CaseJournal(root / "cases.jsonl", run_id)
    results, stop_reason = [], "interrupted"
    write_plan(root / "batch-plan.json", plan)
    journal.lock_plan(plan["plan_sha256"])
    budget = BatchBudget(plan)
    try:
        while True:
            selected = budget.admit_next()
            if selected is None:
                stop_reason = budget.stop_reason
                break
            spec = CaseSpec(**selected)
            context = contexts[spec.case_id]
            result = run_case(root, journal, args.gateway_binary.resolve(), args.config.resolve(), context["profile"],
                context["client"], spec.mode, args.gateway_revision, context["endpoint_id"], declaration["task_seconds"],
                specification=spec, locked_plan=plan, material_paths=material_paths, scenario=context["scenario"],
                opencode_title=context["opencode_title"], structured_output=context["structured_output"])
            results.append(result)
            print(json.dumps(result), flush=True)
            audit_plan(plan, journal.events)
            unknown = any(issue in {"attempt_evidence_import_failed", "no_gateway_attempt_capture"} for issue in result["issues"])
            budget.settle(spec.case_id, requests=None if unknown else result["upstream_attempts"],
                          dispatches=result["boundary_counts"]["dispatch"])
    finally:
        events = read_journal(journal.path)
        write_private(root / "replay.json", json.dumps(summarize(events), indent=2))
        write_private(root / "batch-audit.json", json.dumps(audit_plan(plan, events), indent=2))
        write_private(root / "summary.json", json.dumps({"scope": "predeclared-cross-provider-matrix",
            "gate_status": "unverified", "batch_plan_sha256": plan["plan_sha256"],
            "batch_stop_reason": stop_reason, "cases": results}, indent=2))
    if not results or stop_reason != "complete" or not all(case["safe_task_success"] for case in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
