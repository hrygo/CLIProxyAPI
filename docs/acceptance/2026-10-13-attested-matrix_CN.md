# 跨 Provider 工具按需加载：统一矩阵与版本校验

核验日期：2026-10-13。本文是有限实测报告，不是全部客户端、模型和运行配置的兼容性认证。实施沿用[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)中的验收门槛；不发布、不切换默认配置、不重启原服务，也不刷新操作方 OAuth 凭据。

## 本轮交付

- `live_matrix.py` 在首个候选进程启动前，锁定所有路由、客户端、任务、eager/lazy 配对、材料指纹、均衡执行顺序和全局物理请求/dispatch 预算。失败、重试、未完成的已开始任务及未开始计划都保留。
- 修正 Antigravity 验收 scope 的 `upstream_format`：按照实际 `antigravity-generate-v1` 记录为 `antigravity`，不再误写成 `openai-response`。
- `live_clients.py` 和 `live_matrix.py` 都检查候选二进制的真实 Go 构建信息：必须为 Git 构建、完整提交号准确、`vcs.modified=false`，并核对校验前后以及计划材料中的二进制摘要。不接受仅由调用方声称的版本。
- 本轮工程回归：254 项 Python 测试通过。真实二进制验证也拒绝了错误提交号。另修复一项 WebSocket 错误排序问题，见下文；修复后全量 Go 测试 103 个包、`go vet ./...`、server 构建及相关两个包 race 检查通过。

统一矩阵源码提交：`c72ee37f2cd2fcfe60c2db4e766fc9f4c26cd1a0`；版本校验修复和正式矩阵候选：`a2f7c8b310a265b6083e12b4b01514a74e5b6067`。构建使用 Go `1.26.4`；正式候选构建信息已核实为该完整提交号及 `vcs.modified=false`。

WebSocket 排队错误修复提交：`a6239fa3b7ecf644609c668fc1776305c02947cf`；其工程检查已通过，但不能套用旧候选矩阵的版本证据。

## 预声明范围

固定客户端版本：Codex CLI `0.160.0`、OpenCode `1.19.0`、Claude Code `2.1.289`。每个任务都使用独立客户端目录和候选网关，实际客户端、二进制、配置及验收源码摘要进入冻结计划。

| 路线 | 实际 executor / upstream model | 客户端及案例 |
| --- | --- | --- |
| GPT | `codex` / `gpt-6-luna` | Codex、OpenCode，共 12 |
| Gemini | `antigravity` / `gemini-3.5-flash-lite` | Codex、OpenCode，共 12 |
| Qwen 入口 | `openai-compatible-qwen` / `deepseek-v4.1-flash` | Codex、OpenCode，共 12；Claude Code 另 12 |
| GLM | `openai-compatible-opencode-go` / `glm-5.3-flash` | Codex、OpenCode，共 12；Claude Code 另 12 |
| Muse | `meta` / `muse-spark-1.3-contributor` | Codex、OpenCode，共 12 |

合计 **84 个预声明案例**：原五条路线 × 两个客户端 × 三种工具任务 × eager/lazy = 60；Claude Code 两条路线 × 六种任务 × eager/lazy = 24。六种任务包含查询、无匹配、依赖多工具调用，以及 budget/retry/identity 三个独立校验的编码任务。新增 Claude 不替代原有路线。

每任务最多 90 秒、8 个上游物理请求、8 次工具 dispatch；全局请求和 dispatch 上限分别为 672。辅助模型发现请求也计入物理请求预算。未知请求数保留预留上限，不能按零退还。OpenCode 使用明确的固定标题；Muse 保留原输出模式，其他路线显式启用原生 JSON-schema 输出。不同输出模式不混合认证。

启动前，两个精确绑定的 OAuth 文件摘要未变化，现有 access token 的过期时间均超过五分钟；仅使用现成 Antigravity project。未自动寻找其他账号或刷新凭据。长批次中若过期，按实际失败计入。

默认配置启动前 SHA-256：`43dc5a62be638bfeda68a2c588b9f4bbe03d0c96a5d39d83031fc7840ff1e394`。本文仅记录任务相关配置摘要，不包含凭据或私人路径。收尾时再次检查。

## Gemini 预检及无效证据

正式矩阵前，4 个 Gemini 预检案例都在模型目录校验阶段失败，未启动客户端推理，也未进入工具发现/调用。各发生一次 `model_discovery` 辅助请求，捕获结果为 `transport_error`、HTTP status `0`、响应体 0 字节。

该预检还存在操作错误：传入的完整提交号为 `c72ee37f5e69790ea03aa826207cd0a3e2a692c2`，实际候选构建为 `c72ee37f2cd2fcfe60c2db4e766fc9f4c26cd1a0`；旧入口只验证字符串格式，没有核对二进制。四个已开始案例及原始捕获全部保留，增加 `evidence-status.json` 标注 `invalid-scope`，**不计入正式矩阵**。随后补版本校验和负例回归，正式矩阵重新预声明。

不把辅助请求失败解释成 Gemini 不支持工具检索，也不将一次无凭据 HEAD 探测成功解释成推理路线成功。当前只能确认失败发生在工具调用之前；账号、具体 TLS/传输和实际模型授权边界仍需有效证据。

## 正式矩阵结果

状态：进程已结束，退出码 1；计划、journal 和捕获已复核。84 个计划全部开始且全部结束，安全成功 **53/84（63.1%）**，失败 31：预算耗尽 1、Provider HTTP 错误 6、任务/终态失败 6、未知 18。没有未开始或未完成案例。

| 客户端 | 模型路线 | eager 安全成功 | lazy 安全成功 |
| --- | --- | --- | --- |
| Codex | GPT | 0/3 | 0/3 |
| Codex | Gemini | 0/3 | 0/3 |
| Codex | Qwen 入口 | 0/3 | 0/3 |
| Codex | GLM | 1/3 | 0/3 |
| Codex | Muse | 3/3 | 3/3 |
| OpenCode | GPT | 3/3 | 3/3 |
| OpenCode | Gemini | 0/3 | 0/3 |
| OpenCode | Qwen 入口 | 3/3 | 3/3 |
| OpenCode | GLM | 3/3 | 2/3 |
| OpenCode | Muse | 3/3 | 3/3 |
| Claude Code | Qwen 入口，含编码 | 6/6 | 6/6 |
| Claude Code | GLM，含编码 | 6/6 | 5/6 |

编码任务安全成功 11/12；GLM 的 lazy budget 编码任务耗尽 90 秒且未通过独立代码检查。汇总通过率只是本批固定任务的描述，不能当成生产可靠率。

journal 记录 191 次物理请求，其中 4 次辅助模型发现；有 14 个案例的请求证据未知，总请求数与全局预算合规结论仍为未知。76 个候选存在 attempt journal，84 个均有退出回执；8 个 Gemini 候选启动失败退出 1，其余退出 0。不能把缺失捕获解释成确定零请求。已知 dispatch 81，但 18 个案例没有工具边界证据，因此全批总 dispatch 未知。

66 个有工具边界证据的案例：禁止工具 dispatch、副作用及最终 active 均为零；其余 18 个未知，不能声称全批已经证明零副作用。全部退出回执的 `source_config_unchanged=true`，默认配置收尾摘要与启动前一致。

全部 84 个 scope 的 region/cache 条件或相关认证范围仍不完整，`gate_status=unverified`。仅案例数量达到 60+，质量、收益置信区间以及全部路线闭环没有达标。

### 输入收益

以下为同范围三对普通工具任务的模板簇中位数，包含每个预声明任务；不把缺失或失败删去后重算收益。编码任务的 fixture 范围不同，另列在原始分析中，不并入这张表。

| 路线 | 三对安全成功情况 | 首轮输入减少 | 峰值输入减少 | 全任务输入减少 |
| --- | --- | --- | --- | --- |
| OpenCode / GPT | 6/6 | 91.8% | 74.9% | 71.0% |
| OpenCode / Qwen 入口 | 6/6 | 91.0% | 77.3% | 73.1% |
| OpenCode / Muse | 6/6 | 90.7% | 77.0% | 72.8% |
| Claude Code / Qwen 入口 | 6/6 | 94.6% | 89.4% | 87.3% |
| Claude Code / GLM | 6/6 | 95.0% | 90.4% | 88.4% |
| Codex / Muse | 6/6 | 73.4% | **43.7%** | 39.5% |
| OpenCode / GLM | 5/6 | 91.1% | 未知，缺 1 对 | 未知，缺 1 对 |
| Codex / GLM | 1/6 | 77.9% | 77.9% | 77.9% |
| Codex / GPT、Qwen；两客户端 / Gemini | 0/6 各路线 | 未知 | 未知 | 未知 |

Codex / Muse 的峰值减少 **未达 50% 门槛**。Codex / GLM 输入较少主要伴随未执行任务，不能用来证明完成任务的经济收益。所有三簇收益区间下界均未知；不存在已通过收益认证的路线。

### 耗时、缓存与账单

完整三对工具任务的总耗时 p50（秒）和 cache-read/input-total 比例如下。耗时包含候选启动、目录查询、MCP、推理和清理，不是纯模型延迟；缓存条件未控制，不能据此宣称模型更快或账单下降。

| 路线 | eager / lazy 总耗时 p50 | eager / lazy cache-read 比例 |
| --- | --- | --- |
| OpenCode / GPT | 9.096 / 10.430 | 79.13% / 51.40% |
| OpenCode / Qwen 入口 | 9.906 / 13.341 | 50.51% / 55.80% |
| OpenCode / Muse | 9.619 / 13.491 | 49.68% / 73.17% |
| Claude Code / Qwen 入口 | 10.598 / 9.620 | 50.45% / 48.52% |
| Claude Code / GLM | 6.868 / 7.004 | 52.57% / 0%（本批已观察值） |
| Codex / Muse | 7.952 / 12.309 | 56.76% / 42.58% |

cache-write 在多条路线有缺失，实际价格、附加收费和账单都未知，不填零。逐组完整值见私有 `economics.json`。

### 责任边界

- **配置/验收模式不匹配：** GPT 实际目录报告 `tool_mode=code_mode_only`、`supports_search_tool=true`；当前 Codex 验收入口要求 direct/tool-search 模式，因此六个 Codex/GPT 案例在目录校验被拒绝。OpenCode 同模型六个通过。不能据此说 GPT 不支持工具调用；后续应新增明确的 Codex Code Mode profile，或单独预声明允许 direct 的隔离路线，不能修改默认配置后继承本批证据。
- **结构化输出限制：** 六个 Codex/Qwen 案例的上游返回 HTTP 400、`invalid_parameter_error`，原文为 `This response_format type is unavailable now`。请求确实经 Responses→Chat 映射为原生 `json_schema`。同路线 OpenCode 和 Claude 六个普通工具任务各自通过；其结构化输出机制不同。修复方向是明确路线输出能力及客户端模式，不能静默删除 schema 要求或更换供应商。
- **Gemini 启动/外部状态：** 四个正式案例各捕获一次模型发现传输失败；其余八个候选启动失败，没有 attempt journal。收尾查到精确绑定的 Antigravity 文件内容摘要已变化，而 Codex 摘要未变；这是本任务没有写入或刷新凭据的情况下观察到的外部漂移。新快照仍有效，但不能替换冻结绑定然后混入本批。当前通用启动错误没有保留逐案例安全原因，尚不能逐项证明八次失败全部由漂移造成；需补安全诊断并另起预声明批次。
- **行为与终态：** Codex/GLM 有五个任务未形成正确工具执行和结果回填；OpenCode/GLM lazy 多工具任务答案和 dispatch 正确但终态未通过；Claude/GLM lazy budget 编码超预算。暂不修改 nonce、终态或独立代码验证标准来抬高通过率。
- **网关已确认缺陷：** WebSocket 排队错误被 EOF 分支覆盖，已以红/绿回归复现并修复。该修复尚未进入本批冻结二进制，不能宣布最终版本跨 Provider 验收完成。

本机私有证据目录：`.omo/tool-loading-completion/t9-attested-matrix/`；计划 `batch-plan.json`，append-only 记录 `cases.jsonl`，收尾输出 `batch-audit.json`、`replay.json`、`summary.json`。原预检另保存在 `t9-native-antigravity-pilot/`。私有证据不进入发布产物。

## 达标还缺什么

### 本轮新增的 WebSocket 修复

矩阵运行期间，独立的协议测试在 `TestResponsesWebsocketReplaysImmediatelyAfterPinnedAuthFailure/rate_limit_to_websocket` 出现一次 close `1006`，预期为 replay close `1012`。原始失败保存在 `t9-attested-protocol-tests.log`。原单测重复 10 次及原版本 race 检查通过，但没有据此抹掉失败。

进一步的回归用例同时准备已关闭的数据通道和已排队的凭据错误，复现了错误被 EOF 处理覆盖：返回 `408 stream closed before response.completed`，replay suppression 未被调用。根因在 WebSocket forwarder：`select` 选中数据关闭分支后立即关闭下游，未先取出已排队的错误。修复复用已有 `handlers.PendingStreamError`，把同一错误处理路径用于正常错误接收和 EOF 前的排队错误，保持错误身份、取消原因和 replay 策略；不生成完成事件，不添加推理超时。

新用例覆盖普通/duplex 两种流，每种 128 次已准备通道的选择；连同原 replay 用例重复 30 次通过。修复后全量 Go 测试、vet、server 构建和 `sdk/api/handlers/openai` / `sdk/api/handlers` 的 race 检查通过。错误排序修复在当前矩阵候选 `a2f7c8b...` 之后实施，**不属于该 84 案例二进制**；后续新候选必须重新验证，不能继承或混合这批矩阵的版本认证。

### 质量与经济性认证

**案例数与质量置信门槛是两个独立条件。** 当前分析器按 task template 聚类、按完整 scope 分组，采用“发生至少一次 eager 成功/lazy 失败的任务簇概率”的单侧精确上界，忽略改善，作为质量差值的保守下界。独立同分布且具有代表性的任务簇是前提，当前固定模板不满足抽样认证。

零退化时，下界为 `-(1 - 0.05^(1/n))`：3 簇为 −63.16pp，30 簇为 −9.50pp，148 簇为 −2.0038pp，149 簇为 −1.9905pp。因此，每路由至少 149 个独立任务簇、298 个 eager/lazy 任务，才可能在零退化情况下触及 −2pp；这只是现有方法的数学必要规模，**不是样本功效计算，也不保证通过**。出现退化后需按实际结果重算，不能追加相同模板冒充独立样本。

收益区间也要求完整成对 usage；现有中位数秩统计方法在少于 5 个独立簇时无有限单侧 95% 下界。请求体减少、工具声明字节减少、输入 token 减少、缓存变化及最终账单，均为不同指标；缺失不能填零。

后续需先形成有来源的代表性任务集合和独立抽样设计：冻结任务家族及工具目录，保留每实例的输入/源码/测试摘要，避免为了跨实例统计而去掉身份或版本核验。现有三个编码任务因目录/fixture scope 不同，不能直接合并成同一路线的三个可互换独立样本。可在真正固定的通用编码工具目录下扩展任务实例，再审定如何保留实例证据和任务集合范围。仅扩增重试次数不足以完成质量认证。

此外仍需补全原生 Anthropic/Gemini 以及未覆盖 Claude 路线的实测范围、实际 region/endpoint 证据、缓存条件、可核对的计费信息，以及长期会话恢复/Provider 切换下的同等验收。没有通过的路线继续标未知或有限实测，不通过切换客户端模式抹掉原模式失败。

## 复现入口

见 [验收工具 README](../../ops/tool-loading/README.md)。先提交并构建干净候选，用 `git rev-parse HEAD` 获取完整版本，再提供显式 JSON 路线声明和新的私有输出目录。`live_matrix.py` 自动锁定计划和材料；`analyze_pairs.py` 从原 journal 和 plan 分析。它们不会修改默认配置、刷新凭据或重启既有服务。
