"""Validate journal structure/accounting; this does not certify live behavior."""

import argparse
import json
from pathlib import Path

from run_acceptance import read_journal, summarize
from batch_plan import read_plan, audit_plan


def validate_evidence(path: Path, *, plan_path: Path | None = None) -> dict:
    events = read_journal(path)
    report = summarize(events)
    result = {"journal_valid": True, "run_id": report["run_id"],
            "started_cases": report["started_cases"], "safe_successes": report["safe_successes"],
            "unfinished_cases": report["unfinished_cases"],
            "usage_incomplete_cases": sum(case["usage"]["total_input_tokens"] is None for case in report["cases"]),
            "unknown_scope_cases": sum("unknown" in case["spec"]["scope"].values() for case in report["cases"]),
            "incomparable_pairs": sum(not pair["comparable"] for pair in report["pairs"]),
            "gate_status": "unverified"}
    if plan_path is not None:
        try:
            result.update(batch_plan_valid=True, batch_audit=audit_plan(read_plan(plan_path), events))
        except (OSError, ValueError, KeyError, TypeError):
            result.update(batch_plan_valid=False, batch_error="invalid_batch_binding")
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--journal", required=True, type=Path)
    parser.add_argument("--plan", type=Path)
    args = parser.parse_args()
    try:
        result = validate_evidence(args.journal, plan_path=args.plan)
    except (OSError, ValueError, KeyError, TypeError):
        print(json.dumps({"journal_valid": False, "error": "invalid_evidence"}))
        return 2
    print(json.dumps(result, sort_keys=True))
    return 2 if result.get("batch_plan_valid") is False else 0


if __name__ == "__main__":
    raise SystemExit(main())
