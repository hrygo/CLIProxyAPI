# T12 Claude Coding 与 Codex Bridge 实测验收

核验日期：2026-10-13。只读核对已有证据；原始 evidence 与既有裁判结果未改写。所有已开始任务均保留在统计中，失败项纳入节省率；未报告的 token/cache 值按未知处理。

## 分批结论

| 独立批次 / gateway revision | 已开始 / 严格成功 | 物理请求 / dispatch | 原判 |
| --- | ---: | ---: | --- |
| Codex 非 GPT 原输出配置，`b7fbad02e649b325759b2582405bdfaae40ab86a` | 6 / 0 | 10 / 2 | `unverified`；Qwen 两例 400，GLM 两例无工具调用；Muse 两例虽各 dispatch 一次，旧模式观测使最终 answer 谓词失败。 |
| Claude coding，`b7fbad02e649b325759b2582405bdfaae40ab86a` | 4 / 4 | 20 / 14 | 本轮 coding pilot 通过；仅一个任务模板，不构成跨 Provider 最终认证。 |
| Codex Bridge plain-JSON retest，`3ef0213c12580112463b85849e44b3db775bfa0e` | 6 / 1 | 17 / 7 | `unverified`；仅 Meta lazy 严格成功。三批结论各自保留，不跨 revision 合并。 |

初始 Codex native JSON 批次中，Qwen eager/lazy 的上游响应均为 HTTP 400，原错误为 `code=invalid_parameter_error`、`param=null`、`message="This response_format type is unavailable now"`、`type=invalid_request_error`；两例没有 dispatch，首轮/峰值/全任务输入及缓存值为 `null`，不能记作零。GLM eager/lazy 都以 `finish_reason=stop` 和 `[DONE]` 结束，但 `tool_calls` 为空、没有 final 工具调用或 dispatch。Muse eager/lazy 各 dispatch 一次，但 `checks.answer=false`，仍判失败。来源：[旧批次 audit](../../.omo/tool-loading-completion/t12-codex-non-gpt-pilot/batch-audit.json)、[case summaries](../../.omo/tool-loading-completion/t12-codex-non-gpt-pilot/summary.json)；Qwen 原始错误见 eager/lazy 各自 `gateway-state/capture/response-000001.body:1`，GLM 终态见两例同目录的 `response-000001.body`。六例退出回执均记录 `exit_code=0`、`source_config_unchanged=true`。

此批只有 GLM/Qwen 启用了 Codex 原生 JSON-schema 输出；Muse 保留原有 plain JSON profile。Muse 两例的 nonce、dispatch、format、terminal 检查均通过，但旧验收器只识别 custom/Lark 入口，未识别真实 Responses function bridge，故标记 `effective_tool_mode_not_observed`。新的离线诊断识别到匹配的桥接声明、执行和 lazy typed schema，但仅用于定位原因，不追溯重判旧 0/6。

`3ef0213c` 补上 Responses 的 plain-custom-`exec` 稳定哈希别名和 Chat 的 `exec`/string-input wrapper 观测，并在每轮原始声明中保留 namespace 核对，拒绝错误路径、歧义入口、坏 schema 和重复 JSON 参数。历史归一化仅建立分析视图，不改原始 capture、声明字节或 usage。独立审查确认 namespace 误报已关闭；空结果任务仍可合法零加载，要求 dispatch 的任务另验实际调用前的完整签名。

## Claude coding eager / lazy

下表每个百分比均为同路由 eager 相对 lazy 的输入减少率。`cache_read` 是 `input_total` 的组成部分，不另加到总输入；`cache_write=null` 表示未报告。

| 路由 / alias | 首轮输入 eager→lazy（减少） | 峰值输入 eager→lazy（减少） | 全任务输入 eager→lazy（减少） | case elapsed eager→lazy | cache_read eager→lazy | 代码与 dispatch |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Qwen / `deepseek-v4.1-flash` | 49,168→2,724（94.46%） | 50,008→3,979（92.04%） | 198,556→17,196（91.34%） | 18.640→16.508 s | 148,352→12,288 | 两例各 3 dispatch；code check 各 11/11 通过，`changed=true`。 |
| OpenCode Go / `glm-5.3-flash` | 48,055→2,469（94.86%） | 48,787→3,931（91.94%） | 193,871→23,086（88.09%） | 38.340→55.827 s | 91,008→0 | eager 3、lazy 5 dispatch；code check 各 11/11 通过，`changed=true`。 |

四例 `checks.code=true`、无 forbidden dispatch/effect、最终 `active=0`；四份 gateway 退出回执均为 `exit_code=0` 且源配置未变。elapsed 是 harness 的整例计时，不是 Provider latency 或 TTFT。来源：[Claude batch audit](../../.omo/tool-loading-completion/t12-claude-coding-pilot/batch-audit.json)、[summary 的 `cases[].usage` / `code_outcome`](../../.omo/tool-loading-completion/t12-claude-coding-pilot/summary.json)、[elapsed 与边界记录](../../.omo/tool-loading-completion/t12-claude-coding-pilot/cases.jsonl#L11)、其余 `case_finished` 行 23、39、49，以及各 case 的 `gateway.exit.json`。

## Codex Bridge 新六例

冻结计划使用 `schedule_seed=t12-code-mode-bridge-plain-json-retest`。它与旧 native-JSON Qwen 400 是两个 profile 的独立观察：新 profile 的 Qwen 请求返回了 usage，但 eager 多出一次 confirm，lazy 虽 dispatch 却 `format=false`。下表按每个 route 配对，**包含失败 eager/lazy 的全部用量**；百分比是已观察的 reduction，不是认证收益。

| 模型 / executor alias | 首轮 eager→lazy（减少） | 峰值 eager→lazy（减少） | 全任务 eager→lazy（减少） | 严格通过 eager / lazy |
| --- | ---: | ---: | ---: | ---: |
| `glm-5.3-flash` / OpenCode Go | 52,266→13,595（73.99%） | 52,266→14,660（71.95%） | 52,266→42,753（18.20%） | 0 / 0 |
| `muse-spark-1.3-contributor` / Meta | 54,533→16,655（69.46%） | 55,077→27,174（50.66%） | 164,359→88,258（46.30%） | 0 / 1 |
| `deepseek-v4.1-flash` / Qwen | 53,410→14,184（73.44%） | 54,023→23,925（55.71%） | 161,167→61,794（61.66%） | 0 / 0 |

| route / mode | elapsed / 物理请求 | cache_read / cache_write | dispatch 与判定 |
| --- | ---: | ---: | --- |
| GLM eager | 14.935 s / 1 | 0 / 0 | 0；无合格答案，`format=false`。 |
| GLM lazy | 5.898 s / 3 | 27,008 / 0 | 1；lazy 调用发生在完整 schema 加载前（`loaded_fixture_schema_tools=[]`），且 `format=false`。 |
| Meta eager | 13.479 s / 3 | 108,770 / 未知 | 2；重复 `ledger_budget_lookup`，`checks.dispatch=false`。 |
| Meta lazy | 16.548 s / 4 | 59,987 / 未知 | 1；唯一严格成功，answer、dispatch、nonce、format、terminal 均通过。 |
| Qwen eager | 10.921 s / 3 | 107,008 / 未知 | 2；lookup 后额外 confirm，`checks.dispatch=false`。 |
| Qwen lazy | 10.275 s / 3 | 37,888 / 未知 | 1；已 dispatch、answer=true，但 `format=false`。 |

新六例逐例均为 `forbidden_dispatch=0`、`forbidden_effect=0`、`active=0`；六份 `gateway.exit.json` 均为 `exit_code=0`、`source_config_unchanged=true`。这是 `ledger-json-lookup`，不是 coding case，六例的 `checks.code=null`。Meta 的 grammar fidelity 只记录为 `described`；GLM/Qwen 是 `not-forwarded`，并且 `exec_grammar_observed=false`。Codex Chat bridge 未转发 native grammar，不能称为“原生 grammar”。此表 elapsed 同为 harness 整例计时，不是 Provider latency 或 TTFT。来源：[新批次 audit](../../.omo/tool-loading-completion/t12-codex-bridge-retest/batch-audit.json)、[冻结计划与 revision](../../.omo/tool-loading-completion/t12-codex-bridge-retest/batch-plan.json)、[paired savings](../../.omo/tool-loading-completion/t12-codex-bridge-retest-pairs.json)、[每例用量、校验和 grammar observation](../../.omo/tool-loading-completion/t12-codex-bridge-retest/summary.json)、[elapsed / case boundary](../../.omo/tool-loading-completion/t12-codex-bridge-retest/cases.jsonl)、Meta eager 与 Qwen eager 的 `client/mcp-events.jsonl`（序号 1–10 可见重复 lookup / lookup+confirm）。

## 计量与未完成门槛

输入 token 统计口径见 [`normalize_usage.py`](../../ops/tool-loading/normalize_usage.py#L128)：全任务总输入包含 cache-read，存在未知 usage 时总值保持未知；整例 elapsed 的起止点见 [`live_clients.py`](../../ops/tool-loading/live_clients.py#L262) 与 427–428；started、物理请求与未完成项计数见 [`batch_plan.py`](../../ops/tool-loading/batch_plan.py#L230)；严格成功谓词见 [`run_acceptance.py`](../../ops/tool-loading/run_acceptance.py#L241)。

新 pair 文件每个模型仅有一个模板簇，`cache_condition` 与 region 均 unknown；收益 95% 下界为 `null`（`insufficient_clusters`），gate 仍是 `unverified`，且 billing 未评估。三批都不能满足完整的 60 例最终候选、Codex 代表性编码、非劣与收益区间、取消传播和账单判断。Claude 四例确实通过了 coding check，但它们仍是单模板 pilot；新 Codex 六例没有代码验证。取消缺口是独立待实施项，见[Codex MCP 取消传播缺口](../compatibility/codex-mcp-cancellation-gap_CN.md)。

当前 v2 lookup 规则允许重复的幂等 read，并要求仍计入 dispatch budget；当前 prompt 文本本身没有 `exactly once`（[`task_scenarios.py`](../../ops/tool-loading/task_scenarios.py#L25)、[README](../../ops/tool-loading/README.md#L581)）。冻结 retest 计划中的 `harness-task_scenarios-py` SHA 为 `88d78053e1c2a469eb56fe7fc853d7e99f8bb44df116dde24cf9a8fd55b97467`，当前源文件 SHA 为 `09e9affa4e1d029a658fd8dd27f85961a34aa99f9d63858fc124b5ffeb1e6f4b`；v2 修正属于新候选，不能回改这次 bridge 的严格 **1/6** 原判。

旧 README 文档确有单次要求，问题是它没有进入模型收到的任务。`4b428edfa70e98df3780f4850ab1d7129b95ed14` 将 lookup 模板改为 `ledger-json-lookup-read-repeats-v2`：只允许预算内完整的重复 lookup，继续拒绝 confirm、无关工具和不完整序列；8 次/9 次 dispatch 的端到端回归分别证明上限内通过、超限仍失败。该候选也已干净构建；旧三批不合并或重新判分。

最终 Python oracle 为 **283 tests OK**（[日志](../../.omo/tool-loading-completion/t12-python-oracle-final.log)）。Bridge 候选按 Go `1.26.4` 干净构建，revision 为 `3ef0213c12580112463b85849e44b3db775bfa0e`；本轮没有生产 Go 修改，也没有声称重跑 full Go gate。Codex 原生 GPT Code Mode 的边界证据与本报告分开，见[既有报告](2026-10-13-codex-code-mode_CN.md)。
