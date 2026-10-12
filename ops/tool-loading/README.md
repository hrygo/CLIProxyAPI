# Tool loading acceptance infrastructure

This directory contains the scoped evidence schema and a synthetic MCP fixture.
Version-pinned client adapters and a local first-request probe are available.
Live scheduling, statistical certification and live evidence remain pending.
Offline case replay and request-level usage normalization are
available below. Fixture tests do not certify
Codex/OpenCode permission handling, model quality, or context savings.

## Native client isolation and first-request probe

`client_adapters.py` supports exactly Codex CLI `0.160.0` and OpenCode `1.19.0`.
It checks the installed version before creating artifacts, rejects unknown
adapter keys/modes and non-loopback endpoints, and never inherits credentials
or broad experimental flags. All configuration, catalogs, XDG state and the
Codex home are run-local. Model metadata for real tests must be supplied for
the actual selected model; `synthetic_codex_model` is only for local probes.

Codex uses strict CLI overrides and the model's `supports_search_tool` gate,
with direct tool mode. There is no fabricated `namespace_tools` configuration
key or legacy `features.tool_search` flag. Whether both effective gates hold
is established by the outbound request, not the requested flag alone.
OpenCode V1 uses its explicit Code Mode environment flag; client-denied
underlying MCP tools remain separate from service-side denial.

With both exact CLI versions installed, run:

```sh
"$task_validation/venv/bin/python" ops/tool-loading/probe_clients.py \
  --output-dir "$task_validation/native-probe"
```

This starts a temporary loopback HTTP fixture and real MCP subprocesses, then
deliberately returns HTTP 400 to end inference. It checks eager declarations
versus lazy search/execute entry points, records every received request
(including OpenCode's title request), and keeps private request/output artifacts.
It does not use CLIProxyAPI or a real Provider and does not certify execution,
task quality, terminal semantics, or token savings. `gate_status` remains
`unverified`; Provider usage is null. Serialized tool-definition bytes are a
transport measurement, distinct from model input tokens.

## Opt-in upstream HTTP attempt evidence

`internal/acceptance.HTTPAttemptJournal` and
`helps.WithHTTPAttemptObserver` provide a request-scoped collector for isolated
acceptance harnesses. There is no production flag, default observer, or service
configuration change. Attach the hook to the incoming request context from
explicit harness middleware. The usage transport recovers that hook from the
existing Gin context when handlers use a fresh execution parent; it does not
inherit arbitrary values or deadlines. The selected proxy/TLS transport remains
in use. Nested usage wrappers produce one observation per physical dispatch.

Starts are synced before transport dispatch. Private request/response bodies
have exclusive filenames and SHA-256 digests; headers, URLs and transport-error
messages are excluded. Use only synthetic fixtures. Supply the independently
verified outbound wire contract and explicit request/body budgets. A missing
body-copy handle, exhausted attempt budget or failed capture setup prevents
dispatch. Later capture failures fail the evidence run through `Close`.
Response capture is bounded without changing bytes delivered to the executor.
An early close, read error, truncated capture or unfinished start cannot become
complete usage evidence. HTTP EOF does not establish a legal protocol terminal.

`http_attempts.py` validates sequences, identities, allowlisted metadata, body
sizes/digests and regular files, rejecting symlink artifacts and duplicate
finishes. It consolidates supported usage snapshots without summing cumulative
tokens. Missing/conflicting usage remains null. `import_http_attempts` imports
all attempts into an already-started `CaseJournal` before the case is finished;
its post-run records refer to the upstream journal's pre-dispatch evidence.
Task checks, MCP boundaries, coding results and protocol terminals are separate.

Reproduce real gateway handler → bridge → local HTTP → digest verification:

```sh
task_http_dir=$(mktemp -d)
TOOL_LOADING_TEST_HTTP_EVIDENCE_DIR="$task_http_dir/capture" \
  GOTOOLCHAIN=go1.26.4 go test ./test \
  -run TestAcceptanceHTTPJournalThroughResponsesHandler -count=1
PYTHONPATH=ops/tool-loading python3 - "$task_http_dir/capture" <<'PY'
from pathlib import Path
import sys
from http_attempts import read_http_attempts
records = read_http_attempts(Path(sys.argv[1]))
assert len(records) == 1
assert records[0]["raw_usage"] == {"input_tokens": 31, "output_tokens": 2}
print("local gateway HTTP evidence verified; live certification remains unknown")
PY
```

The exported fixture usage above is scripted, not measured Provider tokens.
The buffered and streaming tests use the actual Responses handler, auth manager,
Codex executor and bridge with a temporary local upstream. WebSocket traffic,
other handler/provider routes, real-client live runs, scope fingerprints,
immutable run export and statistical certification still require integration.

`run_task` bounds the isolated POSIX process group, cancellation and combined
stored stdout/stderr. It first sends SIGINT to the main CLI and drains capped
output during a two-second grace period, then terminates its process group.
`forced_process_cleanup` describes whether the main process still needed
termination after that grace period, not whether every descendant had exited.
It records exit/budget
outcomes; a zero process exit never establishes task success. Original-schema,
client permission and cancellation evidence must still come from actual MCP
calls and immutable artifacts before the live acceptance journal can certify
them. CPU/output process tests alone are not Code Mode runtime budget proof.

## Scripted native execution boundaries

With the same exact CLI versions and isolated SDK environment:

```sh
"$task_validation/venv/bin/python" ops/tool-loading/scripted_clients.py \
  --output-dir "$task_validation/scripted-clients"
```

This runs both clients in eager/lazy modes against deterministic local SSE
responses. Four scenarios cover an allowed ledger call, client denial while
the service permits the target, an invalid original-schema constant, and
service revocation after declaration/discovery but before invocation. Each
case records its start before CLI launch, every received request, responses,
MCP events, full client output and summary in a fresh private directory.
The service's catalog event proves positive target authorization independently
of client filtering; absence of MCP calls alone cannot establish client denial.

The local model learns the fresh nonce exclusively from the client's actual
tool-result receipt. Allowed cases require exactly one dispatch, matching
nonce in the final assistant message, zero effects/active operations and a
legal client terminal. Codex's wall-time output wrapper is decoded explicitly.
Denied cases require a returned error receipt and zero dispatch; client-denied
cases additionally require zero MCP requests. Invalid and revoked cases require
the corresponding service rejection reason. The revocation artifact ties the
generation change to the invocation, excluding the preceding Code Mode search.

These probes bypass CLIProxyAPI and real Providers. Scripted model decisions
do not test retrieval strategy, coding quality, reliability or token savings.
Native CLI usage defaults to zero when this fixture omits usage; those values
are not accepted as measurements. Summary usage remains null, task success is
unverified and the overall gate stays unverified. Cancellation, dynamic client
permission changes/index refresh and cumulative Code Mode limits remain
uncertified. The cancellation and budget probes below retain their failures.
Formal attempt accounting remains the
responsibility of the live journal and gateway collector, not this fixture.

## Cancellation and execution-budget diagnostics

Run cancellation after the actual MCP wait dispatch, independently of timing:

```sh
"$task_validation/venv/bin/python" ops/tool-loading/scripted_clients.py \
  --output-dir "$task_validation/native-cancel" --cases cancel
"$task_validation/venv/bin/python" ops/tool-loading/scripted_clients.py \
  --output-dir "$task_validation/native-limits" --clients opencode --modes lazy \
  --cases loop dispatch-budget large-result
```

`cancel` requires actual MCP cancellation/release events and zero active state;
process death alone does not pass. Missing cleanup events after group termination
are unverified cleanup, not proof that a resource survived process death.
The loop probe must return a runtime budget error with a legal client terminal;
outer process-budget termination is a failed runtime-limit probe. The dispatch
and large-result probes separately prove the synthetic service's limits,
not client-wide cumulative limits or Provider request limits.

Official cancellation APIs have separate profiles; they are not interchangeable
with CLI SIGINT. Both commands preserve every started/failed case:

```sh
"$task_validation/venv/bin/python" ops/tool-loading/scripted_clients.py \
  --output-dir "$task_validation/codex-app-cancel" --clients codex \
  --transport codex-app-server --cases cancel
"$task_validation/venv/bin/python" ops/tool-loading/scripted_clients.py \
  --output-dir "$task_validation/opencode-session-cancel" --clients opencode \
  --transport opencode-server --cases cancel
```

Codex binds initialize/thread/start/turn/start/turn/interrupt to actual thread and
turn identities, denies unrecognized server requests and waits for a matching
`interrupted` terminal plus MCP release. OpenCode starts a private loopback
headless server, sends a session prompt and aborts only after actual wait dispatch.
An abort response alone cannot pass: the matching session must be idle with an
assistant `MessageAbortedError`, and MCP active/effect counters must reach zero.
The OpenCode server profile currently supports cancellation only.

The stdio fixture records `protocol_cancel_received` before forwarding that
notification unchanged to the official MCP SDK. A real SDK stdio cancellation
test checks this observation and subsequent release. In the 2026-10-12 native
API probes, OpenCode eager/lazy passed with one notification and release each;
Codex eager/lazy returned an interrupted terminal but sent no observed MCP
cancel notification before the task budget expired. Scope, raw API/stdio traces
and client delivery boundaries are in the
[official cancellation API report](../../docs/acceptance/2026-10-12-client-native-cancellation_CN.md).

`client_sdk_probe.mjs` separately exercises the external OpenCode `v1.19.0`
CodeMode package with explicit limits. `opencode-codemode-source.json` pins all
25 TypeScript source files and three dependency versions before importing it.
Use an isolated extraction of the official tag with Bun; never apply changes to
the installed OpenCode executable or install packages globally. With Bun already
available, prepare the external package in the temporary validation directory:

```sh
curl -fL https://codeload.github.com/anomalyco/opencode/tar.gz/refs/tags/v1.19.0 \
  -o "$task_validation/opencode.tar.gz"
mkdir "$task_validation/codemode"
tar -xzf "$task_validation/opencode.tar.gz" --strip-components=3 \
  -C "$task_validation/codemode" opencode-1.19.0/packages/codemode
python3 -c 'import json,pathlib,sys; p=pathlib.Path(sys.argv[1]); d=json.loads(p.read_text()); d["dependencies"].update({"effect":"4.0.0-beta.83","typescript":"5.8.2"}); d.pop("devDependencies",None); p.write_text(json.dumps(d))' \
  "$task_validation/codemode/package.json"
bun install --cwd "$task_validation/codemode" \
  --cache-dir "$task_validation/bun-cache" --ignore-scripts
bun ops/tool-loading/client_sdk_probe.mjs \
  --sdk-root "$task_validation/codemode" \
  --output "$task_validation/sdk-summary.json"
```

The SDK probe currently exits **1**: timeout, call admission and cancellation
release pass, but a 256-byte serialized value limit produces 348 bytes. This
negative result is intentional evidence, not a green native-client regression.
The output path must be fresh. Native clients, gateway/Provider closure and
usage remain unverified by this SDK probe. See the
[client cancellation and runtime report](../../docs/acceptance/2026-10-12-client-runtime-limits_CN.md)
for the separate client delivery and exact evidence scope.

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

## Intent-to-treat case journal and replay

`run_acceptance.py` currently provides the `CaseSpec`/`CaseJournal` collection
interface and offline replay. It does not launch Codex, OpenCode, a gateway or
Provider requests. Client adapters and the final live scheduler remain separate
work. Each case declares its version/route scope, eager/lazy mode, cache condition,
prompt fingerprint, template cluster, fixture seed, task limits and gold checks.

An adapter must call `start_case` before launching a task and `start_request`
before every actual upstream attempt, including gateway retries. Allocate an
attempt ID before sending; a later Provider response ID cannot prove that an
interrupted request was counted. Consolidate streaming snapshots before calling
`record_request` with request-total usage. Finish checks require actual tool/nonce,
terminal and coding evidence from the adapter; reported booleans alone do not
certify those observations. Client exit code is only auxiliary evidence.

The single-writer journal is created exclusively with mode 0600, appended and
flushed/fsynced before returning. An existing path, repeated case/pair-mode,
request, or finish is rejected. Every started case remains in the denominator;
unfinished cases are failures. Started requests without settled usage invalidate
complete task input totals and cannot substantiate success. Budget overruns keep
all observed requests instead of deleting their costs. Counting an overrun does
not implement process termination; the client adapter still needs task limits.

One run cannot mix gateway revisions. Open a new run for changed assertions,
client/fixture contracts or candidates; retain earlier journals. Pair comparison
requires equal scope except the intentionally changed `client_mode`, plus equal
prompt, template, fixture seed, limits, permissions and cache condition. Missing
partners, mismatches or unknown scope produce no saving estimate. Failed pairs
are retained. Counts and latency observations are grouped by complete route,
client/version/mode and cache scope; repeated templates remain cluster labels.

```sh
"$task_validation/venv/bin/python" ops/tool-loading/run_acceptance.py \
  --replay /path/to/run/events.jsonl > /path/to/run/replay-report.json
"$task_validation/venv/bin/python" ops/tool-loading/validate_evidence.py \
  --journal /path/to/run/events.jsonl
```

The validator checks event ordering, transitions, fields, scope and accounting.
Truncated, reordered or malformed journals are rejected. Export uses explicit
fields and counter paths; arbitrary credential/content fields are discarded,
while malformed counters/cache objects retain a content-free invalid marker.
Do not encode secrets or personal information in identifiers. Original raw
Provider captures and their verified artifact digests still need to be connected
by the real collector; this journal is not a signed or tamper-proof artifact.

Every replay/validation report keeps `gate_status: unverified`. Structure checks
and descriptive paired savings do not prove 60 live cases, actual permission
enforcement, non-inferiority, benefit confidence intervals, latency/cost gates or
deployment readiness. Missing usage never becomes zero or a free failed task.
