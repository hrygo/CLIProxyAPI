# T15 primary coding pilot 验收记录

核验日期：2026-10-13。候选 `4b428edfa70e98df3780f4850ab1d7129b95ed14` 的受限纯函数 coding pilot：24/24 started cases 原判 `safe_task_success=true`。它证明的是固定 Python 函数任务在指定 host gold suite 中完成，不是广泛 repository 编码能力或最终版本认证。

## 批次身份与完整性

证据：[batch plan](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/batch-plan.json)、[batch audit](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/batch-audit.json)、[case journal](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/cases.jsonl)、[summary](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/summary.json)、[paired diagnostics](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1-pairs.json)。

| 项目 | 实测值 |
| --- | --- |
| run ID / canonical `plan_sha256` | `live-matrix-8e96638e19b94a0ebf03d0e859201863` / `f1ba0f7595438e4e390b9b6c3cf9fd29ef4c4e1757e1b1401d91283eed7c7baa` |
| 原始 plan 文件 SHA-256 | `8a39674868fd4eb0b0f57276e55250a6395a1218987410a84d43935c637840dc` |
| 原始 `cases.jsonl` journal SHA-256 | `485fa4c57fd6812435364682d1510341945e78ac710b07b6a94b9c17f104afb8` |
| summary / paired 文件 SHA-256 | `597521d593e4ba320e99e5cd0ee13b5c7f49cfa67e5d30b702b2734a6a00705c` / `4aeff9bdd489ab894dc162a075a5efce2ac811e728017b3844f4500b8258f44c` |
| candidate revision / source-config fingerprint | `4b428edfa70e98df3780f4850ab1d7129b95ed14` / `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7` |
| gateway binary / Claude client / Codex client | `96f4546eae22f39bd07ad7dd8afa457136b44311ce49cd9f28320c08e49485c7` / Claude CLI `2.1.289` / Codex CLI `0.160.0` |
| planned / started / not started / unfinished / safe | 24 / 24 / 0 / 0 / 24 |
| journaled requests / known dispatches / unknown request evidence / unknown dispatch cases | 123 / 81 / 0 / 0 |
| gate / unknown scope / admission budget | `unverified` / 24 cases / `budget_within_limits=true` |

paired 文件的 run ID 与 canonical plan SHA 对上。计划记录的 source-config 指纹只用于识别本批候选，不表示批外或全局配置状态。

## 十二个 eager/lazy 配对

输入列为同一 task 的 eager→lazy 实测 tokens；括号中的减少率来自 paired diagnostics 的 `(E−L)/E`。`total input` 已含 cache-read，不重复相加。四路由各含 `coding-budget`、`coding-identity`、`coding-retry` 三种纯函数任务。

| 客户端 / 路由 / task | requests / dispatch E→L | 首轮输入 E→L（减少） | 峰值输入 E→L（减少） | 全程输入 E→L（减少） | cache-read E→L；cache-write E→L | elapsed E→L |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Claude / GLM / budget | 5→7 / 3→5 | 48,057→2,469（94.86%） | 48,892→4,011（91.80%） | 242,758→23,315（90.40%） | 189,696→12,672；0→0 | 16.975→37.272 s |
| Claude / GLM / identity | 6→6 / 4→3 | 48,058→2,470（94.86%） | 49,081→3,661（92.54%） | 291,868→19,018（93.48%） | 189,632→9,216；未知→0 | 33.623→31.587 s |
| Claude / GLM / retry | 5→6 / 3→3 | 48,058→2,470（94.86%） | 48,865→3,664（92.50%） | 242,649→19,050（92.15%） | 188,928→9,216；未知→0 | 20.488→21.186 s |
| Claude / Qwen / budget | 4→5 / 3→3 | 49,171→2,727（94.45%） | 49,993→4,052（91.89%） | 198,526→17,369（91.25%） | 148,352→12,416；未知→未知 | 19.024→20.708 s |
| Claude / Qwen / identity | 4→5 / 3→3 | 49,171→2,727（94.45%） | 49,910→3,887（92.21%） | 198,326→16,967（91.44%） | 195,840→12,288；未知→未知 | 13.035→10.569 s |
| Claude / Qwen / retry | 5→5 / 3→3 | 49,171→2,727（94.45%） | 50,044→3,947（92.11%） | 248,428→17,136（93.10%） | 197,376→12,288；未知→未知 | 22.589→15.404 s |
| Codex / GPT-6 / budget | 6→5 / 5→3 | 51,894→17,023（67.20%） | 53,755→27,463（48.91%） | 317,716→124,627（60.77%） | 260,864→96,000；0→0 | 27.047→14.570 s |
| Codex / GPT-6 / identity | 4→7 / 3→5 | 51,897→17,026（67.19%） | 53,094→27,937（47.38%） | 210,305→179,905（14.46%） | 154,880→149,760；0→0 | 16.898→20.860 s |
| Codex / GPT-6 / retry | 4→5 / 3→3 | 51,897→17,026（67.19%） | 53,075→27,322（48.52%） | 210,265→124,305（40.88%） | 154,880→96,000；0→0 | 13.891→13.832 s |
| Codex / Muse / budget | 4→5 / 3→3 | 54,858→16,738（69.49%） | 56,658→21,386（62.25%） | 223,453→99,104（55.65%） | 166,611→76,740；未知→未知 | 22.166→21.929 s |
| Codex / Muse / identity | 5→5 / 4→3 | 54,858→16,738（69.49%） | 56,650→23,208（59.03%） | 278,603→106,511（61.77%） | 221,124→83,140；未知→未知 | 22.206→22.983 s |
| Codex / Muse / retry | 5→5 / 4→3 | 54,858→16,738（69.49%） | 56,699→27,820（50.93%） | 278,725→124,803（55.22%） | 221,124→95,684；未知→未知 | 62.211→26.215 s |

summary 的 123 个 request usage 均将 `input_total` 标为 inclusive，且 `unknown_input_requests=0`。cache-write 在 Qwen、Muse 和部分 GLM 请求中缺字段，保持未知；本批没有货币费用或 provider bill 数据。四组的 `region_id` 与 `cache_condition` 均未知。

Codex / GPT-6 三个任务的峰值输入降幅分别为 **48.91%、47.38%、48.52%**，均低于 50% 点估计门槛；这些单簇结果没有收益置信下界，不能宣称 G5 收益通过。

### GPT 峰值归因：发现输出扩大了累计历史

从三组 lazy 的原始 request body 可回链到 `functions.exec` 中的 `ALL_TOOLS.filter`：模型用包含 `fixture`、`coding` 等词的宽泛正则匹配 name 和 description，然后输出全部命中对象；三案正则略有不同。这些过滤均命中全部 19 个 fixture 工具，不含 `tool_search`、`query`、`limit`、`max_results` 或数量截断。结果作为一个 **41,074 字节**的 `custom_tool_call_output`，从 `request-000002.body` 起持续进入后续请求。客户端记录加载了 19 个 fixture schema，三个任务实际执行的只有 `coding_task_read`、`coding_source_replace`、`coding_tests_run`。这不是检索器 Top-K 取太多的证据。

| GPT task | eager 峰值请求 / 累计历史 JSON 字节 | lazy 峰值请求 / 累计历史 JSON 字节 | 峰值 exec 输出字符串字节 E→L |
| --- | --- | --- | --- |
| budget | `request-000006` / 10,953 B | `request-000005` / 51,565 B | 4,776→44,605 B |
| identity | `request-000004` / 7,711 B | `request-000007` / 56,925 B | 3,238→45,271 B |
| retry | `request-000004` / 5,494 B | `request-000005` / 48,746 B | 3,302→44,233 B |

这些字节计数来自捕获 JSON，是结构代理，不是分词后的 token 归因；Provider 未给出 `tool_prompt_tokens`，不能据此计算每份 schema 的精确 token 贡献。声明本身已缩小，但完整目录输出抵消了部分峰值收益。后续临时客户端/任务策略可验证“先按需求筛选 metadata，只输出必要声明”；必须用新冻结 prompt 做配对，不重写本批。Gateway 不得擅自截断、摘要或删除真实 Code Mode 输出。

上述六个 GPT case 的 upstream attempts 均有 HTTP 200 完成记录；E/L 请求轮次为 budget 6/5、identity 4/7、retry 4/5。只在这六例的已捕获 HTTP 边界上，未见传输失败重试；这不扩大为其余 Provider 或未来任务的保证。

## 代码差异与 host gold

24 个 case 的 sandbox `source.diff` 均显示相对 baseline 的实际代码修改，24/24 `code_outcome.changed=true`。三种任务的实现目标分别是：`remaining_budget` 校验输入并只累计 `posted` 记录；`unique_call_ids` 校验字符串 ID、跳过空串并保留顺序去重；`retryable` 校验状态/暴露标志，暴露后不重试、允许 429 和 500–599 中除 501/505 外的状态。每个 eager/lazy 版本均通过本机 host gold：budget 11/11、identity 10/10、retry 15/15；合计 288/288 次测试执行通过。

| 路由 | budget diff E/L | identity diff E/L | retry diff E/L |
| --- | --- | --- | --- |
| Claude / GLM | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-budget-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-budget-native-json-schema-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-identity-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-identity-native-json-schema-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-retry-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-opencode-go-glm53-flash-coding-retry-native-json-schema-lazy/source.diff) |
| Claude / Qwen | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-budget-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-budget-native-json-schema-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-identity-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-identity-native-json-schema-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-retry-native-json-schema-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/claude-qwen-ds41-flash-coding-retry-native-json-schema-lazy/source.diff) |
| Codex / GPT-6 | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-budget-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-budget-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-identity-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-identity-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-retry-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-gpt-6-luna-code-mode-only-coding-retry-lazy/source.diff) |
| Codex / Muse | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-identity-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-identity-lazy/source.diff) | [E](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-retry-eager/source.diff) · [L](../../.omo/tool-loading-completion/t15-primary-coding-24-pilot-v1/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-retry-lazy/source.diff) |

## 失败、预算、退出与统计边界

- summary 中 24/24 `failure_class=none`、`issues=[]`；各 case 的 answer/dispatch/nonce/code/format/terminal checks 均通过。24 份 per-case `gateway.exit.json` 均为 `exit_code=0`；`forbidden_dispatch`、`forbidden_effect`、`active` 合计均为 0。
- journal 有 24 个 `case_started`、24 个 `case_finished`、123 个 `request_started` 和 123 个 `request_finished`；audit 的 `request_evidence_unknown_cases=0`。所有 case 的 admission `budget_rejections`、`closed_rejections`、`failed_rejections` 均为 0，`active_bodies=0`；planned limits 为每 case 最多 8 requests/dispatch、90 秒，audit 标记在批次限额内。
- request-finished journal payload 记录 contract、usage、request ID 与 usage kind，不含 HTTP status 或 retry counter；因此本报告不把 case-level 全通过扩大成“Provider 层零错误/零重试”。`coding-retry` 是本 pilot 的纯函数 task，其 gold 结果单列于上表。
- 每个 route/task paired scope 都只有 1 个 matched E/L pair、1 个 template cluster。不同 task 的 `fixture_hash` / `permission_hash` 不同；12 组收益下界均为 `null`（`insufficient_clusters`），条件性质量下界为 −0.95。不能把三种任务或四路由池化成置信区间，也不能把描述性节省率称为认证收益。
- 本 pilot 是三种固定函数、隔离 Python module 的代表性范围；没有真实多文件 repository 任务、广泛代码库维护或生产仓库变更。整体 pair gate 仍为 `unverified`；cache/region scope 和账单成本未知，尚不构成默认策略验收。
