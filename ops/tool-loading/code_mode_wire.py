"""Observe explicit Code Mode function bridges without changing wire evidence."""

import hashlib
import json

from http_attempts import _json
from native_wire import _declaration_containers, declarations

# Bind each supported source identity to its actual declaration scope.
EXEC_ALIAS = "ccb_" + hashlib.sha256(json.dumps(["", "exec", "custom"], separators=(",", ":")).encode()).hexdigest()[:48]
EXEC_NAMESPACED_ALIAS = "ccb_" + hashlib.sha256(json.dumps(["functions", "exec", "custom"], separators=(",", ":")).encode()).hexdigest()[:48]
ENTRY_NAMES = {"exec", EXEC_ALIAS, EXEC_NAMESPACED_ALIAS}


def _entry_candidates(tools, namespace=(), depth=0):
    if depth > 16 or not isinstance(tools, list) or len(tools) > 4096:
        return None
    candidates = []
    for tool in tools:
        if not isinstance(tool, dict):
            continue
        if tool.get("type") == "namespace":
            nested = _entry_candidates(tool.get("tools"), namespace + (tool.get("name"),), depth+1)
            if nested is None:
                return None
            candidates.extend(nested)
        else:
            function = tool.get("function", tool)
            if isinstance(function, dict) and function.get("name") in ENTRY_NAMES:
                candidates.append((tool, function, namespace))
    return candidates


def code_mode_entry(tools, contract=None):
    candidates = _entry_candidates(tools)
    if candidates is None or len(candidates) != 1:
        return None
    tool, function, namespace = candidates[0]
    if not isinstance(function.get("description"), str):
        return None
    form = tool.get("format")
    if (tool.get("type") == "custom" and function.get("name") == "exec"
            and isinstance(form, dict) and form.get("type") == "grammar" and form.get("syntax") == "lark"
            and isinstance(form.get("definition"), str) and form["definition"].strip()):
        if namespace not in ((), ("functions",)):
            return None
        return {"description": function["description"], "wire_name": "exec", "bridge": False, "grammar_fidelity": "native"}
    if contract not in {"openai-responses-v1", "openai-chat-v1"} or tool.get("type") != "function":
        return None
    name = function.get("name") if contract == "openai-responses-v1" else "exec"
    allowed_names = {EXEC_ALIAS, EXEC_NAMESPACED_ALIAS} if contract == "openai-responses-v1" else {"exec"}
    # declarations() flattens wrappers. The final history observer separately
    # verifies the required scope against the original containers.
    allowed_scopes = ((), ("functions",)) if name == EXEC_NAMESPACED_ALIAS else ((),)
    if (name not in allowed_names or namespace not in allowed_scopes
            or tool.get("namespace") not in (None, "") or function.get("namespace") not in (None, "")):
        return None
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
        expected = ("functions",) if entry["wire_name"] == EXEC_NAMESPACED_ALIAS else ()
        return namespace == expected and outer in (None, "") and inner in (None, "")
    return namespace in ((), ("functions",)) and outer in (None, "", "functions") and inner in (None, "", "functions")


def normalize_code_mode_wire(bodies, contract):
    """Return an analysis-only history; raw captures and metering are untouched."""
    entry = code_mode_entry(declarations(contract, bodies[0]), contract) if bodies else None
    if entry is None:
        return {"bodies": bodies, "integrity": False}
    for body in bodies:
        tools = declarations(contract, body)
        candidates = _entry_candidates(tools)
        if candidates is None or candidates:
            current = code_mode_entry(tools, contract)
            if (current is None or current["wire_name"] != entry["wire_name"]
                    or current["bridge"] != entry["bridge"] or not _entry_scope_valid(body, contract, current)):
                return {"bodies": bodies, "integrity": False}
    if not entry["bridge"]:
        return {"bodies": bodies, "integrity": True}
    integrity, normalized = True, []
    for body in bodies:
        items, pending, known_calls, output_ids = [], set(), set(), set()
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
            if call_id in known_calls:
                integrity = False
                return
            known_calls.add(call_id)
            pending.add(call_id)
            items.append({"type":"custom_tool_call", "name":"exec", "namespace":"functions",
                          "call_id":call_id, "input":value["input"]})
        def result(call_id, output, item_id=None):
            nonlocal integrity
            eligible = pending if contract == "openai-chat-v1" else known_calls
            if isinstance(call_id, str) and call_id in eligible:
                item = {"type":"custom_tool_call_output", "call_id":call_id, "output":output}
                if item_id is not None:
                    if not isinstance(item_id, str) or not item_id or item_id in output_ids:
                        integrity = False
                        return
                    output_ids.add(item_id)
                    item["id"] = item_id
                items.append(item)
                pending.discard(call_id)
        if contract == "openai-responses-v1":
            history = body.get("input") if isinstance(body, dict) else None
            for item in history if isinstance(history, list) else []:
                if not isinstance(item, dict):
                    continue
                if item.get("type") == "function_call":
                    allowed = (None, "", "functions") if entry["wire_name"] == EXEC_NAMESPACED_ALIAS else (None, "")
                    if item.get("name") == entry["wire_name"] and item.get("namespace") not in allowed:
                        integrity = False
                        continue
                    call(item.get("name"), item.get("call_id"), item.get("arguments"))
                elif item.get("type") == "function_call_output":
                    result(item.get("call_id"), item.get("output"), item.get("id"))
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
