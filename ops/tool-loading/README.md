# Tool loading acceptance infrastructure

This directory contains the scoped evidence schema and a synthetic MCP fixture.
Version-pinned client adapters and a local first-request probe are available.
Predeclared paired live scheduling and bounded Chat/Responses calibration are available;
the final multi-route matrix and statistical certification remain pending.
Read-only paired diagnostics are available in `analyze_pairs.py`; their
conditional bounds never promote a run to a certification label.
Offline case replay and request-level usage normalization are
available below. Fixture tests do not certify
Codex/OpenCode permission handling, model quality, or context savings.

## Local HTTP admission diagnostics

The isolated `live-gateway` writes private `capture/admission.json` after SDK
shutdown and the response-body release barrier. Version 1 records the explicit
attempt limit, reserved capture slots, local budget/closed/failed rejection
counts, active bodies and journal closure/failure flags. Reservations are not
physical HTTP requests: only durable request starts in `attempts.jsonl` count as
observed physical attempts. Rejected calls never create artificial attempts.

The Python reader validates the fixed schema, actual locked limit, journal count
and closed/inactive state. Missing old snapshots stay unknown. Reaching the
attempt limit alone does not prove a denial; an observed `budget_rejections`
counter is required. Local capture/closure failures take precedence over budget
attribution. Actual captured upstream HTTP statuses of 400 or higher are reported
as `provider` when no local evidence/admission failure takes precedence; that is
the response boundary, not proof of the underlying cause or Provider overload.
HTTP-200 embedded errors and unrelated client/MCP budgets are not classified by
this helper. Existing semantic/permission/usage checks still apply.

`live_clients.py` retains the diagnostic in each task summary. A new candidate
without a valid admission artifact cannot gain a passing task through this
runner. Old evidence can still be replayed without adding zero-valued diagnostics
or rewriting historical failure classes. These controls are opt-in acceptance
instrumentation, not changes to production timeouts, retry policy or defaults.

## Paired diagnostic analysis

```sh
python3 ops/tool-loading/analyze_pairs.py \
  --journal "$task_validation/live/cases.jsonl" \
  --plan "$task_validation/live/batch-plan.json" \
  > "$task_validation/paired-analysis.json"
```

The supplied plan must validate against the journal. A single run/revision is
analyzed at a time. Exact scope, cache condition, limits, template, prompt,
gold and fixture seed must match within each pair; different client versions,
providers, profiles or revisions are not pooled. Unknown-but-equal scope permits
explicit descriptive paired input ratios, while the ordinary replay retains its
`comparable=false` result. Both reports keep `gate_status=unverified`.

All starts, failed cases, unfinished cases, requests and unpaired cases remain
visible. Missing or unmatched pairs block aggregate benefit statistics; known
subtotals are never substituted for missing complete-task costs. Negative
savings remain negative. These metrics are Provider input usage including cached
input, not bills. First input refers to the first actual observed request.

Repeats are grouped by the predeclared template ID. The quality diagnostic
uses the negative one-sided exact binomial upper bound on the probability of a
template cluster containing any eager-success/lazy-failure pair, ignoring
improvements. This conservatively bounds the equal-template mean of the
within-template success differences **only under the stated sampling assumptions**.
Zero observed regressions among three independent clusters still gives a lower
bound of approximately -0.632; it does not prove non-inferiority.

Benefit diagnostics report the median of within-template paired-savings medians
and a binomial order-statistic lower bound. Three template clusters have no finite
one-sided 95% lower bound, regardless of how many times they are repeated.
The independent, identically sampled representative-cluster and fixed repeat
design assumptions are unverified by a journal. This module reports conditional
diagnostics, cannot declare formal gates passed, does not perform a power
calculation, and does not certify latency, cache effectiveness or billing.

## Native client isolation and first-request probe

Both clients support an explicit `--structured-output` acceptance profile.
The gold and prompt are unchanged. Codex receives a private `--output-schema`
file; OpenCode uses its native HTTP session `format.json_schema` and
`StructuredOutput` tool. The schema specifies shape, never the positive task's
budget or fresh receipts. The opt-in policy, permission addition and adapter
sources enter the locked fixture scope and pair ID. Default CLI profiles remain
unchanged and must not be pooled with the structured profile.

OpenCode creates a fresh isolated session and sends exactly one synchronous
prompt request on a bounded background thread, preserving the actual request
and response. Success requires that response's matching session/assistant,
parent ID, completed message time, legal finish, idle session, valid structured
document and exactly one completed `StructuredOutput` call with identical input.
Shutdown must release the reader thread; forced process cleanup prevents success.
The normal server shutdown exit (-SIGTERM) is separate from session completion
and does not certify interruption. Actual MCP dispatch/receipt/gold and all
upstream terminals are still checked independently.

On the tested OpenCode 1.19.0 build, an asynchronous structured prompt followed
by reading all messages returns HTTP 400 while encoding the user's output-format
object. This client history-API gap is retained in the acceptance report; the
synchronous prompt endpoint returns the actual assistant response without that
history read. Neither usage nor answer text is synthesized. Native Codex and
OpenCode first-request constraints can be checked with
`probe_clients.py --structured-output --opencode-title "Tool loading acceptance"`;
this local probe intentionally returns HTTP 400 and does not certify execution.

### Explicit coding pilot profile

`live_clients.py --scenarios coding-budget coding-retry coding-identity` selects
three independent pure-function tasks: posted/refund budget arithmetic, retry
eligibility before/after output exposure, and stable call-ID deduplication. The
normal lookup/unavailable/multitool profiles retain their original permissions.
This is an initial coding corpus, not the full representative Coding suite.

The private Git workspace contains one editable `module.py`. Only this explicit
MCP profile advertises `coding_task_read`, `coding_source_replace` and
`coding_tests_run`; source replacement requires the current SHA-256. Built-in
shell remains read-only in Codex and denied in OpenCode. An MCP server is a
separate execution boundary: the Codex shell sandbox alone does not make MCP
tools read-only. The profile's permission fingerprint explicitly grants this
one synthetic source write, without changing operator permissions or config.

Host-owned tests never reside in the client workspace and cannot be changed
through these tools. The runner independently scores the final source after
the client exits, saves `code-outcome.json` and `source.diff`, and requires a
changed source, passing behavior, final source-hash/receipt use, and sequential
read/edit/test dispatches with every release observed. A valid final JSON claim
alone fails the code gate. The code outcome, full catalog and explicit permission
profile are locked before task start; eager/lazy use the same task and gold.

Execution supports only the documented pure Python subset, in a fresh `-I -S`
worker with no inherited environment. Imports, attributes, dynamic calls,
decorators, annotations, comprehensions and exponentiation are rejected. AST
statement and collection bounds apply on all admitted POSIX platforms; CPU and
parent process limits cap execution, and Linux also caps address space. Darwin
does not admit lowering RLIMIT_DATA/AS in the tested runtime, so this profile
does not claim a kernel heap limit there. This is not a general Python sandbox.
Cancellation kills and joins the checker before the MCP callback is released.
Symlinks/hardlinks and workspace identity changes are rejected, and the source
revision is checked on the write descriptor before truncation. Allowed source
edits are separate from the forbidden synthetic effect counter, which must stay
zero. Worker failures are framework failures, not successful model outcomes.

Use the existing bounded live command with those scenarios, optionally adding
`--structured-output --opencode-title "Tool loading coding pilot"` for the
OpenCode native-output profile. Provider limitations still apply: a route that
rejects Codex JSON schema is not made compatible by this fixture. Every started
case, failed code check, request and MCP attempt remains in the same journal.
Three templates are insufficient for the formal non-inferiority gate.

`client_adapters.py` supports exactly Codex CLI `0.160.0` and OpenCode `1.19.0`.
It checks the installed version before creating artifacts, rejects unknown
adapter keys/modes and non-loopback endpoints, and never inherits credentials
or broad experimental flags. All configuration, catalogs, XDG state and the
Codex home are run-local. Each task workspace has its own Git root, initialized
without operator Git configuration or templates, so OpenCode's ancestor rule
lookup stops at the task boundary. `--pure` disables plugins; it does not disable
instruction files. Verify the actual system request before claiming complete
runtime isolation. Model metadata for real tests must be supplied for
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
Transport completeness is never rewritten. A closed Chat/Responses SSE body may
supply usage only with intact frame boundaries, a verified protocol terminal,
consistent identity, successful status and no capture truncation. Read errors,
unfinished starts and unproven terminals retain unknown usage. HTTP EOF does not
establish a legal protocol terminal or release the underlying response body.
`CloseContext` freezes admission and waits for actual body close, including after
EOF. A canceled wait retains unfinished evidence. Body Close errors fail sealing,
including after EOF, without capturing private source-error details. The isolated
live candidate
uses this barrier after SDK shutdown; the launcher owns its existing 35-second
cleanup budget and retains a forced exit as failure. No upstream deadline is added.

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

The registered catalog contains 68 synthetic tools: 64 inventory/ledger tools,
one mutation-counter tool, one cancellable wait, one large-result tool, and a
dependent ledger confirmation tool. Default service filtering exposes 67.
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

## Fixed live task scenarios

`live_clients.py --scenarios lookup unavailable multitool` predeclares three
templates per client in both modes. The default remains the lookup calibration.
Each template has a distinct pair ID and prompt hash, with identical gold and
limits within the eager/lazy pair. This expands the executable task set, not
the set of certified Provider routes. The runner still accepts only explicitly
identified Chat routes and native `codex`/`meta` Responses routes, and every
gate remains `unverified` until the corresponding evidence is sufficient.

`lookup` requires exactly one completed/released ledger dispatch and its fresh
receipt in plain final JSON. `unavailable` asks for project omega, which no
fixture schema admits; it requires a catalog observation, zero dispatches, and
exact unavailable JSON. This is a no-supported-tool outcome, not proof that the
client's search API returned an empty list. The model must not substitute alpha.

An explicit OpenCode CLI title can be predeclared with
`live_clients.py --opencode-title "Tool loading acceptance"`. This uses the
installed CLI's supported `--title` option in both modes and leaves the default
automatic-title profile unchanged. The local first-request probe accepts the
same option. It must actually eliminate the auxiliary title request; do not
filter a captured title request out of token accounting. Fixed-title results
describe that profile, not the default OpenCode workload.

The title policy is bound to the case's fixture hash and a distinct pair ID.
Fixture fingerprints also bind the fixture, client adapter, and scenario source
files. Switching title policy, source, or revision requires a fresh run; drift
from the locked case fails before candidate launch. Titles are supplied only
to the isolated CLI and are not written to operator configuration. Suppressing
auxiliary inference does not prove model format compliance or protocol quality.

`multitool` requires lookup completion followed by a separate confirmation
execution using its actual receipt. The fixture rejects confirmation before a
successful lookup or with a wrong receipt before dispatch. The final JSON must
use both receipt values. Outbound history must first contain the lookup result
without a confirmation result, then include confirmation in a later request.
Two calls inside one Code Mode execution do not prove this multi-round case.
Tool-result wrappers are decoded; user/system/assistant text and call arguments
cannot supply the intermediate receipt evidence. Extra dispatches or missing
completion/release events fail the task gold.

An installed CLI's first-request probe derives its expected exposed tool count
from the current catalog. Catalog changes invalidate previous scope fingerprints;
do not pool earlier calibration runs into a final-version matrix.

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

## Native clients through an isolated fixture gateway

`gateway_clients.py` launches the installed, pinned Codex/OpenCode clients,
the real gateway handlers/auth manager/executors, a scripted loopback upstream,
and the actual MCP fixture. This is protocol and evidence-chain verification;
it is not a live Provider benchmark. It never joins the sixty-case live sample.

```sh
GOTOOLCHAIN=go1.26.4 go build -o "$task_validation/tool-loading-gateway" ./ops/tool-loading/gateway
"$task_validation/venv/bin/python" ops/tool-loading/gateway_clients.py \
  --gateway-binary "$task_validation/tool-loading-gateway" \
  --output-dir "$task_validation/native-gateway-chat"
"$task_validation/venv/bin/python" ops/tool-loading/gateway_clients.py \
  --gateway-binary "$task_validation/tool-loading-gateway" \
  --executor codex --output-dir "$task_validation/native-gateway-responses"
```

Use the isolated SDK environment described above and new output directories.
The launcher filters the child environment, uses synthetic credentials only,
and checks the actual child PID, loopback address, executor and declared wire
contract before launching each client. The standalone gateway does not load
operator configuration or token stores. Always use this launcher; the shared
API constructor may read management environment settings if invoked directly.
No candidate is installed, and no existing service or default config is changed.

Every case starts before process launch. Gateway attempt journal v2 includes
the actual executor and SHA-256 fingerprints of its model and auth record ID;
empty or missing identity cannot pass the pinned executor import. Historical
v1 captures remain readable but cannot satisfy this identity check. Executor
identity does not infer a wire protocol: the isolated constructor pins that
contract explicitly. Hashes do not constitute full routing/config provenance.

The model fixture selects only actual declarations, learns opaque gateway
aliases from the returned manifest, and learns the verification nonce only
from a real MCP result. Success requires that result in the final answer, one
actual dispatch, legal client terminal, sealed attempt evidence and clean
gateway exit. OpenCode title requests remain in the request denominator but
do not advance the scripted tool state, including titles with built-in tools.
Responses fixtures provide the required usage `total_tokens` field.

Private artifacts include the binary digest, readiness/exit records, native
client output, MCP events, upstream bodies, attempt journals and case replay.
Captures are restricted to this synthetic fixture. Scripted counters test
decoding, not actual Provider tokens; reports retain null measured input and
`gate_status: unverified`. Serialized tool bytes are a transport metric only.
Live scope manifests, balanced scheduling and clustered quality/benefit gates
remain separate work.

## Read-only live candidate bootstrap

`live-gateway/` uses the official SDK's configuration, API-key synthesis,
executor registration and model alias initialization. Operator configuration
is read into memory. Native API-key profiles do not read the OAuth directory.
Native OAuth profiles require explicit `--auth-file-binding` arguments, each
containing a lowercase SHA-256 of one filename followed by a colon and its
content SHA-256. At most eight regular JSON files are selected within the
configured auth directory; unrelated file bodies are never loaded. Bindings
are part of the locked case fixture hash and readiness profile. Filename hashes
are scope identifiers, not secrets or authorization tokens.

Selected files use the production auth synthesizer, then a deep memory snapshot
removes source paths, storage, refresh tokens and Meta DCA exchange authority.
An explicit OAuth selection excludes configured API-key accounts for that
executor. The original files are checked against their bound content after
the candidate stops. Source configuration and credentials are never written
by the snapshot store; credentials are not copied to disk or command arguments.
Isolation readiness version 5 includes the credential rules and requires
trusted preparation diagnostics; older candidates cannot be used for a new run.

`live_clients.py` supports native `codex`/`meta` via the explicit
`openai-responses-v1` wire view. Namespace leaves, Lite historical
`input[].type = additional_tools` items' `tools` arrays, and native
`tool_search_output.tools` arrays are counted as declarations. Repeated wire
occurrences are retained in both counts and serialized-container bytes; active
tool deduplication must not erase transport overhead. Unrelated result fields
are not declarations, and malformed recognized containers reject the observation.
Successful terminals require
an actual completed response; failed/incomplete responses, broken frames,
identity conflicts and duplicate or trailing terminals cannot pass. The
independent usage reader may retain known usage for failed terminal responses,
which does not make the task successful. Antigravity, Gemini CLI/plugin and AI Studio relay
routes still need independent bootstrap, transport capture and wire gates;
this runner does not guess that they use the ordinary Gemini HTTP executor.
Offline bootstrap checks do not certify real Provider availability or quality.

The isolated candidate seals `capture/preparation.json` separately from physical
HTTP attempts and admission diagnostics. Only a typed `ToolCompatibilityError`
returned at the local preparation boundary produces an entry, containing a
request sequence, fixed reason, phase and status code. It never stores the error
message, schemas, arguments or credential identifiers. The HTTP execution context
preserves the optional observer even when a handler supplies a separate parent
context. Buffered, streaming and token-count entry points share this observation.

The reader requires sealed evidence with no active requests or recorder failure;
missing evidence remains unknown and fails the new calibration. A known local
declaration/attempt/shared-capacity rejection is classified as budget, other known
preparation failures as protocol with their exact reason retained, and an unknown
reason as framework. Upstream error text or JSON code cannot impersonate a typed
preparation diagnostic. Early handler validation, route-guard errors and failures
after preparation are outside this recorder's scope and retain their other evidence
or unknown classification. Local rejections do not become synthetic HTTP attempts
and do not erase previously incurred usage. Older runs cannot be retrospectively
reclassified without the recorded boundary evidence.

```sh
GOTOOLCHAIN=go1.26.4 go build -o "$task_validation/tool-loading-live-gateway" ./ops/tool-loading/live-gateway
"$task_validation/venv/bin/python" ops/tool-loading/probe_live_gateway.py \
  --gateway-binary "$task_validation/tool-loading-live-gateway" \
  --output-dir "$task_validation/live-bootstrap-fixture"
```

To verify only startup using an explicitly selected operator model/route:

```sh
"$task_validation/venv/bin/python" ops/tool-loading/live_gateway.py \
  --gateway-binary "$task_validation/tool-loading-live-gateway" \
  --config "$task_operator_config" --output-dir "$task_validation/live-bootstrap" \
  --client-model "$task_model_id" --upstream-model "$task_upstream_model_id" \
  --executor "$task_executor_id" --contract "$task_contract"
```

This command starts and closes only its own candidate and performs no inference.
All output directories must be new. The SDK requires an explicit listener port;
the launcher releases its reservation before launch. A collision fails closed:
both actual child PID and random instance nonce must match a bounded local HTTP
readiness probe. Startup text alone cannot prove endpoint ownership. Existing
processes are never killed or restarted. Child environment is filtered; the
original owner home is retained solely for resolving the requested auth source.
Native client homes remain separately isolated.

Isolation version 4 deep-copies native JSON metadata, removes refresh tokens,
and gates native snapshot background refresh before startup. API-key credentials
are excluded by the SDK refresh scheduler. The SDK's routing selector and
session-affinity lifecycle are preserved; neither is replaced or prematurely stopped.
Access credentials and provider identity remain intact. Expired credentials
produce unavailable/failed observations; shared OAuth refresh is not a fallback.
The original refresh credentials and nested source objects remain unchanged.
The wrapper serves only model listing, synthetic readiness, and POST Chat or
Responses for the single pinned client model. No operator management endpoint,
config watcher, plugin, Home, discovery, logging or cooldown persistence is used.

Supported constructor profiles are `codex`/Responses, `meta`/Responses, `gemini`/GenerateContent,
`claude`/Messages and configured `openai-compatible-*`/Chat. These are explicit
wire-contract constraints, not certification. Other executors remain unknown;
no familiar provider name silently becomes Chat. The transport observer rejects
wrong actual executor/model or an auth fingerprint outside the frozen initial
set before capture/dispatch. Readiness includes source config/policy hashes and
sorted credential-ID fingerprints; the launcher validates their set digest.

Binary digest, readiness, exit code, unchanged-source check, collector artifacts
and original failed startup records are private evidence. Credential-ID hashes
do not prove endpoint/account region, credential material or full scope freshness.
The calibration runner below locks selected actual model metadata and starts
each case before invoking this bootstrap. Immutable scope/endpoint manifests,
cache conditions and representative template scheduling remain incomplete.
The startup-only command and synthetic probe do not contribute to sixty live
cases, coding success, context savings or confidence intervals.

## Real Provider calibration

`live_clients.py` runs installed, version-pinned Codex/OpenCode against the
read-only SDK candidate and the synthetic MCP catalog. It consumes real Provider
quota. Declare the exact configured alias, upstream model, executor and full
endpoint SHA-256; no raw endpoint or credential is exported. The endpoint ID
is derived and validated instead of manually truncating a digest.

```sh
"$task_validation/venv/bin/python" ops/tool-loading/live_clients.py \
  --gateway-binary "$task_validation/tool-loading-live-gateway" \
  --config "$task_operator_config" --output-dir "$task_validation/calibration" \
  --gateway-revision "$task_gateway_revision" --endpoint-sha256 "$task_endpoint_sha256" \
  --client-model "$task_model_id" --upstream-model "$task_upstream_model_id" \
  --executor "$task_executor_id" --task-seconds 90
```

Only explicit OpenAI-compatible Chat profiles are currently admitted here.
Each task is journaled before candidate/client launch, limited to eight physical
upstream attempts and its declared task time; all failures remain in the replay.
Client groups and gateway children are isolated and closed after each task.
The model catalog comes from the actual candidate endpoint; there is no synthetic
metadata fallback. Codex lazy cases require the catalog's search capability.
Nullable `tool_mode` admits an explicitly isolated direct profile; a declared
Code Mode model is rejected. Requested mode still requires actual declaration
evidence. The OpenCode adapter's explicit context/output limits are preserved
in its private configuration; they do not certify a model's full capacity.

The task requires a real MCP dispatch, a fresh receipt nonce in the final answer,
zero forbidden dispatch/effects, released callbacks, legal client and upstream
terminals, and strict plain JSON. Receipt use is independent from hard formatting:
a JSON Markdown fence can prove receipt use while failing the format gate. An
earlier correct message cannot satisfy an incorrect final answer. EOF or `[DONE]`
alone never substitutes for a Chat finish reason. Malformed choices fail closed.

Raw first-request input includes background title generation; all such requests
also remain in whole-task usage. A first agent request is a separate diagnostic,
not a retroactive replacement for the locked first-request gate. Chat/Responses
closed-response metering uses independently tested terminal evidence, preserving
the original `closed` outcome and `complete: false`. Missing usage, unfinished
requests and unsupported closed wire contracts remain unknown. See the
[metering and lifecycle report](../../docs/acceptance/2026-10-13-closed-metering-lifecycle_CN.md).
Credential/account region and full immutable runtime scope remain unknown, so
pair certification and confidence gates remain unverified. These diagnostics
must not be pooled as a final-revision sixty-case matrix.

## Predeclared paired batches

With both modes selected, `live_clients.py` writes `batch-plan.json` before any
task and fsyncs a `batch_locked` digest as the first CaseJournal event. The plan
binds exact case specs, a concrete gateway revision, prompt/gold/permission/catalog
fingerprints, file material digests, schedule seed and total request/dispatch
budgets. The runner checks the selected binary, source config, client binaries,
Python interpreter and harness files before and after each case. Paths and secret
contents are not exported. A drift is a started framework failure before launch.

Pairs must agree in every scope dimension except client mode, and in their
template, cache condition, prompt, fixture seed, gold and per-task limits.
The deterministic schedule shuffles pairs and routes, alternates first mode
within each route and interleaves route blocks. An odd number of pairs permits
one-order imbalance per route. The Python interpreter and explicit stored order
are pinned; rerunning uses a fresh run, not an appended denominator.

`--schedule-seed` chooses the predeclared order. `--batch-max-requests` and
`--batch-max-dispatches` set total ceilings; defaults reserve eight of each per
planned task. A full task ceiling is reserved before launch. Only verified actual
counts refund unused capacity. Missing capture/boundary evidence keeps the full
reservation, and insufficient capacity stops admission. Planned-but-unstarted
tasks are reported separately; every started failure or interruption stays in
the journal. Single-mode diagnostics retain the legacy path and do not accept
paired-budget flags. This single-process harness has no resume mode.

```sh
python3 ops/tool-loading/validate_evidence.py \
  --journal "$task_validation/calibration/cases.jsonl" \
  --plan "$task_validation/calibration/batch-plan.json"
```

The audit checks the durable lock, exact specs, schedule prefix and recorded
budgets. Missing lock or a replaced plan is a binding error, distinguished from
an invalid journal. Recorded request count is kept separate from unknown physical
attempt evidence when a started case has no request records. An audit is always
`unverified`: it does not lock installed dependency contents, actual model metadata,
OAuth material, account region, cache conditions or statistical acceptance.
Planning sixty synthetic slots does not execute sixty real cases. The current
live CLI still runs one lookup template against an explicit Chat profile; the
three-scene L2 matrix, all real routes and Coding tasks remain separate work.
