"""Decode the executor's wrapped generate wire without guessing transport EOF."""

FINISH_REASONS = {"STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST",
                  "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "IMAGE_SAFETY",
                  "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "IMAGE_RECITATION",
                  "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE",
                  "MALFORMED_RESPONSE", "ESCALATION", "PUP_LIMITED_DISABLED"}


def responses(body, decode_json, *, transport_complete=False):
    try:
        document = decode_json(body)
        values = document if isinstance(document, list) else [document]
    except (ValueError, UnicodeDecodeError):
        text = body.decode("utf-8").replace("\r\n", "\n")
        if "\r" in text or not transport_complete and not text.endswith("\n\n"):
            raise ValueError("incomplete Antigravity stream framing")
        values = []
        for line in text.splitlines():
            if not line or line.startswith(":"):
                continue
            # The executor consumes one JSON payload per data line. Do not
            # reinterpret multiline JSON or an unrelated terminal sentinel.
            if not line.startswith("data:"):
                raise ValueError("unknown Antigravity stream field")
            values.append(decode_json(line[5:].lstrip(" ")))
    if not values or len(values) > 10000:
        raise ValueError("missing or oversized Antigravity response sequence")
    result = []
    response_id = None
    for value in values:
        if (not isinstance(value, dict) or "error" in value
                or not isinstance(value.get("response"), dict) or "error" in value["response"]):
            raise ValueError("invalid Antigravity response wrapper")
        if "responseId" in value["response"]:
            identity = value["response"]["responseId"]
            if not isinstance(identity, str) or not identity or response_id not in (None, identity):
                raise ValueError("conflicting Antigravity response identity")
            response_id = identity
        result.append(value["response"])
    return result


def terminal(body, decode_json, *, transport_complete=False, success=True):
    try:
        values = responses(body, decode_json, transport_complete=transport_complete)
        response_id = None
        seen, finished = set(), {}
        substantive = False
        usage = None
        for value in values:
            if "responseId" in value:
                identity = value["responseId"]
                if not isinstance(identity, str) or not identity or response_id not in (None, identity):
                    return False
                response_id = identity
            candidates = value.get("candidates", [])
            if not isinstance(candidates, list):
                return False
            local = set()
            for position, candidate in enumerate(candidates):
                if not isinstance(candidate, dict):
                    return False
                index = candidate.get("index", position)
                if type(index) is not int or not 0 <= index < 10000 or index in local or index in finished:
                    return False
                local.add(index)
                seen.add(index)
                content = candidate.get("content")
                if content is not None:
                    if not isinstance(content, dict) or not isinstance(content.get("parts"), list):
                        return False
                    if any(not isinstance(part, dict) for part in content["parts"]):
                        return False
                    substantive = substantive or any(
                        isinstance(part.get("text"), str) and bool(part["text"]) or
                        isinstance(part.get("functionCall"), dict) and bool(part["functionCall"].get("name"))
                        for part in content["parts"])
                if "finishReason" in candidate:
                    reason = candidate["finishReason"]
                    if not isinstance(reason, str) or reason not in FINISH_REASONS:
                        return False
                    finished[index] = reason
            if "usageMetadata" in value:
                usage = value["usageMetadata"]
        if not seen:
            return False
        if set(finished) == seen:
            return not success or all(reason == "STOP" for reason in finished.values())
        # Match the gateway's healthy-EOF repair scope without treating a closed,
        # read-error or unfinished capture as EOF. Never repair a partial finish.
        return bool(transport_complete and not finished and substantive and response_id and
                    isinstance(usage, dict) and
                    all(type(usage.get(key)) is int and 0 <= usage[key] <= 2**63 - 1
                        for key in ("promptTokenCount", "candidatesTokenCount")))
    except (ValueError, UnicodeDecodeError, TypeError):
        return False
