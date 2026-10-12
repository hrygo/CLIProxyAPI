# Tool loading acceptance infrastructure

This directory contains the scoped evidence schema and a synthetic MCP fixture.
Client adapters, the paired acceptance runner, usage normalization and live
certification remain pending. Passing these fixture tests does not certify
Codex/OpenCode permission handling, model quality, or context savings.

## Isolated dependencies and offline checks

Use Python 3.11 or later. The official MCP Python SDK is pinned to
[v1.26.0](https://github.com/modelcontextprotocol/python-sdk/releases/tag/v1.26.0),
not the current SDK main branch. This version and its installed dependency
versions were verified on 2026-10-12. The fixture uses the SDK for initialization,
stdio, request handling and cancellation; it does not implement JSON-RPC itself.

From the repository root:

```sh
task_validation=$(mktemp -d)
python3 -m venv "$task_validation/venv"
"$task_validation/venv/bin/python" -m pip install -r ops/tool-loading/requirements-mcp.txt
"$task_validation/venv/bin/python" -m unittest discover -s ops/tool-loading/tests -p 'test_*.py'
```

Package installation requires the package index. The tests themselves use
local processes/in-memory transports and make no Provider requests. CI runs
the same offline checks in a temporary environment. The dependencies never
enter the Go server or modify the system Python environment.

## Catalog and execution boundaries

The registered catalog contains 67 synthetic tools: 64 inventory/ledger tools,
one mutation-counter tool, one cancellable wait and one large-result tool.
`QUERY_GROUND_TRUTH` identifies expected tools for representative English and
Chinese queries; it is a retrieval oracle, not a replacement search engine.
The complete registered catalog has a stable hash independent of the nonce.
Permission filtering changes the exposed list and index generation, while
preserving the registered catalog. Compare eager/lazy with identical permissions.

The fixture checks its original Draft 2020-12 JSON Schemas before dispatch,
including required fields, types, constants and additional properties. It
rechecks current permission state on every call, so an old declaration cannot
grant execution permission. SDK input validation is disabled only at the
handler wrapper to record every rejected attempt; the fixture's validator
still runs. An SDK warning about an unlisted tool describes its own cache,
not the fixture's authorization or validation behavior.

Default `fixture_mutation` permission is denied. Even when explicitly allowed,
it changes only an in-memory counter; no production filesystem or service is
modified. A successful ledger call returns the run's synthetic verification
nonce. The acceptance runner must check that the client actually uses it.

## Launching an isolated fixture

An MCP client launches this command as a stdio subprocess; stdout carries only
SDK protocol traffic. Supply a new synthetic nonce for each run. Paths below
are run artifacts, not client defaults or credentials:

```sh
case_nonce=$("$task_validation/venv/bin/python" -c 'import secrets; print(secrets.token_hex(16))')
"$task_validation/venv/bin/python" ops/tool-loading/catalog_fixture.py \
  --nonce "$case_nonce" --events "$task_validation/events.jsonl" \
  --max-dispatches 8 --max-result-bytes 65536
```

For permission-change cases, add `--permissions /path/to/run/permissions.json`:

```json
{"generation":1,"denied":["fixture_mutation"]}
```

Update this run-local file atomically and increase `generation` whenever the
permission set changes. Malformed, unknown-tool, stale-generation and oversized
documents fail closed. `tools/list` reports the index generation in `_meta`;
observed changes during calls emit `notifications/tools/list_changed`.
There is no background filesystem watcher. Call/list boundaries observe the
new policy; this alone does not prove a client's discovery index refreshes.

## Accounting and limits

The JSONL event file retains every recorded boundary with monotonic sequence
numbers. Events distinguish request, attempt, rejection, dispatch, completion,
cancellation and release, and record permission/index generation and separate
dispatch/effect/active counters. They omit arguments and nonce values. Unknown
tool names are replaced with `unknown`. The in-process event view keeps at
most 256 recent records; use the full JSONL file for acceptance denominators.

The dispatch budget is shared across the fixture process, not reset per tool
call. Admission also stops after four times that many attempts. The result
budget covers the serialized MCP result value, including both text and
structured content, but excludes the outer JSON-RPC envelope. Oversize output
returns `result_budget`, never silent truncation. Cancellation records no
completion and releases active state. A waiting call rechecks permission before
returning its result.

These service limits do not bound a client's CPU loop or terminate an entire
coding task. The client adapter must separately enforce task/process budgets.
Client-deny tests must keep the fixture-side permission allowed and verify zero
MCP attempts; service-deny tests verify zero dispatch/effects. Testing both
locations prevents the service guard from hiding a client permission bypass.
