"""Explicit Codex 0.160.0 Code Mode profile and wire observations."""
import re
import json
from catalog_fixture import build_catalog


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


def observe_code_mode(tools, mode, *, coding_task=None, client_denied=(), service_denied=()):
    candidates = [tool for tool in tools if tool.get("name") == "exec" and tool.get("type") == "custom"
                  and isinstance(tool.get("format"), dict) and tool["format"].get("type") == "grammar"
                  and tool["format"].get("syntax") == "lark"
                  and isinstance(tool["format"].get("definition"), str) and tool["format"]["definition"].strip()]
    if len(candidates) != 1 or not isinstance(candidates[0].get("description"), str):
        return {"effective": False, "exec_grammar_observed": False, "nested_fixture_signatures": None}
    description = candidates[0]["description"]
    known = {tool["name"] for tool in build_catalog(coding_task)}
    expected = known - set(client_denied) - set(service_denied)
    observed = _schema_tools(description, known)
    effective = (observed == expected if mode == "eager"
                 else not observed and bool(re.search(r"\bALL_TOOLS\b", description)) if mode == "lazy" else False)
    return {"effective": bool(effective), "exec_grammar_observed": True,
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


def observe_code_discovery(bodies, *, coding_task=None):
    expected = {tool["name"] for tool in build_catalog(coding_task)}
    seen, discovery_calls = {}, set()
    loaded, invoked, premature = set(), set(), set()
    observed, integrity = False, True
    for item in _history_items(bodies):
        key = (item.get("type"), item["call_id"])
        fingerprint = json.dumps(item, sort_keys=True)
        if key in seen:
            integrity = integrity and seen[key] == fingerprint
            continue
        seen[key] = fingerprint
        if (item.get("type") == "custom_tool_call" and item.get("name") == "exec"
                and item.get("namespace") == "functions" and isinstance(item.get("input"), str)):
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
