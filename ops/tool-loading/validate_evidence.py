"""Validate journal structure/accounting; this does not certify live behavior."""

import argparse
import json
from pathlib import Path

from run_acceptance import read_journal, summarize


def validate_evidence(path: Path) -> dict:
    events = read_journal(path)
    report = summarize(events)
    return {"journal_valid": True, "run_id": report["run_id"],
            "started_cases": report["started_cases"], "safe_successes": report["safe_successes"],
            "unfinished_cases": report["unfinished_cases"],
            "usage_incomplete_cases": sum(case["usage"]["total_input_tokens"] is None for case in report["cases"]),
            "unknown_scope_cases": sum("unknown" in case["spec"]["scope"].values() for case in report["cases"]),
            "incomparable_pairs": sum(not pair["comparable"] for pair in report["pairs"]),
            "gate_status": "unverified"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--journal", required=True, type=Path)
    args = parser.parse_args()
    try:
        result = validate_evidence(args.journal)
    except (OSError, ValueError, KeyError, TypeError):
        print(json.dumps({"journal_valid": False, "error": "invalid_evidence"}))
        return 2
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
