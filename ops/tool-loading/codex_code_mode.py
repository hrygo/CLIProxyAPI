"""Explicit Codex 0.160.0 Code Mode profile and wire observations."""
import re
import json
from catalog_fixture import build_catalog
from code_mode_wire import code_mode_entry


def requested_codex_mode(client, value=None):
    if value is None:
        return "direct"
    if client != "codex" or type(value) is not str or value not in {"direct", "code_mode_only"}:
        raise ValueError("invalid explicit Codex tool profile")
    return value


def metadata_admits_mode(metadata, requested):
    # An explicit flag selects the mode only when model metadata omits it.
    # A model-specified mode always takes precedence in the pinned client.
    return metadata.get("tool_mode") in (None, requested)


def _schema_tools(value, expected, depth=0):
    if depth > 16:
        return set()
    found = set()
    if isinstance(value, str):
        if len(value) > 1024 * 1024:
            return found
        text = value.partition("\nOutput:\n")[2] if "\nOutput:\n" in value else value
        try:
            decoded = json.loads(text)
        except ValueError:
            decoded = None
        if decoded is not None:
            found.update(_schema_tools(decoded, expected, depth+1))
        for name in expected:
            pattern = (r"declare const tools: \{ mcp__fixture_{1,2}" + re.escape(name)
                       + r"\(args: (?:(?!declare const tools:)[\s\S]){1,16000}?\): Promise<")
            if re.search(pattern, text):
                found.add(name)
    elif isinstance(value, (dict, list)) and len(value) <= 4096:
        for child in value.values() if isinstance(value, dict) else value:
            found.update(_schema_tools(child, expected, depth+1))
    return found


def observe_code_mode(tools, mode, *, coding_task=None, client_denied=(), service_denied=(), wire_contract=None):
    entry = code_mode_entry(tools, wire_contract)
    if entry is None:
        return {"effective": False, "exec_grammar_observed": False, "nested_fixture_signatures": None}
    description = entry["description"]
    known = {tool["name"] for tool in build_catalog(coding_task)}
    expected = known - set(client_denied) - set(service_denied)
    observed = _schema_tools(description, known)
    effective = (observed == expected if mode == "eager"
                 else not observed and bool(re.search(r"\bALL_TOOLS\b", description)) if mode == "lazy" else False)
    return {"effective": bool(effective), "exec_grammar_observed": not entry["bridge"],
            "exec_bridge_observed": entry["bridge"], "grammar_fidelity": entry["grammar_fidelity"],
            "nested_fixture_signatures": len(observed)}


def _history_items(bodies):
    for body in bodies:
        items = body.get("input") if isinstance(body, dict) else None
        if not isinstance(items, list) or len(items) > 4096:
            continue
        for item in items:
            if not isinstance(item, dict) or not isinstance(item.get("call_id"), str):
                continue
            yield item


def _unqualified_exec_declared(bodies):
    # Native Lite may omit the namespace in a call. Resolve it only against
    # one actual initial declaration; never infer identity from the name alone.
    if not bodies or not isinstance(bodies[0], dict):
        return False
    body = bodies[0]
    containers = [body.get("tools", [])]
    history = body.get("input")
    if isinstance(history, list):
        containers += [item.get("tools") for item in history if isinstance(item, dict) and item.get("type") == "additional_tools"]
    matches = []
    def visit(tools, namespace=(), depth=0):
        if not isinstance(tools, list) or depth > 16:
            return
        for tool in tools:
            if not isinstance(tool, dict):
                continue
            if tool.get("type") == "namespace":
                visit(tool.get("tools"), namespace+(tool.get("name"),), depth+1)
            elif tool.get("name") == "exec":
                matches.append((namespace, tool))
    for tools in containers:
        visit(tools)
    if len(matches) != 1 or matches[0][0] not in {(), ("functions",)}:
        return False
    tool = matches[0][1]
    form = tool.get("format")
    return (tool.get("type") == "custom" and isinstance(form, dict)
            and form.get("type") == "grammar" and form.get("syntax") == "lark"
            and isinstance(form.get("definition"), str) and bool(form["definition"].strip()))


def _exec_call(item, unqualified=False):
    return (item.get("type") == "custom_tool_call" and item.get("name") == "exec"
            and (item.get("namespace") == "functions" or unqualified and item.get("namespace") is None)
            and isinstance(item.get("input"), str))


def observe_code_discovery(bodies, *, coding_task=None):
    expected = {tool["name"] for tool in build_catalog(coding_task)}
    seen, discovery_calls = {}, set()
    loaded, invoked, premature = set(), set(), set()
    observed, integrity = False, True
    unqualified = _unqualified_exec_declared(bodies)
    for body in bodies:
        output_ordinals, body_seen = {}, set()
        for item in _history_items([body]):
            identity = None
            if item.get("type") == "custom_tool_call_output":
                ordinal = output_ordinals.get(item["call_id"], 0)
                output_ordinals[item["call_id"]] = ordinal + 1
                identity = ("id", item["id"]) if isinstance(item.get("id"), str) and item["id"] else ("ordinal", ordinal)
            key = (item.get("type"), item["call_id"], identity)
            fingerprint = json.dumps(item, sort_keys=True)
            if key in body_seen:
                integrity = False
            body_seen.add(key)
            if key in seen:
                integrity = integrity and seen[key] == fingerprint
                continue
            seen[key] = fingerprint
            if _exec_call(item, unqualified):
                code = item["input"]
                if re.search(r"\bALL_TOOLS\b", code):
                    discovery_calls.add(item["call_id"])
                targets = set(re.findall(r"\btools\.mcp__fixture_{1,2}([A-Za-z0-9_]+)\s*\(", code)) & expected
                invoked.update(targets)
                premature.update(targets - loaded)
            elif item.get("type") == "custom_tool_call_output" and item["call_id"] in discovery_calls:
                observed = True
                loaded.update(_schema_tools(item.get("output"), expected))
    return {"discovery_result_observed": observed, "loaded_fixture_schema_tools": sorted(loaded),
            "observed_fixture_invocations": sorted(invoked), "invocations_before_schema": sorted(premature),
            "history_identity_consistent": integrity}


def observe_hidden_tool_rejection(bodies, target):
    calls = set()
    for item in _history_items(bodies):
        if (item.get("type") == "custom_tool_call" and item.get("name") == "exec"
                and item.get("namespace") == "functions" and isinstance(item.get("input"), str)
                and re.search(r"\btools\.mcp__fixture_{1,2}"+re.escape(target)+r"\s*\(", item["input"])):
            calls.add(item["call_id"])
        elif item.get("type") == "custom_tool_call_output" and item["call_id"] in calls:
            text = json.dumps(item.get("output"))
            if re.search(r"TypeError: tools\.mcp__fixture_{1,2}"+re.escape(target)+r" is not a function", text):
                return True
    return False


def code_mode_receipt_outputs(bodies):
    unqualified = _unqualified_exec_declared(bodies)
    for body in bodies:
        pending, seen, results = set(), set(), []
        for item in _history_items([body]):
            call_id = item["call_id"]
            if item.get("type") == "custom_tool_call":
                if call_id in seen:
                    pending.discard(call_id)
                    continue
                seen.add(call_id)
                if (_exec_call(item, unqualified)
                        and re.search(r"\btools\.mcp__fixture_{1,2}ledger_budget_(?:lookup|confirm)\s*\(", item["input"])):
                    pending.add(call_id)
            elif item.get("type") == "custom_tool_call_output" and call_id in pending:
                pending.remove(call_id)
                output = item.get("output")
                if "Script failed" not in json.dumps(output) and "Script error:" not in json.dumps(output):
                    results.append(output)
        yield results
