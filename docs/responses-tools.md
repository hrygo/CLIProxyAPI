# Responses Client Tool Protocol (Core)

This document describes the core handling of the Responses client tool
protocol: client-executed `tool_search` discovery and `custom` tools. The
handling lives entirely in the core and requires no plugin or dynamic
library.

## Background

Codex-style clients only defer tool definitions when the selected model
advertises `supports_search_tool`. When it does not, the client falls back to
injecting the whole tool surface — the top-level `tools` array and
`input.additional_tools` — into the **first** request of every turn.

Measured on 2026-09-26 with the Codex 0.156.1 desktop client on non-OpenAI
routes, that fallback put roughly 120K input tokens into the opening request
of a task, almost all of it tool definitions the model never called. The cost
is paid on every turn: latency, a cache miss on the prefix, and per-request
spend — for tools the model may never touch.

## Problem

Shrinking the first packet requires a discovery protocol, and neither
client-executed shape maps cleanly onto a plain `function` tool list:

- `tool_search` — the model emits a search call, the client answers with the
  matching tool definitions, and only the discovered tools may then be called.
- `custom` tools — free-form string input, optionally constrained by a grammar.

A proxy that only forwards the declared tool list breaks that loop: deferred
tools are expanded before the client ever searched, discovered tool names come
back renamed, the model's search call and the client's search result stop
matching, and `custom` calls are rejected by upstreams that accept only strict
function schemas. Capability was also decided in the wrong place — inferred
from templates and provider type rather than from the route actually used — so
models were advertised as supporting search when they could not close the loop.

## Value

With client-executed `tool_search` closing the round trip, deferred tools stay
out of the first packet and are materialized only when the model actually
searches for them. That is the highest-value outcome of this feature, and the
reason the protocol contract below is worth its cost: the search loop must
survive renaming, history replay, streaming and provider translation without
ever silently losing a tool.

## Goals and non-goals

Goals:

- Keep the first packet small: deferred tools are never materialized into the
  initial request, and discovery is the only path that activates them.
- Complete client `tool_search` and `custom` bridging with core features only,
  without installing any dynamic library.
- Convention over configuration: routes that declare nothing still get a
  correct policy, derived from what the runtime already knows about the call.
- Resolve the strategy from the actual upstream route (selected provider,
  auth kind, real upstream model, actual protocol, endpoint scoping), never
  from client aliases.
- Declare `supports_search_tool` only when every selectable route behind a
  public model alias can complete the client search loop.

Non-goals: a Muse-specific provider, a tool execution server, a second
generic JSON-RPC ABI, automatic model fallback, or a cross-session tool
content database.

## Configuration

`requests.responses-tools` is the only configuration source. There is no
second policy in `models.json`, model aliases, or plugin YAML. Every field is
optional: an absent block already applies the convention defaults.

```yaml
requests:
  responses-tools:
    enabled: true          # emergency gate; omit or set false to disable all
    limits:
      max-active-tool-bytes: 262144
      max-active-attempts: 512
      max-state-bytes: 33554432
      max-attempt-bytes: 1048576
      max-schema-expansion-bytes: 65536
      max-schema-expansion-nodes: 10000
      max-depth: 64
    routes:                 # optional overrides of the convention defaults
      - match:
          provider: "xai"
          auth-kind: "api-key"
          upstream-model: "grok-4.5"
          upstream-format: "codex"
          base-url: "https://api.x.ai/v1"
        client-search: "bridge"
        custom-grammar: "reject"
        schema:
          complete-search-required: false
          local-refs: "preserve"
```

### Route identity

`match` fields must describe the route the runtime actually selects, not the
name the client typed. The provider-to-format mapping is fixed:

| `provider` | `upstream-format` |
|---|---|
| `codex`, `xai`, `meta` | `codex` |
| `claude` | `claude` |
| `gemini`, `vertex`, `aistudio` | `gemini` |
| `kimi` | `openai` |
| `antigravity` | `antigravity` |
| `openai-compatible-<name>` | `openai` |

`codex` is the internal name for the OpenAI Responses dialect, not a vendor
label: `xai` and `meta` speak the Responses wire format and reuse the same
translator. A misspelled or mismatched field never errors — the rule simply
does not match, and the convention applies instead.

## Defaults

Nothing needs to be configured for the common case. The effective policy is
derived from the upstream format **and** the provider:

- `client-search: native` only when the upstream format is `openai-response`
  or `codex` **and** the provider forwards the `tool_search` built-in
  unchanged. Today that is `codex` and `meta`. The search built-in and its
  history stay native. Codex Lite requests using `input[].additional_tools`
  retain a response adapter: the exact `tool_search/tool_search_tool`
  namespaced function alias is restored to a client `tool_search_call`, with
  object arguments and consistent item IDs across stream events. Declared
  business tools with that identity are never treated as search aliases.
  Response-only repair preserves request bytes and declaration budgets without
  pinning unadapted function names that later route optimizers may rename.
- `client-search: bridge` for every other upstream, including providers that
  speak a native Responses format but rewrite the built-in: the `xai`
  executor strips it, so native passthrough would silently drop discovery.
  The bridge stays inert until a client actually declares `tool_search`, so
  requests without it are untouched.
- `custom-tools: inherit` and `custom-grammar: reject` for every provider
  whose upstream accepts the native tool surface. The core never infers
  custom handling on its own.
- `schema.local-refs: preserve` and `schema.complete-search-required: false`
  for every route. Neither is ever set to a rewriting value by the convention.
  `meta` is the one provider that needs a portable tool surface today: it
  rejects `type: custom` and requires every property to be listed in
  `required` and rejects recursive schemas, so the convention folds its tools
  with `custom-tools: function`, `custom-grammar: describe`,
  `schema.complete-search-required: true`, and `schema.local-refs: flatten`.
  Native client search on a `custom-tools: function` route also activates
  discovered tools from `tool_search_output` in top-level `tools`, reusing the
  same identity and cumulative declaration budget checks as the search bridge. Native
  search history remains intact; provider function calls are restored to the
  original tool identity. On these portable native routes, Lite
  `input[].additional_tools` containers are lifted into top-level `tools`
  before schema/custom adaptation, for eager and search requests alike.
  Identical declarations are deduplicated, matching namespace containers
  are merged, and conflicting definitions fail locally with 422. Namespace
  containers receive a neutral description if Lite supplied an empty or
  missing one, satisfying the portable native required-field contract.
  Nonempty client descriptions and remaining conversation/search-history
  IDs are preserved, and lifted declarations
  remain subject to the active declaration budget. Inherited native routes
  retain the original Lite input dialect and request bytes.

  Portable native routes also retain a response-only adapter for eager,
  non-deferred namespaced functions. Some upstreams return `namespace.name`
  in the function `name` without a separate `namespace`; the existing request
  contract restores the declared leaf name and namespace without changing
  call IDs, arguments or outgoing request bytes. Deferred or discovered-only
  identities do not activate this eager adapter. Transparent native policies
  and plain top-level functions retain their previous passthrough behavior.
  Lifting changes declaration scope: a declaration originally between
  conversation items becomes visible at whole-request scope. The internal
  `Prepared.Transformation` sidecar reports original Lite declaration indices,
  interleaving and same-identity updates. A lift reports
  `position_preserved: false` and `additional_tools_position_lift`; native
  passthrough reports `position_preserved: true`, including response-only alias
  repair. Diagnostics contain no prompts, names, schemas or fingerprints.
  This report concerns declaration positions only, not full semantic fidelity.
  It is not an HTTP response field and does not alter route policy.

  Exact position semantics are not certified on portable lifting routes.
  A future opt-in strict contract should reject an unmappable interleaved
  declaration or same-identity update locally with 422
  `unsupported_protocol`, before opening an upstream connection; clients
  should then select a route with position-preserving support rather than
  retrying the same incompatible payload. This strict contract is a proposal,
  not an enabled configuration option. Existing ambiguous definition conflicts
  already fail with 422 `ambiguous_identity`. The compatibility behavior
  above remains in effect pending whole-branch review.
  `schema.local-refs: inline` stays opt-in for every provider, `meta`
  included: the convention never expands a client-authored `$ref`; flattening
  only replaces the references that close a cycle with an unconstrained
  schema and keeps acyclic `$defs` untouched.
  An effective `inline` or `flatten` policy also prepares ordinary function
  declarations without discovery or custom metadata. Recursive schemas on an
  `inline` route fail locally; they do not bypass validation as plain functions.

Lossy strategies stay opt-in, because each of them either drops a capability
or rewrites user schema. The single exception is the portable-surface folding
described above, which the convention applies to `meta` because that upstream
accepts nothing else:

- `custom-tools: strip` and `custom-grammar: describe`
- `schema.complete-search-required: true` and `schema.local-refs: flatten`
- `client-search: disabled`, to turn search off for one route

A `routes[]` rule overrides only the strategies it states. Every strategy it
omits resolves on its own to the convention of the route the call actually
uses, so a rule that changes one strategy keeps the rest of that provider's
convention. Stating one schema strategy does not opt the route out of the
other: `schema.local-refs` and `schema.complete-search-required` are resolved
independently.

`enabled: false` is the emergency gate: it turns the whole feature off without
deleting any other configuration.

Strategy enums:

- `client-search`: `inherit` (follow the convention for this route), `native`
  (passthrough on a true native Responses route), `bridge` (function translation),
  `disabled` (explicit client search requests fail with 422, never silently
  dropped).
- `custom-tools`: `inherit`, `native`, `function` (wrap and restore supported
  custom calls),
  `strip` (lossy: drops future declarations but keeps convertible history;
  forced calls to stripped tools fail with 422), `reject` (any custom
  contract fails with 422).
- `custom-output-replay`: `single` (default for adapted history: repeated
  outputs fail with 422 `unsupported_protocol`) or `multiple` (replay every
  result without merging). `multiple` requires an explicit route whose
  `upstream-format` is `codex` or `openai-response`; it never follows a
  convention automatically. Verify the exact upstream before enabling it:
  speaking Responses does not prove one-to-many result support. The option
  preserves each payload, original `call_id`, reversible item identity and
  global input order, while keeping duplicate-call and orphan-output guards.
  The final-send guard binds bridged history to prepared output counts,
  native tool-event order and canonical hashes of complete output items.
  Post-rule changes to result content, metadata or identity fail locally
  before dispatch; exact number tokens are retained when hashing. The
  context stores hashes and identifiers, not raw results.
- `custom-grammar`: `reject` (default), `describe` (function mode only:
  folds grammar syntax into the description; sampling constraints are not
  enforced upstream).
- `schema.complete-search-required`: tri-state. Absent follows the route
  convention, `true` completes the required fields and widens optional fields
  to nullable, `false` keeps the declared schemas. It adapts only
  client-search schemas, never strictifies every tool globally.
- `schema.local-refs`: `preserve` (default) or `inline` (expands local
  references; recursive, external, missing, or unsupported references fail
  instead of deleting constraints) or `flatten` (keeps every reference
  untouched except the ones that close a cycle, which become an unconstrained
  schema; external or missing references still fail instead of deleting
  constraints).

Match rules:

- `provider`, `auth-kind`, `upstream-model`, and `upstream-format` are all
  required exact conditions. No wildcards, regex, or capability inference from
  model names are supported.
- API-key routes must set `base-url` so the same provider/model name on
  different endpoints cannot share one rule. OAuth routes leave it empty.
- Overlapping rules with conflicting strategies are rejected at load time;
  dynamic conflicts at routing time return an explicit configuration error,
  never a silent first-match.
- Limits must be positive with reasonable upper bounds; negative values never
  mean unlimited.

## Rollback

Set `requests.responses-tools.enabled: false`. No other configuration is
affected, and the convention policy stops applying on the next request. When
changing route rules, wait for active requests to drain so no request changes
protocol semantics mid-flight.

## Item identity and history repair

A Responses item id carries the namespace of the item type that minted it.
The bridge changes item types, so it changes ids with them: a client stores an
output item exactly as the proxy emitted it and replays it verbatim on the next
turn, where a strict upstream rejects an id from the wrong namespace.

| Item type | Id prefix |
|---|---|
| `function_call` | `fc_` |
| `function_call_output` | `fco_` |
| `custom_tool_call` | `ctc_` |
| `custom_tool_call_output` | `ctco_` |
| `tool_search_call` | `tsc_` |
| `tool_search_output` | `tso_` |

Only these six types participate. `message`, `reasoning`, and every other item
keep the identity the upstream gave them. An explicit type conversion is
allowed in one direction only, and each pair is reversible:

- `function_call` ↔ `custom_tool_call`
- `function_call` ↔ `tool_search_call`
- `function_call_output` ↔ `custom_tool_call_output`
- `function_call_output` ↔ `tool_search_output`

Before a request is adapted, a replayed `input[]` item whose type and id
prefix form one of those pairs is migrated in place. The repair changes the id
value only: the request is not re-encoded, and no other byte moves. It runs
before the route policy is consulted, so a full polluted history is recovered
on a native route without declaring a tool and without building an attempt.

What the repair deliberately does not do:

- It does not guess. Server-executed search, an id in no known namespace, a
  missing or non-string id, and every non-tool item are left alone.
- It does not invent a request id. An input item may omit its optional id.
- It does not resolve opaque history. When a repair is needed and the request
  carries `previous_response_id`, `previous_item_id`, or an `item_reference`,
  the request is refused with 422 `opaque_history`; the parent link is never
  dropped to make the repair pass. Opaque history that needs no repair is not
  affected.
- It does not reuse an id. When a migration would give two items the same
  client-visible id, the request is refused with 422 `ambiguous_identity`
  rather than renamed or resolved by arrival order.

On the response side the same conversion runs in the other direction. A
bridged item that arrives without a usable id is an upstream contract violation
and fails with 502 `upstream_contract`: any id the proxy minted would be a new
identity the client would store and replay. Restoring a tool name never changes
the item type, because a name that resolves to a custom tool is not evidence
that the upstream produced a custom call.

In a stream, the client-visible type and id are computed once, when the item is
first tracked, and every later reference reuses them: the `added` item, the
argument delta and done events, `output_item.done`, and the terminal
`response.completed` output all name the same id. The upstream id stays the
tracking key, so the existing `call_id` and `output_index` consistency checks
keep comparing what the upstream actually sent.

For Chat-to-Responses streaming, the OpenAI-compatible executor requires both
the upstream `[DONE]` marker and a translated `response.completed` or
`response.incomplete` event with the matching response status. `[DONE]` alone
does not complete an unfinished tool call. If conversion cannot produce a
legal terminal event, the executor reports a 502 stream error without
synthesizing arguments or completion. An explicit source finish reason may
still carry invalid model-generated arguments; the execution endpoint must
validate those arguments before dispatch. A complete call item and a complete
response are separate boundaries: a disconnect after an exposed call must not
replay that call on another credential.

Native WebSocket duplex connections repair every successor `response.create`,
`response.append`, and `response.steer` frame, not only the first request: those
frames never pass through the Manager. A frame that cannot be repaired is
answered with a local error frame carrying the same status and reason code and
is not forwarded, and it never becomes pending or steering state. Once the
connection is established, that status lives inside the error frame; no HTTP
status is rewritten.

## Budgets

| Setting | Meaning |
|---|---|
| `limits.max-active-tool-bytes` | Declared tool bytes retained per attempt |
| `limits.max-active-attempts` | Concurrent retained attempts, Manager scope |
| `limits.max-state-bytes` | Shared Manager state budget |
| `limits.max-attempt-bytes` | Per-attempt state ceiling |
| `limits.max-schema-expansion-*` | Schema rewriting ceilings |

These ceilings bound state the bridge itself retains: declarations, tracked
calls, buffered arguments and the SSE buffer. The forwarded request payload is
caller content that the proxy already holds in full, so it is not charged to
the attempt budget; conversation length never trips a limit.

Diagnostics carry only route/policy generation, tool kind counts,
declaration bytes, used budget, and fixed reason codes. Arguments, patches,
grammar bodies, prompts, results, tokens, and credentials are never logged.

## Boundaries

- `interactions`, direct plugin-executor routes, custom unknown executors,
  images/video, and `responses/compact` never enable the bridge; they keep
  their existing paths and make no new search capability claims.
- A configured rule that hits an unsupported target returns an explicit 422
  instead of silently passing through.
- `CountTokens` shares request preparation and declaration budget but builds
  no response stream state; executors that cannot count surface their own
  error behavior.
- Native stateful passthrough/steering: only passthrough that needs no
  transformation is allowed. Bridged ordinary WebSocket traffic uses replay;
  live steering combinations that need bridging are rejected explicitly, and
  live duplex is not bridged frame-by-frame.
  The per-turn gate probes the same request preparation as HTTP, including
  ordinary schema conversion, Lite declaration lifting, response-only repair,
  request ID repair and local rejection. It uses private temporary state,
  releases it before returning, and performs no upstream or tool dispatch.
  Resolved client aliases and pinned credentials retain their actual format
  and endpoint policy; partial route hints cannot override a pinned credential.
  An unmatched rule still follows the convention. Unchanged ordinary functions
  and native search requests keep passthrough, except portable native eager
  namespaces that require response-only identity repair. Replay does not certify duplex:
  `response.steer` in a replay turn fails explicitly with 422 before dispatch.
- Server-side history: only locally expandable history converts; requests
  that depend on remote opaque `previous_response_id` references fail with
  422 on routes that need transformation. Native paths keep their existing
  capabilities.

## Multiple custom outputs

A custom execution can emit a notification and a final result with the same
`call_id`. On `custom-tools: function` or `custom-tools: strip` routes, an
otherwise valid explicit history containing more than one custom output for
one call is rejected locally with 422 `unsupported_protocol`. The error is
request-scoped and availability-neutral, and no upstream attempt is created.
It describes an adaptation limit, rather than malformed client input.

The current downstream adapters cannot preserve every such output as a tool
result. Passing them through those adapters would discard, collapse, or turn
some outputs into ordinary user text. Duplicate calls, missing call IDs, and
orphan outputs remain `invalid_custom_input`; this change does not relax their
identity checks. A native passthrough test proves only that `Prepare` leaves
the local request bytes unchanged, not that a particular provider accepts the
history. Full multi-output support remains unverified. See the
[Codex Code Mode compatibility boundary](compatibility/codex-code-mode-multiple-results_CN.md)
for the observed failure and remaining validation requirements.

## Cumulative discovery activation

Bridge routes and portable native routes using `custom-tools: function` retain
all discovered identities from the explicit request history. After discovering
B, a previously discovered A remains callable with its canonical alias; portable
native function-call replay uses that alias as well. A later discovery of the
same identity supplies its latest declaration, while earlier calls and results
remain historical facts. Conflicting definitions within one round fail with
422 `ambiguous_identity`.

Forced function `tool_choice` and function entries in `allowed_tools` use the
same activated alias on both paths. Portable native routes keep `tool_search`
choices native; eager and undiscovered choices are not remapped.

The cumulative active declaration set must fit `max-active-tool-bytes`. If it
does not, the entire request fails locally with 413 `declaration_budget`, without
silently dropping older rounds, sending a partial activation, or rotating
credentials. All attempt leases are released on rejection. This changes the
older best-effort admission behavior: clients that relied on implicit eviction
must explicitly bound their retained history or use a sufficient limit. Pure
native passthrough continues to use the upstream's activation semantics.

Discovery history is not execution authorization. Removing an identity from a
later search result does not prove revocation of an earlier tool. The client or
MCP execution service must check current permissions and validate arguments at
dispatch, including after schema updates; the gateway preserves call identity
and does not execute tools or infer permission grants from historical manifests.
