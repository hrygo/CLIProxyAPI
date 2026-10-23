# Conservative upstream v8.0.35 intake

Verified on 2026-10-23 (Asia/Shanghai). This is a selective source intake, with
25 independently reviewed repairs from the 169 commits in `v8.0.23..v8.0.35`.
The range contains 151 non-merge commits and 18 merge wrappers; 144 mature
commits remain excluded. The wider review includes 63 later commits through
v8.0.39, which remain ineligible for this intake.

## Baseline and maturity

- Accepted previous selective upstream baseline: `v8.0.23`
  (`d318bcc3afb9ea8782862f5e8cdb541afdc9dfa5`).
- Clean published fork baseline: `1ad1d35c319aa028d4081bbe6551071792992270`.
- Mature candidate: `v8.0.35` (`ae3ee3be303fb07fb8173279923413005bf32177`), published
  `2026-10-19T19:02:46Z`; the 72-hour window cleared before assessment.
- Code candidate verified by the complete intake gate:
  `cea8acb5170e431df9705a0542d3454de70e7694`.
- Releases v8.0.36–v8.0.39 had not cleared the window when selected.
  A fetched tag or a favorable review is not an adopted change.

The separate unpublished README commit `73fb84d7`, the existing dirty working
branch, configuration and credentials remain outside this PR. The task uses an
isolated managed worktree based on published fork main.

## Selected repairs

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

Each port retains its full upstream SHA in the commit message. Where upstream
append context belongs to a deferred feature, only the actual added regression
is relocated. The final squash preserves the port mapping in this report and
in the [machine-readable per-commit decisions](../ops/upstream-intake/reviews/v8.0.35-20261023.json).
Local hashes above identify reviewed task commits; the integration commit is
the PR squash, whose tree must match the verified candidate.

## Integration choices

- Cache-control validation needs the eight-line `isValidClaudeCacheControl`
  helper from previously deferred `eb6a768d`. Only that helper is carried;
  explicit cache options and the larger cloaking rewrite remain deferred.
- Keep the fork translator's one-result public API and exact Gemini/Claude
  tool identity restoration. Long-call-ID tests use an independent fixed hash
  collision oracle rather than deriving expected input from the helper tested.
- Drop replayed Vertex thought-only tails before payload overrides, retain the
  existing post-rule Gemini user-boundary normalization, and leave countTokens
  untouched. This ports the repair without importing the payload finalizer.
- Meta filtering changes only subscription telemetry delivery. The fork retains
  its responses-tools, completion and usage accounting; the deferred apply_patch
  stream implementation is not imported.
- Plugin identity coverage includes a relative configured directory with an
  absolute incoming path, as well as absolute and relative matching paths.
  The existing single-auth callback contract stays in place.
- Remote config seeding hashes the management secret before Git/object/SQL
  persistence. Invalid YAML or aliases retaining the plaintext value refuse
  the seed. Legacy and v8 layouts, comment preservation and repeated refusal
  are covered. Fake backends exercise real MinIO request construction and a
  database/sql driver; no production store or credentials are used.
- Home empty catalogs now succeed, while malformed records and error envelopes
  still fail. The existing Home wire contract is unchanged, so no companion
  CLIProxyAPIHome code update is required for this local correction.

## Explicit exclusions

The complete 232-SHA decision list is versioned in the linked manifest. Its
`review_recommendation` records the earlier analysis; `decision` records what
this intake actually implements. In particular, `adopt`/`adapt` recommendations
left outside these 25 ports are deferred, not silently included.

- Preserve the fork response-side integral-float canonicalizer, recursive
  local-ref flattening, generic/custom tools, and central responses-tools
  pipeline; selected upstream repairs do not replace those behaviors.
- Defer the native Claude tool-search migration, SIWC, Vertex OpenAPI, Home
  cluster/search cache changes, mDNS, remote compaction, model catalog additions
  and removals, new public usage/plugin APIs, and cloaking/header policies.
- Defer the auth dispatch-watermark, watcher reconciliation, model-publication
  fencing and config concurrency chains. Existing-main failures were reproduced
  during the deep review; these are unresolved risks, not regressions introduced
  by this intake. Their final closure includes later releases, notably the
  snapshot and unreadable-directory repairs in v8.0.39. Do not import partial
  chains under a conservative policy.
- Defer Go 1.26.10 and the mixed dependency bump from the immature release.
  Both fork CI workflows and this intake retain Go 1.26.4. This does not claim
  the later toolchain security patches have been adopted.
- Exclude schema ref/sibling inlining `c558d320` and open-object argument cleanup
  `202b8b8c`: confirmed defects were reported upstream as
  [#6897](https://github.com/router-for-me/CLIProxyAPI/issues/6897) and
  [#6898](https://github.com/router-for-me/CLIProxyAPI/issues/6898).
  Their dependent schema-cleaner changes remain deferred as well.

## Verification and limits

| Check | Result |
| --- | --- |
| Clean baseline full intake gate, Go 1.26.4 | PASS |
| Candidate full intake gate, Go 1.26.4 | PASS |
| Offline tracked Codex model catalog validation | PASS |
| Per-port regression before/after the repair | 25 behavioral RED → package GREEN checks |
| Identical fixed requests on complete original baseline and candidate | 21 top-level tests: baseline 16 FAIL + 5 PASS; candidate 21 PASS |
| Invert cache-control validity condition using a temporary Go overlay | Regression fails on the expected assertion; original source stays unchanged |
| Independent whole-branch review | APPROVE: no Critical/Required/Nit; one Optional direct WebSocket telemetry test gap |
| Additional race checks: config, store, pluginhost, API and OpenAI handlers | PASS |
| Integration gate | Require exact-head PR build and matching remote-main tree; results recorded in the PR |

The complete intake gate covers maintenance fixtures, the pinned formatter,
vet, responses-tools invariants, the full Go suite, seven race-sensitive
packages and a server build. Codex catalog validation is recorded separately.
The paired replay uses the same request bodies, local/fake configuration,
Go 1.26.4 and test names on both trees; it has no compilation failures. Nested
subtests are not counted as independent top-level successes.

Raw logs, replay sources and scripts, mutation overlay, per-port mapping and
hashes are retained in the repository's local task-evidence directory
`upstream-intake-v8.0.35-20261023`. Reusable regressions are ordinary checked-in
Go tests beside the affected packages.

The optional review follow-up is a direct WebSocket-forward regression mixing
subscription telemetry with ordinary output, tool calls and completion events.
Existing Meta executor/SSE coverage passes; the independent review found no
implementation defect in the WebSocket filter. This test gap does not block intake.

**Live client/provider replay and performance measurement were not performed.**
No new official-client success rate, provider entitlement or cost-saving claim
follows from these fixtures. No release/tag, tap, installation, production
configuration change or service restart is included.
