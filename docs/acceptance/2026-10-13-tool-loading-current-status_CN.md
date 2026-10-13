# 跨 Provider 工具按需加载：当前验收状态

核验日期：2026-10-13（Asia/Shanghai）。结论：**闭环和上下文收益已有有限实测，G1–G5 尚未全部通过，不能宣称完备解决或切换默认配置。** CLIProxyAPI 为实施主体，Codex、Claude Code 为主要客户端；OpenCode 仅作历史对照。

## 候选与配置边界

T14/T15 真实 Provider 试验使用候选 `4b428edfa70e98df3780f4850ab1d7129b95ed14`，隔离网关 binary SHA-256 为 `96f4546eae22f39bd07ad7dd8afa457136b44311ce49cd9f28320c08e49485c7`。T17 使用 `ca0758f4943baa97e651545a56c376adcdcb8e3d`，binary SHA-256 为 `f1d0214f3e75808fd457f2d06a2fe9852b05ae4988b9b037aff9bcd73a1fb278`。最新 T19 响应诊断候选为 `58f5523b90d0561ff7812ad8da8257f14acb4ef5`，binary SHA-256 为 `42920710b5c0feda73458abb4bf16e79b57989fdcd8a89b726b8bac59e8e3c9c`。Codex CLI `0.160.0`、Claude CLI `2.1.289`；配置指纹为 `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`。

用户已确认 12:07 的默认配置变化属于其他任务的预期改动。本任务不回退它；原指纹下失效的 10-case 批次保留原始分母、失败与未知值，随后新建冻结基线。确认来源不等于已验证所有配置字段；当前声明只覆盖冻结材料与实际 route。

T14/T15 live 数据属于 `4b428edf`，T17 属于 `ca0758f4`，T19/T20 属于 `58f5523b`，不同执行候选不能合并认证。T20 已完成当前候选的 60-case 数量覆盖，但只有 41 safe，代表性编码和正式统计仍未认证。用户于 2026-10-13 将首轮/峰值输入降幅门槛改为 45%；历史 50% 判定保留，新门槛不免除置信证据和正式测试前锁定要求。

## 已取得的证据

| 层次 | 结果 | 实际可证明的范围 |
| --- | --- | --- |
| 本地协议回归 | Go 1.26.4 全仓 103 个有测试 package 通过；`go vet ./...`、server build 返回 0；验收 Python 更新至 304 tests 通过；响应诊断四个相关 package race 通过 | 回归与构建，不是所有 Provider 功能或真实断流的新增认证。T20 仅新增 Python 登记分析与文档，网关执行源码未变，未重复 Go 门禁。新响应诊断版本不得继承 T17 live 认证。 |
| T14 跨 Provider smoke | 两份冻结计划累计 60 started / 60 finished，44 safe successes，210 已记录请求、55 已知 dispatch | 同一候选的数量覆盖；保留两份 plan/journal 边界，不能据此给跨路线可靠率。所有已开始案例均入统计。 |
| T15 受限编码 | 24/24 safe；123 已记录请求、81 已知 dispatch；24/24 有代码修改；host gold 288/288 次测试执行通过 | 三个隔离 Python 函数任务，不代表广泛多文件 repository 编码质量。 |
| T17 Codex 发现策略对照 | 24/24 started/finished，20/24 safe；123 请求、75 dispatch；六个选择性 lazy 全部 safe 且只加载所需三个工具 | 同候选、同 task 的原始/选择性策略校准；有指导的 exact-name discovery，不能当自然语言检索或原生 `tool_search` 认证。四例失败保留。 |
| T19 新响应诊断校准 | 4/4 safe；20 请求、12 dispatch；代码修改与 host gold 44/44 测试执行通过；四份响应回执正常封存 | Codex GPT/Muse 一个 selective budget task 的 E/L；真实 collector 正常路径，不是全部 route 或真实拒绝事件认证。 |
| T20 当前候选跨 Provider 复测 | 60 started / finished，41 safe；161 已记录请求、46 已知 dispatch；52 份响应诊断正常封存；8 例凭据材料不匹配启动失败 | 当前候选数量覆盖；未知计数不补零，实际请求/dispatch 总量及全部预算未知。三个非编码固定模板，仍不是正式任务质量认证。 |
| 可观测安全与释放 | T14/T15/T17/T19 各 case 记录的 forbidden dispatch、forbidden effect、最终 active 均为 0；进程退出回执均为 0 | 仅对应各批观测；不能覆盖独立取消缺口，也不能用退出码替代任务成功。 |

详细数据：[T14 覆盖审计](2026-10-13-primary-60-coverage_CN.md)、[T15 编码试验](2026-10-13-primary-coding-pilot_CN.md)、[T17 选择性发现](2026-10-13-codex-selective-discovery_CN.md)、[T19 响应诊断校准](2026-10-13-response-diagnostics-calibration_CN.md)、[T20 六十例复测](2026-10-13-final-candidate-60-retest_CN.md)、[新基线及失效批次](2026-10-13-primary-routes-refreeze_CN.md)、[本地诊断回归回执](../../.omo/tool-loading-completion/t16-diagnostic-verification.json)。

## 上下文、质量与成本

T14 前八组的三个固定模板输入减少率中位数：首轮 67.12%–95.04%，峰值 49.08%–90.39%，全任务 21.31%–88.11%。Gemini 两组输入用量均未知，不参与这些范围，也不记零。T15 每个 route/task 分别只有一对 E/L；全任务减少 14.46%–93.48%，Codex/GPT 三个任务峰值减少 47.38%–48.91%。历史报告据 50% 门槛判其点估计不足；按现行 45% 数值门槛达到，但置信收益仍未认证。

T17 同版本 GPT 三任务的峰值降幅中位数原始 48.38% → 选择性 64.88%，全任务降幅中位数 40.84% → 65.91%。选择性 Muse 两个已知配对峰值均约 66.55%，另一 eager 失败而用量未知，不补成完整中位数。选择性 lazy 六例通过；整批四个失败与其实际输入、请求和预算仍计入。

T19 各 route 只有一个固定模板配对：GPT first/peak/whole 降幅 67.10% / 64.62% / 65.75%；Muse 为 69.38% / 66.49% / 67.83%。数值达到 45%/10% 门槛，但代表性与收益下界仍未满足，不能合并旧候选样本提高置信度。

T20 非 Gemini 八条 client/route 的首轮中位降幅 67.13%–95.05%，峰值 49.35%–90.44%；全任务 −5.02%–88.04%，Codex GLM 的负收益保留。Claude GPT/Muse/GLM/Qwen 各 6/6 safe；Codex GPT 6/6、Muse 5/6、GLM 1/6、Qwen 3/6；Gemini Codex 2/6、Claude 0/6。Gemini 用量仍未知。三个固定模板的质量置信下界不足，收益置信下界均缺失。

收益因任务变化，不能保证 lazy 总是更省：此前新基线 Codex/Muse lookup 配对全任务输入增加 16.37%，请求从 2 增至 6；该失败收益样本仍保留。按需加载已减少首轮工具目录注入，但发现、执行轮次与累计历史可以抵消收益。

固定模板不足以认证 −2pp 质量非劣。T14 每组仅三簇，T15 各 scope 仅一簇；收益置信下界缺失。输入包含 cache-read，不重复计数；cache-write 缺字段仍为未知。elapsed 包含准备与清理，不等于 TTFT；缓存冷热条件、region、货币费用和账单未知，不能从 mixed raw tokens 推导费用节省。

GPT lazy 峰值已进一步归因：`functions.exec` 使用 `ALL_TOOLS.filter` 的宽泛正则命中全部 19 个 fixture 工具，将 41,074 字节的发现输出重复带入历史，而实际调用只有三个 coding 工具；它不是 `tool_search` Top-K 响应。T17 已验证临时任务策略的选择性 metadata 输出，将加载工具限制到所需三个；没有写入默认客户端策略，也没有由网关丢弃真实结果。字节归因和 Provider token 计量分别报告，详见 [T15 增补](2026-10-13-primary-coding-pilot_CN.md)与 [T17 对照](2026-10-13-codex-selective-discovery_CN.md)。

## 剩余问题与责任

| 问题 | 已确认事实 | 下一步责任与交付 |
| --- | --- | --- |
| Codex Code Mode 多输出 | `notify` 与终态共享 `call_id`；现有 Chat/Claude 适配会降级、丢弃或折叠额外结果。本轮改成 availability-neutral、request-scoped `422 unsupported_protocol`，仍拒绝有损转换 | Gateway 已交付准确诊断；完整支持须逐 Provider 保真转发，或另订客户端/事件封套契约。见[多输出边界](../compatibility/codex-code-mode-multiple-results_CN.md)。原生本地透传不代表 Provider 接受。 |
| Codex MCP 取消 | 两例本机 app-server interrupt 有 ack 与 interrupted 终态，但清理前无取消通知/释放，最后观测 active=1；不经过 Gateway | Codex 客户端单独交付 request token → 实际 MCP request handle 取消，再做编译和实测；网关不能代发其本机 request ID。见[取消缺口](../compatibility/codex-mcp-cancellation-gap_CN.md)。 |
| Gemini / Claude 可用性 | T14 六例记录上游 HTTP 429；同模型 Codex 五例成功，一例额外 confirm 任务质量失败 | 429 来自 Provider 响应边界，但准确配额条件/持续性和 thinking 因果未证实。不能删除失败或把其他客户端成功追溯改判。 |
| Gemini 冻结材料漂移 | T20 八例启动回执均为 `credentials / binding_content_mismatch`，默认配置摘要未变；Claude 两例另记录 12 次上游 429 | 保留旧分母；后续先明确稳定身份与执行材料绑定，不能自动刷新/换账号。未知请求与执行计数不补零。 |
| Gemini 用量 | 原始 scalar counters 确有 thought/total 回退及 prompt 修正；最后 STOP snapshot 加总一致，辅助请求缺计量契约 | 仍缺 Antigravity 最终值权威性与辅助成本契约，输入收益保持未知；不能从内部一致推导账单正确。见[计量边界](2026-10-13-gemini-usage-boundary_CN.md)。 |
| 响应失败诊断 | T17 Muse 返回空 custom bridge 参数 `{}`，网关执行前拒绝，原 harness 记 framework | [独立响应诊断](../compatibility/responses-tool-failure-diagnostics_CN.md)已实现、本地回归并完成 T19 正常路径 live 校准；T19 没有再次触发非法参数，真实拒绝回执与 WS 诊断仍未实测。旧 verdict 不重写。 |
| 模型/任务质量 | 部分 GLM、Qwen、Muse 案例缺中间回执重放、发现后加载或规定最终格式，另有未请求 confirm | 客户端与任务策略按固定 oracle 做新版本对照；不能用网关伪造工具执行或回执。 |
| 正式统计与代表性编码 | 固定模板、单函数任务和未知成本仍不足 | 先锁定代表性任务族、exact case fingerprints 与 family/cluster 设计、路由/版本、冷热缓存和费用上限，再扩大测试。不能删除 fingerprint 来强行池化。 |

在零观察回归且独立代表性簇假设成立的理想情况下，单组至少 149 簇才能使条件性单侧 95% 失败概率上界低于 2%；这是样本下限示例，不是存在回归时的完整 power 设计。十个 client/route 单元各做 149 簇 × 两模式为 2,980 cases；按每 case 八请求上限投影为 23,840 请求，远大于本轮 pilot，真实费用仍未知。重复同一模板或改随机参数不能冒充独立代表性簇。大规模正式试验须先形成可评审的采样、预算和停止条件，不能直接按该投影启动。

[正式验收设计](../superpowers/plans/2026-10-13-tool-loading-formal-acceptance-design_CN.md)已落盘；[任务族登记分析](../../ops/tool-loading/task_registry.py)已实现并覆盖 16 项回归，保留完整 CaseSpec 摘要，拒绝事后登记、伪拆分和材料漂移。该模块尚未自动接入 live runner；repository fixture、自然语言任务集、独立抽样与预算设计仍待交付，未启动大规模试验。T20 不因新模块而获得事后认证。

## 门槛状态

- **G1：部分完成。** 本地回归通过，多输出桥接能力仍有明确缺口；当前候选已有十个 client/route 单元的 60 例有限实测，但部分失败，真实响应拒绝和新增 WS 诊断待验证。
- **G2：未闭合。** T20 的 52 例可观测 forbidden/effect/active 为零，8 例未知；Codex 请求级取消和完整权限生命周期尚未认证。
- **G3：当前候选数量覆盖已达 60，正式认证未完成。** 41/60 safe，失败、预算与材料漂移保留；编码仍是受限 pilot，代表性 repository 任务未完成。
- **G4：未通过。** 无满足 −2pp 的代表性、成对、按路线质量置信证据。
- **G5：未通过。** 现行 45% 点估计门槛不再是 GPT 本批的阻碍；代表性收益下界仍缺失，Gemini 用量未知，账单尚无证据。

门槛来自[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。本轮没有发布、默认配置切换、客户端升级或原服务重启。所有长脚本均异步运行，原始失败、重试、预算和未知值保留。
