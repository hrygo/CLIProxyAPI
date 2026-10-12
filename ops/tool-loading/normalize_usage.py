"""Conservative usage decoding by explicit wire contract, never model-name guesses."""

from __future__ import annotations

import argparse
from decimal import Decimal, InvalidOperation
import json
import sys
from typing import Any

TOKEN_FIELDS = ("input_total", "input_uncached", "cache_read", "cache_write",
                "output_total", "reasoning_output", "tool_prompt_tokens")
CONTRACTS = ("openai-responses-v1", "openai-chat-v1", "anthropic-messages-v1", "gemini-generate-v1", "antigravity-generate-v1")


def normalize_usage(provider: str, raw_usage: Any) -> dict[str, Any]:
    """Decode one request-total object. Null denotes missing/invalid evidence.

    ``provider`` identifies a verified wire contract, not a credential/provider
    label. A translated OAuth endpoint must select its actual observed contract.
    """
    tokens = dict.fromkeys(TOKEN_FIELDS)
    issues: list[str] = []
    sources: dict[str, str] = {}
    raw = raw_usage if isinstance(raw_usage, dict) else {}
    if raw_usage is not None and not isinstance(raw_usage, dict):
        issues.append("invalid_usage_object")

    def counter(path: str) -> int | None:
        value: Any = raw
        for key in path.split("."):
            if not isinstance(value, dict) or key not in value:
                return None
            value = value[key]
        if value is None:
            return None
        if type(value) is not int or not 0 <= value <= 2**63 - 1:
            issues.append("invalid_counter:" + path)
            return None
        return value

    def assign(field: str, path: str) -> None:
        tokens[field] = counter(path)
        if tokens[field] is not None:
            sources[field] = path

    semantics = "unknown"
    ttl = None
    total = None
    if provider in ("openai-responses-v1", "openai-chat-v1"):
        semantics = "inclusive"
        is_chat = provider == "openai-chat-v1"
        assign("input_total", "prompt_tokens" if is_chat else "input_tokens")
        assign("output_total", "completion_tokens" if is_chat else "output_tokens")
        detail = "prompt_tokens_details" if is_chat else "input_tokens_details"
        assign("cache_read", detail + ".cached_tokens")
        assign("cache_write", detail + ".cache_write_tokens")
        assign("reasoning_output", ("completion_tokens_details" if is_chat else "output_tokens_details") + ".reasoning_tokens")
        total = counter("total_tokens")
    elif provider == "anthropic-messages-v1":
        semantics = "exclusive"
        assign("input_uncached", "input_tokens")
        assign("cache_read", "cache_read_input_tokens")
        assign("cache_write", "cache_creation_input_tokens")
        assign("output_total", "output_tokens")
        assign("reasoning_output", "output_tokens_details.thinking_tokens")
        values = [tokens[key] for key in ("input_uncached", "cache_read", "cache_write")]
        if all(value is not None for value in values):
            tokens["input_total"] = sum(values)
            sources["input_total"] = "sum(input_tokens,cache_read_input_tokens,cache_creation_input_tokens)"
        if raw.get("cache_creation") is not None:
            ttl = {"5m": counter("cache_creation.ephemeral_5m_input_tokens"),
                   "1h": counter("cache_creation.ephemeral_1h_input_tokens")}
            if any(value is None for value in ttl.values()):
                issues.append("incomplete_cache_ttl")
            elif tokens["cache_write"] is not None and sum(ttl.values()) != tokens["cache_write"]:
                issues.append("inconsistent_cache_ttl")
    elif provider in ("gemini-generate-v1", "antigravity-generate-v1"):
        semantics = "inclusive"
        assign("input_total", "promptTokenCount")
        assign("cache_read", "cachedContentTokenCount")
        assign("reasoning_output", "thoughtsTokenCount")
        assign("tool_prompt_tokens", "toolUsePromptTokenCount")
        candidates = counter("candidatesTokenCount")
        if candidates is not None and tokens["reasoning_output"] is not None:
            tokens["output_total"] = candidates + tokens["reasoning_output"]
            sources["output_total"] = "sum(candidatesTokenCount,thoughtsTokenCount)"
        total = counter("totalTokenCount")
    else:
        issues.append("unknown_contract")

    if semantics == "inclusive" and tokens["input_total"] is not None:
        caches = [tokens["cache_read"], tokens["cache_write"]]
        if sum(value for value in caches if value is not None) > tokens["input_total"]:
            issues.append("cache_exceeds_input")
        elif all(value is not None for value in caches):
            tokens["input_uncached"] = tokens["input_total"] - sum(caches)
            sources["input_uncached"] = "input_total-cache_read-cache_write"
    if tokens["reasoning_output"] is not None and tokens["output_total"] is not None and tokens["reasoning_output"] > tokens["output_total"]:
        issues.append("reasoning_exceeds_output")
    if total is not None and tokens["input_total"] is not None and tokens["output_total"] is not None:
        if total != tokens["input_total"] + tokens["output_total"]:
            issues.append("inconsistent_total")
    return {"contract": provider if provider in CONTRACTS else "unknown", "input_semantics": semantics,
            "tokens": tokens, "cache_write_by_ttl": ttl, "source_paths": sources,
            "missing_fields": [field for field in TOKEN_FIELDS if tokens[field] is None], "issues": issues}


def aggregate_usage(requests: list[dict[str, Any]]) -> dict[str, Any]:
    """Include every attempt; callers first consolidate stream snapshots by ID.

    Repeated request IDs or unverified usage kinds are errors, not opportunities
    to silently select the successful attempt or sum cumulative stream counters.
    """
    seen = set()
    values = []
    for request in requests:
        request_id = request.get("request_id")
        if not isinstance(request_id, str) or not request_id or request_id in seen:
            raise ValueError("each request attempt needs a unique nonempty ID")
        if request.get("usage_kind") != "request-total":
            raise ValueError("usage must be consolidated to one request-total per attempt")
        seen.add(request_id)
        values.append(normalize_usage(request.get("contract"), request.get("raw_usage")))

    def complete_sum(field: str) -> int | None:
        counts = [value["tokens"][field] if not value["issues"] else None for value in values]
        return sum(counts) if counts and all(count is not None for count in counts) else None

    inputs = [value["tokens"]["input_total"] if not value["issues"] else None for value in values]
    known = [count for count in inputs if count is not None]
    return {"request_count": len(values), "first_input_tokens": inputs[0] if inputs else None,
            "peak_input_tokens": max(known) if known and len(known) == len(inputs) else None,
            "total_input_tokens": complete_sum("input_total"), "known_total_input_tokens": sum(known) if known else None,
            "unknown_input_requests": len(inputs) - len(known), "total_output_tokens": complete_sum("output_total"),
            "invalid_usage_requests": sum(bool(value["issues"]) for value in values),
            "cache_read_tokens": complete_sum("cache_read"), "cache_write_tokens": complete_sum("cache_write"),
            "requests": values}


def estimate_cost(usage: dict[str, Any], prices: dict[str, Any], *, fixed_cost: Any = None) -> dict[str, Any]:
    """Account for disjoint buckets using explicit per-million prices and currency.

    Fixed/tool/request charges must be explicitly supplied, even when known zero.
    TTL-specific cache-write prices override the aggregate bucket when present.
    This estimates token charges, not an account subscription allocation/bill.
    """
    missing: list[str] = []
    subtotal = Decimal(0)

    def amount(value: Any, field: str) -> Decimal | None:
        if value is None or isinstance(value, bool):
            missing.append(field)
            return None
        try:
            number = Decimal(str(value))
            if not number.is_finite() or number < 0:
                raise InvalidOperation
            return number
        except (InvalidOperation, ValueError):
            missing.append(field)
            return None

    for field in ("currency", "price_source"):
        if not isinstance(prices.get(field), str) or not prices[field]:
            missing.append(field)
    if usage["issues"]:
        missing.append("valid_usage")
    fixed = amount(fixed_cost, "fixed_cost")
    if fixed is not None:
        subtotal += fixed
    buckets = {field: usage["tokens"][field] for field in ("input_uncached", "cache_read", "cache_write", "output_total")}
    if usage["cache_write_by_ttl"] is not None:
        del buckets["cache_write"]
        buckets.update({"cache_write_" + ttl: count for ttl, count in usage["cache_write_by_ttl"].items()})
    for field, count in buckets.items():
        if count is None:
            missing.append("tokens." + field)
        elif count:
            price = amount(prices.get(field), "price." + field)
            if price is not None:
                subtotal += Decimal(count) * price / Decimal(1000000)
    # Invalid input counters/relationships cannot substantiate even a subtotal.
    known = None if usage["issues"] else format(subtotal, "f")
    return {"complete": not missing, "cost": known if not missing else None,
            "known_subtotal": known, "currency": prices.get("currency") if "currency" not in missing else None,
            "missing_fields": missing}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract", required=True, choices=CONTRACTS)
    args = parser.parse_args()
    json.dump(normalize_usage(args.contract, json.load(sys.stdin)), sys.stdout, sort_keys=True)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
