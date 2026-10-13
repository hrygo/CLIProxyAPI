# T14 primary routes 覆盖审计

核验日期：2026-10-13。范围仅为候选 `4b428edfa70e98df3780f4850ab1d7129b95ed14` 的两个已结束 T14 批次；本报告不重写原批 verdict，不将它们解释为单一 run 的 60-case 验收。

## 两批计划与材料

| 批次 | run ID / plan SHA | started / safe / unfinished | journaled requests / known dispatches / unknown dispatch cases | 原 gate |
| --- | --- | ---: | ---: | --- |
| [primary 48 matrix](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/batch-plan.json)；[audit](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/batch-audit.json)；[paired diagnostics](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1-pairs.json) | `live-matrix-570dcf84b89f4aa48c8de6f85fe01cbc` / `beed5675269b2259d052513334bfa184671078008087acadbcdcab247686caef` | 48 / 39 / 0 | 142 / 48 / 0 | `unverified` |
| [Gemini 12 matrix](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/batch-plan.json)；[audit](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/batch-audit.json)；[paired diagnostics](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1-pairs.json) | `live-matrix-5435cb1801ad4ef4a39a833e9d4e901b` / `5063efddc523733c1c9181ffd4cd97a38c736b0710914ab629210ac2d37e026e` | 12 / 5 / 0 | 68 / 7 / 0 | `unverified` |

两份 plan 的 60 个 case 均已开始并结束。两批 candidate revision 均为 `4b428edfa70e98df3780f4850ab1d7129b95ed14`，`source-config` 与 `policy_generation` 均为 `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`。两份 plan 各列 37 个材料摘要；36 项一致，包括 `gateway-binary=96f4546eae22f39bd07ad7dd8afa457136b44311ce49cd9f28320c08e49485c7`、Claude/Codex client、32 个 harness 材料与 source-config；仅 `matrix-declaration` 随批次变化（48: `74132224bf0be8553380e8b5d69affbf67f567fda98125e519502d2a0a0b5b6e`；Gemini 12: `0b282672575845b76da047d93c094e3342c4de6deea4e36212b2294c391ef1e6`）。各 paired 文件的 run ID 和 plan SHA 与对应 plan/audit 相符。

按两份冻结批次做覆盖计数为 **44/60 safe successes、210 journaled request attempts、55 known dispatches**。这是同 revision、材料和配置指纹的两个独立 run 的汇总覆盖数，不是合并后的单 run、整体成功率或任一路由可靠率。原 [48-case summary/journal](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/summary.json) 与 [Gemini 12-case summary/journal](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/summary.json) 各自保留批次边界。

## 路由配对结果

下表的输入数是 paired diagnostics 按三个模板簇计算的首轮、峰值、全任务**输入减少率中位数**，`(eager − lazy) / eager`；配对含失败用量，不剔除失败。它们是描述性点估计。`quality LB` 是原诊断给出的条件性单侧 95% 下界；每组只有 3 个模板簇，不能替代代表性抽样。

| 客户端 / Provider / model alias | safe E/L（各 3 个模板） | 首轮 / 峰值 / 全任务输入减少率中位数 | quality LB |
| --- | ---: | ---: | ---: |
| Claude / `openai-compatible-qwen` / `deepseek-v4.1-flash` | 3/3 · 2/3 | 94.64% / 89.40% / 82.07% | −86.46pp |
| Codex / `openai-compatible-qwen` / `deepseek-v4.1-flash` | 2/3 · 2/3 | 73.40% / 55.20% / 39.22% | −63.16pp |
| Claude / `openai-compatible-opencode-go` / `glm-5.3-flash` | 3/3 · 3/3 | 95.04% / 90.39% / 88.11% | −63.16pp |
| Codex / `openai-compatible-opencode-go` / `glm-5.3-flash` | 2/3 · 1/3 | 73.94% / 68.90% / 29.25% | −86.46pp |
| Claude / `codex` / `gpt-6-luna` | 3/3 · 3/3 | 93.81% / 89.09% / 86.36% | −63.16pp |
| Codex / `meta` / `muse-spark-1.3-contributor` | 2/3 · 1/3 | 69.42% / 66.52% / 21.31% | −86.46pp |
| Claude / `meta` / `muse-spark-1.3-contributor` | 3/3 · 3/3 | 94.35% / 88.67% / 83.88% | −63.16pp |
| Codex / `codex` / `gpt-6-luna` | 3/3 · 3/3 | 67.12% / 49.08% / 32.99% | −63.16pp |
| Claude / `antigravity` / `gemini-3.5-flash-lite` | 0/3 · 0/3 | **未知 / 未知 / 未知** | −63.16pp* |
| Codex / `antigravity` / `gemini-3.5-flash-lite` | 3/3 · 2/3 | **未知 / 未知 / 未知** | −86.46pp* |

前 8 组的每项 token 收益均为 3 个已匹配模板簇；paired diagnostics 的首轮、峰值、全任务收益下界全部为 `null`（`insufficient_clusters`）。Gemini 两组均为 `known_pairs=0, missing_pairs=3`，三个 token 指标、cache-read/write 都未知，不能填零。星号处的质量下界是 diagnostics 的条件性计算值；用量缺失、失败和簇数不足仍使整组保持 `unverified`。

## Cache 与 case elapsed

此表单独列出 summary 的 cache-read 中位数和 cases journal 的 `case_finished.elapsed_ms` 中位数；时间为 eager/lazy 各三个模板的 case elapsed 中位数。两份 plan 中 `region_id` 与 `cache_condition` 均为 `unknown`，所以 cache-read 是观测计量，不是受控冷热缓存对照。Cache-read 已包含在输入总量中，不重复加算。Cache-write 任一模板缺字段即记为 `未知/不完整`，不将缺失当零。

| 客户端 / route | cache-read 中位数 E→L（tokens） | cache-write E/L（tokens） | case elapsed 中位数 E→L |
| --- | ---: | ---: | ---: |
| Claude / Qwen | 49,792→10,880 | 未知/不完整 | 6.043→7.433 s |
| Codex / Qwen | 54,400→60,416 | 未知/不完整 | 8.859→11.651 s |
| Claude / GLM | 44,800→0 | 未知/不完整 | 9.075→7.258 s |
| Codex / GLM | 0→39,040 | 未知/不完整 | 7.515→12.416 s |
| Claude / Codex GPT-6 | 39,424→3,584 | 0/0 | 9.121→12.761 s |
| Codex / Meta Muse | 54,129→58,963 | 未知/不完整 | 11.059→16.315 s |
| Claude / Meta Muse | 47,985→11,317 | 未知/不完整 | 8.341→10.539 s |
| Codex / Codex GPT-6 | 93,696→43,264 | 0/0 | 7.976→14.146 s |
| Claude / Gemini | 未知→未知 | 未知/未知 | 90.435→90.471 s |
| Codex / Gemini | 未知→未知 | 未知/未知 | 8.492→14.485 s |

Elapsed 是本批实际 case 结束时间，不是有充分样本的稳定 p95，也不构成延迟非劣证明。所有 Gemini 输入/cache usage 均缺失，elapsed 仍可从 journal 单独观测。

## 失败归因与退出边界

- 48-case 批原判 39/48；8 例为 summary 的 `failure_class=model`，1 例为 `protocol`。Qwen、GLM、Muse 的失败体现为必需 dispatch/answer/format/nonce 检查未通过或 intermediate receipt 未重放；具体情况见 [48-case summary](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/summary.json) 和 [case journal](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/cases.jsonl)。保留原分类，不据此改判为 Gateway 根因。
- Codex / Muse unavailable lazy 是独立的 `protocol` 失败：`preparation.json` 记录 `prepare` 阶段 `422 invalid_custom_input`；捕获到 `notify("hello world test")` 之后，client 收到 `responses_tools_prepare_rejected`，理由为自定义 history 对同一 `call_id` 重复输出。这是待协议诊断项，不与模型质量失败合并。[准备阶段诊断](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-unavailable-lazy/gateway-state/capture/preparation.json)；[client 错误行](../../.omo/tool-loading-completion/t14-primary-48-matrix-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-unavailable-lazy/client/stdout.jsonl#L9)。
- Gemini 12-case 批原判 5/12：Claude/Gemini 的 6 个 case 都记录 `upstream_http_error`，对应上游 attempts 中出现 HTTP 429；用量全未知，不能把预算类失败排除后认证其余路线。[Gemini summary](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/summary.json)；代表性 [Claude eager attempts](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/claude-gemini-3.5-flash-lite-native-json-schema-eager/gateway-state/capture/attempts.jsonl)。Codex/Gemini lookup lazy 的 summary 显示必需 dispatch 检查失败；客户端在 lookup 后又发起额外 `ledger_budget_confirm`，属该任务的额外确认行为，保留为 model/task quality 失败。[对应 case summary](../../.omo/tool-loading-completion/t14-primary-gemini-12-matrix-v1/summary.json)。
- 60 份 `gateway.exit.json` 均存在且 `exit_code=0`；两批 summary/journal 中 `forbidden_dispatch=0`、`forbidden_effect=0`、`active=0`。进程退出为 0 不代表 task success；它也不覆盖缺失的 usage 或取消协议证据。

## 验收状态

G1–G5 仍不能判通过。两批的零 forbidden/effect/active 只覆盖本次可观测边界，不能替代完整权限/非法参数验收；完整取消与权限生命周期仍有缺口，G2 未闭合。G3 的同 revision 数量覆盖已达到 60，但 48-case 批保留一例 Gateway protocol 422，后续多输出诊断修复也尚未在最终候选完成复测；模型任务失败另行保留归因。单一 run 不是额外验收门槛，分批证据仍需保留各自的冻结计划和统计边界。G4 每组只有 3 个固定模板簇，质量下界为 −63.16pp 或 −86.46pp，未达到 −2pp 非劣门槛。G5 的收益区间下界全缺，Gemini 全部用量未知，cache-write 部分缺失且无账单证据；收益数字只能作描述。

这两批也没有覆盖代表性编码质量和最终版本编程能力认证。Codex cancellation 是独立未完成项，见[取消传播缺口交付](../compatibility/codex-mcp-cancellation-gap_CN.md)。T15 编码试验独立报告，不与这里的两批结果合并认证。

验收门槛定义见[跨 Provider 工具加载方案 G1–G5](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md#L519)；固定模板簇样本不足以证明 −2pp 非劣的统计边界见[统一矩阵审计](2026-10-13-attested-matrix_CN.md#L124)。
