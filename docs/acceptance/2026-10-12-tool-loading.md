# Cross-provider tool-loading acceptance

Verified on 2026-10-12 with Codex 0.160.0, OpenCode 1.18.32, and an isolated
CLIProxyAPI candidate built from this change. This report publishes aggregate
results; raw requests, responses, credentials, and workstation paths remain local.
The installed gateway and default client/gateway configurations were not changed.
The candidate was stopped after acceptance. This PR does not deploy the fixes.

> 🧠 **From Hindsight memory (协议与配置验收入口)** — The change follows the existing unified `internal/responsestools` pipeline, identity/declaration-budget constraints, and Go 1.26.4 verification convention. These historical conventions were checked against current source and tests; memory is not evidence of live acceptance.

## Fixes and responsibility boundaries

| Finding | Change or verified boundary |
| --- | --- |
| GPT Responses Lite returns a namespaced search function alias | Restore the exact `tool_search/tool_search_tool` alias to a client `tool_search_call`, including object arguments, preserved call identity, and consistent buffered/SSE item IDs. Response-only adaptation preserves request bytes and budgets without pinning names that existing route optimizers can change. Declared business tools and server search are excluded. |
| Muse native search does not activate discovered tools | Activate declarations from `tool_search_output` in top-level `tools` on native-search routes using `custom-tools: function`. Keep native search history and restore original tool identities. Reuse conflict, latest-discovery-round, declaration-budget, and outbound checks. |
| Muse Lite rejects `input[].additional_tools` | Lift declaration containers into top-level `tools` before schema/custom adaptation on portable native routes, including eager requests containing ordinary functions. Merge namespaces and deduplicate identical declarations; reject conflicting definitions and invalid/opaque input locally. Preserve remaining history IDs. Inherited native routes retain the original Lite dialect. |
| Muse requires a nonempty namespace description | Fill missing/empty descriptions with a neutral description; retain existing nonempty descriptions. Removing the field also failed the upstream contract and is not the final fix. |
| Chat-to-Responses drops an explicitly ended reasoning-only response | At `[DONE]`, map explicit `stop` to `response.completed`, and `length`/`max_tokens`/`content_filter` to `response.incomplete`. Preserve reasoning and late usage; repeated DONE does not duplicate completion. Missing finish reason, incomplete function arguments, and EOF without DONE still fail closed. |
| Historical GLM terminal failure | The earlier failed case lacks a raw upstream stream. Replay proves the converter defect above, but does not prove that it caused that historical failure. Current successful GLM captures contain explicit finish reasons and DONE. |
| Gemini search initially selects decoys | Client search results already contain eight decoys before reaching the gateway; a later `alpha` query selects the target. Codex ranks its catalog locally and the model supplies queries. A pipeline-to-Antigravity regression verifies that selected declarations, parameters, search calls, and results survive translation. No Gemini translator implementation change was needed. |

```mermaid
sequenceDiagram
    participant A as Coding agent
    participant G as CLIProxyAPI
    participant P as Provider
    participant M as Client MCP catalog/runtime
    A->>G: Search entry / Lite declarations
    Note over G: Muse: lift declarations and satisfy namespace contract
    G->>P: Compatible tool definitions
    P-->>G: Model-generated search query
    G-->>A: Client search call (GPT Lite alias restored)
    A->>M: Search client catalog
    M-->>A: Selected tool schemas
    A->>G: Search output and selected schemas
    Note over G: Activate selected tools and preserve identity/budgets
    G->>P: Active declarations
    P-->>G: Target call / upstream end signals
    Note over G: Map explicit terminal signals; reject truncation
    G-->>A: Restored call and terminal events
    A->>M: Execute target MCP tool
    M-->>A: Budget and fresh verification code
```

This diagram describes Codex search. OpenCode Code Mode uses its own `execute`
and discovery runtime. Both clients execute MCP tools; the gateway does not own
the full deferred catalog and cannot rank tools that the client has not sent.

## Method and success criteria

The same fixture, prompt, routes, and criteria were retained across rounds:
64 read-only MCP tools, 63 decoys, and `ledger_budget_lookup` at catalog index 37.
The target schema has 36 optional fields; decoy schemas have 12. Every case uses
a fresh random verification code unavailable to the model before actual execution.
Core success requires actual `ledger_budget_lookup(project=alpha)` execution,
`remaining_budget=424242` and the correct code in the final answer, valid terminal
events, and no forbidden shell/file/web/other-agent calls. JSON-only formatting is
scored separately; a correct answer inside a code fence is not format compliance.

Codex uses isolated catalog/CLI overrides and Responses. OpenCode uses isolated
XDG settings and OpenAI-compatible Chat. The candidate uses the production SDK,
executors, translators, and existing route selection with memory-only configuration
and credentials; watchers and state persistence are disabled. No default catalog,
configuration, installed binary, or running service was changed.

The temporary observer records filtered tool contracts, event structure, finish
reasons, DONE, and usage, excluding credentials, headers, provider hosts, prompts,
and reasoning text. Gemini retains Antigravity's original HTTP/1.1 transport;
its attribution uses client-boundary traces and translation regression, not a raw
upstream capture. Raw evidence remains local and is not part of this public PR.

## Acceptance results

The first repair round retained 29 formal cases: 26 core successes (89.7%).
GPT Lite and ordinary Muse Responses each passed three repeated target-path
cases; the lazy matrix was 8/10, eager was 10/10, and both real-MCP smoke tests
passed. Failures included GLM terminal handling, Gemini retrieval, and Muse Lite
input rejection. Later success does not replace those historical failures.

The subsequent compatibility/retest round retained 39 development/acceptance
attempts: 32 core successes and seven failures across different candidate
revisions and transient provider availability errors. This is not a final-revision
reliability denominator. Sixteen of the 32 successes also satisfied JSON-only output.

| Latest evidence set | Core success | Interpretation |
| --- | ---: | --- |
| First cross-provider matrix in the subsequent round | 19/20 | Eager 10/10; lazy 9/10, with Muse Lite namespace description failing. |
| Muse Lite search after the final compatibility fix | 3/3 | Three requests each; 85,594 / 85,589 / 85,610 input tokens. Elapsed 10.96 / 108.57 / 14.24 seconds. |
| Muse Lite eager | 1/1 | Two requests; verifies ordinary-function `additional_tools` compatibility. |
| Codex Gemini search retests | 3/3 | Five / four / four requests; actual target execution. Correct answers used JSON code fences, so JSON-only formatting failed. |
| OpenCode Gemini Code Mode fixture | 2/2 | Three requests each; real MCP is counted separately below. |
| GLM Codex search | 4/4 | Two diagnostics, matrix, and final candidate retest; the historical terminal failure did not recur. |
| Real codebase MCP | 2/2 | Codex Muse Lite and OpenCode Gemini Code Mode actually executed `index_status` and returned `graph_status=ready`. |
| Combinations with passing evidence | 20/20 | Nineteen unaffected matrix combinations plus repaired Muse Lite. This is coverage, **not one fresh 20/20 matrix run or a reliability estimate**. |

Early region/precondition errors and one `auth_unavailable` cooldown occurred
before discovery, then recovered under unchanged configuration. Recovery cause
was not established. The remaining development failures exercised empty/missing
namespace descriptions. Original failed records were retained.

## Context measurements

Numbers below are actual provider-reported **input tokens, including cached input**,
compared within the same client/model/prompt. They are not billing savings and
should not be compared across tokenizers. Muse pairs the validated Lite eager and
Lite search cases; other rows use the matrix's successful cases. Additional search
rounds are included in full-task totals.

| Client / route model ID | First eager → lazy | First reduction | Full task eager → lazy | Full-task reduction | Requests eager → lazy |
| --- | ---: | ---: | ---: | ---: | ---: |
| Codex / `gpt-6-luna` | 55,451 → 19,465 | 64.90% | 110,986 → 68,298 | 38.46% | 2 → 3 |
| Codex / `qwen-ds41-flash` | 61,655 → 20,502 | 66.75% | 123,515 → 75,069 | 39.22% | 2 → 3 |
| Codex / `opencode-go-glm53-flash` | 61,007 → 19,915 | 67.36% | 122,106 → 75,915 | 37.83% | 2 → 3 |
| Codex / `gemini-3.5-flash-lite` | 62,283 → 19,360 | 68.92% | 124,932 → 100,061 | 19.91% | 2 → 4 |
| Codex / `opencode-go-muse-spark-1.3` | 61,010 → 20,730 | 66.02% | 122,156 → 85,594 | 29.93% | 2 → 3 |
| OpenCode / `gpt-6-luna` | 36,714 → 7,137 | 80.56% | 73,505 → 35,754 | 51.36% | 2 → 3 |
| OpenCode / `qwen-ds41-flash` | 47,833 → 8,429 | 82.38% | 95,837 → 39,841 | 58.43% | 2 → 3 |
| OpenCode / `opencode-go-glm53-flash` | 46,658 → 8,082 | 82.68% | 93,375 → 72,807 | 22.03% | 2 → 5 |
| OpenCode / `gemini-3.5-flash-lite` | 50,462 → 10,009 | 80.17% | 101,009 → 45,228 | 55.22% | 2 → 3 |
| OpenCode / `opencode-go-muse-spark-1.3` | 46,456 → 8,450 | 81.81% | 93,044 → 39,545 | 57.50% | 2 → 3 |

Codex Gemini's three successful search tasks consumed **145,039 / 100,061 /
109,319** input tokens. Against eager 124,932, this is **16.09% more / 19.91% less /
12.50% less**. First-request input remains about 19.4k (about 69% lower), but
query quality and extra round trips determine whether the complete task saves input.

Wire evidence confirms loading is deferred: none of the fixture's 64 names appears
in Codex's first lazy request; later requests include selected declarations.
Codex still sends 21/22 resident declarations. OpenCode changes 64 eager function
declarations to one `execute` declaration; a few compact catalog names in its body
are not full schema injection. An auxiliary fixed `o200k_base` estimate for schema
JSON falls by about 89% (Codex) and 93.49% (OpenCode); provider usage above remains
the primary measurement.

## Regression coverage and verification

The combined change adds 19 test functions plus table-driven scenarios covering
buffered/SSE aliases and fragmented arguments, negative business/server identities,
native activation/passthrough, response-only outbound guards, declaration conflicts
and budgets, Lite lifting/namespace merging/descriptions, history identity, explicit
reasoning-only terminal events, the Gemini discovery boundary, and production SDK
Meta buffered/streaming execution. Existing truncated-stream safeguards remain.

Go 1.26.4 full verification passed: 100 packages and 13,715 tests/subtests with no
failures, `go vet ./...`, server build, related race checks, pinned-toolchain
formatting, and `git diff --check`. The 28 maintenance tooling tests and offline
tracked Codex model catalog validation also passed. Live acceptance and unit regression
are separate evidence sources; CI does not contact the live providers.

## Remaining work and limits

- Client/model: improve Codex BM25 ranking for decoys/negated descriptions, search
  query generation, and enforcement of forbidden tool calls. Gateway translation
  cannot guarantee retrieval quality or JSON-only model output.
- Gateway: broader cross-protocol capability negotiation, sanitized terminal-event
  diagnostics, and an ongoing compatibility matrix remain product work. This PR
  fixes observed contracts without adding these larger capabilities.
- Provider/transport: the historical GLM failure still lacks upstream evidence;
  actual truncation must remain distinguishable from an explicitly finished stream.
  Transient provider availability and Muse's 108.57-second passing case remain
  observations, not resolved protocol defects.
- Scope: five current route aliases, these client versions, and a bounded fixture
  do not establish reliability for all models, server search, custom grammars, or
  long conversations. No default selection or service deployment is included.
