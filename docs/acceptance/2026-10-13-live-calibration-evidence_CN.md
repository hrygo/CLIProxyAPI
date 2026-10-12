# 实际 Provider 校准：Qwen 与 GLM

核验日期：2026-10-13。使用隔离 SDK 候选、本机 Codex 0.160.0 / OpenCode 1.19.0、实际配置的 API-key 路由及真实 Provider 推理；MCP 仅提供合成数据。候选 Go 入口保存于 `dc824cb`。此报告是研发校准，**不是最终单一版本 60-case、Coding 或统计认证**。

## 全部开始与失败

| 私有证据批次（`.omo/tool-loading-completion/` 下） | 已开始案例 | 完整任务通过 | 实际 HTTP attempts |
| --- | ---: | ---: | ---: |
| `t6-live-qwen-calibration-2` | 4 | 2 | 8 |
| `t6-live-glm-calibration-2` | 4 | 0 | 7 |
| `t6-live-qwen-codex-null-fixed` | 2 | 2 | 5 |
| `t6-live-glm-codex-null-fixed` | 2 | 0 | 5 |
| 全部诊断，不作可靠率估计 | 12 | 4 | 25 |

前两批的四个 Codex 案例在实际 metadata 校验阶段拒绝，零推理请求；仍保留在 12 个开始案例中。更早两个 `calibration-1` 批次因手工截取 endpoint-ID 长度错误在 case admission 前失败，零已启动客户端/Provider 推理；原始日志保留，未替换为成功。已改为接收完整摘要并自行生成 ID。

每个已接纳案例先 fsync `case_started` 再启动候选/客户端；每次真正的上游 HTTP dispatch 有预写 journal。每任务预算 90 秒、最多 8 次实际请求；没有追加无预算重试。所有候选均已终止；11 个退出 0，GLM/OpenCode eager 候选退出 1，原值保留。所有源配置检查均未变化；没有发布、安装、默认 catalog/config 切换或原服务重启。

## 完整闭环与当前收益

Qwen 两个客户端的 eager/lazy 都有真实 ledger dispatch、随机回执回填、合法终态、禁止 dispatch/effect 为 0 和最终 active 为 0。这四个成功样本只有一个合成 lookup 模板，不代表任务质量非劣。

| 客户端 / Qwen 路由 | eager → lazy 首个实际请求输入 | 峰值输入 | 全任务输入 |
| --- | ---: | ---: | ---: |
| OpenCode | 582 → 582（0%） | 52,822 → 14,974（减少 71.65%） | 106,112 → 52,500（减少 50.52%） |
| Codex | 53,775 → 12,268（减少 77.19%） | 53,928 → 未知 | 107,703 → 未知 |

计数来自实际 upstream `prompt_tokens`，包含缓存输入，不是统一账单折扣。OpenCode eager/lazy 分别为 3/5 次 HTTP 请求；首个请求是标题生成，其后第一项工具任务输入分别为 52,708/7,483，另作诊断，不替换表中的首请求指标。若正式门槛仍使用第一项实际请求，OpenCode 此样本不能通过首轮 50% 门槛。

OpenCode 工具定义序列化字节为 199,334 → 10,912，只能证明运输减少，不能替代 Provider input usage。Codex lazy 后两次响应虽有 terminal/usage，采集 outcome 为 `closed`，当前导入器标为 incomplete，峰值与总输入保留未知。account/region、缓存条件和完整不可变 manifest 尚未锁定，因此表中只是同配置路由的描述性比较；不颁发配对认证，不报告收益置信区间或成本节省。

## 责任边界

1. **验收适配器缺陷已修：** 实际模型目录 `tool_mode: null` 被误拒。Codex 0.160.0 的 [官方 metadata 契约](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/protocol/src/openai_models.rs) 将它定义为 `Option<ToolMode>`。适配器现允许可空字段进入显式 direct 隔离 profile，保留原对象，仍拒绝实际声明 Code Mode；修复后 Qwen/Codex 2/2 通过。
2. **GLM 硬格式质量未过：** 四个实际执行案例均分发 ledger 工具、回收 MCP callback，但最终均输出 Markdown 围栏，违背预先声明的“只输出 JSON”。独立 receipt 分析在三例解析出正确随机回执；另一例不满足当前最终答复解析条件。格式门槛未放宽，四例仍失败，不能统称为工具检索失败。原 summary 不覆盖，复核结果另存 `t6-live-calibration-receipt-analysis.json` 并保留 stdout SHA。
3. **采集器计量缺口：** GLM 多个响应有真实 `finish_reason`、`[DONE]` 和 usage，实际消费者关闭 body 时未读取 EOF；当前严格 importer 丢弃这些计量，尚未实现有协议终态证明的 closed-response usage。不能把 EOF 未见自动归因 Provider，也不能改写 transport complete。须补独立协议测试后再使用相应 token 值。
4. **生命周期待查：** GLM/OpenCode eager 的第一个标题请求未记录 finish/DONE/usage，关闭候选时仍有未完成 attempt，候选退出 1。工具任务本身随后有合法终态。需要区分后台标题任务取消、网关上游 body 释放和 Provider 响应；当前证据不支持单方归因，也不支持真实断流伪造完成。
5. **正式验收基础仍缺：** 全部路由动态目录核验、不可变 endpoint/material/scope、任务调度与缓存配对、聚类区间、代表 Coding 及客户端取消/runtime 限制修复。上述 12 个研发案例跨适配器修复，不能拼成单一最终版本样本。

## 回归与复现

最终 Python 离线 suite 104/104 通过，RED→GREEN 覆盖新真实校准模块、完整 endpoint 摘要、nullable metadata、不得用早期正确答复替代最终错误答复、非法 Chat choices、语义回执与硬格式独立验证。Go bootstrap 前置完整维护 gate、race 与候选构建已通过；后续仅修改 Python 验收适配器和文档，没有将 Go gate 宣称再次运行。

参见 [验收工具 README](../../ops/tool-loading/README.md)、[候选启动证据](2026-10-13-live-bootstrap-evidence_CN.md)、[完整方案与门槛](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。整体目标保持进行中。
