"""Read-only paired diagnostics; conditional bounds are not acceptance certificates."""

from __future__ import annotations

import argparse
from copy import deepcopy
import json
import math
from pathlib import Path
from statistics import mean, median

from batch_plan import audit_plan, read_plan
from run_acceptance import METRICS, read_journal, summarize


def _alpha(value: float) -> None:
    if type(value) not in (int, float) or not math.isfinite(value) or not 0 < value < 1:
        raise ValueError("alpha must be finite and between zero and one")


def _binomial_cdf(k: int, n: int, p: float) -> float:
    if k < 0:
        return 0.0
    if k >= n or p == 0:
        return 1.0
    if p == 1:
        return 0.0
    terms = [math.lgamma(n + 1) - math.lgamma(i + 1) - math.lgamma(n - i + 1)
             + i * math.log(p) + (n - i) * math.log1p(-p) for i in range(k + 1)]
    maximum = max(terms)
    return min(1.0, math.exp(maximum) * math.fsum(math.exp(term - maximum) for term in terms))


def binomial_upper(failures: int, count: int, alpha: float = .05) -> float:
    """One-sided exact upper bound, inverted binomial CDF (Clopper-Pearson)."""
    _alpha(alpha)
    if type(count) is not int or type(failures) is not int or not 0 <= failures <= count or count < 1:
        raise ValueError("positive integer count and valid integer failures required")
    if failures == count:
        return 1.0
    if failures == 0:
        return -math.expm1(math.log(alpha) / count)
    left, right = 0.0, 1.0
    for _ in range(80):
        middle = (left + right) / 2
        if _binomial_cdf(failures, count, middle) > alpha:
            left = middle
        else:
            right = middle
    return right


def median_lower(values: list[float], alpha: float = .05) -> float | None:
    """Order-statistic lower bound under independent identically sampled clusters.

    Pick the largest rank j with P(Binomial(n, .5) <= j-1) <= alpha.
    With fewer than five clusters there is no finite one-sided 95% bound.
    """
    _alpha(alpha)
    if any(type(value) not in (int, float) or not math.isfinite(value) for value in values):
        raise ValueError("benefits must be finite numeric values")
    if not values:
        return None
    left, right = 0, len(values)
    while left < right:
        middle = (left + right + 1) // 2
        if _binomial_cdf(middle - 1, len(values), .5) <= alpha:
            left = middle
        else:
            right = middle - 1
    return sorted(values)[left - 1] if left else None


def _identity(case: dict) -> dict:
    spec = case["spec"]
    return {"scope": {key: value for key, value in spec["scope"].items() if key != "client_mode"},
            "cache_condition": spec["cache_condition"], "limits": spec["limits"]}


def _benefits(rows: list[dict], metric: str) -> dict:
    known = [row for row in rows if row["savings"][metric] is not None]
    result = {"known_pairs": len(known), "missing_pairs": len(rows) - len(known),
              "observed_cluster_median": None, "conditional_lower_95": None,
              "reason": "missing_or_unmatched_pair"}
    if not rows or len(known) != len(rows):
        return result
    clusters: dict[str, list[float]] = {}
    for row in rows:
        clusters.setdefault(row["template_id"], []).append(row["savings"][metric])
    values = [median(cluster) for cluster in clusters.values()]
    result.update(observed_cluster_median=median(values), conditional_lower_95=median_lower(values))
    result["reason"] = "conditional_iid_clusters" if result["conditional_lower_95"] is not None else "insufficient_clusters"
    return result


def analyze_pairs(events: list[dict]) -> dict:
    report = summarize(events)
    pair_cases: dict[str, dict[str, dict]] = {}
    groups: dict[str, dict] = {}
    pair_contracts = {pair["pair_id"]: pair for pair in report["pairs"]}
    for case in report["cases"]:
        spec = case["spec"]
        pair_cases.setdefault(spec["pair_id"], {})[spec["mode"]] = case
        identity = _identity(case)
        key = json.dumps(identity, sort_keys=True)
        group = groups.setdefault(key, {**deepcopy(identity), "cases": [], "pair_ids": set()})
        group["cases"].append(case)
        group["pair_ids"].add(spec["pair_id"])
    results = []
    for group in groups.values():
        cases = group.pop("cases")
        pair_ids = group.pop("pair_ids")
        rows = []
        for pair_id in sorted(pair_ids):
            modes, contract = pair_cases[pair_id], pair_contracts[pair_id]
            matched = contract["reason"] in {"matched", "unknown_scope"}
            row = {"pair_id": pair_id, "template_id": contract["template_id"],
                   "matched": matched, "reason": contract["reason"], "difference": None,
                   "regression": None, "savings": dict.fromkeys(METRICS)}
            if matched:
                eager, lazy = modes["eager"], modes["lazy"]
                row["difference"] = int(lazy["safe_task_success"]) - int(eager["safe_task_success"])
                row["regression"] = eager["safe_task_success"] and not lazy["safe_task_success"]
                for metric in METRICS:
                    left, right = eager["usage"][metric], lazy["usage"][metric]
                    if eager["finished"] and lazy["finished"] and left is not None and left > 0 and right is not None:
                        row["savings"][metric] = 1 - right / left
            rows.append(row)
        matched_rows = [row for row in rows if row["matched"]]
        clusters: dict[str, list[dict]] = {}
        for row in matched_rows:
            clusters.setdefault(row["template_id"], []).append(row)
        complete = bool(rows) and len(matched_rows) == len(rows)
        regressions = sum(any(row["regression"] for row in cluster) for cluster in clusters.values())
        quality = {"regression_clusters": regressions, "cluster_mean_difference": None,
                   "conditional_lower_95": None, "reason": "missing_or_unmatched_pair"}
        if complete:
            quality.update(cluster_mean_difference=mean(mean(row["difference"] for row in cluster) for cluster in clusters.values()),
                           conditional_lower_95=-binomial_upper(regressions, len(clusters)),
                           reason="conditional_iid_clusters")
        unknown = sum(row["reason"] == "unknown_scope" for row in rows)
        reasons = ["sampling_design_unverified", "billing_not_evaluated"]
        if unknown:
            reasons.append("unknown_scope")
        if not complete:
            reasons.append("missing_or_unmatched_pair")
        results.append({**group, "started_cases": len(cases),
                        "unfinished_cases": sum(not case["finished"] for case in cases),
                        "started_by_mode": {mode: sum(case["spec"]["mode"] == mode for case in cases) for mode in ("eager", "lazy")},
                        "success_by_mode": {mode: sum(case["safe_task_success"] for case in cases if case["spec"]["mode"] == mode) for mode in ("eager", "lazy")},
                        "matched_pairs": len(matched_rows), "unmatched_pairs": len(rows) - len(matched_rows),
                        "unknown_scope_pairs": unknown,
                        "template_clusters": len({case["spec"]["template_id"] for case in cases}),
                        "matched_template_clusters": len(clusters),
                        "journaled_request_attempts": sum(case["usage"]["request_count"] for case in cases),
                        "pairs": rows, "quality": quality, "benefits": {metric: _benefits(rows, metric) for metric in METRICS},
                        "unverified_reasons": reasons, "gate_status": "unverified"})
    return {"schema_version": 1, "run_id": report["run_id"],
            "started_cases": report["started_cases"], "safe_successes": report["safe_successes"],
            "unfinished_cases": report["unfinished_cases"], "failure_counts": report["failure_counts"],
            "confidence_level": .95, "quality_estimand": "equal-template mean of within-template paired success differences",
            "benefit_estimand": "median across template medians of paired input reductions",
            "conditional_assumptions": ["independently and identically sampled representative template clusters",
                                        "predeclared within-template repeat design", "all started pairs retained"],
            "quality_method": "negative exact upper bound for probability of a cluster with any eager-success/lazy-failure; improvements ignored",
            "benefit_method": "one-sided binomial order-statistic lower bound on template-median benefits",
            "groups": results, "gate_status": "unverified"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--journal", required=True, type=Path)
    parser.add_argument("--plan", type=Path)
    args = parser.parse_args()
    try:
        events = read_journal(args.journal)
        audit = audit_plan(read_plan(args.plan), events) if args.plan is not None else None
        result = analyze_pairs(events)
        result["batch_audit"] = audit
    except (OSError, ValueError, KeyError, TypeError):
        print(json.dumps({"error": "invalid_evidence", "gate_status": "unverified"}))
        return 2
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
