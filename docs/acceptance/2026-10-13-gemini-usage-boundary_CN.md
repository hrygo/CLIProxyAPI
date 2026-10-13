# Gemini / Antigravity 用量与失败责任边界

核验日期：2026-10-13（Asia/Shanghai）。本次只读分析既有 T14 capture，没有新增 Provider 请求或修改原始 verdict。**Gemini 输入收益继续标未知；发现了真实计数回退，尚不能认定 Provider 账单错误或以最后值替代整个计量契约。**

## 已确认的边界

T14 同模型 `gemini-3.5-flash-lite`：Claude 六例收到实际上游 HTTP 429 `RESOURCE_EXHAUSTED`；Codex 六例中五例闭环成功，一例发生任务未要求的额外 confirm。HTTP 响应证据定位到 Provider 边界；客户端差异、账号条件、额度恢复时间和 thinking 因果尚未验证。不能据此称 Gemini 一概不支持发现或把配额失败改记为 Gateway 检索缺陷。

用量问题与任务成功、HTTP 429 是不同维度。成功任务也可能无法计算收益。

## 原始流的计数变化

检查 T14 `codex-gemini-3.5-flash-lite-native-json-schema-eager` 的两个真实模型响应，已 unwrap 实际 Antigravity 外层。辅助请求单独计数，不当作 GenerateContent 模型调用。

| Capture | 原始 snapshot 变化 | 最后候选 finish reason | 结果 |
| --- | --- | --- | --- |
| response-000002 | `thoughtsTokenCount` 194 → 388 → 194；`totalTokenCount` 58,046 → 57,852 | STOP | thought/total 有回退 |
| response-000003 | `thoughtsTokenCount` 277 → 554 → 277；`totalTokenCount` 58,540 → 58,457；`promptTokenCount` 57,937 → 58,131 | STOP | thought/total 回退，prompt 改变 |

这是 scalar counters 的实际变化，不只是数组顺序或额外 metadata 引起的 JSON 不相等。原审计保留 `conflicting_usage_snapshots`。

Google 公共 GenerateContent 文档说明：`promptTokenCount` 包含缓存内容，`cachedContentTokenCount` 是其中的缓存部分，`totalTokenCount` 包含 prompt、thinking 和候选输出。不能把 cache-read 再加一次到 total input。[官方 UsageMetadata 文档](https://ai.google.dev/api/generate-content#UsageMetadata)

两份最后 STOP snapshot 均满足加总关系：57,635 + 194 + 23 = 57,852；58,131 + 277 + 49 = 58,457。这只能证明末尾数值内部一致。公开文档没有确认本次 Antigravity 私有 wrapper 的中间修正规则、最后 snapshot 的账单权威性或辅助请求成本；“最后值可能是校正值”仍是推断，不作为收益认证证据。

## 需要补齐的计量能力

| 责任方 | 交付 | 验收条件 |
| --- | --- | --- |
| Provider / Antigravity 协议维护方 | 明确中间与最终 usage 的权威性、prompt 修正、thinking、cache 与辅助调用语义 | 有版本化契约或独立权威对账，不能仅依照一次 capture 推广 |
| CLIProxyAPI / 验收计量 | 区分 raw snapshots、终态、模型与辅助调用；仅在该路线契约成立时归并最终用量 | 同步保留修正证据；多终态、截断、负值、字段缺失和总量矛盾不得获得正向认证 |
| 客户端 / 实验设计 | 区分工具发现失败、任务失败、HTTP 失败与用量未知 | 所有尝试、失败、预算耗尽和未知辅助成本留在分母；按 exact route 成对比较 |

目前最小正确处理是保留未知。不能选 max、末值或只挑成功请求来生成一个“节省率”；也不能为通过门槛隐藏辅助调用或将缺失账单填零。后续计量适配必须同时测试流完成、多个候选、usage 修正、缓存包含关系与辅助请求，并单独报告输入估算和真实账单。

## 可复核证据

[只读计数审计与原始 SHA-256](../../.omo/tool-loading-completion/t18-gemini-usage-boundary.json)、[T14 原验收](2026-10-13-primary-60-coverage_CN.md)、[当前状态](2026-10-13-tool-loading-current-status_CN.md)。计数审计只保存必要的数值、序号、finish reason 与文件摘要；不复制请求正文、系统提示或凭据。

此调查没有发布、配置写入或服务重启。未解决的计量问题不会阻止记录实际工具执行，但阻止宣称该路线的上下文收益或账单节省。
