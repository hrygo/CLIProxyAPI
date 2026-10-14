"""Public numeric export and paired accounting; unknown values remain null."""
from collections import defaultdict

COUNTERS = {
    "input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens",
    "cache_read_input_tokens", "cache_creation_input_tokens", "cached_tokens", "cache_write_tokens",
    "reasoning_tokens", "thinking_tokens", "promptTokenCount", "candidatesTokenCount",
    "thoughtsTokenCount", "totalTokenCount", "cachedContentTokenCount", "toolUsePromptTokenCount",
    "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens",
}
DETAILS = {"input_tokens_details", "output_tokens_details", "prompt_tokens_details",
           "completion_tokens_details", "cache_creation"}


def numeric_usage(raw):
    if not isinstance(raw, dict):
        return None
    result = {}
    for key, value in raw.items():
        if key in COUNTERS and type(value) is int and 0 <= value <= 2**63 - 1:
            result[key] = value
        elif key in DETAILS and isinstance(value, dict):
            nested = numeric_usage(value)
            if nested:
                result[key] = nested
    return result


def reduction(eager, lazy):
    if type(eager) is not int or type(lazy) is not int or eager <= 0 or lazy < 0:
        return None
    return (eager - lazy) / eager


def paired_summary(cases):
    grouped = defaultdict(dict)
    for item in cases:
        key = (item["client"], item["model"], item["task"])
        mode = item["mode"]
        if mode not in {"eager", "lazy"} or mode in grouped[key]:
            raise ValueError("each declared pair needs unique eager/lazy cases")
        grouped[key][mode] = item
    pairs = []
    for (client, model, task), values in grouped.items():
        eager, lazy = values.get("eager"), values.get("lazy")
        finished = bool(eager and lazy and all(x.get("status") == "finished" for x in (eager, lazy)))
        comparable = bool(finished and all(x.get("effective_mode") is True for x in (eager, lazy)))
        pair = {"client": client, "model": model, "task": task, "comparable": comparable,
                "both_successful": bool(finished and all(x.get("safe_task_success") is True for x in (eager, lazy)))}
        for field in ("first_input_tokens", "peak_input_tokens", "total_input_tokens"):
            pair[field.removesuffix("_tokens") + "_reduction"] = (
                reduction((eager.get("usage") or {}).get(field), (lazy.get("usage") or {}).get(field))
                if comparable else None)
        pairs.append(pair)
    return {"started": len(cases), "finished": sum(x.get("status") == "finished" for x in cases),
            "safe_successes": sum(x.get("status") == "finished" and x.get("safe_task_success") is True for x in cases),
            "pairs": pairs, "billing": None,
            "confidence": "limited measured evidence; no reliability or cost certification"}

