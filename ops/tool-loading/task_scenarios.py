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

    @property
    def gold(self):
        return {"requires_dispatch": self.requires_dispatch, "requires_nonce": self.requires_nonce,
                "requires_code": False, "format_required": True}


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


def get_scenario(name):
    if name not in SCENARIOS:
        raise ValueError("unknown task scenario")
    return SCENARIOS[name]


def output_schema(name):
    """Declare answer shape without supplying the tool-derived gold values."""
    get_scenario(name)
    if name == "unavailable":
        properties = {"status": {"type": "string", "minLength": 1},
                      "project": {"type": "string", "minLength": 1}}
    else:
        properties = {"remaining_budget": {"type": "integer"},
                      "verification_code": {"type": "string", "minLength": 1}}
        if name == "multitool":
            properties["confirmation_code"] = {"type": "string", "minLength": 1}
    return {"type": "object", "properties": properties, "required": list(properties), "additionalProperties": False}


def answer_matches(name, answer, nonce):
    get_scenario(name)
    if not isinstance(answer, dict):
        return False, False, False
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


def dispatch_matches(name, events):
    get_scenario(name)
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
