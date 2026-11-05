# Fork capability inventory

This index identifies known behaviors and operational boundaries that must be
accounted for when replaying the fork onto an upstream release. It is a starting
inventory, not proof that every fork change has been classified or that an
upstream implementation is equivalent. The accepted baseline and decisions for
each intake are recorded in [absorbed.md](absorbed.md).

The v8.0.51 replay is an unaccepted candidate. Its [spike record](reviews/v8.0.51-replay-spike.md)
describes the candidate and its limits; it does not establish the current `main`
baseline. Compare the actual `origin/main` commit and all changes since the last
accepted replay as well as the previous replay's patch series before declaring
this inventory complete. Include production behavior, configuration and SDK
compatibility, tests, documentation, CI, releases, and installation. Classify
unmapped changes before integration rather than treating them as disposable.

## Known requirements and evidence entry points

The tests and paths below are evidence entry points, not exhaustive test lists.
Each row needs an explicit per-release disposition and evidence in the intake
record, even when the new upstream appears to have similar code.

| ID | Requirement to preserve or replace equivalently | Evidence entry points |
| --- | --- | --- |
| F01 | Strict Codex clients receive canonical integral tool arguments without changing unrelated values. | [translator](../../sdk/translator/codex_tool_arguments.go), [tests](../../sdk/translator/codex_tool_arguments_boundaries_test.go) |
| F02 | Responses tool discovery, custom tools, and local schema references retain the documented request and response semantics. | [tool guide](../../docs/responses-tools.md), [schema tests](../../internal/responsestools/schema_test.go), [integration tests](../../test/responses_tools_integration_test.go) |
| F03 | Adapted tool requests are checked at the actual outbound boundary, including provider and WebSocket paths; invalid identities are not sent upstream. | [outbound tests](../../test/responses_tools_outbound_guard_test.go), [Codex WebSocket tests](../../internal/runtime/executor/codex_websockets_itemids_test.go), [Meta tests](../../internal/runtime/executor/meta_executor_identity_test.go) |
| F04 | The opt-in first-frame timeout abandons and retries silent attempts within its budget, and returns the appropriate error before response bytes are sent. It does not impose a post-first-frame network timeout. | [handler](../../sdk/api/handlers/handlers_stream.go), [bootstrap tests](../../sdk/api/handlers/handlers_stream_bootstrap_test.go) |
| F05 | Gemini-family streaming preserves verified terminal, tool and observed-usage behavior across Gemini, Vertex, AI Studio and Antigravity. Timely completion and late-data limits are recorded per candidate. | [Gemini usage tests](../../internal/runtime/executor/gemini_usage_partial_replay_test.go), [held-connection tests](../../internal/runtime/executor/gemini_finish_without_usage_hold_test.go), [Vertex tests](../../internal/runtime/executor/gemini_vertex_executor_terminal_test.go), [AI Studio tests](../../internal/runtime/executor/aistudio_http_response_terminal_test.go), [Antigravity tests](../../internal/runtime/executor/antigravity_executor_split_usage_test.go), [spike limits](reviews/v8.0.51-replay-spike.md) |
| F06 | OpenAI-compatible streams do not report truncated tool calls as successful; reasoning-only completion and post-plugin terminal checks remain covered. | [tool terminal tests](../../internal/runtime/executor/openai_compat_tool_terminal_test.go), [reasoning tests](../../internal/runtime/executor/openai_compat_reasoning_terminal_test.go), [plugin tests](../../internal/runtime/executor/openai_compat_terminal_plugin_test.go) |
| F07 | Concurrent Claude executor preparation safely reads shared credential metadata. | [race tests](../../internal/runtime/executor/claude_executor_auth_race_test.go) |
| F08 | GLM tool results omit the incompatible `name` field while other compatibility routes retain their required behavior. | [request translator](../../internal/translator/openai/claude/openai_claude_request.go), [tests](../../internal/translator/openai/claude/glm_tool_result_test.go) |
| F09 | Existing programmatic Codex multi-agent configuration and request failure observers remain compatible. | [configuration tests](../../internal/config/client_compatibility_test.go), [observer tests](../../sdk/api/handlers/handlers_preparation_diagnostics_test.go) |
| F10 | Home's optional WebSocket integration preserves session identity, authorization and terminal delivery. Resource release is verified within the documented transport fixture limits. | [transport tests](../../sdk/api/handlers/openai/openai_responses_websocket_home_transport_test.go), [disconnect tests](../../sdk/api/handlers/openai/openai_responses_websocket_home_disconnect_regression_test.go), [spike limits](reviews/v8.0.51-replay-spike.md) |
| F11 | Model fallback, interaction item identity and Vertex resource prefixes retain their reviewed behavior. | [model tests](../../internal/client/codex/models/models_test.go), [interaction tests](../../internal/translator/openai/interactions/responses/interactions_openai_responses_response_test.go), [Vertex tests](../../internal/runtime/executor/gemini_vertex_executor_resource_prefix_test.go) |
| F12 | Reviewed security and credential boundaries remain effective, including configuration-secret handling and plugin network scope. Include earlier absorbed fixes when comparing the previous fork baseline with upstream. | [configuration tests](../../internal/store/config_seed_test.go), [plugin network tests](../../internal/homeplugins/network_scope_test.go), [intake ledger](absorbed.md) |
| F13 | Fork repository governance, module path, required CI and explicitly excluded upstream workflows remain intentional. | [project rules](../../AGENTS.md), [intake gate](verify-absorb.sh), [maintenance tests](../tests/test_maintenance.py) |
| F14 | Fork-owned release tags, curated notes, checksums, Homebrew installation and rollback remain separately verifiable. | [release procedure](../../docs/maintenance.md), [Homebrew procedure](../homebrew/README.md) |

## Per-release reconciliation

For every requirement and every classified fork-only change, record:

- the behavior or operational contract, old fork source commit or path, and
  relevant tests;
- the exact upstream release and commit, the candidate implementation or patch
  commits, and whether the item is retained, adapted, or replaced by an
  equivalent upstream implementation;
- the comparison and regression evidence, including route, configuration,
  negative cases and any known limits; and
- the final integration commit and tree, with a separate follow-up record when
  those identifiers become known.

Do not retire a fork requirement solely because a commit applies cleanly,
becomes empty, has a matching subject, or is an ancestor. Check the covered
behavior and tests. A previously acknowledged limitation can remain documented;
it is not evidence that an unreviewed fork capability is safe to discard.

Whenever `main` gains a fork-specific change, update this index or its linked
per-release record with the source commit and tests. Before the next replay,
reconcile those later changes with the last accepted patch series. Use
`git range-diff` and tree comparisons as review aids, not as semantic proof.
