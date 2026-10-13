"""Observe explicit Code Mode function bridges without changing wire evidence."""

import hashlib
import json

from http_attempts import _json
from native_wire import _declaration_containers, declarations

# The pinned client registers exec as a plain custom tool. A native Codex
# upstream wrapper namespace is not the source identity used by the bridge.
EXEC_ALIAS = "ccb_" + hashlib.sha256(json.dumps(["", "exec", "custom"], separators=(",", ":")).encode()).hexdigest()[:48]


def code_mode_entry(tools, contract=None):
    candidates = []
    for tool in tools:
        if not isinstance(tool, dict):
            continue
        function = tool.get("function", tool)
        if isinstance(function, dict) and function.get("name") in {"exec", EXEC_ALIAS}:
            candidates.append((tool, function))
    if len(candidates) != 1:
        return None
    tool, function = candidates[0]
    if not isinstance(function.get("description"), str):
        return None
    form = tool.get("format")
    if (tool.get("type") == "custom" and function.get("name") == "exec"
            and isinstance(form, dict) and form.get("type") == "grammar" and form.get("syntax") == "lark"
            and isinstance(form.get("definition"), str) and form["definition"].strip()):
        return {"description": function["description"], "wire_name": "exec", "bridge": False, "grammar_fidelity": "native"}
    if contract not in {"openai-responses-v1", "openai-chat-v1"} or tool.get("type") != "function":
        return None
    name = EXEC_ALIAS if contract == "openai-responses-v1" else "exec"
    parameters = function.get("parameters")
    if (function.get("name") != name or not isinstance(parameters, dict)
            or parameters.get("type") != "object" or parameters.get("properties") != {"input": {"type": "string"}}
            or parameters.get("required") != ["input"] or set(parameters) - {"type", "properties", "required", "additionalProperties"}
            or parameters.get("additionalProperties", False) is not False
            or contract == "openai-responses-v1" and parameters.get("additionalProperties") is not False):
        return None
    description = function["description"]
    if "Run JavaScript code" not in description or "ALL_TOOLS" not in description:
        return None
    described = "lark" in description and "grammar" in description
    return {"description": description, "wire_name": name, "bridge": True,
            "grammar_fidelity": "described" if described else "not-forwarded"}


def _entry_scope_valid(body, contract, entry):
    matches = []
    def visit(tools, namespace=()):
        for tool in tools:
            if tool.get("type") == "namespace":
                visit(tool["tools"], namespace + (tool.get("name"),))
            else:
                function = tool.get("function", tool)
                if isinstance(function, dict) and function.get("name") == entry["wire_name"]:
                    matches.append((namespace, tool.get("namespace"), function.get("namespace")))
    # declarations() validates the bounded containers before this traversal.
    for container in _declaration_containers(contract, body):
        visit(container)
    if len(matches) != 1:
        return False
    namespace, outer, inner = matches[0]
    if entry["bridge"]:
        return namespace == () and outer in (None, "") and inner in (None, "")
    return namespace in ((), ("functions",)) and outer in (None, "", "functions") and inner in (None, "", "functions")


def normalize_code_mode_wire(bodies, contract):
    """Return an analysis-only history; raw captures and metering are untouched."""
    entry = code_mode_entry(declarations(contract, bodies[0]), contract) if bodies else None
    if entry is None:
        return {"bodies": bodies, "integrity": False}
    for body in bodies:
        tools = declarations(contract, body)
        if any(isinstance(tool.get("function", tool), dict)
               and tool.get("function", tool).get("name") in {"exec", EXEC_ALIAS} for tool in tools):
            current = code_mode_entry(tools, contract)
            if (current is None or current["wire_name"] != entry["wire_name"]
                    or current["bridge"] != entry["bridge"] or not _entry_scope_valid(body, contract, current)):
                return {"bodies": bodies, "integrity": False}
    if not entry["bridge"]:
        return {"bodies": bodies, "integrity": True}
    integrity, normalized = True, []
    for body in bodies:
        items, pending = [], set()
        def call(name, call_id, arguments):
            nonlocal integrity
            if name != entry["wire_name"]:
                return
            if not isinstance(call_id, str) or not call_id or not isinstance(arguments, str) or len(arguments) > 262144:
                integrity = False
                return
            try:
                value = _json(arguments.encode())
            except (ValueError, UnicodeError):
                integrity = False
                return
            if not isinstance(value, dict) or set(value) != {"input"} or not isinstance(value["input"], str):
                integrity = False
                return
            pending.add(call_id)
            items.append({"type":"custom_tool_call", "name":"exec", "namespace":"functions",
                          "call_id":call_id, "input":value["input"]})
        def result(call_id, output):
            if isinstance(call_id, str) and call_id in pending:
                items.append({"type":"custom_tool_call_output", "call_id":call_id, "output":output})
                pending.remove(call_id)
        if contract == "openai-responses-v1":
            history = body.get("input") if isinstance(body, dict) else None
            for item in history if isinstance(history, list) else []:
                if not isinstance(item, dict):
                    continue
                if item.get("type") == "function_call":
                    if item.get("name") == entry["wire_name"] and item.get("namespace") not in (None, ""):
                        integrity = False
                        continue
                    call(item.get("name"), item.get("call_id"), item.get("arguments"))
                elif item.get("type") == "function_call_output":
                    result(item.get("call_id"), item.get("output"))
        else:
            messages = body.get("messages") if isinstance(body, dict) else None
            for message in messages if isinstance(messages, list) else []:
                if not isinstance(message, dict):
                    continue
                calls = message.get("tool_calls")
                if message.get("role") == "assistant" and isinstance(calls, list):
                    for item in calls:
                        function = item.get("function") if isinstance(item, dict) else None
                        if isinstance(function, dict) and item.get("type") == "function":
                            call(function.get("name"), item.get("id"), function.get("arguments"))
                elif message.get("role") == "tool":
                    result(message.get("tool_call_id"), message.get("content"))
        normalized.append({"input": items})
    return {"bodies": normalized, "integrity": integrity}
