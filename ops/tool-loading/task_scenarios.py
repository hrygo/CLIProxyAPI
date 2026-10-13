"""Fixed task gold for positive, unavailable, and dependent multi-tool runs."""

from dataclasses import dataclass
import json
import re

from catalog_fixture import confirmation_code


@dataclass(frozen=True)
class TaskScenario:
    template_id: str
    prompt: str
    requires_dispatch: bool
    requires_nonce: bool
    coding_task: str | None = None

    @property
    def gold(self):
        return {"requires_dispatch": self.requires_dispatch, "requires_nonce": self.requires_nonce,
                "requires_code": self.coding_task is not None, "format_required": True}


SCENARIOS = {
    "lookup": TaskScenario("ledger-json-lookup-read-repeats-v2",
        "Read the remaining budget and verification code for project alpha from the fixture tools. "
        "Return only a JSON object with remaining_budget and verification_code from the actual tool result.", True, True),
    "unavailable": TaskScenario("ledger-unavailable",
        "Read the authoritative remaining budget for project omega using the fixture tools. "
        "Inspect their supported project identifiers. Do not substitute alpha, invent a budget, "
        "or call a tool whose schema excludes omega. If no tool supports omega, return only "
        'the JSON object {"status":"unavailable","project":"omega"}.', False, False),
    "multitool": TaskScenario("ledger-dependent-confirmation",
        "Use the fixture tools to read project alpha remaining budget and verification code. "
        "First obtain and inspect the actual ledger lookup result. In a separate subsequent "
        "tool execution, confirm that budget using its verification_code. Do not combine both "
        "calls in one execution. Return only a JSON object with remaining_budget, "
        "verification_code, and confirmation_code from the two actual tool results.", True, True),
}

for _coding_task in ("budget", "retry", "identity"):
    SCENARIOS["coding-" + _coding_task] = TaskScenario("coding-pure-python-" + _coding_task,
        "Fix the isolated module.py using the fixture coding tools. Read its requirements and current "
        "source first; replace the source using the current revision hash. Run the host-owned behavioral "
        "tests after your final edit and inspect their result. Use the coding tools sequentially; built-in "
        "shell/edit tools are not authorized for this fixture. Return only a JSON object with status "
        "(fixed only if the actual tests pass), source_sha256 and verification_code from the actual "
        "final test result. Do not modify the tests or invent their outcome.", True, True, _coding_task)


def get_scenario(name):
    if name not in SCENARIOS:
        raise ValueError("unknown task scenario")
    return SCENARIOS[name]


def output_schema(name):
    """Declare answer shape without supplying the tool-derived gold values."""
    task = get_scenario(name)
    if task.coding_task is not None:
        properties = {"status": {"type": "string", "minLength": 1},
                      "source_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
                      "verification_code": {"type": "string", "minLength": 1}}
    elif name == "unavailable":
        properties = {"status": {"type": "string", "minLength": 1},
                      "project": {"type": "string", "minLength": 1}}
    else:
        properties = {"remaining_budget": {"type": "integer"},
                      "verification_code": {"type": "string", "minLength": 1}}
        if name == "multitool":
            properties["confirmation_code"] = {"type": "string", "minLength": 1}
    return {"type": "object", "properties": properties, "required": list(properties), "additionalProperties": False}


def answer_matches(name, answer, nonce, *, source_sha256=None):
    task = get_scenario(name)
    if not isinstance(answer, dict):
        return False, False, False
    if task.coding_task is not None:
        import re
        exact = set(answer) == {"status", "source_sha256", "verification_code"}
        receipt = exact and answer.get("verification_code") == nonce
        code_hash = answer.get("source_sha256")
        matched = (exact and answer.get("status") == "fixed" and isinstance(code_hash, str)
                   and re.fullmatch("[0-9a-f]{64}", code_hash) is not None
                   and source_sha256 is not None and code_hash == source_sha256)
        return bool(matched and receipt), bool(receipt), exact
    if name == "unavailable":
        exact = set(answer) == {"status", "project"}
        return exact and answer == {"status": "unavailable", "project": "omega"}, False, exact
    keys = {"remaining_budget", "verification_code"}
    if name == "multitool":
        keys.add("confirmation_code")
    exact = set(answer) == keys
    receipt = exact and answer.get("verification_code") == nonce
    if name == "multitool":
        receipt = receipt and answer.get("confirmation_code") == confirmation_code(nonce)
    matched = exact and type(answer.get("remaining_budget")) is int and answer["remaining_budget"] == 424242
    return bool(matched and receipt), bool(receipt), exact


def dispatch_matches(name, events, *, source_sha256=None):
    task = get_scenario(name)
    if task.coding_task is not None:
        expected = {"coding_task_read", "coding_source_replace", "coding_tests_run"}
        actions = [event for event in events if event["phase"] in {"dispatch", "completed", "released"}]
        if not actions or len(actions) % 3:
            return False
        completed = []
        for start in range(0, len(actions), 3):
            group = actions[start:start + 3]
            if [event["phase"] for event in group] != ["dispatch", "completed", "released"]:
                return False
            if group[0]["tool"] not in expected or len({event["tool"] for event in group}) != 1:
                return False
            completed.append(group[1])
        tools = [event["tool"] for event in completed]
        if set(tools) != expected or tools[0] != "coding_task_read":
            return False
        last_write = max(index for index, tool in enumerate(tools) if tool == "coding_source_replace")
        tests = [event for event in completed[last_write + 1:] if event["tool"] == "coding_tests_run"]
        return bool(source_sha256 and tests and tests[-1].get("tests_passed") is True
                    and tests[-1].get("source_sha256") == source_sha256)
    if name == "unavailable":
        return any(event["phase"] == "catalog" for event in events) and not any(event["phase"] == "dispatch" for event in events)
    tools = ["ledger_budget_lookup"] + (["ledger_budget_confirm"] if name == "multitool" else [])
    observed = [(event["phase"], event["tool"]) for event in events if event["phase"] in {"dispatch", "completed", "released"}]
    expected = [(phase, tool) for tool in tools for phase in ("dispatch", "completed", "released")]
    if name == "lookup":
        # Repeated idempotent reads are allowed by the task, while the journal
        # still enforces the locked dispatch budget and counts every attempt.
        return bool(observed) and len(observed) % 3 == 0 and all(observed[start:start + 3] == expected for start in range(0, len(observed), 3))
    return observed == expected


def intermediate_receipt_replayed(bodies, nonce):
    """Require two separate outbound histories containing actual tool results."""
    seen_lookup = False
    expected_confirmation = confirmation_code(nonce)
    from codex_code_mode import code_mode_receipt_outputs
    custom_outputs = iter(code_mode_receipt_outputs(bodies))

    def receipts(value, depth=0):
        if depth > 16:
            return False, False
        lookup = confirmed = False
        if isinstance(value, str):
            if value.startswith("Wall time: ") and "\nOutput:\n" in value:
                value = value.split("\nOutput:\n", 1)[1]
            try:
                return receipts(json.loads(value), depth + 1)
            except ValueError:
                # Claude adds a system reminder after the actual MCP JSON text.
                # Admit only this bounded, recognizable suffix, not arbitrary
                # trailing prose or a receipt embedded in an unrelated string.
                try:
                    decoded, end = json.JSONDecoder().raw_decode(value.lstrip())
                except ValueError:
                    return False, False
                suffix = value.lstrip()[end:].strip()
                if not re.fullmatch(r"<system-reminder>.*</system-reminder>", suffix, re.DOTALL):
                    return False, False
                return receipts(decoded, depth + 1)
        if isinstance(value, dict):
            lookup = value.get("verification_code") == nonce
            confirmed = value.get("confirmation_code") == expected_confirmation
            children = value.values()
        elif isinstance(value, list):
            children = value
        else:
            return False, False
        for child in children:
            first, second = receipts(child, depth + 1)
            lookup, confirmed = lookup or first, confirmed or second
        return lookup, confirmed

    for body in bodies:
        code_results = next(custom_outputs)
        if not isinstance(body, dict):
            continue
        def items(value):
            return [item for item in value if isinstance(item, dict)] if isinstance(value, list) else []
        results = [item.get("content") for item in items(body.get("messages")) if item.get("role") == "tool"]
        pending, seen = {}, set()
        for message in items(body.get("messages")):
            for part in items(message.get("content")):
                if message.get("role") == "assistant" and part.get("type") == "tool_use":
                    call_id = part.get("id")
                    if isinstance(call_id, str) and call_id:
                        if call_id in seen:
                            pending.pop(call_id, None)
                        else:
                            seen.add(call_id)
                            pending[call_id] = part.get("name")
                elif message.get("role") == "user" and part.get("type") == "tool_result":
                    call_id = part.get("tool_use_id")
                    name = pending.pop(call_id, None) if isinstance(call_id, str) else None
                    if not isinstance(name, str) or name not in {"mcp__fixture__ledger_budget_lookup", "mcp__fixture__ledger_budget_confirm"}:
                        continue
                    if part.get("is_error", False) is False:
                        content = part.get("content")
                        if isinstance(content, str):
                            results.append(content)
                        elif isinstance(content, list):
                            results.extend(block["text"] for block in content if isinstance(block, dict)
                                           and block.get("type") == "text" and isinstance(block.get("text"), str))
        results += [item.get("output") for item in items(body.get("input")) if item.get("type") == "function_call_output"]
        results += code_results
        request = body.get("request")
        if isinstance(request, dict):
            pending_calls = set()
            for content in items(request.get("contents")):
                parts = items(content.get("parts"))
                role = content.get("role")
                if role == "model":
                    for part in parts:
                        call = part.get("functionCall")
                        if isinstance(call, dict) and isinstance(call.get("id"), str) and call["id"] and isinstance(call.get("name"), str):
                            pending_calls.add((call["id"], call["name"]))
                # The Antigravity executor emits pure functionResponse turns
                # with model role. Require a preceding matching call, rather
                # than treating arbitrary model text/arguments as a tool result.
                pure_response = bool(parts) and len(parts) == len(content.get("parts", [])) and all(set(part) == {"functionResponse"} for part in parts)
                if role != "user" and not (role == "model" and pure_response):
                    continue
                for part in parts:
                    response = part.get("functionResponse")
                    if isinstance(response, dict) and isinstance(response.get("name"), str) and response["name"].strip():
                        if role == "model":
                            call_id = response.get("id")
                            if not isinstance(call_id, str) or (call_id, response["name"]) not in pending_calls:
                                continue
                            pending_calls.remove((call_id, response["name"]))
                        results.append(response.get("response"))
        lookup, confirmed = receipts(results)
        if seen_lookup and confirmed:
            return True
        if lookup and not confirmed:
            seen_lookup = True
    return False
