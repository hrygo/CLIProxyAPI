# Absorbed upstream releases

One row per upstream release that was assessed. "Absorbed" means the listed
commits were ported onto our `main` **and** `verify-absorb.sh` exited 0.

| Upstream | Assessed | Ported | Skipped | Gate | Recorded |
| --- | --- | --- | --- | --- | --- |
| v8.0.7 | 2026-10-01 | `82f8e92b` `a8ffd5a8` `67cb32b6` `9e71c20d` `97f244b8` | none | PASS | 2026-10-01 |
| v8.0.13 | 2026-10-03 | 28 substantive ports + 1 test-only port (initial and follow-up mappings below) | 6 entries, including 3 merge wrappers (final decisions below) | PASS; follow-up effective on merge to main | 2026-10-03 |
| v8.0.16 | 2026-10-09 | 24 ports (mapping below) | 16 non-merge + 7 merge wrappers (decisions below) | PASS; merged to main in #22 | 2026-10-09 |
| v8.0.23 | 2026-10-14 | 26 upstream ports, including 1 equivalent-fix regression port (mapping below) | 15 non-merge + 6 merge wrappers (decisions below) | PASS; effective on merge | 2026-10-14 |
| v8.0.35 | 2026-10-23 | 25 initial repairs + 3 helper follow-ups (mappings below) | 123 non-merge + 18 merge wrappers; 63 later reviewed commits deferred by age | Initial intake PASS, merged #32; follow-up PASS, effective on verified PR merge | 2026-10-23 |

## Comparison baseline

**Accepted comparison baseline: `v8.0.35`**
(`ae3ee3be303fb07fb8173279923413005bf32177`). The initial 25 selective ports
reached main through PR #32 (`6efd7088ef740214f018d993c46f5f335d32fac7`)
and shipped in `v1.3.3-upstream8.0.35` at `5cc1929a58156977d2d9b59ee215609be0aea311`.
Alignment identifies a reviewed release ceiling, not a wholesale upstream merge.
The three-helper follow-up below does not advance this ceiling.

Previous comparison baseline: `v8.0.23`
(`d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5`), selectively absorbed through
PR #28 (`63aa6004400a2314f1e41877dd225edae6d2368b`). Every previously deferred
item remains deferred unless explicitly superseded by a subsequent ledger entry.

Earlier selective baseline: `v8.0.16`
(`a2976eb8a303f11b4ea5177bce9f9ff752634dfc`), merged through PR #22.

Earlier selective baseline: `v8.0.13`
(`d7914afdedca7af95ee974a42453dc49fc1388ce`), advanced from v8.0.7 on 2026-10-03.

Previous baseline: `v8.0.7` (`97f244b8ddb9cbf564b6e6faab0159102cca8617`), advanced
from `v8.0.5` on 2026-10-01 when every commit in `v8.0.5..v8.0.7` was ported.

Earlier baseline: `v8.0.5` (`e5b5a1cfca354ec80d5c31242ccfb6c0f6b5fbcd`).

Established on 2026-10-01 from repository history rather than from tag
presence. Evidence:

- `v8.0.0` through `v8.0.5` are ancestors of our `main`; `v8.0.6` and
  `v8.0.7` are not. The fork shares upstream history, so ancestry — not tag
  existence — is what establishes that the code is already on our trunk.
- The tags were reached through the shared fork history, not by a reviewed
  port, so this baseline records inherited content only. It carries no
  per-commit assessment and no replay evidence.
- Verified against `main` at `f667aaf566ecabd5e419d11bd50a79a9b6d1637d`.

Do not advance this baseline on tag presence alone. An assessed release is not
automatically a new baseline; record that decision explicitly, along with any
work left skipped.

For each assessment, record the explicit baseline, candidate and our main SHA,
baseline/candidate gate logs, upstream-to-local commit mapping and replay coverage
(or "not performed"). An assessed release is not automatically a new baseline;
record that decision explicitly, including any remaining skipped work.

## Assessments

### v8.0.7 — absorbed, 2026-10-01, ahead of the age gate by owner decision

Baseline `v8.0.5`, candidate `v8.0.7` (`97f244b8ddb9cbf564b6e6faab0159102cca8617`),
our `main` `cbce10a9`.

**The 72-hour gate was waived for this release.** `v8.0.7` was published
`2026-09-30T19:14:25Z` and was roughly 7h old when it was ported; the policy
window would have expired at `2026-10-03T19:14:25Z`. The repository owner
authorized the early intake on 2026-10-01 after `codex`-client traffic to a
Meta Muse upstream exposed an unfixed tool-integer-normalization failure. The
gate script enforces `MIN_AGE_HOURS >= 72` in code and offers no waiver flag, so
the assessment was performed by hand and this entry is the record of the
deviation. Every other intake must still clear the window normally.

All five commits in `v8.0.5..v8.0.7` were ported with `git cherry-pick -x`, in
chronological order, with no conflicts:

| Upstream | Local | Subject | Upstream issue |
| --- | --- | --- | --- |
| `82f8e92b` | `4463cea7` | fix(executor): support negotiated ALPN protocols in uTLS client | #6223 |
| `a8ffd5a8` | `65aaae24` | fix(codex): skip tool parameter integer normalization for codex executor targets | #6244 |
| `67cb32b6` | `7c291254` | fix(codex): determine tool integer normalization by target executor identity | #6244 |
| `9e71c20d` | `27ddba81` | fix(codex): handle upstream stream disconnect before first payload as bad gateway | #6247 |
| `97f244b8` | `d15c8c32` | fix(codex): pass target executor to compatibility translation and token counting | #6244 |

Nothing was skipped. `67cb32b6` and `97f244b8` revise the approach taken by
`a8ffd5a8`, so all three were ported together.

Gate evidence, both run through `ops/upstream-intake/verify-absorb.sh`:

| Tree | Result |
| --- | --- |
| baseline (`cbce10a9`) | PASS |
| candidate (`d15c8c32`) | PASS |

The gate must run with Go 1.26.4, the version both workflows pin. The local
toolchain was Go 1.27.1, whose `gofmt` reformats three tracked files that CI
accepts; that first baseline run failed on `gofmt` for a toolchain-version
reason, not a source defect. Re-run with `GOTOOLCHAIN=go1.26.4`, both trees
pass. Do not read a Go 1.27 `gofmt` result as a source failure.

**Replay coverage: not performed.** No real Codex client turn was replayed
against either tree, so the gate result covers the automated checks only and
does not establish that a live Muse request behaves correctly. The reported
failure is expected to remain after this port: see the note below.

### Effect on the reported Meta Muse failure: none

The reported symptom is a tool argument arriving as the JSON number `2500.0`
where the Codex client's strict deserializer expects an integer. The ported
commits scope the existing schema rewrite so it is skipped for Codex executor
targets, where upstream validates reserved tool schemas strictly.

That scope change does not reach the Meta executor. `meta_executor_execute.go`
was not migrated to the executor-aware helper; it still calls
`ApplyPayloadConfigWithRequest`, which passes an empty target executor, and it
additionally calls `NormalizeCodexToolIntegerTypes` unconditionally at line 67.
`isCodexTargetExecutor` is false for that path both before and after the port,
so Meta requests are rewritten exactly as they were on the baseline. If a Muse
request still produces `2500.0` after this port, the cause is a tool or
parameter missing from `codexClientToolIntegerFields` in
`internal/client/codex/tool-schema/tool_schema.go`, which is a whitelist gap
rather than a release-alignment gap.

**Resolved locally rather than by widening the whitelist.** The response side is
now canonicalized for Responses clients by
`sdk/translator/codex_tool_arguments.go`, which rewrites integral float
literals in emitted tool-call arguments without touching schemas, so it covers
every tool and parameter and a whitelist entry is no longer the remedy. Tracked
upstream as `router-for-me/CLIProxyAPI#6255`.

### v8.0.13 — selective absorption, effective on main, 2026-10-03

Baseline `v8.0.7`, candidate `v8.0.13` (`d7914afdedca7af95ee974a42453dc49fc1388ce`),
our clean baseline `main` `ceccde5b28c174b2e8d592c1c267c3c4f4e77986`. The published latest stable release was
verified with the upstream GitHub API on 2026-10-03. The range contains
35 commits: 32 non-merge commits and 3 merge commits.

**Owner-authorized age exception, this intake only.** The owner explicitly
waived the time condition on 2026-10-03. `v8.0.13` was published
`2026-10-03T09:02:46Z`; its normal window expires `2026-10-06T09:02:46Z`.
`assess-release.sh v8.0.13 v8.0.7` returned 3 because the release was 4h old.
Assessment then proceeded manually under that authorization, using only
`refs/upstream/tags/*` fetched with `--no-tags`. Neither the script nor the
72-hour policy was changed. No release, tap publication, installation or
service restart is part of this intake.

#### Ported commits

Each selected commit was cherry-picked individually with `-x`. The local SHAs
below identify reviewed task-branch commits before GitHub's rebase merge;
upstream provenance in the commit messages remains the durable lookup key.

| Upstream | Reviewed local | Decision and user-visible result |
| --- | --- | --- |
| `b467a83c` | `5f6a863e` | fix(xai): bump pinned grok client version to 1.0.44 for chat-proxy |
| `cf3102cc` | `5de25777` | refactor(executor): optimize translation logic and improve plugin invocation handling |
| `8fbf152b` | `d38d7b63` | fix(executor): preserve top-level responses token usage when service tier is present |
| `6d57ac90` | `3097e02f` | fix(claude): pin session date in cloaked reminder to prevent prompt cache invalidation |
| `c163bae4` | `208f0662` | fix(auth): preserve terminal unauthorized state and prevent unsafe refresh retry |
| `e6f6f26a` | `767827cc` | fix(claude): handle pause_turn stop reason in responses translation |
| `2044a01f` | `2a20220c` | fix(codex): preserve web search sources in responses request include |
| `5dbce4f3` | `44fd7da4` | perf(claude): prefilter JSON payloads before diagnostic walks |
| `e3abd9ae` | `b01b5735` | fix(antigravity): replace retired Claude 4.6 models with 5.5 |
| `c575291a` | `d5b5173e` | fix(antigravity): align Claude 5.5 token limits with upstream catalog |
| `6b037f61` | `2ae23a4a` | fix(antigravity): use actual Claude 5.5 high model IDs |
| `42d484f9` | `5156b2d3` | fix(claude): map request timeout status to timeout_error |
| `d4692663` | `083a6516` | fix(auth): snapshot auth before unlocking during registration and update |
| `ea8ffd5f` | `5ee551d3` | fix(devin): stream content early and defer thought stop for late signatures |
| `ed4d972e` | `d5ef963b` | fix(responses): complete reasoning summary stream lifecycle events |
| `2c1dcc7c` | `b4b711ce` | fix(claude): include web search sources in codex request when search tool is present |
| `d7914afd` | `5ee5ed04` | fix(codex): expand integer field normalization mappings for tools |

#### Initially skipped commits and reasons

These are the initial assessment decisions. The deep reassessment below
supersedes twelve entries while retaining this record of the first intake.

| Upstream | Decision |
| --- | --- |
| `fd48ea68` | Merge-only wrapper for `b467a83c`; ported its reviewed non-merge commit. |
| `3ebee065` | Defer the 96-file `apply_patch` bridge (14,573 additions). It adds an executor/translator error contract and model capability API overlapping our generic custom/tool-search pipeline. It needs a separate joint protocol review; retain the existing fork implementation. |
| `63c04b4b` | Defer client configuration migration to `client.codex.*`; tied to the deferred bridge and changes config/SDK shape. |
| `028f6a19` | Skip promotional/provider README refresh; our neutral fork READMEs remain authoritative. |
| `f3fd2f23` | Defer routine signature log suppression; low-priority diagnostic churn. |
| `9f35c1dc` | Defer the companion signature logging/sanitizer refactor; replaces structured fields with formatted messages, contrary to this fork's logging convention. |
| `2783e10c` | Not applicable to the retained Antigravity executor: it publishes parsed terminal usage directly before `EnsurePublished`, and has no upstream bridge's `StreamUsageBuffer` to flush. Existing split-usage tests remain enabled. |
| `6fecc6e5` | Merge-only wrapper; assess individual commits instead. |
| `5ec31442` | Keep existing fork Gemini/Vertex/AIStudio terminal fixes (`cbcbe41c`, `47b61ca0`, `a589ffdb`, `6edda221`, `eba4cb0a`, `408f0da4`, `cec7bb55`) and tests. The upstream patch mixes these semantics with deferred bridge state/finalization; do not overwrite the fork helpers. |
| `30aa1f12` | Keep the already-shipped connection-pool test tolerance (`13e5ca57`). Both baseline and candidate normal/race suites pass; no new evidence requires weakening its bounds further. |
| `d7c3c0aa` | Defer optional connection debug tracing; wraps transports across multiple operations and is not needed for the selected compatibility fixes. |
| `52d5507d` | Defer shared provider config migration to `upstream.*`; changes YAML placement, alias precedence and WebSocket option scoping. |
| `3be5fa44` | Defer alias projection/save-layout changes coupled to the skipped config migrations; current layout is retained. |
| `e2bff010` | Merge-only wrapper; assess individual commits instead. |
| `8348923a` | Defer new `host.routing.reset_cooldown` plugin API/ABI callback; independent public capability with no selected-fix dependency. |
| `d306f2c5` | Depends on deferred model `apply_patch` capability/config implementation; existing fork custom capability policy remains in force. |
| `0fb50a18` | Defer v8 management API auth-index injection; broad config/management serialization change unrelated to selected fixes. |
| `a3b77566` | Already satisfied: the retained Codex executor publishes primary model usage before `publishCodexImageToolUsage`; the ordering was only reversed by the deferred bridge. |

#### Conflict and compatibility review

- `2044a01f`: kept its three new search-source response tests while excluding
  an unchanged `apply_patch` test used as upstream patch context. That test's
  bridge helpers do not exist in the retained fork architecture.
- `ed4d972e`: added reasoning summary lifecycle events while retaining the
  entire existing function/custom-call identity branch.
- `cf3102cc`: adapted only its new signature-log test to count the fork's
  structured diagnostic message. The first targeted run exposed this
  upstream-log-format dependency; no sanitizer/logging implementation changed.
- The integer schema mapping update remains executor-aware through existing
  request helpers. The fork's response-side integral-float canonicalization,
  opt-in context and custom-text preservation are unchanged and pass the gate.
- Antigravity's embedded catalog now uses `claude-opus-5-5-high` and
  `claude-sonnet-5-5-high` with upstream limits (1,000,000 context and 128,000
  completion tokens). This records the upstream catalog update, not a live
  provider availability check. Operator aliases to the removed 4.6 IDs should
  be reviewed before a later deployment; this task edits no runtime config.
- No Go dependencies, module paths, plugin APIs, YAML layout, maintenance
  workflows or deprecated management endpoints were changed.

#### Validation and replay evidence

`GOTOOLCHAIN=go1.26.4 bash ops/upstream-intake/verify-absorb.sh`:

| Tree | Result |
| --- | --- |
| baseline (`ceccde5b`) | PASS: maintenance fixtures, gofmt, vet, responses-tools invariants, full suite, all race-sensitive packages, server build |
| candidate source (`74404748`) | PASS: same complete gate |

The tracked Codex catalog is additionally checked offline using the PR workflow's
catalog validation command. The task retains the full logs outside tracked source
under `.git/task-evidence/upstream-v8.0.13-20261003/`.

**Isolated regression request replay:** exactly the same XAI client-version and
Claude server-tool-stop regression tests were run against the baseline (using
Go's source overlay, without editing the baseline) and candidate. Baseline
failed for pinned XAI version `0.2.120` and `pause_turn` emitting completed in
both stream and buffered responses; candidate passed both, including normalized
`PAUSE_TURN`, `max_tokens` and normal stop controls. Files:
`baseline-replay.log`, `candidate-replay.log`, `baseline-replay-overlay.json`.
These are credential-free HTTP/translator fixtures, not production client
traffic. **Real client turn replay: not performed.** The gate and fixture replay
do not prove live xAI, Claude, Devin or Antigravity provider availability.

### v8.0.13 — deep reassessment of deferred work, 2026-10-03

The owner requested individual deep analysis and absorption of valuable deferred
work. The same release and one-off age waiver remain in scope; the permanent
72-hour policy is unchanged. This follow-up begins on clean `main`
`8bbcd48215e61ad3929fb1276e3544f90520d840` (PR #18). Its entries become effective
when the verified follow-up branch reaches `main`.

Eleven more upstream commits contribute substantive code, and one contributes
tests for already-satisfied native catalog behavior. Together with the first
intake, this accounts for 28 substantive ports, one test-only port, and six
remaining entries from the original 35-commit release range. **Partial ports do
not claim complete adoption of their upstream feature.**

| Upstream | Reviewed local | Final decision |
| --- | --- | --- |
| `3ebee065` | `76641cda` | Partial: patch instructions and Unicode fidelity in the canonical generic custom bridge. Preserve namespace/history/response identity and attempt budgets; do not add a second executor bridge or automatic catalog capability. Reject malformed UTF-8/UTF-16 before outer JSON decoding can alter executable input. |
| `63c04b4b` | `45846cc9`, `b413ba52` | Partial: canonical `client.codex.optimize-multi-agent-v2`, historical YAML aliases and client-wide behavior. Preserve old programmatic SDK fields, effective runtime settings and snapshot/save projection. Exclude the separate apply_patch catalog switch. |
| `f3fd2f23` | `1dd07538` | Adapt jointly with the follow-up: deterministic first-seen per-request aggregation; omit the suppression that upstream subsequently reverted. |
| `9f35c1dc` | `1dd07538` | Adapt final aggregate diagnostics while retaining structured fields and signature secrecy. Adjust the translate-once assertion to the aggregate event. |
| `5ec31442` | `497c7b11` | Partial: residual AI Studio HTTPResp terminal/error handling and acceptance of all 2xx starts. Keep the existing Gemini/Vertex usage, MAX_TOKENS, EOF and read-error implementation. |
| `30aa1f12` | `7376080e` | Port robust connection-test bounds. Production pool settings are unchanged; a disabled-pool control opens 24 connections and still fails the bound of 16. |
| `d7c3c0aa` | `4e6d8656` | Port debug connection reuse tracing, redirect coverage and idle cleanup forwarding; preserve the outbound tool-contract guard. |
| `52d5507d` | `b12b011f` | Port shared `upstream.*` configuration and API-key applicability, preserving OAuth-only headers and the fork's `executorDuplexInput`. |
| `3be5fa44` | `4dddd5c2`, `b413ba52` | Adapt historical v8 projection, PATCH/PUT/DELETE, comments and canonical save behavior. Reject invalid alias containers before rewriting. Exclude the deprecated endpoint-specific writer change and its test. |
| `8348923a` | `e4f2de87`, `4483c8cc` | Port additive `host.routing.reset_cooldown` callback and add terminal-401/closed-instance controls. Token files are not saved; separate cooldown-state persistence follows existing manager behavior. |
| `d306f2c5` | `242d601f` | Test-only: nine native templates retain freeform by default and a non-template control remains null. No catalog implementation change is needed. |
| `0fb50a18` | `2dc89686` | Port v8 auth-index synthesis and live-index preference across provider groups; remove transient auth-index fields from update payloads and persistence. |
| `fd48ea68` | existing first intake | Merge wrapper around the already-ported xAI fix. |
| `028f6a19` | retained README | Promotional README/model refresh; keep the neutral fork documentation. |
| `2783e10c` | existing executor | No upstream StreamUsageBuffer in the retained executor; usage is already published before EnsurePublished. |
| `6fecc6e5` | no local merge | Merge wrapper without additional behavior. |
| `e2bff010` | `4e6d8656` substantive port | Merge wrapper; its connection tracing was ported separately. |
| `a3b77566` | existing executor | Main Codex usage already precedes image-tool usage. |

Reviewed local SHAs identify the intake branch source and attribution, not a
promise that a squash merge preserves those SHAs on `main`.

#### Follow-up verification

The clean baseline and candidate source `242d601faf63d759258e0cb42111d95a313fe4d2`
both pass the complete gate with `GOTOOLCHAIN=go1.26.4`: maintenance tooling,
tracked formatting, vet, responses-tools invariants, full suite, race-sensitive
packages and server build. The candidate additionally passes offline Codex model
catalog validation.

The task evidence is retained under
`.git/task-evidence/upstream-v8.0.13-deep-review/`:

- `baseline-gate.log`, `candidate-gate.log`, `catalog-validation.log`;
- `aistudio-baseline-replay.log` and `aistudio-candidate-replay.log`: the same
  five buffered-response cases fail on baseline and pass on candidate;
- `custom-baseline-replay.log`, `custom-candidate-tests.log` and
  `sdk-custom-tests.log`: instruction/Unicode controls, exact valid input,
  namespace identity and programmatic compatibility;
- `alias-boundary-before.log`: the unadapted alias normalizer incorrectly
  accepts a canonical scalar container; candidate rejects it;
- `alias-auth-config-tests.log`, `auth-index-race.log` and
  `connection-terminal-race.log`: config/index compatibility, terminal auth
  protection and extra race checks;
- `transport-unpooled-control.log`: a disabled pool produces 24 connections
  and fails the healthy-pool limit of 16. Normal tests passed 15 repeated runs;
  connection/terminal race cases passed three repeated runs;
- `native-catalog-tests.log`, `source-review.json`: retained template behavior
  and recorded correctness/architecture/security/performance review.

Some earlier exploratory logs record compile or assertion failures corrected
before the final gate; they are not acceptance evidence. Baseline replay failures
and negative controls are intentional regression demonstrations.

**Real client/provider replay: not performed.** These are credential-free
protocol, HTTP/WebSocket and configuration fixtures. No release, tap,
installation, production config or service restart was performed. No dependency,
module-path, workflow or permanent intake-gate change was made.

The detailed owner-facing Chinese report is
[upstream-v8.0.13-deep-review.zh-CN.md](../../docs/upstream-v8.0.13-deep-review.zh-CN.md).

### v8.0.16 — selective absorption, effective on main, 2026-10-09

Baseline `v8.0.13`, candidate `v8.0.16` (`a2976eb8a303f11b4ea5177bce9f9ff752634dfc`),
our clean baseline `main` `6eff48fba9564a03610b98d437d6a2a7ba54ea65`. The range
contains 47 commits (40 non-merge + 7 merge wrappers). `v8.0.16` was published
`2026-10-05T21:46:28Z` and cleared the 72-hour window normally; later releases
(`v8.0.17`–`v8.0.22`) had not settled at assessment time and are out of scope.
Reviewed intake branch `codex/upstream-v8.0.16-intake`, initial head `87ced434`.
Its selected changes reached main through PR #22, merge
`d11564c0aeee2c75af4122441db541bb989ed0de`; this was verified by ancestry on
2026-10-14. This confirmation does not add replay evidence to that intake.

This intake also records the new conflict-resolution rule: when an upstream
commit and our fork solve the same problem, prefer the upstream
implementation and keep fork-only deltas only where they cover behavior
upstream does not address (SOP commit `d1e363ca`).

#### Ported commits

Each selected commit was cherry-picked individually with `-x`; adapted commits
note the fork delta retained.

| Upstream | Local | Decision and user-visible result |
| --- | --- | --- |
| `d2597a24` | `c3f17e76` | fix(openai): preserve unknown encrypted content in responses compat mode |
| `e1e3a811` | `928a6e89` | fix(gemini): enforce object type on schema nodes with properties |
| `ba1bbd75` | `54cae308` | fix(gemini): support document blocks in Claude request translation |
| `97dd9eec` | `7f0b207d` | fix(openai): report error on truncated stream missing finish_reason |
| `d31b61cb` | `ae3e6c2c` + `70dac18f` | fix(websockets): propagate upstream disconnect errors during session activation. Follow-up `70dac18f` completes the port: upstream only resets the recorded terminal error in the Codex `configureConn`, the xAI `configureXAIWebsocketConn` was missed, so a stale terminal error could leak across a retry dial. Fixed by resetting there too. |
| `5c959cb0` | `8a409630` | fix(claude): mark missing thread state error for client replay |
| `26e5efbb` | `2bd7a7c4` | fix(claude): reserve cache breakpoint for thread continuation |
| `5d890405` | `6fdc3bdf` | fix(claude): keep system messages top level for terminal user turns |
| `33ec5502` | `18db5efe` | fix(claude): refine model version parsing for assistant prefill rejection |
| `68afd0d1` | `eb9bd42b` | feat(codex): support action sources in Claude web search translation |
| `16d98881` | `ac0c34a7` | feat(codex): preserve URL citations in OpenAI response translation |
| `611ebba4` | `eed55169` | refactor(executor): unify model reporting and replay logs |
| `678a9257` | `d088c01f` | feat(claude): preserve OAuth tool aliases across thread continuations |
| `a2976eb8` | `f6b7d425` | fix(claude): share OAuth tool alias store across scoped executor copies |
| `c7b4d573` | `1b366586` | Adapted: record reasoning effort from finalized upstream payload after cacheHelper, preserving the fork's outbound tool-contract guard. Upstream usage test targets a test file the fork does not carry; effort reporting remains covered by `usage_helpers_test.go`. |
| `9b9c2bdf` | `5636f8b4` (joint) | feat(antigravity): support Claude 5.5 CAQS thinking signature replay |
| `174248ca` | `5636f8b4` (joint) | fix(antigravity): reject ambiguous CAQS signature wrappers |
| `7e857c37` | `a042b26e` | feat(antigravity): refresh per-account model entitlements safely |
| `17dc1b81` | `57ea709f` | feat(antigravity): context hash rejection logging + replay phases |
| `40f4df07` | `0d9a451d` | feat(antigravity): context hash rejection logging + batch retry logic |
| `8a01bc02` | `44f07ad0` | feat(auth): mutation locking and persistence delta merging |
| `9521fe29` | `31636b68` | fix(antigravity): preserve model lifecycle and cooldown state |
| `ad381435` | `6bcfbdb8` | fix(antigravity): coalesce probes and respect plugin model ownership |
| `61a70dc6` | `87ced434` | Adapted: native messages JSON to SSE adapter kept; fork's unused-param signature retained per new same-problem-prefers-upstream rule. |
| `8a945b3f` | `5e4ed963` | fix(antigravity): restore Claude 4.6 model definitions. Operator confirmed 2026-10-09 that Claude 4.6 (`claude-opus-4-6-thinking`, `claude-sonnet-4-6`) is still live on Antigravity, so the earlier "retired" skip rationale no longer holds; direct `-x` port alongside the 5.5 CAQS support already absorbed. |

#### Skipped commits and reasons

| Upstream | Decision |
| --- | --- |
| `980c3bc3` `6d06098d` `d2e3a77e` `dfd7c638` | Skip promotional/community README additions; neutral fork READMEs remain authoritative. |
| `8ef43e4d` | Defer AxisNow sponsorship removal; conflicts with fork README sponsorship handling, needs separate review. |
| `01e28567` | Defer Devin-catalog home-build disable; the fork has no `catalog_sources.go` home-build path (custom catalog system not ported), conflicts. Revisit if the catalog-sources feature is ever adopted. |
| _(removed)_ | `8a945b3f` absorbed as a ported commit above (operator-confirmed 4.6 live on 2026-10-09); row removed so the skipped list stays honest. |
| `91c934f6` | Defer local shell tool translation; conflicts with the fork's custom/apply_patch tool pipeline (`responses_tool_index.go`, response aggregator). Needs joint protocol review like the earlier deferred bridge. |
| `868e137f` | Defer payload-finalizer architecture; cross-cutting executor rewrite touching every request path plus an AGENTS.md invariant the fork has not adopted. Needs separate design review. |
| `cbc4cd55` | Defer Antigravity interactions session continuation; response-file conflict depends on the fork-absent `applypatch` package. Needs joint review with the deferred bridge. |
| `c997cbb5` | Defer error-aware response flushing; stream-file conflict with the fork's delivery/usage accounting. Needs joint review, not a blind port. |
| `eb6a768d` | Defer explicit prompt-cache options (950-line test, cloaking rewrite); conflicts across three executor files. Assess separately given cloaking is fork-sensitive. |
| `0ea4e1dc` | Defer clean-EOF `[DONE]` synthesis; depends on `CanFinalizeResponseStream` in the fork-diverged `apply_patch.go` plus conflicts in compat executor and responses translator. Revisit after the bridge/apply_patch direction is settled. |
| `a4acc9f7` | Defer terminal-disconnect-as-success; stream-file conflict with fork delivery accounting. Needs joint review. |
| `4e4dede4` | Defer backend-error surfacing + multi-line payloads; stream-file conflict with fork delivery accounting. Needs joint review. |
| `1672be5d` | Defer custom model catalog sources (263-line new subsystem + config shape); same rationale as the v8.0.13 intake which deferred the coupled config migrations. |
| Merge wrappers `abe54e85` `7dc25a7e` `30700d44` `3e83083b` `13da3cee` `38a089ea` `0594a632` | No independent behavior; covered by assessing individual commits. |

#### Validation and replay evidence

`GOTOOLCHAIN=go1.26.4 bash ops/upstream-intake/verify-absorb.sh` on the intake
branch head: PASS (maintenance fixtures, gofmt, vet, responses-tools
invariants, full suite, all race-sensitive packages, server build).

**Request/response replay: not recorded for this historical intake.** The
current ledger supplies no baseline-vs-candidate fixture evidence for v8.0.16;
its main ancestry and automated gate must not be presented as a live replay.

**Real client/provider replay: not performed.** Credential-free checks only. No
release, tap, installation, production config or service restart was performed.

### v8.0.23 — selective intake, effective with this ledger's merge, 2026-10-14

Comparison baseline v8.0.16; clean main
`5349958cfc0d4ca1e805dbecee219fce646afe2e`; candidate upstream
`d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5`. Upstream release metadata was checked
on `2026-10-14T04:10:57Z`: v8.0.23 was published `2026-10-09T14:32:57Z`,
109 hours before assessment. `assess-release.sh v8.0.23 v8.0.16` passed the
normal 72-hour gate. Newer published releases through v8.0.28 had not cleared
that window at assessment and were not candidates for absorption.

The assessed range contains 47 commits: 41 non-merge commits and 6 merge
wrappers. Decisions cover all 47. The task branch is
`codex/upstream-v8.0.23-intake`; selected source and adapted regressions are
frozen at `439e9f12`. No dependency, public SDK request-converter signature,
configuration schema or service lifecycle migration is included.

#### Ported commits

All 26 upstream commits were selected individually and carry `-x` provenance.
The local mapping identifies the reviewed commits before a protected-main
squash/rebase, rather than claiming the upstream commits are main ancestors.

| Upstream | Reviewed local | User-visible result |
| --- | --- | --- |
| `d510dccf` | `5c271bab` | fix(gemini): support camelCase systemInstruction in request translation |
| `a0d31924` | `a5a173b8` | fix(gemini): map refusal finish reasons and handle partless stream completion in Claude response translation |
| `68ea171d` | `2fd069d8` | fix(gemini): handle carrier thinking blocks for signed visible text and tool calls in Claude response translation |
| `50d5fe18` | `e6eb1566` | fix(cliproxy): avoid selecting rejected credentials during refresh and preserve active quota cooldowns |
| `6920ed5d` | `17ea97af` | fix(cliproxy): propagate credential version and registration epoch in execution results |
| `935aa6e3` | `f3ebf7f5` | fix(cliproxy): prevent in-flight token refresh from overwriting concurrently updated credentials |
| `dbc4aad9` | `23be272e` | fix(gemini): ignore context cancellation after terminal chunk in Vertex stream |
| `eca1d113` | `d7f42380` | fix(claude): keep system prompt at top level for single terminal user turn |
| `39632e52` | `6b0ea1f4` | fix(claude): keep system prompt at top level for leading user message runs |
| `14dc4e05` | `d872e35e` | fix(claude): relocate caller system prompt to messages for OAuth requests |
| `cd9e5ace` | `9cda842d` | fix(api): compact json error response bodies for SSE compatibility |
| `e7b22260` | `c43b02f5` | fix(claude): support plain-text and structured response bodies for missing thread errors |
| `249e9ef9` | `fbe0a532` | fix(meta): preserve foreign reasoning encrypted content in responses requests |
| `0a23627d` | `cf1f1d3b` | fix(config): normalize collection styles and unquote mapping keys in v8 api |
| `7c2133e2` | `c91e2401` | fix(cliproxy): reconcile concurrent auth updates during batch model registration |
| `ab151f5d` | `18d02909` | fix(antigravity): include thoughts token count in openai completion tokens |
| `80809679` | `9a4b402c` | fix(claude): ignore overage billing resets in unified rate limit parsing |
| `a881c036` | `fc0d336f` | fix(claude): respect registry completion limit for default responses max tokens |
| `67465884` | `bd9e2bf7` | fix(pluginhost): detach unselected plugins on config apply |
| `51bde0ff` | `a922ae50` | fix(codex): preserve resolved upstream model in direct openai image requests |
| `9a7db8e3` | `ba2fcbc1` | fix(claude): defer stream message delta emission to trailing usage chunk |
| `2d8ec73b` | `3dbe5d5c` | fix(claude): preserve mid-conversation effort directives during system insertion |
| `f8e4a840` | `397d7a8f` | fix(openai-compat): measure streaming TTFT on effective token frames |
| `4a8ecf5c` | `ad42a57f` | fix(auth): skip local filtering for home dispatch |
| `e9c8769a` | `9fa5e112` | fix(executor): normalize and restore non-portable tool call IDs |
| `76c87ce9` | `7db9b260` | fix(xai): reset terminal error on websocket connection configuration |

#### Fork compatibility and convergence

- `76c87ce9` is equivalent to fork follow-up `70dac18f`: the existing
  `resetTerminalError(conn)` call already matches upstream. Only the missing
  upstream replacement-connection regression was added. The behavior is now
  attributed to upstream; no second reset or parallel implementation exists.
- Gemini response translation retains exact tool-name restoration, including
  case/underscore collisions. Auth result version/registration guards also cover
  the fork-only availability-neutral tool-contract failure paths, so a stale
  stream cannot penalize refreshed or re-registered credentials.
- Claude system-placement changes retain fork CLI detection, session-date
  pinning, OAuth tool aliases, caller cache behavior and the original count_tokens
  contract. Tests requiring the previously deferred explicit-cache subsystem
  were adapted to existing helpers; its unrelated test file was not imported.
- Upstream `51bde0ff` preserves configured image-model identities. Follow-up
  `7811431c` applies payload rules once after JSON/multipart normalization;
  explicit model overrides and filters remain authoritative on the wire.
- Upstream `e9c8769a` provides reversible Devin IDs and collision-safe Claude
  history repair. Follow-up `439e9f12` repairs IDs before generation payload
  rules, preserving explicit final overrides/filters; local and native upstream
  count_tokens paths also repair call/result pairs. Tests inspect the actual
  Connect protobuf rather than requiring the deferred production finalizer.
  Finalizer-only payload rules for Devin/count_tokens are not claimed supported.
- `dbc4aad9` ignores only `context.Canceled` after a terminal chunk has actually
  been delivered. The fork's real-read-error handling remains intact: truncated
  streams do not receive synthetic completion. Both Vertex authentication paths
  are covered by local HTTP fixtures.
- Home selection now bypasses redundant local availability filtering. Existing
  Home wire fields and dispatch/result contracts are unchanged, so no companion
  CLIProxyAPIHome source update is required for this local fix. A live Home
  deployment was not exercised.

#### Deferred or inapplicable non-merge commits

These are explicit exclusions from this selective alignment, not validated
features. The decision is based on changed contracts and dependencies, not a
commit's `feat`/`fix` prefix.

| Upstream | Decision and remaining boundary |
| --- | --- |
| `6a853dce` | Defer new Grok TTS endpoints, speech handler and model registration. This adds a public audio surface beyond current coding-client compatibility intake. |
| `306c9bd8` | Defer the coupled speech-only routing, TTS payload finalization and speech error-scoping fixes with `6a853dce`; the speech executor/entrypoint they repair is not adopted. |
| `da5f109f` | Inapplicable catalog-restore test update: the custom catalog test/source subsystem was not adopted in v8.0.16 and the target test file does not exist in this fork. |
| `57bde351` | Defer apply_patch-bridge failure usage buffering with the previously deferred bridge/finalizer. Its `RecordApplyPatchStreamFailureWithUsage`/translation-error paths are absent here; keep the fork's responses-tools accounting. |
| `3b69068e` | Defer attachment/empty-turn conversion changes as a coupled error-propagation change. Its initial checked-request wrapper preserves the old converter signature, but it still changes dispatch rejection and shared translation contracts. Review with the two following converter commits and the fork tool pipeline. |
| `f51624aa` | Defer unsendable-attachment rejection across converters/executors with `3b69068e`; do not silently discard attachment-only turns or claim this rejection contract is implemented here. |
| `0f96f568` | Defer the public SDK `RequestTransform`/request converters changing from `[]byte` to `([]byte, error)` (140 files). The fork deliberately retains its existing public signature. |
| `7c41cd05` | Defer global `github-token`, reload precedence and plugin-store fallback credentials. HTTPS api.github.com scoping exists upstream, but config/persistence and cross-consumer credential precedence require separate adoption review. |
| `90654da5` | Defer the npm-backed Grok version updater and new background lifecycle/outbound dependency; keep the pinned client version. The lookup uses an HTTP/context timeout outside credential acquisition, conflicting with this fork's current timeout rule. |
| `34e73c75` | Defer proxy/semver/lifecycle corrections with the updater. They do not remove its lookup timeout contract; no updater is introduced partially. |
| `2d5351b3` | Defer opt-in Vertex native Interactions mode, credential flag and new streaming path; it depends on the unadopted translation-error/apply_patch helper chain. Existing Vertex generateContent repairs are ported independently. |
| `54946fa3` | Inapplicable removal of upstream community/QQ README content; fork READMEs already omit that section. |
| `6331b38b` | Defer new `response.interrupt` control protocol, async downstream reader, session interruption capability and HTTP fallback `response.incomplete`. It needs joint validation with fork frame authorization, delivery/usage accounting, and turn lifecycle; current WS support is retained without claiming interrupts. |
| `d318bcc3` | Defer late-interrupt response-ID tracking, lifecycle locks and sanitized diagnostics with `6331b38b`. This corrects the new interrupt mechanism, not existing fork behavior; the pair must be adopted together. |
| `2bc236d2` | Defer Claude Responses compaction: it introduces an extra model request and opaque Antigravity-style capsules, rewrites orphan tool history, and changes budget/replay semantics. It needs separate validation against fork tool contracts and accounting. |

Merge wrappers have no additional independently ported behavior:
`061001f8` (TTS), `f3703e82` (GitHub token), `7cd8f7ba` (attachments),
`298649f6` (Grok updater), `84740392` (TTFT), `9612b7ac` (Home).
Their constituent changes are accounted for above.

#### Validation and evidence boundary

| Check | Baseline / candidate | Result |
| --- | --- | --- |
| Full maintenance gate with Go 1.26.4 | clean `5349958c` | PASS: maintenance tooling, pinned gofmt, vet, responses-tools invariants, full suite, seven race-sensitive packages, server build |
| Same full maintenance gate | source `439e9f12` | PASS: the same seven stages, including full suite and race checks |
| Identical fixed-request fixtures on complete source trees | baseline `5349958c` / candidate `439e9f12` | Four expected assertion failures on baseline; all four pass on candidate |
| Remove pre-policy Claude tool-ID repair with a temporary Go overlay | candidate mutation | Both Execute and ExecuteStream regressions fail on the expected behavioral assertion; unmodified source passes |

The paired fixture replay uses identical test inputs, local/no-auth configuration
and Go 1.26.4 on a complete archived baseline source tree and the candidate.
The same three frozen regression test files are supplied to both trees (copies
in the temporary baseline and a Go overlay for the candidate). The four
leaf cases cover camelCase Gemini system instructions, compact JSON SSE errors,
and Antigravity completion-token accounting with and without visible tokens.
A failure counts only after compilation and the expected assertion is observed.
This is targeted compatibility evidence, not a statistical success-rate estimate.

Raw gate logs, frozen test copies, source archive, overlays and one-off replay
scripts are under the OS temporary directory, outside Git. Reusable regression
cases are ordinary Go tests beside the production packages; no test framework
or process report is added.

**Real client/provider and performance replay: not performed in this intake.**
There is no new official-client closure rate, provider coverage or token/cost
saving measurement. Earlier tool-loading reports retain their stated candidate
and scope; this intake does not promote them to measurements of this new tree.
No release/tag, tap publication, installation, production configuration change
or service restart is included.

### v8.0.35 — conservative selective absorption, 2026-10-23

Baseline v8.0.23; mature candidate v8.0.35 (`ae3ee3be303fb07fb8173279923413005bf32177`), published
`2026-10-19T19:02:46Z`. The 72-hour window was enforced without an exception.
Published fork baseline `1ad1d35c319aa028d4081bbe6551071792992270` was verified
before porting. A separate unpublished README commit and the original dirty
working branch were preserved outside the task worktree.

The mature range contains 169 commits (151 non-merge + 18 merge wrappers).
Port 25 independent repairs; leave 126 non-merge commits and all 18 wrappers
excluded. The additional 63 reviewed commits through v8.0.39 remain deferred
by age. The [intake report](../../docs/upstream-v8.0.35-intake.md) explains the
scope and integration choices; the [232-SHA decision manifest](reviews/v8.0.35-20261023.json)
records every port, skip, dependency, earlier recommendation and pending item.

#### Ported mapping

| Upstream | Local task commit | Behavior |
| --- | --- | --- |
| `be8c35f2` | `ac70c4ec` (+ `0e85062c`) | fix(claude): ignore invalid and null cache control entries in executor |
| `b0debb35` | `554a4fae` | fix(codex): shorten over-length tool call IDs in chat completions |
| `cad1b503` | `aa5fab3b` | fix(devin): do not seed input token estimates for claude streams |
| `dfe0a0ef` | `c66c9e0e` | fix(antigravity): omit tool_calls from default streaming delta template |
| `18b6cc8a` | `1368c458` | refactor(registry): use model getters for Devin and Meta |
| `15d2148b` | `ccff1507` | fix(gemini): ensure trailing user turn for function response with media |
| `a2718314` | `484267a0` | fix(antigravity): drop trailing thought-only model turns |
| `02fc76b1` | `27747f0e` | fix(claude): restore passthrough MCP tools wrapped with their full mcp__ name |
| `bbcd5734` | `5e543264` | fix(registry): treat credential_quota as quota cooldown in model availability |
| `40ce0e01` | `310eff09` | feat(kimi): support schema normalization for namespaced tools and input tool lists |
| `57d005fe` | `917b796a` | fix(codex): strip out-of-range int64 numeric bounds from tool schemas |
| `56eee6a4` | `51e1a4fc` | fix(cliproxy): treat HTTP/2 closed connection errors as transient lifecycle drops |
| `58257007` | `6545ad42` | feat(claude): ensure unique tool use IDs across turns in Gemini response translator |
| `912a038b` | `5b1d9971` | fix(antigravity): correct token usage calculations for cached tokens in Gemini responses |
| `ceb4397f` | `ed0324b6` | feat(executor): drop trailing thought-only model turns in Gemini Vertex requests |
| `bbfd7886` | `8d7c3ca5` | fix(xai): drop web_search options the Responses API rejects |
| `2d7f5377` | `c91e09cd` | feat(vertex): honor full /publishers/ prefix in API-key base-url |
| `3a072298` | `2fd895d9` | feat(meta): filter subscription usage telemetry from SSE streams and responses |
| `db1bab01` | `74f3c5de` | feat(runtime): narrow Claude per-minute rate-limit cooldown when short Retry-After is present |
| `a7608a8a` | `3196d66f` | feat(pluginhost): preserve auth identity and model registration for relative auth paths |
| `4337f1f0` | `53f2fb0c` | fix(logging): escape request path in access log to prevent log injection |
| `904ec710` | `a577cabc` | feat(auth): enable expired antigravity AI credits negative hint recovery and retry logic |
| `07feedf7` | `5c8e26cd` | fix(api): allow empty home model catalog |
| `4d34502f` | `41b29cba` | fix(api): reject home models error envelopes |
| `f9437700` | `bd9fd0b6` | feat(config): add PreapareConfigFileForUpload and seed remote storage with hashed secrets |

`be8c35f2` also carries only the cache-control validity helper from `eb6a768d`
in follow-up `ac70c4ec`. Explicit cache options remain deferred. The one-result
translator API, exact tool identities, responses-tools ownership and existing
Vertex post-rule boundary normalization are preserved; the report records all
production adaptations. Local hashes identify task commits, not the final
squash commit.

#### Verification

- Clean baseline and candidate: `GOTOOLCHAIN=go1.26.4 bash ops/upstream-intake/verify-absorb.sh`
  PASS in all seven stages, including full suite, seven race-sensitive packages
  and server build. Verified code is `cea8acb5170e431df9705a0542d3454de70e7694`.
- Offline tracked Codex catalog validation: PASS.
- All 25 ports have observed behavioral regression failures before their fix
  and passing affected packages afterward; helper-only new APIs are not treated
  as baseline compilation failures proving a bug.
- Paired complete-source replay: identical 21 top-level tests; original baseline
  has 16 expected assertion failures and 5 passing controls, candidate has 21
  passes. Compilation succeeds on both trees.
- A temporary overlay inverting cache-control validity is killed by the expected
  assertion; production sources remain unchanged.
- Independent whole-branch review: APPROVE, no blocking findings. Optional:
  add a direct WebSocket telemetry filtering regression; no implementation defect
  found. Additional race checks across five affected packages: PASS.
- Integration requires an exact-head PR build and remote-main tree match, recorded
  in the PR. This entry is effective only after verified merge.

#### Deferred boundaries

No new feature root, model removal, native tool-search replacement, dependency
upgrade or partial auth/watcher/config concurrency chain is included. The
confirmed stale-success cooldown and uncertain auth-scan risks remain pending
with their complete upstream repair chains. The two confirmed upstream schema
bugs are excluded and reported as router-for-me/CLIProxyAPI#6897 and #6898.
The fork's integral-float canonicalizer and recursive schema flattening remain
necessary. Home empty-catalog handling changes no Home wire shape and needs no
companion source change.

Real client/provider and performance replay: **not performed**. Local/fake
backend fixtures do not establish new client closure, entitlement, latency or
cost savings. No release/tag, tap, installation, production configuration change
or service restart was performed. Raw evidence is retained locally under
`.git/task-evidence/upstream-intake-v8.0.35-20261023/`.

## How to fill this in

After `assess-release.sh` and `verify-absorb.sh`:

```markdown
| v8.0.6 | 2026-10-01 | `abc1234` tool-schema fix, `def5678` reconnect backoff | `9999999` CI churn, `8888888` config rename | PASS | 2026-10-01 |
```

Keep the skipped column honest. A release where most commits are skipped is a
normal outcome and worth recording; a release with an empty ported column
should say why in a comment below the table.


### v8.0.35 — three-helper convergence follow-up, 2026-10-23

Starting main: `5cc1929a58156977d2d9b59ee215609be0aea311`.
Code candidate: `deb043fb3e7af463ea78a0479264d37135cf279d`.
This entry supersedes only the three `defer_scope` decisions listed below in
[the original review](reviews/v8.0.35-20261023.json). All other decisions and
all four fork purposes remain unchanged. This follow-up becomes absorbed only
on a verified protected-branch merge. No release or local service change is part
of this follow-up.

| Upstream | Local | Scope and retained boundaries |
| --- | --- | --- |
| `7795de19ca80940adeafe45ebe7c5dbfc4931cc5` | `878927fba4e911822de299dc29354dafd382721f` | Adopt Codex SSE scanner, executor integration and tests; preserve raw bootstrap budgets, replay, responses-tools and cancellation handling. |
| `b0db5448e8f8cce9b58e5d02eab23d32d4ec5dca` | `dc5664053835a48e36907b06b9af767e7b5a6441` | Adapt per-response fallback item IDs only; preserve supplied IDs and namespaced custom tools; exclude the separate upstream apply_patch pipeline/tests. |
| `e240bd84321c55b96b226d43fa557c9a8be55a00` | `deb043fb3e7af463ea78a0479264d37135cf279d` | Adapt model, multi-agent template and validator fallback; prefer gpt-5.5 when present. Exclude synthesized fetch entries/visibility changes and catalog/prompt snapshots. |

Stable source releases cleared the 72-hour gate: v8.0.25 was published
`2026-10-12T10:43:27Z`; v8.0.28 was published `2026-10-14T00:41:06Z`.
The latest observed v8.0.40 (`2026-10-23T00:10:37Z`) was used for issue rechecks,
not as an intake source.

Validation used `GOTOOLCHAIN=go1.26.4`:

- Clean baseline and code candidate `ops/upstream-intake/verify-absorb.sh`: PASS
  for maintenance tooling, tracked gofmt, vet, responses-tools invariants,
  full suite, race-sensitive packages and server build. No stage was waived.
- Each port passed affected package tests and a server build before its commit.
- Identical fixed-request/unit fixtures, using baseline production-source overlays
  and candidate tests: six top-level behavioral failures on the baseline, six
  passes on the candidate, without compilation failures. Coverage includes split
  SSE completion in buffered/unbuffered streaming and non-stream execution,
  cross-response ID collision, model loading, multi-agent metadata and validation.
- Reversing the scanner's incomplete-JSON condition is caught by its tests.
  Explicit IDs/custom identity and preferred-template ordering also pass.
- Independent read-only review: APPROVE for each of the three commits, no required
  findings. Optional direct coverage for cancellation while an incomplete event
  is held remains a test gap; existing cancellation paths were retained.
- One initial executor-package run failed an unrelated Antigravity credits
  `Known=false` assertion (`antigravity_executor_credits_test.go:942`). The cause
  was not established. Baseline isolated count20 and broader subset, candidate
  package recheck and complete candidate gate passed. No credits code was changed;
  the initial failure is preserved, not treated as a fixed defect.

Replay limit: deterministic local fixtures/transports, not a new live provider
or desktop client certification. Production config/credentials were not used.
Detailed local logs are under `.git/task-evidence/convergence-ports-20261023/`;
portable decisions and mapping are in
[the follow-up manifest](reviews/v8.0.35-followup-20261023.json).

Upstream feedback (objective scope, no duplicate bug reports):

- New opt-in bounded recursive-schema mode proposal: [#6900](https://github.com/router-for-me/CLIProxyAPI/issues/6900).
- Response-side number compatibility: [#6255 implementation notes](https://github.com/router-for-me/CLIProxyAPI/issues/6255#issuecomment-6355257555).
- Route/attempt-scoped portable tools: [#6221 scoped proposal](https://github.com/router-for-me/CLIProxyAPI/issues/6221#issuecomment-6355257755).
- Stream regression coverage: [#6258 recheck](https://github.com/router-for-me/CLIProxyAPI/issues/6258#issuecomment-6355257968), explicitly recording that split usage and MAX_TOKENS pass in v8.0.40.
- Existing schema defects [#6897](https://github.com/router-for-me/CLIProxyAPI/issues/6897#issuecomment-6355258205) and [#6898](https://github.com/router-for-me/CLIProxyAPI/issues/6898#issuecomment-6355258484) rechecked on v8.0.40, still reproducible offline.
- Exact-name samples Read/read and _lookup/lookup pass on v8.0.40; no new bug
  was filed for those samples and no broad equivalence claim is made.
