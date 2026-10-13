"""Pre-task registry for conditional route analysis with exact pair evidence.

The registry digest belongs in batch materials before the durable task lock.
Registration does not prove representative sampling or confer certification.
"""

from copy import deepcopy
from dataclasses import asdict
import argparse
import json
import math
import os
from pathlib import Path
import stat
from statistics import mean

from analyze_pairs import _benefits, analyze_pairs, binomial_upper
from batch_plan import HASH, _digest, _pairs, _unique_fields, _invalid_constant, audit_plan, read_plan, validate_plan
from run_acceptance import METRICS, _identifier, read_journal, summarize

TASK_SCOPE_FIELDS = {"fixture_hash", "permission_hash", "catalog_hash"}
ASSIGNMENT_FIELDS = {"pair_id", "route_id", "task_id", "family_id", "cluster_id", "repeat_id"}
THRESHOLD_FIELDS = {"quality_margin", "first_input_reduction", "peak_input_reduction", "total_input_reduction"}


def build_registry(experiment_id, cases, assignments, task_frame_sha256, seed, thresholds):
    if (not _identifier(experiment_id) or not _identifier(seed)
            or not isinstance(task_frame_sha256, str) or not HASH.fullmatch(task_frame_sha256)):
        raise ValueError("explicit experiment, schedule and task frame required")
    if (not isinstance(thresholds, dict) or set(thresholds) != THRESHOLD_FIELDS
            or any(type(value) not in (int, float) or not math.isfinite(value) or not 0 < value < 1
                   for value in thresholds.values())):
        raise ValueError("explicit finite acceptance thresholds required")
    declarations = [asdict(case) if hasattr(case, "__dataclass_fields__") else deepcopy(case) for case in cases]
    if not 1 <= len(declarations) <= 10000:
        raise ValueError("bounded case set required")
    _pairs(declarations)
    pairs = {}
    for case in declarations:
        pairs.setdefault(case["pair_id"], {})[case["mode"]] = case
    if not isinstance(assignments, list) or len(assignments) != len(pairs):
        raise ValueError("every planned pair must be registered")
    routes, members, seen, tasks, fixture_clusters, repeats, identity_routes = {}, [], set(), {}, {}, set(), {}
    cluster_families = {}
    for assignment in assignments:
        if (not isinstance(assignment, dict) or set(assignment) != ASSIGNMENT_FIELDS
                or any(not _identifier(value) for value in assignment.values())):
            raise ValueError("invalid task assignment")
        pair_id, route_id = assignment["pair_id"], assignment["route_id"]
        if pair_id not in pairs or pair_id in seen:
            raise ValueError("duplicate or unregistered pair")
        seen.add(pair_id)
        pair = pairs[pair_id]
        eager = pair["eager"]
        identity = {"scope": {key: value for key, value in eager["scope"].items()
                              if key not in TASK_SCOPE_FIELDS | {"client_mode"}},
                    "client_modes": {mode: case["scope"]["client_mode"] for mode, case in pair.items()},
                    "cache_condition": eager["cache_condition"]}
        if route_id in routes and routes[route_id] != identity:
            raise ValueError("registered route mixes fixed execution identities")
        identity_sha = _digest(identity)
        if identity_sha in identity_routes and identity_routes[identity_sha] != route_id:
            raise ValueError("one fixed route cannot be split into selected routes")
        identity_routes[identity_sha] = route_id
        routes[route_id] = identity
        task_key = (route_id, assignment["task_id"])
        task_identity = {"scope": {field: eager["scope"][field] for field in TASK_SCOPE_FIELDS},
                         "template_id": eager["template_id"], "gold": eager["gold"],
                         "family_id": assignment["family_id"], "cluster_id": assignment["cluster_id"]}
        if task_key in tasks and tasks[task_key] != task_identity:
            raise ValueError("task identity or assigned cluster changed between repeats")
        tasks[task_key] = task_identity
        fixture_key = (route_id, eager["scope"]["fixture_hash"])
        cluster = (assignment["family_id"], assignment["cluster_id"])
        if fixture_key in fixture_clusters and fixture_clusters[fixture_key] != cluster:
            raise ValueError("one fixture cannot manufacture independent clusters")
        fixture_clusters[fixture_key] = cluster
        cluster_key = (route_id, assignment["cluster_id"])
        if cluster_key in cluster_families and cluster_families[cluster_key] != assignment["family_id"]:
            raise ValueError("one cluster must belong to one task family")
        cluster_families[cluster_key] = assignment["family_id"]
        repeat_key = (route_id, assignment["task_id"], assignment["repeat_id"])
        if repeat_key in repeats:
            raise ValueError("duplicate task repeat")
        repeats.add(repeat_key)
        members.append({**deepcopy(assignment),
                        "case_sha256": {mode: _digest(case) for mode, case in pair.items()}})
    payload = {"schema_version": 1, "experiment_id": experiment_id,
               "schedule_seed": seed, "task_frame_sha256": task_frame_sha256,
               "case_set_sha256": _digest(sorted(declarations, key=lambda case: case["case_id"])),
               "thresholds": deepcopy(thresholds), "routes": dict(sorted(routes.items())),
               "members": sorted(members, key=lambda member: member["pair_id"]),
               "gate_status": "unverified"}
    return {**payload, "registry_sha256": _digest(payload)}


def validate_registry(registry, plan):
    validate_plan(plan)
    fields = {"schema_version", "experiment_id", "schedule_seed", "task_frame_sha256",
              "case_set_sha256", "thresholds", "routes", "members", "gate_status", "registry_sha256"}
    if (not isinstance(registry, dict) or set(registry) != fields or not isinstance(registry["members"], list)
            or type(registry["schema_version"]) is not int):
        raise ValueError("invalid task registry contract")
    if any(not isinstance(member, dict) or set(member) != ASSIGNMENT_FIELDS | {"case_sha256"}
           for member in registry["members"]):
        raise ValueError("invalid registered pair contract")
    assignments = [{key: member[key] for key in ASSIGNMENT_FIELDS} for member in registry["members"]]
    rebuilt = build_registry(registry["experiment_id"], plan["cases"], assignments,
                             registry["task_frame_sha256"], registry["schedule_seed"], registry["thresholds"])
    if (registry != rebuilt or plan["schedule_seed"] != registry["schedule_seed"]
            or plan["materials"].get("task-registry") != _digest(registry)
            or plan["materials"].get("task-frame") != registry["task_frame_sha256"]):
        raise ValueError("registry differs from frozen cases, schedule or pre-task material binding")


def write_registry(path, registry):
    """Canonical bytes let fingerprint_materials bind the complete registry."""
    content = json.dumps(registry, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(content)
        stream.flush()
        os.fsync(stream.fileno())


def read_registry(path):
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_size > 64 * 1024**2:
            raise ValueError("invalid registry file")
        content = stream.read(64 * 1024**2 + 1)
        after = os.fstat(stream.fileno())
    identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if (len(content) != before.st_size or identity(before) != identity(after)
            or identity(after) != identity(os.stat(path, follow_symlinks=False))):
        raise ValueError("registry changed during read")
    result = json.loads(content, object_pairs_hook=_unique_fields, parse_constant=_invalid_constant)
    if content != json.dumps(result, sort_keys=True, separators=(",", ":"), allow_nan=False).encode():
        raise ValueError("registry must use canonical frozen bytes")
    return result


def analyze_registered(events, plan, registry):
    validate_registry(registry, plan)
    audit = audit_plan(plan, events)
    report = summarize(events)
    exact = analyze_pairs(events)
    pair_rows = {row["pair_id"]: row for group in exact["groups"] for row in group["pairs"]}
    started = {case["spec"]["case_id"]: case for case in report["cases"]}
    by_pair = {}
    for case in plan["cases"]:
        by_pair.setdefault(case["pair_id"], {})[case["mode"]] = case
    results = []
    for route_id, identity in registry["routes"].items():
        members = [member for member in registry["members"] if member["route_id"] == route_id]
        rows, route_cases = [], []
        for member in members:
            declared = by_pair[member["pair_id"]]
            row = deepcopy(pair_rows.get(member["pair_id"], {
                "pair_id": member["pair_id"], "template_id": declared["eager"]["template_id"],
                "matched": False, "reason": "planned_not_started", "difference": None,
                "regression": None, "savings": dict.fromkeys(METRICS)}))
            row.update(task_id=member["task_id"], family_id=member["family_id"],
                       cluster_id=member["cluster_id"], repeat_id=member["repeat_id"],
                       case_sha256=deepcopy(member["case_sha256"]))
            rows.append(row)
            route_cases.extend(started[case["case_id"]] for case in declared.values() if case["case_id"] in started)
        clusters = {}
        for row in rows:
            clusters.setdefault(row["cluster_id"], []).append(row)
        complete = bool(rows) and all(row["matched"] for row in rows)
        regressions = sum(any(row["regression"] for row in cluster) for cluster in clusters.values())
        quality = {"regression_clusters": regressions, "cluster_mean_difference": None,
                   "conditional_lower_95": None, "reason": "missing_or_unmatched_pair"}
        if complete:
            quality.update(cluster_mean_difference=mean(mean(row["difference"] for row in cluster) for cluster in clusters.values()),
                           conditional_lower_95=-binomial_upper(regressions, len(clusters)),
                           reason="conditional_iid_registered_clusters")
        # _benefits groups on template_id; only its private analysis input is
        # relabeled. Exact templates and hashes remain intact in exported rows.
        cluster_rows = [{**row, "template_id": row["cluster_id"]} for row in rows]
        benefits = {metric: _benefits(cluster_rows, metric) for metric in METRICS}
        reasons = ["sampling_frame_representativeness_unverified", "billing_not_evaluated"]
        if "unknown" in identity["scope"].values() or any(row["reason"] == "unknown_scope" for row in rows):
            reasons.append("unknown_scope")
        if not complete:
            reasons.append("missing_or_unmatched_pair")
        results.append({"route_id": route_id, **deepcopy(identity), "planned_cases": 2 * len(rows),
                        "started_cases": len(route_cases), "planned_not_started": 2 * len(rows) - len(route_cases),
                        "unfinished_cases": sum(not case["finished"] for case in route_cases),
                        "registered_clusters": len(clusters),
                        "started_by_mode": {mode: sum(case["spec"]["mode"] == mode for case in route_cases) for mode in ("eager", "lazy")},
                        "success_by_mode": {mode: sum(case["safe_task_success"] for case in route_cases if case["spec"]["mode"] == mode) for mode in ("eager", "lazy")},
                        "journaled_request_attempts": sum(case["usage"]["request_count"] for case in route_cases),
                        "pairs": rows, "quality": quality, "benefits": benefits,
                        "unverified_reasons": reasons, "gate_status": "unverified"})
    return {"schema_version": 1, "experiment_id": registry["experiment_id"], "run_id": report["run_id"],
            "registry_sha256": registry["registry_sha256"], "case_set_sha256": registry["case_set_sha256"],
            "task_frame_sha256": registry["task_frame_sha256"], "thresholds": deepcopy(registry["thresholds"]),
            "confidence_level": .95, "started_cases": report["started_cases"],
            "safe_successes": report["safe_successes"], "planned_not_started": audit["planned_not_started"],
            "failure_counts": report["failure_counts"], "batch_audit": audit, "routes": results,
            "conditional_assumptions": exact["conditional_assumptions"],
            "quality_estimand": "equal-registered-cluster mean of within-cluster paired success differences",
            "benefit_estimand": "median across registered-cluster medians of paired input reductions",
            "gate_status": "unverified"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--journal", required=True, type=Path)
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--registry", required=True, type=Path)
    args = parser.parse_args()
    try:
        result = analyze_registered(read_journal(args.journal), read_plan(args.plan), read_registry(args.registry))
    except (OSError, ValueError, KeyError, TypeError):
        print(json.dumps({"error": "invalid_evidence", "gate_status": "unverified"}))
        return 2
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
