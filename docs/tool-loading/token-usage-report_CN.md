# 跨 Provider 工具按需加载：token 使用完整报告

核验日期：2026-10-14（Asia/Shanghai）。当前生产代码基准：`743a5e0ae14d100b2c1c8f2101b93adfa420269e`。本文是已取得用量证据的整合终版，**没有当前代码版本的新 Provider 测量**；不是“所有路线收益已通过”的认证报告。[实施依据](design-and-implementation_CN.md)解释收益机制，[验证报告](verification-and-compatibility_CN.md)记录版本、执行结果与失败责任。

## 1. 结论与数据覆盖

两个主要官方客户端编码对照，均在 `gpt-6-luna` / Codex OAuth 路由上：

| 客户端 / 历史 gateway | 全任务输入 eager→lazy | 首轮 / 峰值 / 全任务输入减少 | 请求 E/L | 已知输出 E→L |
| --- | ---: | --- | ---: | ---: |
| Codex `0.160.0` / `0efa8a28` | 255,874→81,343 | 69.71% / 66.94% / 68.21% | 5/5 | 857→745 |
| Claude Code `2.1.289` / `e202aaa8` | 162,062→16,563 | 93.42% / 90.35% / 89.78% | 4/5 | 597→686 |

这些是两个固定编码任务对的点估计，已有检索、全模型请求和 cache-read 输入进入总量。两组属于不同代码版本和客户端，不合并平均收益。代码及各 11 项行为检查通过，执行结果另有独立审计；原始分类差异、Codex wrapper 失败保留于第 3、4 节。

历史非编码路由中位数包含负收益：Codex/GLM 全任务输入增加 5.02%；较早 Codex/Muse lookup 增加 16.37%。Gemini 的首轮、峰值、全任务输入收益继续未知。首轮变小不保证总任务更省。

## 2. 计量口径与完整性

| 指标 | 定义 | 限制 |
| --- | --- | --- |
| 工具声明字节 | 对实际工具声明的 UTF-8 JSON 表示计数 | 与 tokenizer、系统提示及缓存费用不同；不能换算为精确 Provider tokens。 |
| 首轮输入 | 任务首个实际模型请求的 input tokens | 标题等辅助推理若存在也属于模型请求；“首个带工具请求”另注明，不偷换指标。 |
| 峰值输入 | 单任务所有模型请求中 input 的最大值 | 必须有全量有效请求用量；缺失时保持未知。 |
| 全任务输入 | 所有实际模型请求 input 求和 | 包括检索、标题、失败和重试；不能只求成功调用。 |
| 全任务输出 | 所有实际模型请求 output 求和 | 单独报告，不因输入下降而省略输出增加。 |
| cache-read | input 中报告的缓存部分 | 已包含于 input，不再加到总输入；缓存命中量不等于费用。 |
| cache-write | 实际用量契约报告的缓存写入 | 有字段且为 0 才写 0；缺字段为未知。 |
| reasoning | output 中报告的推理子集 | 已包含于 output，不能再次加到 total。 |
| total | 本文两组 Responses 记录为 input + output | 不同协议使用各自契约；不混用 Gemini 口径。 |
| input − cache-read | 由已知数值相减的派生输入 | 不是“应付费 tokens”或费用权威值。 |
| physical requests | 模型及明确辅助 HTTP attempts，包含失败和重试 | 辅助 HTTP 不伪装为推理；无 finish 的 started attempt 不填零消耗。 |
| MCP attempts / dispatch | 进入工具服务的尝试 / 通过执行前门槛的实际调用 | 不是 Provider 请求；非法参数可有 attempt 而没有 dispatch。 |
| elapsed | case 已记录的准备、执行、验证和清理时长 | 不等于 TTFT、稳定 p95 或用户可见模型延迟。 |
| 账单/货币成本 | 可核实价格、计费数量和其他费用形成的成本 | 当前没有完整账单证据，保持未知。 |

减少率统一为 `(eager − lazy) / eager`。负数表示 lazy 增加；零分母无定义，缺失值不计算。缓存和 reasoning 的百分比仅描述子集变化，不参与额外加总。

成对比较绑定同 candidate、客户端/有效模式、真实 route/模型/协议、工具/权限目录、任务和输出要求。不同 tokenizers 不求一个跨 Provider 总收益百分比。固定顺序、冷热缓存未知及模板高度相关时，不生成可靠率、非劣性或稳定经济性置信结论。

## 3. 官方 Codex 编码对照：逐请求明细

### 3.1 范围与结果

- Gateway：`0efa8a28bc8e68bf5f13293b17d424c7a9065f7c`。
- 官方 Codex：`0.160.0`，`code_mode_only`；模型 `gpt-6-luna`，Codex OAuth，原生 Responses 精确多结果 profile。
- 一个固定 budget coding 任务，eager→lazy 顺序；每臂最多八请求、八 dispatch、120 秒。
- 2 started / 2 finished，10 request_started / 10 request_finished，6 MCP attempts/dispatch。两臂代码修改和 11/11 gold 通过，真实通知与最终结果回放保留。
- 各臂 forbidden MCP dispatch、effect、final active 均为观察值 0；MCP 观测不覆盖 hosted 执行。
- 原 journal 两条 `failure_class=model` 与全部六项 checks=true、独立审计两个 case_safe_results=true 不一致；原记录未改写。外层汇总脚本排他写入错误退出 1，独立只读核对没有重跑 Provider。

计量契约为 `openai-responses-v1`，每请求 `usage_kind=request-total`。以下使用 E1–E5、L1–L5 的脱敏模式内序号，未公开原请求 ID、call ID、prompt、工具参数或结果正文。

### 3.2 十次请求

| 模式 / 请求序号 | input | cache-read | cache-write | output | reasoning（已含于 output） | total |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| E1 | 50,024 | 0 | 0 | 153 | 0 | 50,177 |
| E2 | 50,468 | 48,896 | 0 | 90 | 0 | 50,558 |
| E3 | 51,240 | 49,920 | 0 | 454 | 218 | 51,694 |
| E4 | 51,887 | 50,944 | 0 | 90 | 0 | 51,977 |
| E5 | 52,255 | 50,944 | 0 | 70 | 0 | 52,325 |
| L1 | 15,153 | 0 | 0 | 170 | 30 | 15,323 |
| L2 | 15,614 | 14,080 | 0 | 94 | 17 | 15,708 |
| L3 | 16,390 | 15,104 | 0 | 328 | 103 | 16,718 |
| L4 | 16,913 | 16,128 | 0 | 84 | 7 | 16,997 |
| L5 | 17,273 | 16,128 | 0 | 69 | 0 | 17,342 |

十条记录均有六项非负整数计数；逐条 `total = input + output`，cache-read≤input、reasoning≤output。原始字段为 `input_tokens_details.cached_tokens`、`input_tokens_details.cache_write_tokens` 和 `output_tokens_details.reasoning_tokens`；cache-write 的十个 0 是实际报告值。

### 3.3 汇总与减少率

| 指标（tokens） | eager | lazy | 减少率 |
| --- | ---: | ---: | ---: |
| 首个模型请求输入 | 50,024 | 15,153 | 69.71% |
| 峰值模型请求输入 | 52,255 | 17,273 | 66.94% |
| 全任务输入 | 255,874 | 81,343 | 68.21% |
| 全任务输出 | 857 | 745 | 13.07% |
| input + output | 256,731 | 82,088 | 68.03% |
| cache-read（输入子集） | 200,704 | 61,440 | 69.39% |
| cache-write（报告值） | 0 | 0 | 不适用 |
| reasoning（输出子集） | 218 | 157 | 27.98% |
| input − cache-read（派生值） | 55,170 | 19,903 | 63.92% |

每臂 5 请求、3 MCP dispatch。case elapsed 为 23,292→22,038 ms，观察减少 5.38%；没有稳定延迟结论。cache-read 占全输入约 78.44%→75.53%，条件未受控。模型计费价、缓存折扣、辅助费用及账单未知；不能把 input 降幅直接写为费用降幅。

## 4. 官方 Claude Code 编码对照：逐请求明细

### 4.1 范围与结果

- Gateway：`e202aaa89f5f70fd13eb7d9808842cbdd41fbea1`。
- 官方 Claude Code：`2.1.289`，独立标准隔离模式和原生 StructuredOutput；Messages→Codex OAuth，模型 `gpt-6-luna`。
- 一个固定 selective budget coding 任务，lazy→eager 顺序；每臂最多八请求、八 dispatch、120 秒。
- 2 started / 2 finished，9 request_started / 9 request_finished，6 MCP attempts/dispatch。两臂代码修改和 11/11 gold 通过。
- ToolSearch 实际返回三个引用，之后声明、执行 read/replace/tests 三工具；声明数 eager `[70,70,70,70]`、lazy `[0,3,3,3,3]`。
- 原 journal 两条 `failure_class=model` 与全部六项 checks=true、独立审计两个 case_safe_results=true 不一致；没有追溯改写。外层脚本退出 0，无 Provider 重跑。
- MCP forbidden dispatch/effect/final active 均为观察值 0；两臂既有 image_generation 声明未观察到执行，hosted 权限与费用未知。

用量由实际上游 Responses 记录导入 `openai-responses-v1 / request-total`，不是客户端估算器或 Provider 账单。实际顺序为 lazy→eager，下表按模式排列以便比较，没有声称随机调度。

### 4.2 九次请求

| 模式 / 请求序号 | input | cache-read | cache-write | output | reasoning（已含于 output） | total |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| E1 | 39,965 | 0 | 0 | 45 | 23 | 40,010 |
| E2 | 40,336 | 39,424 | 0 | 380 | 214 | 40,716 |
| E3 | 40,802 | 39,424 | 0 | 29 | 7 | 40,831 |
| E4 | 40,959 | 40,448 | 0 | 143 | 56 | 41,102 |
| L1 | 2,629 | 0 | 0 | 47 | 0 | 2,676 |
| L2 | 2,915 | 0 | 0 | 20 | 0 | 2,935 |
| L3 | 3,261 | 0 | 0 | 457 | 293 | 3,718 |
| L4 | 3,806 | 2,560 | 0 | 20 | 0 | 3,826 |
| L5 | 3,952 | 3,584 | 0 | 142 | 57 | 4,094 |

九条记录同样满足非负整数、逐请求加总和子集关系。cache-write 的九个 0 是实际报告值，不沿用其他模型的缺字段。

### 4.3 汇总与减少率

| 指标（tokens） | eager | lazy | 减少率 |
| --- | ---: | ---: | ---: |
| 首个模型请求输入 | 39,965 | 2,629 | 93.42% |
| 峰值模型请求输入 | 40,959 | 3,952 | 90.35% |
| 全任务输入 | 162,062 | 16,563 | 89.78% |
| 全任务输出 | 597 | 686 | -14.91% |
| input + output | 162,659 | 17,249 | 89.40% |
| cache-read（输入子集） | 119,296 | 6,144 | 94.85% |
| cache-write（报告值） | 0 | 0 | 不适用 |
| reasoning（输出子集） | 300 | 350 | -16.67% |
| input − cache-read（派生值） | 42,766 | 10,419 | 75.64% |

lazy 多一次检索，五请求仍已全部计入。输出增加 89 tokens（14.91%），reasoning 增加 50 tokens；reasoning 已包含于输出。input+output 减少 89.40%，是已报告 token 计数，仍不是费用。

每臂 3 MCP dispatch。case elapsed 为 36,651→28,711 ms，观察减少 21.66%；cache-read 占全输入约 73.61%→37.09%，没有控制缓存条件。固定顺序和单模板不能认证 stable latency、质量非劣或净收益区间。

## 5. 跨 Provider 非编码完整路由表

### 5.1 T20：五模型、两客户端、三个固定模板

Gateway `58f5523b90d0561ff7812ad8da8257f14acb4ef5`，Codex `0.160.0`、Claude Code `2.1.289`。每行三个 matched E/L 模板；Safe E/L 各分母为 3，输入列是模板内成对减少率的中位数。含失败任务已产生的输入，负值和缺失保留。

| Client / 声明上游模型 | Safe E / L | 首轮输入减少 | 峰值输入减少 | 全任务输入减少 |
| --- | --- | --- | --- | --- |
| Codex / `gpt-6-luna` | 3 / 3 | 67.13% | 49.35% | 33.03% |
| Codex / `muse-spark-1.3-contributor` | 3 / 2 | 69.42% | 52.77% | 43.03% |
| Codex / `glm-5.3-flash` | 1 / 0 | 73.95% | 73.50% | **−5.02%** |
| Codex / `deepseek-v4.1-flash`（Qwen endpoint） | 1 / 2 | 73.40% | 55.10% | 42.54% |
| Codex / `gemini-3.5-flash-lite` | 1 / 1 | 未知 | 未知 | 未知 |
| Claude / `gpt-6-luna` | 3 / 3 | 93.82% | 88.65% | 86.31% |
| Claude / `muse-spark-1.3-contributor` | 3 / 3 | 94.35% | 89.08% | 84.09% |
| Claude / `glm-5.3-flash` | 3 / 3 | 95.05% | 90.44% | 88.04% |
| Claude / `deepseek-v4.1-flash`（Qwen endpoint） | 3 / 3 | 94.65% | 89.33% | 87.27% |
| Claude / `gemini-3.5-flash-lite` | 0 / 0 | 未知 | 未知 | 未知 |

全批 60 started / finished、41 safe、19 非成功（8 model、3 budget、8 unknown）。161 已记录物理请求包括 12 个 HTTP 429；八例启动失败缺计数，真实请求/dispatch 总量和全部预算合规性未知。各行只有三个固定模板，收益区间下界缺失，不能从数值范围生成可靠率。

Codex GLM 的首轮与峰值下降但全任务增加，表明轮次和历史累积可能抵消收益。Gemini 所有输入/cache 值未知，不作为 0% 放进跨路由均值。完整失败责任见[验证报告](verification-and-compatibility_CN.md)。

### 5.2 T14：同候选两个 run 的早期覆盖

Gateway `4b428edfa70e98df3780f4850ab1d7129b95ed14`，48-case 主矩阵加 12-case Gemini；44/60 safe、210 请求、55 dispatch。两份 plan 独立，不能拼成“单 run 全通过”。下表保留当时条件性 quality LB；它不是代表性任务质量差值的认证下界。

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

八条有用量路由的收益下界全部缺失，Gemini 为零个已知用量配对。表内 * 标记原诊断的条件性计算值，不消除 Gemini 用量未知。质量 LB 来自原固定模板分析，不作为当前实施门槛或样本设计；尤其两臂均失败不能证明可用。

缓存与时间的独立原值：

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

Cache-read 为各模式 case 的中位数；它与“全批 cache 总量”不同。cache-write 任一模板缺字段保留未知/不完整。elapsed 有观测值不表示输入或账单也有观测值。

## 6. 受限编码任务完整配对

### 6.1 T15：十二个 route/task 对

Gateway `4b428edf`；Claude `2.1.289` 的 GLM/Qwen route，Codex `0.160.0` Code Mode 的 GPT/Muse route；三个单函数任务 budget、identity、retry，24/24 safe、123 请求、81 dispatch，24 有代码修改、host gold 288/288 次执行。

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

此表各 route/task 只有一个模板簇。GPT cache-write 实际 0；Qwen、Muse 和部分 GLM 缺字段保留未知。region、缓存条件、货币成本均未知。每对 input 已含 cache-read；123 个模型请求有 input，但不能据 package/gold 数量增加独立样本数。

### 6.2 T17：原始与选择性 Code Mode 发现

Gateway `ca0758f4943baa97e651545a56c376adcdcb8e3d`，Codex `0.160.0` Code Mode，GPT/Muse×三任务×两种 prompt×E/L。24 finished、20 safe、123 请求、75 dispatch、85 MCP attempts。下表保留全部失败；Muse selective budget eager 在首响应失败，用量未知，该 pair 不计算。

| Route | Task / prompt | Safe E / L | 首轮降幅 | 峰值降幅 | 全任务降幅 |
| --- | --- | --- | --- | --- | --- |
| GPT | budget / 原始 | 1 / 0 | 67.19% | 48.33% | 40.81% |
| GPT | budget / 选择性 | 1 / 1 | 67.09% | 63.05% | 51.01% |
| GPT | identity / 原始 | 1 / 1 | 67.19% | 48.38% | 40.84% |
| GPT | identity / 选择性 | 1 / 1 | 67.08% | 64.88% | 65.91% |
| GPT | retry / 原始 | 1 / 1 | 67.19% | 48.60% | 40.94% |
| GPT | retry / 选择性 | 1 / 1 | 67.08% | 64.93% | 65.95% |
| Muse | budget / 原始 | 1 / 0 | 69.49% | 59.75% | 41.41% |
| Muse | budget / 选择性 | 0 / 1 | 未知 | 未知 | 未知 |
| Muse | identity / 原始 | 1 / 1 | 69.49% | 50.84% | 55.06% |
| Muse | identity / 选择性 | 1 / 1 | 69.39% | 66.55% | 67.87% |
| Muse | retry / 原始 | 1 / 0 | 69.49% | 60.08% | 53.85% |
| Muse | retry / 选择性 | 1 / 1 | 69.39% | 66.55% | 73.19% |

六个 selective lazy 均仅加载三个所需工具。策略使用明确工具全名，有指导；不能外推自然语言检索质量。GPT 三任务峰值降幅中位数 48.38%→64.88%，全任务 40.84%→65.91%。不同 prompt 的 eager/lazy 不跨组拼接。

原始 GPT lazy 的宽泛 `ALL_TOOLS.filter` 匹配了全部 19 个 fixture 工具，发现输出 JSON 为 41,074 B，进入后续 history；实际仅执行三工具。选择性输出按 text 口径为 40,155→1,047–1,049 B。两个字节口径不同，均不是精确 token 分摊；未从 Provider 获得 tool_prompt_tokens。网关没有删减工具结果。

四个非成功为：GPT original budget lazy gold 10/11；Muse original budget/retry lazy 各八请求耗尽并有加载前调用；Muse selective budget eager 返回非法 custom `{}`，零 MCP attempts/dispatch，原 harness framework 标签保留。失败用量和预算不被成功子集覆盖。

## 7. 其他固定 scope 与负收益

### 7.1 新基线四个绝对量配对

T13 新基线 candidate `4b428edf`；十例八 safe，28 已知请求、12 dispatch，两例 Gemini 启动失败，计数未知。下表为四个有完整输入的 pair，未按成功筛选。

| 客户端 / 路由 / 模板 | 首轮输入 E→L（减少） | 峰值输入 E→L（减少） | 全任务输入 E→L（减少） | cache-read E→L；cache-write | case elapsed E→L；请求数 E→L |
| --- | ---: | ---: | ---: | ---: | ---: |
| Claude / `gpt-6-luna` / `ledger-json-lookup-read-repeats-v2` | 39,630→2,422（93.89%） | 39,784→4,276（89.25%） | 79,414→10,875（86.31%） | 39,424→0；0→0 | 8.493→10.500 s；2→3 |
| Claude / Meta `muse-spark-1.3-contributor` / `ledger-json-lookup-read-repeats-v2` | 48,208→2,696（94.41%） | 48,373→4,962（89.74%） | 96,581→12,441（87.12%） | 47,985→4,834；未知→未知 | 7.676→9.914 s；2→3 |
| Codex Code Mode / Meta `muse-spark-1.3-contributor` / `ledger-json-lookup-read-repeats-v2` | 54,536→16,658（69.46%） | 54,895→24,910（54.62%） | 109,431→127,341（−16.37%；lazy 多 16.37%） | 54,129→101,173；未知→未知 | 9.924→20.180 s；2→6 |
| Codex Code Mode / Meta `muse-spark-1.3-contributor` / `coding-pure-python-budget` | 54,855→16,735（69.49%） | 56,525→23,662（58.14%） | 223,175→125,037（43.97%） | 165,971→99,637；未知→未知 | 21.823→29.865 s；4→6 |

Codex/Muse lookup 的全任务输入增加 17,910 tokens，虽然峰值减少 54.62%，请求从 2 增至 6。该反例保持在报告中。一个 matched pair 无法提供高置信总体收益。

### 7.2 响应诊断正常路径校准

T19 candidate `58f5523b`，Codex GPT/Muse 各一个 selective budget pair；4/4 safe、20 请求、12 dispatch。观察减少率：

| route | 首轮 | 峰值 | 全任务 |
| --- | ---: | ---: | ---: |
| GPT | 67.10% | 64.62% | 65.75% |
| Muse | 69.38% | 66.49% | 67.83% |

这验证正常采集路径和受限 coding，未触发真实响应转换拒绝，不继承为最终代码或代表性任务区间。

### 7.3 较早补充证据的计量限度

| 观察范围 | 已报告值 | 限度 |
| --- | --- | --- |
| 首包本机探针 | Codex tools 数组 195,587→9,984 B（94.90%）；OpenCode 199,295→10,912 B（94.52%） | HTTP 400 为预设停止；没有 Provider input 或成功任务。 |
| 早期 OpenCode/Muse unavailable pair / `4b2ad3d` | lazy 六请求、eager 一请求；峰值输入增加 36.60%、全任务增加 234.98% | 旧版本和任务范围，保留为负收益事实，不代替重点官方客户端结果。 |
| 原 GLM/OpenCode 标题缺 finish | 其余任务输入已知 101,439；标题 input 未知 | 101,439 不是全任务输入，不计算配对收益。 |
| 后续 GLM/OpenCode 固定候选 / `1d598d0a` | 首轮 555→555；峰值 50,751→14,284；全任务 101,994→36,174 | 两案一成功，eager 最终纯 JSON 失败；新完整计量不补填旧缺失。 |
| 较早 Gemini 报告值 | 旧 decoder 给出数值，后续 capture 有 snapshot 冲突 | 不纳入权威用量配对；成功执行与计量未知分别成立。 |

其它阶段模板、重复诊断、格式变更和采集修订原表可从整合前 Git 快照定位；它们不与上述固定 scope 池化。保留高价值完整主表不意味着旧未知值被补齐。

### 7.4 其他历史输出与任务 profile

这些数据分别属于早期固定 scope，保留完整 route 表；与前述候选和输出方式独立。首轮包含当时实际模型请求，标题控制的变化不能跨 profile 拼接成收益。

| candidate / profile | route / 客户端 | 严格结果 | 首轮 / 峰值 / 全任务减少 |
| --- | --- | --- | --- |
| `3de2aee8` / 三场景 | Qwen / Codex | 6/6 | lookup 全任务 54.38%；unavailable **−16.55%**；multitool 57.78% |
| `3de2aee8` / 三场景 | Qwen / OpenCode | 4/6 | 三场景全任务 67.04%、21.92%、69.25%；首个 title 请求 input 为 582/614/620，E/L 相等 |
| `4e68b510` / 固定标题 | Qwen / Codex | E 3/3，L 3/3 | 77.24% / 65.61% / 54.38% |
| `4e68b510` / 固定标题 | Qwen / OpenCode | E 3/3，L 2/3 | 91.33% / 77.94% / 73.72% |
| `4e68b510` / 固定标题 | GLM / Codex | E 3/3，L 1/3 | 77.87% / 66.26% / 23.07% |
| `4e68b510` / 固定标题 | GLM / OpenCode | E 2/3，L 0/3 | 91.60% / 77.91% / 74.10% |
| `732e73a1` / native 输出 | Qwen / Codex | E 0/3，L 0/3 | 未知 / 未知 / 未知 |
| `732e73a1` / native 输出 | Qwen / OpenCode | E 2/3，L 3/3 | 90.97% / 77.07% / 74.20% |
| `732e73a1` / native 输出 | GLM / Codex | E 1/3，L 1/3 | 77.86% / 77.86% / 77.86% |
| `732e73a1` / native 输出 | GLM / OpenCode | E 3/3，L 2/3 | 91.17% / 91.11% / 91.11% |
| `06e7b3ee` / shape-only | Qwen / OpenCode | E 3/3，L 3/3 | 90.97% / 77.06% / 73.06% |
| `06e7b3ee` / shape-only | GLM / OpenCode | E 3/3，L 2/3 | 91.25% / 77.88% / 73.61% |

原 native 输出 profile 的 unavailable schema 将 status/project 限为期望答案，削弱了语义错误的可观测性。后 profile 去除单值 enum、让格式检查与独立 gold 分开；原标签和消耗未改写。这些表是证据范围，不能相加为质量认证。

较早纯函数 coding 候选 `ff28319c9f2e7d110bc9a6ffd1f8c21d1258a89f`：

| route / 客户端 | 严格结果 / 代码通过 | 请求 / dispatch | 首轮 / 峰值 / 全任务输入减少中位数 |
| --- | --- | --- | --- |
| Qwen / Codex | 6/6 / 6/6 | 29 / 19 | 77.27% / 66.44% / 62.21% |
| GLM / Codex | 5/6 / 6/6 | 28 / 18 | 77.89% / 66.72% / 61.81% |
| Qwen / OpenCode | 6/6 / 6/6 | 24 / 18 | 90.59% / 88.44% / 89.43% |
| GLM / OpenCode | 6/6 / 6/6 | 28 / 23 | 90.88% / 87.20% / 88.69% |

较早 Claude 原生 StructuredOutput 候选 `1677af587d6e25baff48393898191a44ba9562fb`：

| route | 严格结果 | 请求 / dispatch | 首轮 / 峰值 / 全任务输入减少中位数 |
| --- | --- | --- | --- |
| Qwen | 12/12 | 44 / 26 | 94.65% / 89.44% / 84.66% |
| GLM | 12/12 | 50 / 28 | 95.04% / 90.12% / 88.66% |

每路线包含三个工具模板与三个 coding 模板的 E/L。原报告收益聚合有各自 scope，不能从“12/12”推导无限任务可靠。工具任务 elapsed p50 E/L：Qwen 7.731/9.284 s，GLM 7.810/7.419 s；cache-read/input E/L：Qwen 50.92%/53.73%，GLM 53.56%/22.52%。Qwen cache-write 未知；GLM eager 实际报告 0，lazy 至少一份未报告，整体未知。原纯 JSON profile 的失败不是被新 StructuredOutput 改判。

## 8. Gemini / Antigravity 用量边界

指定 `gemini-3.5-flash-lite` 的 T14 两个实际模型响应有下列 raw scalar 变化。序号是历史 capture 的脱敏定位，不公开内容。

| 响应序号 | 原始 snapshots | 最后 finish |
| --- | --- | --- |
| 2 | thoughts 194→388→194；total 58,046→57,852 | STOP |
| 3 | thoughts 277→554→277；total 58,540→58,457；prompt 57,937→58,131 | STOP |

最后 snapshot 分别满足 57,635+194+23=57,852、58,131+277+49=58,457。内部加总一致只能证明数学关系，不能证明 Antigravity wrapper 最后值是账单权威值，也不能证明中间修正可直接忽略。

历史 UsageMetadata 计量口径将 cached prompt 视为 input 子集，thinking 已进入 total，不能重复相加。本次没有取得私有 wrapper 的版本化权威契约或独立账单对账，因此不取 max、最小值或最后值生成收益，也不用 tokenizer 估计替代实际用量。

额度查询、OAuth refresh、project discovery 等明确辅助 HTTP 独立计数，共用物理请求预算。辅助正文中的 token-like 字段不冒充模型 usage；没有计费契约时辅助成本为未知。模型可完成真实工具调用而 input 收益仍未知，HTTP 429 也不能由工具选择或用量快照推断确切配额根因。

## 9. 成本、质量和高置信收益

当前可确认的是特定候选的输入占用变化。输入减少可以降低资源需求，但缓存折扣、输出与推理比例、请求次数、固定/辅助费用和模型价格不同，不能按 token 百分比推导付费节省。本文没有已验证成本总额或货币 ROI；未知不填零。

高置信的净收益同时需要明确任务分布、路线内配对、全部 attempts、独立重复和有效成本边界。Agent 波动由失败、额外调用、预算耗尽及不同模型策略体现。当前单模板/三个固定模板的证据不满足普遍可靠率或非劣性声明，收益区间下界仍缺失。

旧报告中的 60 例、−2pp、45%/10% 和条件性 LB 保留为历史口径，不强制升级为新的大规模测试设施。实际工程交付围绕具体生产问题；未取得新数据时不创造“最终收益已达标”的数字。

## 10. 脱敏来源、摘要与可追溯性

第 3、4 节由既有私有 `cases.jsonl` 的数值白名单逐请求抽取，重新检查 19 条加总和子集关系，并与既有 `independent-pair-audit.json` 汇总一致。抽取不包含 spec、请求正文、凭据、endpoint 地址和工具输入输出。

| 官方 pair | 原 journal SHA-256 | 独立审计 SHA-256 |
| --- | --- | --- |
| Codex / `0efa8a28` | `3cc8b8524e8008dcca3b1c878e0c1981c7eafb126490c8f001bdc3148aae0094` | `f526516d92f46c53aa8b48095a90a0c5deec59f6e61bb129b1c2363d228507b3` |
| Claude / `e202aaa8` | `e23ab0ee702ae6a5c8f9aa235087ae65d50ec090878da4a7b0614676e00a1473` | `03560a42aa64f3974371234eed4951f4ac547ecd5660ce16962b8f2e9f8cb5c7` |

历史主表的完整 revision 为：

| scope | gateway revision |
| --- | --- |
| T13 / T14 / T15 | `4b428edfa70e98df3780f4850ab1d7129b95ed14` |
| T17 | `ca0758f4943baa97e651545a56c376adcdcb8e3d` |
| T19 / T20 | `58f5523b90d0561ff7812ad8da8257f14acb4ef5` |

候选身份与任务分母见[验证报告](verification-and-compatibility_CN.md)。原始阶段报告固定于[整合前 Git 文档快照](https://github.com/hrygo/CLIProxyAPI/tree/743a5e0ae14d100b2c1c8f2101b93adfa420269e/docs)，原实验工具固定于[实验代码快照](https://github.com/hrygo/CLIProxyAPI/tree/d9e64b8eff81414244703220d44d651d195ee428/ops/tool-loading)。私有原记录未上传；摘要用于核对身份，不替代证据内容真实性检查。

本次仅整合、抽取与校验既有数据，没有新增 Provider 调用、修改原始 journal、重跑失败任务、改变默认配置或重启服务。
