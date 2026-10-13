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


def confirmation_code(nonce: str) -> str:
    return hashlib.sha256(("confirmed:" + nonce).encode()).hexdigest()


def mcp_result(value: dict[str, Any], is_error: bool = False) -> dict[str, Any]:
    return {"isError": is_error, "structuredContent": value,
            "content": [{"type": "text", "text": json.dumps(value)}]}


def build_catalog(coding_task: str | None = None, *, repository: bool = False) -> list[dict[str, Any]]:
    if coding_task is not None and repository:
        raise ValueError("coding and repository profiles are mutually exclusive")
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
    tools.append({"name": "ledger_budget_confirm",
                  "description": "Confirm the alpha ledger budget after its lookup completed. Pass the actual verification_code returned by ledger_budget_lookup; returns a separate confirmation_code.",
                  "inputSchema": {"type": "object", "properties": {
                      "project": {"type": "string", "const": "alpha"},
                      "verification_code": {"type": "string", "minLength": 1, "maxLength": 1024}},
                      "required": ["project", "verification_code"], "additionalProperties": False},
                  "annotations": {"readOnlyHint": True, "destructiveHint": False}})
    if coding_task is not None:
        from coding_tasks import TASKS, MAX_SOURCE_BYTES
        if coding_task not in TASKS:
            raise ValueError("unknown coding task")
        for name, description, properties, required in (
            ("coding_task_read", "Read the coding task requirements and current module.py source with its revision hash.", {}, []),
            ("coding_source_replace", "Replace only the isolated coding task's module.py. Supply its current source_sha256 as expected_sha256 and the complete new source.",
             {"expected_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
              "source": {"type": "string", "minLength": 1, "maxLength": MAX_SOURCE_BYTES}}, ["expected_sha256", "source"]),
            ("coding_tests_run", "Run host-owned behavioral tests for the current module.py; returns pass/fail, source_sha256 and a fresh verification_code. The client cannot modify the tests.", {}, []),
        ):
            tools.append({"name": name, "description": description,
                "inputSchema": {"type": "object", "properties": properties, "required": required, "additionalProperties": False},
                "annotations": {"readOnlyHint": name != "coding_source_replace", "destructiveHint": False}})
    if repository:
        from repository_project import repository_tools
        tools.extend(repository_tools())
    return tools


class InvalidPolicy(ValueError):
    pass


class CatalogFixture:
    def __init__(self, nonce: str, *, denied: set[str] | None = None,
                 max_dispatches: int = 8, max_result_bytes: int = 65536,
                 policy_file: Path | None = None, event_file: Path | None = None, coding_project=None,
                 repository_project=None):
        if not nonce or max_dispatches < 1 or max_result_bytes < 256:
            raise ValueError("nonce and positive fixture budgets are required")
        self.nonce = nonce
        if coding_project is not None and max_result_bytes < 32768:
            raise ValueError("coding profile requires room for bounded source results")
        self.coding_project = coding_project
        self.repository_project = repository_project
        self.tools = {tool["name"]: tool for tool in build_catalog(coding_project.task if coding_project else None,
                                                                repository=repository_project is not None)}
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
        self.repository_audit_failed = False
        self.ledger_completed = False
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

    def _record_repository_outcome(self, phase: str, name: str, **details: Any) -> None:
        try:
            self._record(phase, name, **details)
        except OSError:
            # Losing an audit record cannot turn a committed revision into an
            # uncommitted failure. The receipt explicitly excludes certification.
            self.repository_audit_failed = True

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
        if name.startswith("repository_"):
            arguments = deepcopy(arguments)
        if not self.validators[name].is_valid(arguments):
            return self._reject(name, "invalid_arguments")
        if name == "ledger_budget_confirm":
            if not self.ledger_completed:
                return self._reject(name, "missing_lookup")
            if arguments["verification_code"] != self.nonce:
                return self._reject(name, "invalid_receipt")
        if self.dispatch_count >= self.max_dispatches:
            return self._reject(name, "dispatch_budget")
        if name.startswith("coding_"):
            try:
                snapshot = self.coding_project.read()
                if name == "coding_source_replace":
                    from coding_tasks import MAX_SOURCE_BYTES
                    if len(arguments["source"].encode()) > MAX_SOURCE_BYTES:
                        return self._reject(name, "invalid_arguments")
                    if arguments["expected_sha256"] != snapshot["source_sha256"]:
                        return self._reject(name, "source_revision_mismatch")
            except (OSError, ValueError):
                return self._reject(name, "source_boundary")
        if name.startswith("repository_"):
            from repository_project import RepositoryError
            def authorize_repository():
                self._sync_policy()
                if name in self.denied:
                    raise RepositoryError("permission_revoked")
            try:
                snapshot = self.repository_project.read()
                if name == "repository_patch":
                    preview = self.repository_project.preview_patch(arguments["expected_revision"], arguments["changes"])
                    budget_preview = {**preview, "evidence_complete": False}
                    uncertain = {"error": "repository_commit_uncertain", **budget_preview}
                    if max(len(canonical_bytes(mcp_result(budget_preview))),
                           len(canonical_bytes(mcp_result(uncertain, True)))) > self.max_result_bytes:
                        return self._reject(name, "result_budget")
                elif len(canonical_bytes(mcp_result({**snapshot, "evidence_complete": False}))) > self.max_result_bytes:
                    return self._reject(name, "result_budget")
                authorize_repository()
            except InvalidPolicy:
                return self._reject(name, "invalid_policy")
            except RepositoryError as error:
                return self._reject(name, error.code)
        self.dispatch_count += 1
        self.active_count += 1
        value: dict[str, Any] = {"project": "alpha"}
        try:
            self._record("dispatch", name)
            if name.startswith("coding_"):
                from coding_tasks import TASKS, LANGUAGE_CONTRACT, async_evaluate_source
                if name == "coding_task_read":
                    value = {**snapshot, "requirements": TASKS[self.coding_project.task].requirements,
                             "language_contract": LANGUAGE_CONTRACT}
                elif name == "coding_source_replace":
                    updated = self.coding_project.replace(arguments["expected_sha256"], arguments["source"])
                    value = {"source_sha256": updated["source_sha256"]}
                else:
                    value = {**await async_evaluate_source(self.coding_project.task, snapshot["source"]),
                             "source_sha256": snapshot["source_sha256"], "verification_code": self.nonce}
            elif name.startswith("repository_"):
                from repository_project import RepositoryCommitUncertain
                try:
                    value = (snapshot if name == "repository_read" else self.repository_project.patch(
                        arguments["expected_revision"], arguments["changes"], authorize=authorize_repository))
                except RepositoryCommitUncertain as error:
                    self._record_repository_outcome("commit_uncertain", name, repository_revision=error.receipt["revision"],
                                                    changed_files=error.receipt["changed_files"])
                    value = {"error": error.code, **error.receipt, "evidence_complete": not self.repository_audit_failed}
                    return {"isError": True, "value": value}
            elif name == "fixture_wait":
                self.started.set()
                await self.wait_gate.wait()
                self._sync_policy()
                if name in self.denied:
                    return self._reject(name, "permission_revoked")
            if name.startswith(("coding_", "repository_")):
                pass
            elif name == "fixture_large_result":
                value["data"] = "x" * arguments["size"]
            elif name == "ledger_budget_lookup":
                value.update(remaining_budget=424242, verification_code=self.nonce)
            elif name == "ledger_budget_confirm":
                value.update(confirmation_code=confirmation_code(self.nonce))
            elif name == "fixture_mutation":
                self.effect_count += 1
                value["effect_count"] = self.effect_count
            else:
                value["collection"] = name
            if len(canonical_bytes(mcp_result(value))) > self.max_result_bytes:
                return self._reject(name, "result_budget")
            if name == "ledger_budget_lookup":
                self.ledger_completed = True
            if name.startswith("repository_"):
                value["evidence_complete"] = not self.repository_audit_failed
            details = {"result_sha256": digest(value)}
            if name.startswith("coding_"):
                details["source_sha256"] = value["source_sha256"]
                if name == "coding_tests_run": details["tests_passed"] = value["passed"]
            if name.startswith("repository_"):
                details["repository_revision"] = value["revision"]
                if name == "repository_patch":
                    details["changed_files"] = value["changed_files"]
            if name.startswith("repository_"):
                self._record_repository_outcome("completed", name, **details)
                value["evidence_complete"] = not self.repository_audit_failed
            else:
                self._record("completed", name, **details)
            return {"isError": False, "value": value}
        except asyncio.CancelledError:
            self._record("cancelled", name)
            raise
        except InvalidPolicy:
            return self._reject(name, "invalid_policy")
        except (OSError, ValueError) as error:
            if name.startswith("coding_"):
                return self._reject(name, "source_boundary")
            if name.startswith("repository_"):
                from repository_project import RepositoryError
                return self._reject(name, error.code if isinstance(error, RepositoryError) else "repository_boundary")
            raise
        finally:
            self.active_count -= 1
            if name.startswith("repository_"):
                self._record_repository_outcome("released", name)
                if self.repository_audit_failed:
                    value["evidence_complete"] = False
            else:
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
    import anyio
    from mcp import types
    from mcp.server.lowlevel import NotificationOptions
    from mcp.server.stdio import stdio_server

    server = create_server(fixture)
    async with stdio_server() as (read_stream, write_stream):
        observed_send, observed_read = anyio.create_memory_object_stream(0)
        async def relay():
            async with observed_send:
                async for message in read_stream:
                    rpc = getattr(getattr(message, "message", None), "root", None)
                    if isinstance(rpc, types.JSONRPCNotification) and rpc.method == "notifications/cancelled":
                        request_id = (rpc.params or {}).get("requestId")
                        fixture._record("protocol_cancel_received", "unknown",
                                        request_id=request_id if type(request_id) is int else "string_id")
                    await observed_send.send(message)
        async with anyio.create_task_group() as group:
            group.start_soon(relay)
            try:
                await server.run(observed_read, write_stream,
                                 server.create_initialization_options(notification_options=NotificationOptions(tools_changed=True)))
            finally:
                group.cancel_scope.cancel()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--nonce", required=True)
    parser.add_argument("--events", type=Path, required=True)
    parser.add_argument("--permissions", type=Path)
    parser.add_argument("--max-dispatches", type=int, default=8)
    parser.add_argument("--max-result-bytes", type=int, default=65536)
    parser.add_argument("--coding-task", choices=["budget", "retry", "identity"])
    parser.add_argument("--coding-workspace", type=Path)
    parser.add_argument("--repository-workspace", type=Path)
    parser.add_argument("--repository-owner")
    args = parser.parse_args()
    if (args.coding_task is None) != (args.coding_workspace is None):
        parser.error("coding task and private workspace must be supplied together")
    if (args.repository_workspace is None) != (args.repository_owner is None):
        parser.error("repository workspace and host-owned identity must be supplied together")
    if args.coding_task is not None and args.repository_workspace is not None:
        parser.error("coding and repository profiles are mutually exclusive")
    project = None
    if args.coding_task is not None:
        from coding_tasks import CodingProject
        project = CodingProject(args.coding_workspace, args.coding_task)
    repository = None
    if args.repository_workspace is not None:
        from repository_project import RepositoryProject
        repository = RepositoryProject.open(args.repository_workspace, args.repository_owner)
    asyncio.run(serve(CatalogFixture(args.nonce, event_file=args.events, policy_file=args.permissions,
                                    max_dispatches=args.max_dispatches, max_result_bytes=args.max_result_bytes,
                                    coding_project=project, repository_project=repository)))


if __name__ == "__main__":
    main()
