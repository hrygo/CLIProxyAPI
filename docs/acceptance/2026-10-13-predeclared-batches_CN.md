# 跨 Provider 验收批次的预先锁定

核验日期：2026-10-13。承接[计量与关闭屏障](2026-10-13-closed-metering-lifecycle_CN.md)，继续实施[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) T6。此报告不是最终 60-case 或统计认证。

## 已实现

`batch_plan.py` 将既有 CaseSpec 固定成单一 revision 的成对计划，记录精确 scope、prompt/fixture/permission/catalog/gold、运行顺序、总请求/dispatch 预算和显式文件摘要。计划采用规范 JSON 摘要、独占 0600 文件；读取拒绝重复键、非有限数、symlink、非普通文件和读取中变化。材料核验只读取显式路径，输出角色和摘要，不输出路径、配置内容或凭据。

同对 scope 除 client mode 外必须一致，模板、缓存条件、prompt、fixture seed、gold 和每任务预算也须一致。固定 seed 决定路由及任务对顺序；每路由交替 eager-first/lazy-first，路由块交错运行。奇数任务对允许一例顺序差；顺序及 Python 文件本身都被锁定。

仅有计划文件无法证明事前声明。新 CaseJournal 在第一个 case 前写入并 fsync `batch_locked` 摘要，审计要求它与计划完全相符。锁定不能重复、不能在任务开始后补写；旧诊断仍可按原格式重放，但不能事后绑定到新计划。

执行前预留每任务完整请求/dispatch 上限，只有可核验的实际计数才能退还余额；缺证据保留完整预留，余额不足不再启动任务。未知实际请求数与已记录请求数分开表示。预算停止、计划中未开始的任务、已开始但失败/中断的任务分别保留，不将失败移出分母。暂不提供 resume。

`live_clients.py` 的 paired 模式已接入计划写入、锁定、调度、预算以及运行前后材料检查。source config、候选/客户端二进制、Python 和 harness 漂移会使任务失败；任务开始记录先于核验与候选启动。`validate_evidence.py --plan` 区分无效 journal 与批次绑定错误；任何有效计划或审计都只标 `unverified`。

## 离线证据与限制

14 项新批次测试与客户端集成回归覆盖 60 槽位确定性/平衡、scope/gold/预算冲突、摘要篡改、材料漂移、旧日志拒绝绑定、预算停止及中断保留。全套 Python 126 项通过，隔离候选构建通过；本轮未修改 Go 源码，不声称重新运行完整 Go gate。

新回归也发现并修复 CaseSpec 提取时遗漏的客户端 executable 局部变量，以及 paired 批次异常中断被误标为 diagnostic-only 的问题。macOS 临时路径 `/var` 与 `/private/var` 的断言改为比较规范路径，没有放宽业务判定。

`.omo/tool-loading-completion/t6-predeclared-sixty-fixture/` 保存 60 个合成计划槽位、前置锁及审计：**实际开始 0、Provider 推理 0、通过 0**。这只能验证计划生成，不计入正式 60 案例。八次每任务上限属于当前校准 profile，并非已经锁定的最终矩阵预算。

完整不可变 scope 仍缺实际 model metadata 与已安装依赖内容、OAuth material、account/region 和缓存控制。当前 live CLI 仍只覆盖一个 lookup 模板和显式 Chat profile；三场景、全部路由、真实客户端负例、Coding 与聚类统计尚未完成。文件摘要检查也不构成任意运行时状态已经冻结的证明。

未发布、未改默认配置、未重启原服务；后续固定候选在线结果应独立记录，不能用这 60 个合成槽位替代。

## 固定候选在线复核

随后用干净候选 `c6818d5e0aef1f1701aea7155477804e8be28218`，在 Codex CLI `0.160.0` / `qwen-ds41-flash` → `deepseek-v4.1-flash` / `openai-compatible-qwen` 路由完成 eager/lazy 配对。计划摘要 `9a3216fe4762c98710664f99aa5bd4513c4a1f89ae7239fd7fbec414f0fadb75`，实际顺序 eager→lazy，与事前计划一致。

实测 2/2 严格通过、5 次上游请求、2 次 MCP dispatch、禁止执行/副作用 0、任务结束 active 0。两候选均退出 0，source config 摘要保持不变。计划绑定与 journal 校验通过；审计记录实际开始 2、未完成 0、未开始 0，预算 16/16 内实际请求 5、dispatch 2。region/cache scope 仍未知，整体 gate 仍 `unverified`。

| 实测 Provider input（含 cache） | eager | lazy | 减少 |
| --- | ---: | ---: | ---: |
| 首次请求 | 53,770 | 12,263 | 77.19% |
| 峰值请求 | 53,926 | 19,092 | 64.60% |
| 全任务 | 107,696 | 50,271 | 53.32% |

cache read 分别为 53,248 / 24,960 tokens；cache write 和账单成本未知。这是一对单工具模板，不给置信区间或可靠率认证，也不计为最终 60-case 矩阵。原始证据位于 `.omo/tool-loading-completion/t6-live-qwen-locked-batch/`，离线校验结果另存 `t6-live-qwen-locked-batch-validation.json`。
