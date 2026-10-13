# 跨 Provider 工具按需加载：当前验收状态

核验日期：2026-10-13（Asia/Shanghai）。结论：**闭环和上下文收益已有有限实测，代表性编码任务的高置信全任务净收益尚未建立，不能宣称完备解决或切换默认配置。** CLIProxyAPI 为实施主体，Codex、Claude Code 为主要客户端；OpenCode 非重点。

用户已明确替换线程目标，现行依据为[全任务净收益分阶段方案](../superpowers/plans/2026-10-13-tool-loading-net-benefit-pilot_CN.md)。60 例为覆盖证据，45%／10% 为历史参考，不再作为所有路由统一通过线；正式实验前锁定任务分布、质量非劣界、最小有意义收益、区间方法与投入上限。下文 G1–G5 是旧冻结规则的历史结果，保留而不追溯改判。

**实施边界纠偏：** 使用官方发行的 Codex、Claude Code 等客户端，通过现有受支持配置与扩展接入；停止客户端源码修补、构建和 fork 维护，不以自建客户端作为交付条件。CLIProxyAPI 是主要生产修复载体，客户端缺口交付兼容限制与责任边界。T25 自建候选只保留诊断证据，不计为官方客户端兼容认证。

## 候选与配置边界

T14/T15 真实 Provider 试验使用候选 `4b428edfa70e98df3780f4850ab1d7129b95ed14`，隔离网关 binary SHA-256 为 `96f4546eae22f39bd07ad7dd8afa457136b44311ce49cd9f28320c08e49485c7`。T17 使用 `ca0758f4943baa97e651545a56c376adcdcb8e3d`，binary SHA-256 为 `f1d0214f3e75808fd457f2d06a2fe9852b05ae4988b9b037aff9bcd73a1fb278`。T19 响应诊断候选为 `58f5523b90d0561ff7812ad8da8257f14acb4ef5`，binary SHA-256 为 `42920710b5c0feda73458abb4bf16e79b57989fdcd8a89b726b8bac59e8e3c9c`。Codex CLI `0.160.0`、Claude CLI `2.1.289`；配置指纹为 `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`。

用户已确认 12:07 的默认配置变化属于其他任务的预期改动。本任务不回退它；原指纹下失效的 10-case 批次保留原始分母、失败与未知值，随后新建冻结基线。确认来源不等于已验证所有配置字段；当前声明只覆盖冻结材料与实际 route。

T14/T15 live 数据属于 `4b428edf`，T17 属于 `ca0758f4`，T19/T20 属于 `58f5523b`，不同执行候选不能合并认证。T20 已完成当前候选的 60-case 数量覆盖，但只有 41 safe，代表性编码和正式统计仍未认证。此前 50% → 45% 调整保留在历史记录；最新目标以预登记的质量与实际净收益界评价，不能用放宽数字追溯升级认证。

T22 按用户纠偏暂停 Docker/gold runner 扩建，优先修 CLIProxyAPI 生产路径。已复现并修复 portable native 路由的发现工具声明改成别名、`tool_choice` 仍保留原名的问题；直接强制选择和 `allowed_tools` 均与激活声明及调用历史对齐，原生搜索选择不被 bridge 化。Go 1.26.4 定向回归、`internal/responsestools`、`sdk/cliproxy/auth`、`test` 三包完整回归及 server build 通过；本轮没有新增真实 Provider 复测，T19/T20 结果不转移为新源码认证。Code Mode 同一 `call_id` 的多输出仍未实现跨协议兼容。

T23 继续交付生产功能：`c16b5526` 新增精确原生路由 `custom-output-replay: multiple`，`832c8fbf` 修复最终发送层仍错误限制单结果的问题，并按准备阶段数量、完整结果摘要及工具事件顺序防止后处理篡改。最终干净候选 `832c8fbf2414b7ccea6e865444716d485b0e4577` 的隔离 SDK binary SHA-256 为 `033d57377c8bcba869242a976a3e097beb62401d9920e0a1c66905a856473ce8`；GPT `gpt-6-luna` 两次合成 custom bridge 探测均 HTTP 200、实际上游合法完成，文本与数组多结果的内容、身份和交错次序全部保留。默认仍为 `single`；Muse 上游明确拒绝一对多结果，不宣称跨协议已解决。新候选只取得这一精确路由的协议证据，没有继承旧批次的任务质量或收益认证。详见[多输出生产修复与实测边界](../compatibility/codex-code-mode-multiple-results_CN.md)。后续优先补生产闭环，验证只服务具体修复，继续暂停 Docker/gold runner 扩建。

T24 用同一生产候选完成一个真实 Codex 通知编码任务：7 次物理请求、5 次实际 MCP 调用，每次调用的通知和最终结果均保留，代码修改及 11 项独立行为测试通过。原 verdict 因旧验收器不识别 `functions.exec` 源身份与多结果而失败；最小修复观测并做独立原始记录复核后，首轮 fixture 签名未注入、仅加载并调用三个 coding 工具、无 schema 前调用，历史一致。保留原失败，不改判或增加成功分母。该受限任务未提供 eager 配对、代表性编码任务或新的净收益认证。

T25 曾扩展到隔离 Codex 客户端源码修补与构建：底层 request owner 的 scoped 回归通过，但完整运行包下两个 Code Mode 原生取消仍无 MCP 取消通知，且尚未排除默认关闭的 `code_mode_interrupt` 条件。该支线收束，客户端问题保留为能力边界，重新聚焦 CLIProxyAPI 生产修复。未替换客户端、改默认配置或重启原服务；没有新增真实 Provider 或上下文收益认证。局部通过、companion 构建失败和原生失败均见[取消缺口记录](../compatibility/codex-mcp-cancellation-gap_CN.md)。

T26 返回网关生产路径，`0efa8a28` 为原生多结果回填增加调用参数摘要，阻止最终发送前参数被改写、删除或变为对象；正常多结果及无参调用保持兼容。HTTP/SSE 各 13 种篡改均本地拒绝、零 mock 上游请求，全仓 Go 测试、vet 与 build 通过。官方 Codex `0.160.0` / `gpt-6-luna` 在隔离候选完成一个 eager/lazy 通知编码对，两臂代码及 11 项测试通过，共 10 请求、6 MCP dispatch；全任务输入点估计减少 68.21%。外层汇总脚本退出 1 的文件写入错误保留，另做独立证据核对，无 Provider 重跑；单固定模板不认证置信收益。远端 push 曾被自动审批拒绝；用户随后明确授权，`809d34ff`、`0efa8a28`、`c093ef73` 已同步到 PR #25，远端 `c093ef73` 的 `build` 于 2026-10-13 20:01（北京时间）通过。见[本轮生产修复与实测](2026-10-13-native-call-input-guard_CN.md)。

T27 继续修网关：`4a747f7f` 修复 Claude→Gemini 的长名/字符替换碰撞和响应大小写/前导下划线串名；`e202aaa8` 将 Claude ToolSearch 激活集合与字节预算接入既有最终发送契约，在三个 executor 入口传递，拦截声明丢失、重命名、重新引入未加载函数、重复声明及最终强制选择未加载函数。五种实际 executor 的 HTTP/SSE 共 80 个本地子案例通过；xAI 既有孤立 choice → 默认 auto 修复保留。全仓 103 个有测试 package、vet/build 通过，8 项既有可选 live/corpus/TLS 测试跳过，新增回归无跳过。官方 Claude Code `2.1.289` 在该候选完成一个固定编码对，两臂代码及 11 项独立测试通过，共 9 请求、6 MCP dispatch；lazy 多一次检索仍取得全任务输入 89.78% 的减少点估计。未形成统计置信度，不继承历史候选认证；原生 hosted 工具和动态权限/取消边界仍需另验。见[T27 修复与实测](2026-10-13-claude-deferred-wire-contract_CN.md)。

T28 的 `42b3d5da` 补上 hosted choice 最终声明校验：直接/allowed_tools 选择不存在或被删除的 hosted 声明时本地 422，带 server_label 的 hosted MCP 选择须匹配声明。HTTP/SSE/WebSocket 共 32 个真实 executor 子案例和 6 个 server identity 子案例通过；已有 operator 图像注入与有效声明继续保留。这不认证 hosted 执行授权或费用。全仓 103 个有测试 package、vet/build 通过，8 项既有可选测试跳过。另用官方 Codex `0.160.0`、显式且被 `features list` 接受的 `code_mode_interrupt=true` 完成四个本机探针：两个成功对照通过，两个取消仍无 MCP 取消/释放、清理前 active=1；0 个真实 Provider 请求、没有自建客户端。限制已归客户端边界，不把开关接受当生产保证，也不将其唯一归因于某个源码断点。没有新增收益认证，见[T28 报告](2026-10-13-hosted-choice-and-official-cancel_CN.md)。

## 已取得的证据

| 层次 | 结果 | 实际可证明的范围 |
| --- | --- | --- |
| 本地协议回归 | Go 1.26.4 全仓 103 个有测试 package 通过；`go vet ./...`、server build 返回 0；T21 验收 Python 更新至 327 tests 通过；响应诊断四个相关 package race 通过 | 回归与构建，不是所有 Provider 功能或真实断流的新增认证。T20/T21 改动验收 Python 与文档，网关执行源码未变，未重复 Go 门禁。新响应诊断版本不得继承 T17 live 认证。 |
| T14 跨 Provider smoke | 两份冻结计划累计 60 started / 60 finished，44 safe successes，210 已记录请求、55 已知 dispatch | 同一候选的数量覆盖；保留两份 plan/journal 边界，不能据此给跨路线可靠率。所有已开始案例均入统计。 |
| T15 受限编码 | 24/24 safe；123 已记录请求、81 已知 dispatch；24/24 有代码修改；host gold 288/288 次测试执行通过 | 三个隔离 Python 函数任务，不代表广泛多文件 repository 编码质量。 |
| T17 Codex 发现策略对照 | 24/24 started/finished，20/24 safe；123 请求、75 dispatch；六个选择性 lazy 全部 safe 且只加载所需三个工具 | 同候选、同 task 的原始/选择性策略校准；有指导的 exact-name discovery，不能当自然语言检索或原生 `tool_search` 认证。四例失败保留。 |
| T19 新响应诊断校准 | 4/4 safe；20 请求、12 dispatch；代码修改与 host gold 44/44 测试执行通过；四份响应回执正常封存 | Codex GPT/Muse 一个 selective budget task 的 E/L；真实 collector 正常路径，不是全部 route 或真实拒绝事件认证。 |
| T20 当前候选跨 Provider 复测 | 60 started / finished，41 safe；161 已记录请求、46 已知 dispatch；52 份响应诊断正常封存；8 例凭据材料不匹配启动失败 | 当前候选数量覆盖；未知计数不补零，实际请求/dispatch 总量及全部预算未知。三个非编码固定模板，仍不是正式任务质量认证。 |
| T21 仓库 fixture 基础 | 四模块 allowlist、真实 MCP read/patch、23 项定向及 327 项完整 Python 回归通过 | 仓库 revision、提交回执、预算与权限边界；未提供模型代码测试执行或隔离，不证明代表性编码质量。见[执行端边界](2026-10-13-repository-fixture-boundaries_CN.md)。 |
| T23 原生多结果生产修复 | 配置与 Prepare 支持精确原生路由逐项重放；最终发送保护允许合法多结果、九种篡改零上游请求；103 包、vet、server build 通过；最终 GPT 两次相邻/交错请求均合法完成 | 合成历史的协议能力；没有真实工具执行、Codex 通知任务或新的上下文收益证据。保留此前两次本地拒绝及启动问题，不与旧候选池化。 |
| T24 真实 Codex 通知回填 | 同一生产候选上真实代码修改、11 项行为测试通过；五次 MCP 调用各保留通知及终态，权限和释放观测为零；独立复核确认按需加载闭环 | 原验收器失败保留，并修复 namespace / 多结果观测缺口；一个受限单函数任务，不能外推可靠率、代表性 repository 质量或全任务净收益。 |
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
| Codex Code Mode 多输出 | `notify` 与终态共享 `call_id`；已交付显式原生 Responses 多结果重放及最终发送保护，GPT 两次合成桥接和一个真实通知编码任务均取得闭环证据，旧验收器失败另保留。Muse 明确要求一个调用一个输出；Chat/Claude 的原有转换不能保真 | GPT 精确原生路由取得有限真实客户端证据，未认证可靠率。其他 Provider/跨协议仍需逐目标支持或新客户端/事件契约；默认有损路径继续 422。见[多输出边界](../compatibility/codex-code-mode-multiple-results_CN.md)。 |
| Codex MCP 取消 | T28 官方 `0.160.0` 显式启用并核实接受 `code_mode_interrupt` 后，两例取消仍只有 ack/interrupted，无 MCP 取消/释放，清理前 active=1；两例成功对照通过，不经过 Gateway | 缺少该显式开关不能解释 T28 的失败；该 feature 为 UnderDevelopment，未认证稳定取消。指定 Code Mode/app-server profile 限用，需客户端官方解决或官方可验证能力；不自建客户端、网关不代发其 MCP request ID。见[取消缺口](../compatibility/codex-mcp-cancellation-gap_CN.md)。 |
| Hosted 工具 | T27 的图像声明来自既有 operator 注入策略；T28 修复强制选择没有最终声明的 hosted 工具，保留合法配置行为 | 冻结图像策略、payload rules 和实际 final wire，分别观察客户端声明/operator 注入/模型选择；MCP 零禁止 dispatch 不认证 hosted 权限、副作用或费用。现有 passthrough 可用于隔离排除自动注入；默认配置未切换。见[T28](2026-10-13-hosted-choice-and-official-cancel_CN.md)。 |
| Gemini / Claude 可用性 | T14 六例记录上游 HTTP 429；同模型 Codex 五例成功，一例额外 confirm 任务质量失败 | 429 来自 Provider 响应边界，但准确配额条件/持续性和 thinking 因果未证实。不能删除失败或把其他客户端成功追溯改判。 |
| Gemini 冻结材料漂移 | T20 八例启动回执均为 `credentials / binding_content_mismatch`，默认配置摘要未变；Claude 两例另记录 12 次上游 429 | 保留旧分母；后续先明确稳定身份与执行材料绑定，不能自动刷新/换账号。未知请求与执行计数不补零。 |
| Gemini 用量 | 原始 scalar counters 确有 thought/total 回退及 prompt 修正；最后 STOP snapshot 加总一致，辅助请求缺计量契约 | 仍缺 Antigravity 最终值权威性与辅助成本契约，输入收益保持未知；不能从内部一致推导账单正确。见[计量边界](2026-10-13-gemini-usage-boundary_CN.md)。 |
| 响应失败诊断 | T17 Muse 返回空 custom bridge 参数 `{}`，网关执行前拒绝，原 harness 记 framework | [独立响应诊断](../compatibility/responses-tool-failure-diagnostics_CN.md)已实现、本地回归并完成 T19 正常路径 live 校准；T19 没有再次触发非法参数，真实拒绝回执与 WS 诊断仍未实测。旧 verdict 不重写。 |
| 模型/任务质量 | 部分 GLM、Qwen、Muse 案例缺中间回执重放、发现后加载或规定最终格式，另有未请求 confirm | 客户端与任务策略按固定 oracle 做新版本对照；不能用网关伪造工具执行或回执。 |
| 正式统计与代表性编码 | 固定模板、单函数任务和未知成本仍不足 | 先锁定代表性任务族、exact case fingerprints 与 family/cluster 设计、路由/版本、冷热缓存和费用上限，再扩大测试。不能删除 fingerprint 来强行池化。 |

在零观察回归且独立代表性簇假设成立的理想情况下，单组至少 149 簇才能使条件性单侧 95% 失败概率上界低于 2%；这是样本下限示例，不是存在回归时的完整 power 设计。十个 client/route 单元各做 149 簇 × 两模式为 2,980 cases；按每 case 八请求上限投影为 23,840 请求，远大于本轮 pilot，真实费用仍未知。重复同一模板或改随机参数不能冒充独立代表性簇。大规模正式试验须先形成可评审的采样、预算和停止条件，不能直接按该投影启动。

[原正式验收设计](../superpowers/plans/2026-10-13-tool-loading-formal-acceptance-design_CN.md)保留冻结流程与历史方法；[任务族登记分析](../../ops/tool-loading/task_registry.py)已实现并覆盖 16 项回归，保留完整 CaseSpec 摘要，拒绝事后登记、伪拆分和材料漂移。T21 已交付 repository read/patch 基础，gold/隔离测试执行、自然语言任务集、独立抽样、live runner 集成及新统计设计仍未完成。后续按[现行分阶段方案](../superpowers/plans/2026-10-13-tool-loading-net-benefit-pilot_CN.md)投入，未启动大规模试验；T20 不因新模块获得事后认证。

## 旧冻结门槛的历史状态

- **G1：部分完成。** 本地回归通过，多输出桥接能力仍有明确缺口；当前候选已有十个 client/route 单元的 60 例有限实测，但部分失败，真实响应拒绝和新增 WS 诊断待验证。
- **G2：未闭合。** T20 的 52 例可观测 forbidden/effect/active 为零，8 例未知；Codex 请求级取消和完整权限生命周期尚未认证。
- **G3：当前候选数量覆盖已达 60，正式认证未完成。** 41/60 safe，失败、预算与材料漂移保留；编码仍是受限 pilot，代表性 repository 任务未完成。
- **G4：未通过。** 无满足 −2pp 的代表性、成对、按路线质量置信证据。
- **G5：未通过。** 现行 45% 点估计门槛不再是 GPT 本批的阻碍；代表性收益下界仍缺失，Gemini 用量未知，账单尚无证据。

门槛来自[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。本轮没有发布、默认配置切换、客户端升级或原服务重启。所有长脚本均异步运行，原始失败、重试、预算和未知值保留。
