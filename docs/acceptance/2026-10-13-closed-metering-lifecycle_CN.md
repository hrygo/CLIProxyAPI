# 已关闭响应计量与候选关闭屏障

核验日期：2026-10-13。承接[真实 Provider 校准](2026-10-13-live-calibration-evidence_CN.md)。本轮先离线回放此前证据，不把回放计入新增推理或正式验收样本。整体目标未完成。

## 修复及回归

`http_attempts.py` 现在允许已关闭的 Chat/Responses SSE 响应提供 usage，但要求完整帧边界、实际协议终态、身份一致、成功 HTTP 状态且采集未截断。Chat 必须同时有每个已观察 choice 的合法结束原因及最后的 `[DONE]`；Responses 必须有状态与身份匹配的终态对象。损坏 UTF-8、重复 JSON 字段、错误事件、冲突身份、截断及终态后的数据均不能成为依据。多个 usage 快照仍须符合原有一致性检查。

计量记录增加 `usage_evidence` 和 `transport_capture_complete`；原始 `closed`、`complete: false` 保持不变。计量可用不等于任务成功；失败任务仍计入消耗。其他尚未验证的 closed wire contract 保留未知。

`HTTPAttemptJournal.CloseContext` 冻结新请求、等待实际 body Close 和持久化 finish，再关闭 journal。EOF 只表明读取结束，不能提前报告资源释放。等待被取消时保留未完成请求，不关闭实际 body、不补造 finish。候选在 SDK 停止后使用此屏障；35 秒进程清理预算由既有 launcher 管理，没有新增上游网络超时。此修复尚不能证明此前 GLM 标题请求的根因。

RED→GREEN 覆盖 closed terminal 计量、拒绝伪终态/冲突身份、等待 body、冻结分发、取消保留以及 EOF 后必须 Close。108 项 Python 测试通过；两个相关 Go 包 race 通过；Go 1.26.4 完整维护 gate（格式、vet、全量测试、敏感包 race、server build）通过，候选构建通过。日志位于 `.omo/tool-loading-completion/t6-closed-*`。

## 原始证据回放

四个原批次的 12 个开始案例、25 次实际 HTTP 请求与 4 个严格通过保持原值；只生成独立 sidecar，不覆盖旧 journal、summary 或 body。校验原始 journal SHA 与 artifact 摘要，保存解码器 SHA、每请求采信依据及原始 scope。新回放位于 `.omo/tool-loading-completion/t6-closed-metering-replay/`。

| 客户端 / 路由 | 首个实际请求输入 eager → lazy | 峰值输入 | 全任务输入 |
| --- | ---: | ---: | ---: |
| Codex / Qwen | 53,775 → 12,268（77.19%） | 53,928 → 19,136（64.52%） | 107,703 → 50,351（53.25%） |
| OpenCode / Qwen | 582 → 582（0%） | 52,822 → 14,974（71.65%） | 106,112 → 52,500（50.52%） |
| Codex / GLM | 53,152 → 11,777（77.84%） | 53,226 → 16,443（69.11%） | 106,378 → 44,567（58.11%） |
| OpenCode / GLM | 未知 → 556 | 未知 → 14,414 | 未知 → 36,440 |

GLM/OpenCode eager 标题请求仍无 finish，已知任务输入仅为其余两次请求的 101,439，不能作为全任务分母。GLM 四个实际执行案例仍未通过硬格式质量门槛，收益数字不抹除失败。OpenCode 标题请求仍纳入首轮指标，没有改成其后的第一个工具任务。

以上只是描述性计量，包含缓存输入，不能换算成账单节省。缓存条件、region、完整不可变 scope 及模板独立性未锁定，没有配对认证或收益置信区间；不能替代最终版本的 60 个跨 Provider 案例及代表性 Coding 任务。

## 下一步与边界

使用固定候选版本重测 GLM/OpenCode 标题请求，观察 SDK 停止后的实际 body 释放和 finish，而非把退出码改善当成根因证明。随后继续全路由 scope 固定、权限/取消与客户端缺口、平衡调度及统计门槛。未发布、未改默认配置、未重启原服务。
