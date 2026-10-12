"""Fixed task gold for positive, unavailable, and dependent multi-tool runs."""

from dataclasses import dataclass
import json

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
    "lookup": TaskScenario("ledger-json-lookup",
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
    return observed == expected


def intermediate_receipt_replayed(bodies, nonce):
    """Require two separate outbound histories containing actual tool results."""
    seen_lookup = False
    expected_confirmation = confirmation_code(nonce)

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
                return False, False
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
        results = [item.get("content") for item in body.get("messages", []) if item.get("role") == "tool"]
        results += [item.get("output") for item in body.get("input", []) if item.get("type") == "function_call_output"]
        lookup, confirmed = receipts(results)
        if seen_lookup and confirmed:
            return True
        if lookup and not confirmed:
            seen_lookup = True
    return False
