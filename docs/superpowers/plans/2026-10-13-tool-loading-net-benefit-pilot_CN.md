# 工具按需加载全任务净收益：分阶段有界验收方案

> **状态：目标已生效，试验细节待冻结。** 用户于 2026-10-13 明确替换线程目标；本文描述分阶段实验设计，不是 Provider 请求、发布、默认配置切换或服务重启的执行记录。Stage 0 通过、Stage 1 上限及 Stage 2 统计参数冻结前，不启动大批量运行。

> **For agentic workers:** 本文是验收试验协议。评审后按阶段执行；Stage 0 和冻结包未通过前，不发送 Provider 请求或扩展样本。

**当前目标：** 以 CLIProxyAPI 为主要实施载体，重点联动官方发行的 Codex、Claude Code 与 MCP 执行端，OpenCode 非重点。在明确版本、模型和路由范围内，将工具按需加载建设为可复用、可诊断的工程能力；通过代表性编码任务的成对实验，验证其在保持任务质量、权限和协议语义的前提下，能否带来具有统计可信度和实际价值的全任务净收益。网关负责适配与诊断，客户端通过现有受支持能力负责检索、授权和执行；客户端缺口交付兼容报告与责任边界。

**2026-10-13 实施边界纠偏：** 用户明确实际使用不接受自建 Codex CLI、Claude Code 等客户端。停止客户端源码修补、编译及 fork 维护；不将其作为验收前置或生产解决条件。使用官方发行客户端的现有配置、能力开关和扩展，隔离验证仍不授权修改默认配置。无法在这一边界内实现的客户端语义，列为限用、不支持、未知或外部上游依赖，不在本任务交付客户端补丁，也不全局阻塞其他可用路由。T25 自建候选仅保留诊断证据，不计为官方客户端兼容认证；优先交付 CLIProxyAPI 生产修复，验证服务于具体修复。

工程契约严格验证，质量依据实际代码、独立测试和合法终态判定；允许 Agent 随机失败，保留全部失败、重试和预算耗尽。主收益纳入检索、历史增长与失败代价，首轮/峰值只作机制诊断。正式实验前冻结任务族、抽样、质量非劣界和最小有意义收益，以单侧 95% 区间评价，不将模板重复当独立样本。分阶段设置预算与停止规则，按收益成立、限用、不适用或证据不足交付，未知计量不填零。60 例作历史覆盖，45%／10% 作历史参考；不发布、不切默认配置、不重启服务。

**方法：** 先验证隔离 MCP 编码执行端，再做最多 16 次 case 的校准；校准只用于发现预算、任务难度、波动和计量问题，不宣称可靠率。之后基于目标任务分布冻结任务族、质量目标、最小有意义收益、区间方法和总预算，再决定是否启动正式试验。

**涉及组件：** CLIProxyAPI 候选二进制、Codex CLI、Claude Code、MCP fixture 与受限 repository runner、`ops/tool-loading` 冻结及成对分析工具。OpenCode 为次要覆盖。

**依据：** [原始跨 Provider 方案](2026-10-12-cross-provider-tool-loading-completion_CN.md)、[正式验收冻结设计](2026-10-13-tool-loading-formal-acceptance-design_CN.md)、[T20 六十例复测](../../acceptance/2026-10-13-final-candidate-60-retest_CN.md)、[任务族登记说明](../../../ops/tool-loading/README.md#preregistered-task-clusters)。

## 全局约束

- CLIProxyAPI 为实施主体，Codex CLI 与 Claude Code 为重点，OpenCode 为次要覆盖；只对锁定的 client、model、protocol 和 route 作结论。
- 客户端使用官方发行制品，不修改、编译或维护客户端 fork；旧方案中的“客户端缺口单独交付”指缺口报告及责任边界，不代表本任务实现客户端源码。
- 保留全部已开始 case、失败、重试、预算耗尽和未知计量；不删除不利样本，不跨候选 revision 拼接正向结论。
- 未核实的账单、缓存、身份或输入计量保持未知；禁止补零、自动换账号、刷新 OAuth 或扩大权限。
- 不修改默认配置，不重启原服务，不发布；候选只在隔离环境中运行。
- Python harness 按项目当前声明的依赖环境执行；若需要 Go 门禁，固定使用 Go `1.26.4`。本方案不要求以 Go 构建替代端到端证据。

## 审查重点

- Provider 或凭据材料漂移：在发送请求前 fail closed；已开始 case 不删分母。
- invalid/forbidden tool calls：在 dispatch 前拒绝，MCP dispatch/effect 必须为零。
- cancellation 与资源释放：真实取消后确认工作进程和活动资源归零，不能用 runner 自清理冒充。
- repeated task 与任务族变体：只在 cluster 内聚合，不能把重复或同根因任务算作独立样本。
- Cache/计费缺失与中途看数：各自标记 unknown；固定样本不因首次好看的区间提前停止。

---

## 适用目标与历史门槛

本方案执行新的目标：证明工具按需加载在代表性完整任务中带来统计可信且有实际价值的净收益，并守住质量、安全和协议底线。旧方案中的“60 例”“每路质量下界 −2pp”“首轮/峰值 45%”“全任务 10%”不再是当前统一通过线；其中 60 与 45%／10% 保留为历史覆盖和效果量参考，不能据此替代新的任务分布、质量非劣界或实用收益界。

T20 的历史判定原样保留：它在旧门槛下完成 60 个 planned/started/finished case，41 个 safe、19 个非成功；G1 部分完成、G2 未闭合、G3 只达到数量覆盖而没有代表性编码、G4/G5 未通过。新目标不把这些结论改判为通过，也不把旧模板补登记为新实验的独立样本。

运行仍限定在预先锁定的客户端版本、模型、协议模式、Provider route、Gateway 二进制、MCP catalog、权限和 fixture。不同 route 逐一报告；Codex Code Mode、Codex 原生 `tool_search` 与 Claude ToolSearch 不互相代替。未知身份、usage、cache 或费用分别标出，不补零。已开始任务的失败、重试、预算耗尽和缺失观测保留在对应分母。不得修改默认配置、重启原服务或发布。

## 目前证据与设计起点

T20 使用 CLIProxyAPI 候选 `58f5523b90d0561ff7812ad8da8257f14acb4ef5`，Codex CLI `0.160.0`、Claude CLI `2.1.289`，五个声明模型 route 和三个固定非编码任务。它确认部分 route 的输入点估计较大，但每行只有三个固定模板；任务族代表性和独立性未证实，收益下界缺失，不能据此推导完整任务净收益或质量可靠率。

全批次记录为 60 started、60 finished、41 safe、19 非成功。161 次请求可见，8 个启动失败案例的请求/执行计数未知；已知 dispatch 46，只有 52 例有可观测的零 forbidden dispatch/effect/active，另 8 例未知。52 份响应诊断封存，8 例没有该回执。Codex GLM 的全任务输入减少为 −5.02%；Gemini 的输入收益未知；Claude Gemini 遇到 HTTP 429。报告中的其他正向 route 数值是三模板条件下的描述性结果，不是当前目标下的置信结论。

四模块 synthetic ledger fixture 已交付读取和带 revision 的原子 patch；**23 项 repository 定向及 327 项完整 Python 回归通过**，见[执行端边界报告](../../acceptance/2026-10-13-repository-fixture-boundaries_CN.md)。它提供 `repository_commit_uncertain`、`repository_busy`、权限复核和审计失败时的 `evidence_complete=false`，仍没有 `repository_tests_run`。测试证明读写与回执边界，不证明任意代码隔离、实际客户端取消或代表性编码质量。Golden test runner、隔离执行、取消释放和登记 runner 仍须在 Stage 0 按实际使用范围验证后，才能进入 Stage 1。

## 阶段与决策门

| 阶段 | 工作与上限 | 通过后可作出的结论 | 不可声称 |
| --- | --- | --- | --- |
| 0. 离线执行端门槛 | 仅本地 MCP / fixture，零 Provider 请求；执行端安全、gold 隔离、权限和生命周期检查 | 受限执行端能否进入校准 | 跨 Provider 模型成功率、token/cost 收益 |
| 1. 校准 | 最多 16 个 case（8 个 E/L pair），硬预算冻结；所有结果保留 | 任务是否可执行、观测是否完整、单任务资源/时间范围与可见波动 | 可靠率、任务族置信区间、正式质量非劣、普遍适用或实用 ROI |
| 2. 设计冻结 | 不调用 Provider；用 Stage 1 和目标任务定义冻结 estimand、抽样、方法与资源上限 | 是否有可审查的正式试验设计 | Stage 2 执行授权或预先宣称通过 |
| 3. 有界正式试验 | 仅按冻结 registry / plan 执行；达到预先停止条件即结束 | 指定任务分布和 route 下的条件性结论 | 对未测 route、客户端版本或真实流量的外推 |

任何阶段出现权限绕过、禁止工具有 dispatch/effect、越界写入、gold 可被模型修改、真实断流被伪造成完成或资源未释放，立即停止该 route，保留证据。候选、fixture、权限或身份材料发生变化时，关闭本轮并以新 plan 重新冻结；不得覆盖历史结果。

## Stage 0：离线执行端门槛

Stage 0 的目的，是先验证后续编码任务不会把模型任意代码或 shell 命令带入宿主环境。不得调用外部 Provider；只使用临时目录和本地 MCP client。

1. **确认受限仓库边界。** baseline、gold 和执行日志由 host 管理；Agent 可见的文件与工具严格 allowlist。读操作返回树及文件 revision；patch 必须携带当前树 revision 和每个文件 revision，并原子提交。陈旧 revision、重复路径、越界路径、symlink/hardlink、目录替换、内容漂移和无效 UTF-8 均拒绝，且 baseline 与 gold 不变。
2. **提供 host-owned 测试入口。** 只允许调用固定测试集合，参数绑定任务 ID 和实际 tree revision；gold 与 runner 在 Agent 不可写位置。不得暴露任意 shell、任意可执行文件或用户可改写的命令。缺少这一入口时，repository patch 只能证明写入边界，不能证明编码任务结果。
3. **在 MCP 层验证授权和调用。** 无权限、无效参数、权限撤销、结果预算不足和未知工具在执行前拒绝；记录请求、dispatch、副作用、身份、revision 与活动资源计数。每种拒绝均要求零副作用；权限变更须在后续 dispatch 前生效。
4. **验证取消与资源释放。** 实际 MCP client 发起调用、取消正在运行的受控工作，再确认取消事件、工作进程终止、连接关闭和活动 callback/资源归零；不能以任务进程退出或人工强杀代替组件自己的取消与释放证据。
5. **本地协议与声明回归。** 发现清单中的身份和 schema 必须与后续可调用声明一致；按已选校准 route 实际使用的协议范围，检查多轮加载、预算拒绝、重试、合法终态、流事件及断流 fail-closed。只有选定 route 实际使用 WebSocket 时才把 WS 加入本阶段门槛；不使用 WS 的 route 不因 WS 诊断未知而被全局阻塞，WS 能力保持未验证。普通成功路径不能代替流中断、取消和权限撤销测试。

交付物是私有离线 plan/journal、gold 与 baseline 摘要、工具声明、权限事件、dispatch/effect/active 计数、取消/释放回执及定向测试日志。所有计数可复核，目录和宿主配置无额外变更，才可进入 Stage 1。使用当前 `repository_project.py` 原型时，先确认它和当前未提交测试实际通过；不能从文件存在推断测试通过。

## Stage 1：最多 16 case 校准

“case”指一臂的一次完整 Agent 任务；一个 eager/lazy 对为两个 case。上限是 16 个 case、8 个成对重复，不包含 Stage 0 的本地测试。不得因试验中结果不显著或信号变好而加样本。

一个可行的初始分配为两条精确 route × 两个任务提示 × 每提示两个新会话重复 × eager/lazy 两臂，共 16 case：

- Codex CLI `0.160.0` / `gpt-6-luna` / T20 使用的 Code Mode；
- Claude CLI `2.1.289` / `glm-5.3-flash` / T20 使用的 structured-output profile。

上述版本和 route 只是 T20 的校准候选，不是当前运行时已核验值。调用前仍须只读核对执行文件、版本、模型 alias → upstream model / format / endpoint 绑定和同名凭据材料摘要；身份不能核实则不发送请求，不换账号、不刷新凭据。若这两条 route 已不可用，须在 plan 锁定前另选等量候选并记录理由，不能运行后换路由。

两个任务提示应从四模块 ledger fixture 形成两个可复核的编码切片，例如 API 状态/错误映射与输入验证/账目变换。它们用于了解多文件 patch、host gold 和工具交互的可执行性，可能共享 baseline 和根因；只作为校准任务，不当作两个独立任务族，也不外推至真实 CLIProxyAPI 开发工作。提示描述用户意图，不点名工具。

每个“route × 任务提示”执行两次独立配对；同一配对的 eager 与 lazy 从相同 baseline、新会话和权限开始，分别使用隔离 workspace，顺序在两次重复中反转。两次重复用于看单任务波动与执行开销，统计时仍属于同一校准任务，不增加可靠率样本数。每次任务都执行 host-owned gold；不能因某一臂失败而删去对应 pair。

**建议校准硬上限：** 沿用 T20 的每 case 90 秒、最多 8 次物理请求、8 次 dispatch，并将总上限锁为 16 cases、128 次请求和 128 次 dispatch。启动前须确认并锁定实际可执行的 request、dispatch、time 及 token/input admission 上限（仅限当前机制能强制执行的项），以及 Provider 可用额度/配额边界；某项 admission 尚未实现时，明确标为未实现/不可执行，不得把它写成已生效的硬上限。以上是资源停止上限，不是预期用量或费用估计。对于仅主张输入资源/上下文收益的校准，若这些非货币上限可执行，账单金额未知不构成机械阻塞；账单保持 unknown，不声称货币节省。若要主张成本/ROI，或存在按金额计费且需由金额封顶的边界，则须另行核实费率依据与可执行的货币费用上限后再运行。若代码任务无法在已锁定的可执行预算内完成，记录预算失败并停止校准；不能运行中临时加时、加请求或刷新/切换身份。

校准需同时记录完整任务输入、首轮/峰值输入、请求与 dispatch 尝试、gold 结果、失败及重试、每阶段耗时、provider 报告的 cache-read/write、已核实账单（若有）、cancel/release 事件及未知字段。缓存状态若无法控制，标为 observed/unknown，随机化 E/L 次序并报告实际缓存计数，不称 cold 或 warm。输入 usage 已知而 billing 未知时，仍可报告输入；它不能转写为账单节省。

**校准停止规则：** 达到 16 cases、128 请求/dispatch 中任一预算，或单 case 预算即停止，不续跑。出现 Stage 0 安全违规立即停止 route。发生配置/route/凭据摘要漂移时，未启动 case 不发送；已启动者保留原结果并按未知计数处理。HTTP 429、model failure、lazy/eager gold failure、取消失败和缺失 usage 全部原样入账。Stage 1 不做基于效果的中途停止或 95% 区间 pass/fail。

Stage 1 只回答任务/预算是否适合正式研究、计数是否闭合、usage 口径是否可信、不同任务如何波动，以及是否值得投入下一阶段。用它来挑最终任务族或定阈值后，校准 case 不并入 Stage 3 的 confirmatory interval，防止样本选择与分析共用同一结果。

## Stage 2：正式设计必须冻结的内容

Stage 2 先形成可审查的冻结包；任何必填项未决定，Stage 3 不启动。

1. **目标任务分布与任务族。** 指定适用人群与工作类型，建立候选任务 frame，覆盖多文件编码、解析/数据变换、API 行为、错误/重试、取消/清理及自然语言工具发现。先定义独立任务簇：同一基线、同一缺陷根因、措辞/数字变体和同一任务的重复均不得拆成多个独立簇。用可复核的随机/分层抽样选题；依据真实 workload 频率设定或明确等权 benchmark mix。若只有人工挑选任务，就只能对该固定集合做条件性结论，不能声称代表真实用户任务。
2. **路线和协议矩阵。** 分别锁定 Codex、Claude Code 和所需 Provider route；OpenCode 非重点单独标记。记录 client binary/version、模型 alias 与上游 model、协议模式、endpoint、Gateway binary digest/revision、MCP server/catalog/tool schemas、权限策略 generation、fixture/gold digest、opaque credential binding、每臂预算和实际身份。Gemini 只有在材料及计量来源能验证后才进入；不以换账号或协议绕过失败。
3. **主要质量指标及非劣界。** 预先选定质量指标。推荐 coding 主指标为每个 started task 是否通过全部 host-owned acceptance tests、实际完成指定工具调用并以合法终态结束；辅以盲评 rubric/回归测试。定义明确分母与 failure attribution。质量非劣界由任务影响和可接受失败增加决定，不能自动沿用旧 −2pp。若不能说明 margin 的业务含义，不启动正式非劣结论。
4. **最小有意义净收益（MIE，minimum important effect）。** 预先选择主要资源指标及实际意义单位：全任务累计 provider input、权威账单成本，或二者各自为主要/次要指标。累加 discovery、tool result、final continuation、重试与失败已消耗的所有请求；不可只比较首轮或只取成功案例。对同一 route 比较配对 E/L，每个 started task 作为分母。设置 route/工作负载适用的 MIE 及估算理由。输入 token 节省代表上下文/输入资源，不自动等于成本节省；未知费率或 billing 时不作货币 ROI 结论。
5. **区间估计与重复层级。** 冻结 route-specific paired estimand、置信水平、区间算法及多路线/多指标的错误率控制。质量需直接估计 lazy−eager 的双向成功差，展示 eager-only、lazy-only 及两边相同的配对数；收益需在 task cluster 层聚合 repeats，并按目标任务权重估计全任务净资源收益。可比较的候选包括保留 E/L 配对及 family 权重的 task-cluster bootstrap，以及与任务抽样和主要质量指标对应的配对风险差区间；应检验小样本和不平衡失败下的覆盖，不得按 Stage 3 哪种方法结果最好再选方法。分析必须保留失败、重试、预算耗尽、缺失 usage 和计划未开始项；方法应在看 Stage 3 结果前锁定。
6. **总预算与停止规则。** 冻结最大 task clusters、每簇 repeats、总 cases、物理请求、dispatch、可执行的每臂 token/input admission、总时长、并发及 Provider 可用额度/配额边界；未实现的 admission 项须显式标记，不能声称可强制执行。若研究范围主张金额成本/ROI，或有按金额计费且需费用封顶的边界，再冻结经核实费率依据和货币费用上限；billing 未知时只能报告 unknown，不声称货币收益。固定样本设计可在预定样本末只分析一次；若要中期效益/无效检查，预先采用 alpha-spending 或 anytime-valid confidence sequence，不能每次反复查看固定 95% 区间并在首次过线时停止。预定安全停止与资源停止不删已开始分母。达到预算后未满足 MIE 或主要质量指标界，则结论为未建立或负面，不临时扩样。

冻结包需由 `task_registry.py`、`batch_plan.py` 和 `live_matrix.py` 在第一 case 前共同校验。当前登记模块是 opt-in offline analyzer：它绑定 CaseSpec、sampling-frame digest 和任务族，但不证明抽样代表性、不提供 task-frequency weighting、不接入 live runner，且当前质量分析使用“簇内任一 eager 成功/lazy 失败”这一保守界。正式方案应先扩展冻结 schema，记录主要质量指标、margin、MIE、权重、区间方法、序贯规则与完整 resource budget，再实现 runner 校验和分析器；不能将旧 `quality_margin/first_input_reduction/peak_input_reduction/total_input_reduction` 字段静默解释成新标准。

正式结论采用约束式判定，而非把 token 与质量任意折成单一分数：硬安全/协议门槛先通过；质量差的预注册下界高于 −δ_Q；全任务资源收益的预注册下界超过 MIE_B。δ_Q 与 MIE_B 目前都未确定。输入 usage 下界通过只能支持上下文/模型输入效率结论；只有可核对的全任务账单也通过，才称为货币成本收益。任一边界未过就分别报告结果，不宣称整体净收益。

正式样本数按 Stage 1 的任务级方差、目标任务分布、δ_Q / MIE_B、预设置信/错误率控制与目标精度或功效推导，并设置一个不可临时突破的总上限。Stage 1 的 16 case 不足以证明多任务族的独立抽样，也不能单独给出可靠的区间方差；若无法从可辩护的任务 frame 与外部先验/历史任务获得所需参数，应先扩展离线基准建设或保持“证据不足”，而不是从两个提示外推。

## 统计解释：避免把保守界和真实目标混为一谈

现有质量分析以簇内是否出现 eager-success/lazy-failure 定义“回归簇”，计算其概率的 exact upper bound，再取负值作质量下界，并明确忽略 lazy 的改善。独立、同分布、代表性任务簇等假设成立时，这可作为足够保守的质量界；它不是直接的双向平均成功差区间。存在改善时仍被忽略，重复次数越多，簇中观察到“至少一次回归”的机会越大。旧设计中的 149 个零回归簇只对应原 −2pp 规则下的特定零事件示例；它既不是当前标准，也不是任意波动/预算下的通用样本量。新目标使用前，需预先选择能保留改善和失败双向信息的 paired risk-difference 方法，并单独证明其假设及错误率控制。

同一 task 的 repeats、同根因 bug 的措辞变体、跨 route 重用的同一 task 以及 E/L 两臂不是独立样本。重复次数用于估计 task 内随机波动，不得冒充独立任务簇。Task registry 的 hash 与 assignment 可防止部分误登记，但无法从 digest 证明抽样过程或统计独立性；需要 frame、抽样日志与人工任务族审查。

Cache、延迟与账单是不同指标。Fresh client session 不保证 Provider prompt cache cold；输入包含 cache-read 时不表示等额计费。各 route 单独报告 provider token 口径、cache counters、elapsed 与真实账单。缺字段要标 unknown；Gemini T20 的输入计量冲突使该 route 当前输入收益也未知。若计量权威性不能验证，该 route 不能用于相应收益估计。

## 报告格式与阶段产物

每一阶段输出版本、client/model/route、candidate digest、任务/fixture/gold/权限摘要、计划分母、started/finished/unfinished、所有错误和重试、dispatch/effect/cancel/release、输入 usage、cache、elapsed、billing 状态和置信区间。按 route 展示 eager/lazy 配对与 cluster 级差异，不混不同 Provider tokenizer，不把网络或配置失败隐藏为任务成功。标注结论为“有高置信实际净收益”“收益不足/未达到 MIE”“质量/安全未达标”“限定 route 有限证据”或“计量/样本证据不足”；没有足够证据时保持未知。

Stage 1 与 Stage 3 的原始日志/请求留在本机私有 evidence 目录；公开文档只记脱敏汇总与复现入口。候选变更后新旧证据分开，不跨 revision 拼接成功案例。

## 下一步（按序执行）

1. 审查当前未提交的 [repository fixture prototype](../../../ops/tool-loading/repository_project.py)、[MCP boundary tests](../../../ops/tool-loading/tests/test_repository_mcp.py) 与 [repository storage tests](../../../ops/tool-loading/tests/test_repository_project.py)；只在本机运行 `python3 -m unittest discover -s ops/tool-loading/tests -p 'test_repository_project.py'` 和 `python3 -m unittest discover -s ops/tool-loading/tests -p 'test_repository_mcp.py'`，确认实际当前行为。若测试失败，先修复 Stage 0；本方案不把文件存在当作通过。
2. 设计并验证 host-owned immutable gold runner、permission revoke、真实取消和资源释放；做本地 red/green fixture tests，确认 Agent 无法修改 gold 或调用任意 shell。
3. 将 offline evidence 及 Stage 1 的 route、task card、request/dispatch/time、可执行的 token/input admission cap 与 Provider 可用额度/配额边界填入一份不可覆盖的 declaration/plan，并标明各上限的实际执行机制。未实现的 token/input admission 不得虚称可执行；若只做输入资源校准且非货币上限足够硬，billing unknown 不阻塞启动。若主张成本/ROI 或存在按金额计费边界，先核实费率依据与货币费用上限；无法核实时保持账单 unknown，且不作货币收益结论。
4. 评审并至多运行 16-case calibration；保留完整 journal，另写校准报告，明确它不能宣称可靠率或正式净收益。
5. 根据任务分布、校准波动和业务影响冻结 Stage 2 参数，完成分析器覆盖验证和预算表后，再决定是否启动有界 Stage 3。

本文件只记录建议设计；当前没有因此发送 Provider 请求、改变默认配置、重启服务或发布候选。
