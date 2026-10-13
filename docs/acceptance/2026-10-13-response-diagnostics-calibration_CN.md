# 响应诊断候选：Codex GPT / Muse 四例校准

核验日期：2026-10-13（Asia/Shanghai）。**4 started / 4 finished / 4 safe successes；新响应回执可达且正常封存。** 只验证新的最小候选，没有追溯改判 T17 的 20/24，也没有将两版结果合成可靠率。

## 冻结范围

- Gateway revision：`58f5523b90d0561ff7812ad8da8257f14acb4ef5`；binary SHA-256：`42920710b5c0feda73458abb4bf16e79b57989fdcd8a89b726b8bac59e8e3c9c`；Go 1.26.4，clean build、`vcs.modified=false`。
- 新隔离候选 `isolation_version=7`，独立 `response-failures.json` 是新批次必须具备的回执；准备阶段 v1 文件语义不变。历史 v6 证据保留，新 runner 不用旧候选冒充新验收。
- Codex CLI `0.160.0`，GPT `gpt-6-luna` 与 Muse `muse-spark-1.3-contributor`，均为 `code_mode_only`、plain output。Muse 的模型 alias 包含 `opencode-go`，实际客户端仍是 Codex。
- 同一 `coding-budget-selective` 任务做 E/L 配对，明确提示三个工具的实际名字；一个隔离 Python 函数、11 项 host gold。属于有指导的发现校准，不是无提示自然语言检索或多文件编码认证。
- 配置 SHA-256：`a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`；只读凭据绑定、不刷新、不自动切换。每 case 八请求/八 dispatch、90 秒，全批上限 32 请求/32 dispatch。
- 冻结 plan SHA-256：`d37267081f9f1493a25e9408e684593d4c953186e4cb1cadb6ae5ef098795e8f`。

## 实际闭环与输入

20 个物理请求、12 MCP attempts、12 dispatch，预算内，无未知请求/dispatch 案例。四个 case 的 forbidden dispatch、forbidden effect、最终 active 均为零，网关退出回执均为零；四份代码实际修改且 host gold 44/44 次测试执行通过。

两个 lazy 均在调用前发现并加载 `coding_task_read`、`coding_source_replace`、`coding_tests_run`，无先调用后加载，call/result 身份链一致。GPT 采用 native exec grammar；Muse 是 function bridge，grammar fidelity 为 described，不宣称两者 grammar 完全等价。

| Route | 首轮 input E → L | 首轮降幅 | 峰值 input E → L | 峰值降幅 | 全任务 input E → L | 全任务降幅 |
| --- | --- | --- | --- | --- | --- | --- |
| Codex / GPT | 51,971 → 17,101 | 67.10% | 53,250 → 18,841 | 64.62% | 263,297 → 90,171 | 65.75% |
| Codex / Muse | 54,942 → 16,822 | 69.38% | 57,382 → 19,230 | 66.49% | 281,100 → 90,418 | 67.83% |

均为实际 Provider usage，包括 cache-read；不是 body 字节或账单减少率。每 route 只有一对、一个固定模板，虽达到 45%/10% 数值门槛，收益下界仍缺失，质量条件性下界为 −95pp，不能宣称非劣或可靠率。自动分析保留 `unknown_scope` / `unverified`。

## 新诊断的证明边界

四份响应回执都记录 `requests_seen=5`、`active_requests=0`、`sealed=true`、`failed=false`，与准备回执一致，`failures=[]`。证明新 collector 的正常路径实际可达；**本轮模型没有再产生非法 `{}`，因此没有在真实 Provider 调用中复现响应拒绝回执。** 非法参数拒绝及 typed reason 的准确记录由本地确定性回归证明，不能称本轮四例已经覆盖该异常实测。

本地回归覆盖 buffer restore、Feed、Finish，伪造错误文本不可冒充可信诊断，准备回执独立，取消后不报告迟到转换、owner finish 与 callback 同步、封存回执不能被迟到报告修改。历史 HTTP 错误逐请求保留，明确的最终本地转换错误不被早期重试 429 抢占主要归因；既有错误、取消、Close 与重试行为没有改动。详见[响应诊断边界](../compatibility/responses-tool-failure-diagnostics_CN.md)。

## 延迟、缓存与成本

| Route | 全流程 elapsed E / L | cache-read E / L | cache-write E / L |
| --- | --- | --- | --- |
| GPT | 22,695 / 21,830 ms | 154,880 / 70,400 tokens | 0 / 0 tokens |
| Muse | 53,892 / 21,948 ms | 222,660 / 70,852 tokens | 未知 / 未知 |

elapsed 包含准备、host gold 与清理，不是 TTFT；每 arm 一例不能估计 p95。缓存冷热、region、费率和账单仍未知，不能将输入降幅换算为费用收益。Gemini 不在本四例范围，原有 HTTP 429 和用量契约问题仍保留。

## 复核与剩余门槛

[冻结 plan](../../.omo/tool-loading-completion/t19-response-diagnostics-4-v1/batch-plan.json)、[原 journal](../../.omo/tool-loading-completion/t19-response-diagnostics-4-v1/cases.jsonl)、[配对分析](../../.omo/tool-loading-completion/t19-response-diagnostics-4-v1-pairs.json)、[逐案审计](../../.omo/tool-loading-completion/t19-live-audit.json)、[构建 attestation](../../.omo/tool-loading-completion/t19-candidate-attestation.json)、[本地回归回执](../../.omo/tool-loading-completion/t19-response-diagnostic-verification.json)。私有 capture 不作为公开交付附件。

当前新候选尚无完整 60-case 覆盖、多文件代表性任务及正式统计认证；Codex MCP 取消、多输出保真、Gemini 权威计量仍未解决。没有发布、默认配置切换、客户端升级、OAuth refresh 或原服务重启。见[当前状态](2026-10-13-tool-loading-current-status_CN.md)。
