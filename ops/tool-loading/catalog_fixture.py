"""Synthetic MCP catalog and execution boundary for isolated acceptance runs."""

from __future__ import annotations

import argparse
import asyncio
from collections import deque
from copy import deepcopy
import hashlib
import json
from pathlib import Path
from typing import Any

from jsonschema import Draft202012Validator

QUERY_GROUND_TRUTH = [
    {"query": "remaining budget and verification code for project alpha", "expected": ["ledger_budget_lookup"]},
    {"query": "查询 alpha 项目剩余预算和校验码", "expected": ["ledger_budget_lookup"]},
    {"query": "read archived inventory collection 12", "expected": ["collection_12_read"]},
]


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: Any) -> str:
    return hashlib.sha256(canonical_bytes(value)).hexdigest()


def mcp_result(value: dict[str, Any], is_error: bool = False) -> dict[str, Any]:
    return {"isError": is_error, "structuredContent": value,
            "content": [{"type": "text", "text": json.dumps(value)}]}


def build_catalog() -> list[dict[str, Any]]:
    tools = []
    for index in range(64):
        target = index == 37
        name = "ledger_budget_lookup" if target else f"collection_{index:02}_read"
        properties = {"project": {"type": "string", "const": "alpha", "description": "Synthetic project identifier. Use alpha."}}
        for field in range(36 if target else 12):
            properties[f"filter_{field:02}"] = {
                "type": "string", "maxLength": 128,
                "description": f"Optional archive filter {field:02}: a literal selector for stored record metadata. Omit when not filtering. This field does not change permissions or cause writes.",
            }
        tools.append({
            "name": name,
            "description": "Read the remaining budget and verification code for project alpha. Authoritative ledger lookup. 查询项目剩余预算和校验码。" if target else f"Read archived inventory collection {index:02}. Unrelated historical records; cannot provide project budget or verification codes.",
            "inputSchema": {"type": "object", "properties": properties, "required": ["project"], "additionalProperties": False},
            "annotations": {"readOnlyHint": True, "destructiveHint": False},
        })
    for name, description in [
        ("fixture_mutation", "Increment only a synthetic in-memory effect counter; denied by default."),
        ("fixture_wait", "Wait until cancelled; used only for cancellation acceptance."),
        ("fixture_large_result", "Return a bounded synthetic result for output-budget acceptance."),
    ]:
        schema = {"type": "object", "properties": {"project": {"type": "string", "const": "alpha"}}, "required": ["project"], "additionalProperties": False}
        if name == "fixture_large_result":
            schema["properties"]["size"] = {"type": "integer", "minimum": 0, "maximum": 1048576}
            schema["required"].append("size")
        tools.append({"name": name, "description": description, "inputSchema": schema,
                      "annotations": {"readOnlyHint": name != "fixture_mutation", "destructiveHint": name == "fixture_mutation"}})
    return tools


class InvalidPolicy(ValueError):
    pass


class CatalogFixture:
    def __init__(self, nonce: str, *, denied: set[str] | None = None,
                 max_dispatches: int = 8, max_result_bytes: int = 65536,
                 policy_file: Path | None = None, event_file: Path | None = None):
        if not nonce or max_dispatches < 1 or max_result_bytes < 256:
            raise ValueError("nonce and positive fixture budgets are required")
        self.nonce = nonce
        self.tools = {tool["name"]: tool for tool in build_catalog()}
        self.validators = {name: Draft202012Validator(tool["inputSchema"]) for name, tool in self.tools.items()}
        for tool in self.tools.values():
            Draft202012Validator.check_schema(tool["inputSchema"])
        self.catalog_hash = digest(list(self.tools.values()))
        self.denied = {"fixture_mutation"} if denied is None else set(denied)
        if not self.denied <= self.tools.keys():
            raise ValueError("unknown denied tool")
        self.policy_generation = 0
        self.policy_file, self.event_file = policy_file, event_file
        self.max_dispatches, self.max_result_bytes = max_dispatches, max_result_bytes
        self.dispatch_count = self.effect_count = self.active_count = self.attempt_count = 0
        self.events: deque[dict[str, Any]] = deque(maxlen=256)
        self.event_sequence = 0
        self.started, self.wait_gate, self.released = asyncio.Event(), asyncio.Event(), asyncio.Event()
        self._sync_policy()

    @property
    def permission_hash(self) -> str:
        return digest(sorted(self.denied))

    @property
    def index_generation(self) -> str:
        return digest([self.catalog_hash, self.permission_hash, self.policy_generation])

    def set_denied(self, denied: set[str]) -> None:
        if not denied <= self.tools.keys():
            raise InvalidPolicy("unknown denied tool")
        if denied != self.denied:
            self.denied = set(denied)
            self.policy_generation += 1

    def _sync_policy(self) -> None:
        if self.policy_file is None:
            return
        try:
            with self.policy_file.open("rb") as stream:
                document = stream.read(65537)
            if len(document) > 65536:
                raise InvalidPolicy("oversized permission document")
            value = json.loads(document)
            if not isinstance(value, dict) or set(value) != {"generation", "denied"}:
                raise InvalidPolicy("invalid permission document")
            generation, denied = value["generation"], value["denied"]
            if type(generation) is not int or generation < 1 or not isinstance(denied, list) or any(not isinstance(name, str) for name in denied):
                raise InvalidPolicy("invalid permission fields")
            denied = set(denied)
            if not denied <= self.tools.keys() or generation < self.policy_generation or (generation == self.policy_generation and denied != self.denied):
                raise InvalidPolicy("permission change needs a new generation")
            self.denied, self.policy_generation = denied, generation
        except (OSError, ValueError, TypeError) as error:
            raise InvalidPolicy("permission document could not be validated") from error

    def _record(self, phase: str, name: str, **details: Any) -> None:
        event = {"sequence": self.event_sequence, "phase": phase,
                 "tool": name if name in self.tools else "unknown",
                 "index_generation": self.index_generation,
                 "dispatch_count": self.dispatch_count, "effect_count": self.effect_count,
                 "active_count": self.active_count, **details}
        if self.event_file:
            with self.event_file.open("a", encoding="utf-8") as stream:
                stream.write(json.dumps(event, separators=(",", ":")) + "\n")
        self.events.append(event)
        self.event_sequence += 1

    def list_tools(self) -> list[dict[str, Any]]:
        self._sync_policy()
        exposed = [tool for name, tool in self.tools.items() if name not in self.denied]
        self._record("catalog", "catalog", exposed_count=len(exposed),
                     ledger_allowed="ledger_budget_lookup" not in self.denied,
                     policy_generation=self.policy_generation)
        return deepcopy(exposed)

    def _reject(self, name: str, code: str) -> dict[str, Any]:
        self._record("rejected", name, reason=code)
        return {"isError": True, "value": {"error": code}}

    async def call(self, name: str, arguments: Any) -> dict[str, Any]:
        self.attempt_count += 1
        try:
            self._sync_policy()
        except InvalidPolicy:
            self._record("attempt", name)
            return self._reject(name, "invalid_policy")
        self._record("attempt", name)
        if self.attempt_count > self.max_dispatches * 4:
            return self._reject(name, "attempt_budget")
        if name not in self.tools:
            return self._reject(name, "unknown_tool")
        if name in self.denied:
            return self._reject(name, "permission_denied")
        if not self.validators[name].is_valid(arguments):
            return self._reject(name, "invalid_arguments")
        if self.dispatch_count >= self.max_dispatches:
            return self._reject(name, "dispatch_budget")
        self.dispatch_count += 1
        self.active_count += 1
        try:
            self._record("dispatch", name)
            value: dict[str, Any] = {"project": "alpha"}
            if name == "fixture_wait":
                self.started.set()
                await self.wait_gate.wait()
                self._sync_policy()
                if name in self.denied:
                    return self._reject(name, "permission_revoked")
            if name == "fixture_large_result":
                value["data"] = "x" * arguments["size"]
            elif name == "ledger_budget_lookup":
                value.update(remaining_budget=424242, verification_code=self.nonce)
            elif name == "fixture_mutation":
                self.effect_count += 1
                value["effect_count"] = self.effect_count
            else:
                value["collection"] = name
            if len(canonical_bytes(mcp_result(value))) > self.max_result_bytes:
                return self._reject(name, "result_budget")
            self._record("completed", name, result_sha256=digest(value))
            return {"isError": False, "value": value}
        except asyncio.CancelledError:
            self._record("cancelled", name)
            raise
        except InvalidPolicy:
            return self._reject(name, "invalid_policy")
        finally:
            self.active_count -= 1
            self._record("released", name)
            self.released.set()


def create_server(fixture: CatalogFixture):
    from mcp import types
    from mcp.server.lowlevel import Server

    server = Server("tool-loading-acceptance-fixture", version="2.0.0")

    @server.list_tools()
    async def list_tools() -> types.ListToolsResult:
        return types.ListToolsResult(tools=[types.Tool(**tool) for tool in fixture.list_tools()],
                                    _meta={"fixture_index_generation": fixture.index_generation})

    # Validate the original declared schema in the fixture handler so rejected
    # attempts are observable even when an SDK has cached an older declaration.
    @server.call_tool(validate_input=False)
    async def call_tool(name: str, arguments: dict) -> types.CallToolResult:
        request_id = server.request_context.request_id
        fixture._record("request", name, request_id=request_id if type(request_id) is int else "string_id")
        before = fixture.index_generation
        result = await fixture.call(name, arguments)
        if fixture.index_generation != before:
            await server.request_context.session.send_tool_list_changed()
        return types.CallToolResult.model_validate(mcp_result(result["value"], result["isError"]))

    return server


async def serve(fixture: CatalogFixture) -> None:
    from mcp.server.lowlevel import NotificationOptions
    from mcp.server.stdio import stdio_server

    server = create_server(fixture)
    async with stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream,
                         server.create_initialization_options(notification_options=NotificationOptions(tools_changed=True)))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--nonce", required=True)
    parser.add_argument("--events", type=Path, required=True)
    parser.add_argument("--permissions", type=Path)
    parser.add_argument("--max-dispatches", type=int, default=8)
    parser.add_argument("--max-result-bytes", type=int, default=65536)
    args = parser.parse_args()
    asyncio.run(serve(CatalogFixture(args.nonce, event_file=args.events, policy_file=args.permissions,
                                    max_dispatches=args.max_dispatches, max_result_bytes=args.max_result_bytes)))


if __name__ == "__main__":
    main()
