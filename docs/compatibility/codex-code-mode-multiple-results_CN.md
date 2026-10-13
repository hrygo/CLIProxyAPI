# Codex Code Mode 多输出桥接边界

核验日期：2026-10-13（Asia/Shanghai）。CLIProxyAPI 已提供精确路由的 `custom-output-replay: multiple`，逐项重放原生 Responses 多结果；默认适配仍采用 `single`，拒绝无法保真的多输出。**跨协议及 Muse 多输出兼容尚未完成**，不能将新选项外推为所有 Provider 支持。

## 新增生产能力与真实责任边界

`multiple` 只允许显式匹配 `codex` / `openai-response` 格式的路由，配置编译和直接 `Prepare` 均拒绝非原生格式。请求转换逐项保留结果内容、原 `call_id`、可逆迁移的 item ID 及全局顺序；不合并结果，不伪造调用，不降为 user text。省略选项或显式 `single` 保持原有拒绝行为。配置、身份负例、交错与数组内容回归及 Go 1.26.4 全仓测试、vet、server build 已通过。原生格式是必要条件，真实目标的接受性仍需另验。

T23 对生产候选 `f497d7987b0842335e61623a15958c948bb90217` 的受限协议探测确认：`gpt-6-luna` / Codex 的相邻和交错 `function_call_output` 均得到合法完成事件，模型正确读取全部合成标记；Muse 单结果控制成功，多结果返回 400，明确要求每个 `function_call` 恰好对应一个输出。Muse 还明确只接受 `tool_choice: auto`。这证明 GPT 原生 wire 的接受性及 Muse 的上游限制，尚不证明新增 custom bridge 或真实客户端任务已验收。

探测共保留 7 次物理请求（Codex 4、Muse 3），包括首次响应落盘类型错误及后续排他写入错误；完整上游捕获与候选退出回执已保留，未删除请求或把框架错误改判为客户端成功。材料位于 `.omo/tool-loading-completion/t23-native-multi-output-v1`、`v2`、`v3`；不计算任务通过率、上下文收益或费用节省。尚未扩大试验，默认配置、原服务和凭据刷新均未改变。

## 实测触发

候选 `4b428edfa70e98df3780f4850ab1d7129b95ed14` 的 [T14 覆盖审计](../acceptance/2026-10-13-primary-60-coverage_CN.md) 保留 Codex / Muse unavailable lazy 的失败：客户端执行 Code Mode `notify(...)` 后，下一次 Responses 请求在 `prepare` 阶段返回 `422 invalid_custom_input`，没有打开该请求的上游连接。捕获到的四个 custom call ID 均不同；触发点是同一次执行产生额外输出及最终输出，共享其 `call_id`，不是 Provider 重复发出同一调用。

证据：[准备阶段诊断](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-unavailable-lazy/gateway-state/capture/preparation.json) 和 [客户端原始记录](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-unavailable-lazy/client/stdout.jsonl)。原始 verdict 保留；不能用新的诊断分类追溯改判为成功。

Codex Code Mode 的通知契约允许额外 `custom_tool_call_output`。每个历史 item 的可选 `id` 与调用链接 `call_id` 是不同身份：多个输出可以属于同一执行；不能为通过转换而伪造新调用或把输出 item 当作新执行。这个客户端契约不等于所有目标 Provider 已接受对应 wire history。

## 当前转换的损失

以下结论经本地源码复核；图谱涉及文件有 `metadata_changed`，已使用源码回退，未重建索引。

| 转换边界 | 现有行为 | 不能作出的兼容承诺 |
| --- | --- | --- |
| [`rewriteCustomHistoryItem`](../../internal/responsestools/custom.go) | 逐项把 custom output 转成 function output，保持 `call_id` | 仅改变 item 类型不能证明后续仍保留所有结果 |
| [Responses → Chat](../../internal/translator/openai/openai/responses/openai_openai-responses_request.go) | 消耗一次待结果调用；后续同 ID 输出成为普通 user text | 额外结果未保持 tool result 角色 |
| [Chat → Codex Responses](../../internal/translator/codex/openai/chat-completions/codex_openai_request.go) | 每个待调用只消费一次；后续匹配不到的 tool message 被跳过 | 额外结果可能丢失 |
| [Responses → Claude](../../internal/translator/claude/openai/responses/claude_openai-responses_request.go) | 按 raw call ID 预取最后 payload，并只发出一次 tool result | 前面的通知被折叠 |

因此默认保留拒绝有损转换的行为，将该能力缺口准确报告为 `422 unsupported_protocol`。它是 request-scoped、availability-neutral 的错误；不会产生 outbound body 或 Attempt，也不应触发凭据冷却或账号轮换。重复 call、缺失 `call_id`、孤立 output 和先 output 后 call 仍为 `invalid_custom_input`，显式 `multiple` 也不放宽这些身份规则。`CustomToolsFunction` / `CustomToolsStrip` 默认路径均覆盖，原生 `ClientSearchNative + CustomToolsInherit` 本地字节透传保持不变。

## 回归与交付边界

[测试](../../internal/responsestools/attempt_test.go) 覆盖通知加最终文本、数组结果、与其他调用交错的后续结果、两种适配策略及未创建 Attempt 的失败边界；身份负例明确断言 `invalid_custom_input`，原生用例断言请求字节不变。该测试不包含真实 Provider 接受性，不代表 Muse unavailable lazy 已跑通。

后续完整实现需要逐后端给出明确结果编码：保留每项内容、次序、item ID 与真实 `call_id` 链接；覆盖多轮/并发、数组结果、重试和历史引用，不以未标注合并、用户文本降级、丢弃结果或伪造调用代替兼容。必须验证目标协议的真实接受性，再补 request/response 转换回归及同一最终候选上的客户端端到端案例。

| 候选方案 | 可保留的边界 | 新增前提与缺口 |
| --- | --- | --- |
| 逐结果转发 | 显式历史中的 payload、独立 item ID、原 `call_id` 与全局交错次序 | 每个目标 Provider 和转换器都须接受一对多结果；目前尚无通用认证。原生本地透传不等于目标接受。 |
| 网关显式事件聚合 | 在版本化封套中记录各项内容、原 ID 与单调用内次序 | 外层只剩一个 item；并发全局交错和迟到结果需要额外规则，opaque history 无法就地展开。需要新协议契约，不能当作当前适配的等价修复。 |
| 客户端结果收敛 | runtime 按真实调用把进度与终态分别处理；每次执行只回填一个顶层结果 | 需修改 Codex/exec 契约，明确模型如何收到进度内容及完成后的迟到通知；不能静默丢弃通知，也不能改写已有 opaque history。 |

CLIProxyAPI 负责发现此缺口和拒绝有损转换。客户端或 Provider 的新支持需要单独交付和版本声明；本轮未发布、未改默认配置、未重启原服务。
