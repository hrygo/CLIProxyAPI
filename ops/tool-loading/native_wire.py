"""Explicit Chat, Responses and wrapped Antigravity observation contracts."""

import json

from evidence import _json, _closed_protocol_terminal
from antigravity_wire import terminal as antigravity_terminal


def wire_contract(executor):
    if executor in {"codex", "meta"}:
        return "openai-responses-v1"
    if executor == "antigravity":
        return "antigravity-generate-v1"
    if isinstance(executor, str) and executor.startswith("openai-compatible-"):
        return "openai-chat-v1"
    raise ValueError("executor has no verified live wire view")


def _declaration_containers(contract, body):
    if contract == "antigravity-generate-v1":
        if not isinstance(body, dict) or not isinstance(body.get("request"), dict):
            raise ValueError("missing Antigravity request wrapper")
        return [body["request"].get("tools", [])]
    if contract not in {"openai-chat-v1", "openai-responses-v1"} or not isinstance(body, dict):
        raise ValueError("unsupported request wire")
    containers = [body.get("tools", [])]
    if contract == "openai-responses-v1":
        history = body.get("input", [])
        if isinstance(history, list):
            for item in history:
                if not isinstance(item, dict):
                    continue
                if item.get("type") == "additional_tools":
                    # Lite declarations are conversation items, not an embedded
                    # additional_tools field. A missing array is invalid evidence.
                    containers.append(item.get("tools"))
                elif item.get("type") == "tool_search_output" and "tools" in item:
                    containers.append(item["tools"])
    return containers


def declarations(contract, body):
    result = []
    def visit(tools, depth=0):
        if not isinstance(tools, list) or depth > 16:
            raise ValueError("invalid declaration container")
        for tool in tools:
            if not isinstance(tool, dict):
                raise ValueError("invalid declaration")
            if tool.get("type") == "namespace":
                visit(tool.get("tools"), depth + 1)
            else:
                result.append(tool)
                if len(result) > 10000:
                    raise ValueError("declaration observation budget exceeded")
    for container in _declaration_containers(contract, body):
        if contract == "antigravity-generate-v1":
            if not isinstance(container, list):
                raise ValueError("invalid Antigravity tools")
            for tool in container:
                if not isinstance(tool, dict):
                    raise ValueError("invalid Antigravity tool")
                keys = [key for key in ("functionDeclarations", "function_declarations") if key in tool]
                for key in keys:
                    visit(tool[key])
        else:
            visit(container)
    return result


def declaration_bytes(contract, body):
    # Observe serialized container values; flattening for counts must not erase
    # namespace metadata or historical declarations from the transport metric.
    declarations(contract, body)
    return sum(len(json.dumps(container, separators=(",", ":")).encode())
               for container in _declaration_containers(contract, body) if container)


def _chat_terminal(body):
    try:
        values = [_json(body)]
    except (ValueError, UnicodeDecodeError):
        if not _closed_protocol_terminal("openai-chat-v1", body):
            return False
        values = []
        for frame in body.replace(b"\r\n", b"\n").split(b"\n\n"):
            data = b"\n".join(line[5:].lstrip() for line in frame.splitlines() if line.startswith(b"data:"))
            if data and data != b"[DONE]":
                try: values.append(_json(data))
                except (ValueError, UnicodeDecodeError): return False
    reasons = []
    for value in values:
        if not isinstance(value, dict) or not isinstance(value.get("choices", []), list) or "error" in value:
            return False
        for choice in value.get("choices", []):
            if not isinstance(choice, dict): return False
            if choice.get("finish_reason") is not None: reasons.append(choice["finish_reason"])
    return bool(reasons and all(isinstance(reason, str) and reason in {"stop", "tool_calls", "function_call"} for reason in reasons))


def successful_terminal(contract, body, *, transport_complete=False):
    if contract == "antigravity-generate-v1":
        return antigravity_terminal(body, _json, transport_complete=transport_complete)
    if contract == "openai-chat-v1":
        return _chat_terminal(body)
    if contract != "openai-responses-v1":
        return False
    try:
        document = _json(body)
    except (ValueError, UnicodeDecodeError):
        if not _closed_protocol_terminal(contract, body):
            return False
        payloads = []
        for frame in body.replace(b"\r\n", b"\n").split(b"\n\n"):
            data = b"\n".join(line[5:].lstrip(b" ") for line in frame.splitlines() if line.startswith(b"data:"))
            if data and data != b"[DONE]": payloads.append(_json(data))
        return bool(payloads and payloads[-1].get("type") == "response.completed"
                    and payloads[-1]["response"].get("error") is None)
    return (isinstance(document, dict) and isinstance(document.get("id"), str) and bool(document["id"])
            and document.get("status") == "completed" and isinstance(document.get("output"), list)
            and document.get("error") is None)
