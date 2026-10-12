# Tool loading acceptance infrastructure

This directory contains the scoped evidence schema and a synthetic MCP fixture.
Client adapters, the paired acceptance runner and live certification remain
pending. Request-level usage normalization is available below. Fixture tests do not certify
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

## Request-level usage contracts

`normalize_usage.py` decodes one consolidated request-total object using an
explicit contract: `openai-responses-v1`, `openai-chat-v1`,
`anthropic-messages-v1`, or `gemini-generate-v1`. These describe observed wire
formats, not model/provider names or certifications. An Antigravity, Muse or GLM
route must choose the contract actually captured at its measurement boundary;
this module does not infer semantics from a familiar field or model name.

```sh
python3 ops/tool-loading/normalize_usage.py --contract openai-responses-v1 < request-usage.json
```

Missing counters remain JSON null. OpenAI/Gemini input totals include cache;
Anthropic input totals require adding its three disjoint input buckets.
Reasoning is already included in OpenAI/Anthropic output totals; Gemini candidate
and thought counts are separate. Tool-use prompt counts are reported separately,
without assuming they add to the ordinary prompt or bill. Missing cache writes
cannot be invented as zero to obtain an uncached-input or price estimate.

`aggregate_usage` requires a unique ID for every Provider attempt, including
failed requests and retries, and rejects cumulative stream snapshots. The
collector must consolidate snapshots per request first. Missing or internally
contradictory usage invalidates complete totals and peak input; known valid
input is exposed as a separate subtotal. An empty observation list proves no
cost or input total. It does not prove no Provider request occurred.

`estimate_cost` uses explicit per-million prices, currency, price provenance and
known fixed/tool charges. Mixed Anthropic cache TTLs need their own write prices.
Incomplete prices or buckets return no complete cost; a known subtotal is
separate. Account subscriptions, model tiers, multimodal rates and other invoice
adjustments need independent billing evidence. These helpers do not certify
task quality, paired savings or confidence intervals.

Contract references verified on 2026-10-12:

- [OpenAI Responses usage fields](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_usage.py)
  and [Chat usage fields](https://github.com/openai/openai-python/blob/main/src/openai/types/completion_usage.py).
- [Anthropic input/cache accounting](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
  and [authoritative output total](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/usage.py).
- [Gemini UsageMetadata](https://ai.google.dev/api/generate-content#UsageMetadata).

Future upstream changes require reviewing these named contracts; a passing
synthetic decoder test alone does not prove any live route still honors them.
