# 跨 Provider 工具按需加载：验证与兼容报告

核验日期：2026-10-14（Asia/Shanghai）。生产代码基准：`743a5e0ae14d100b2c1c8f2101b93adfa420269e`。[PR #25](https://github.com/hrygo/CLIProxyAPI/pull/25) 保留网关生产修复和 Go 回归。本文汇总工程结果、官方客户端和真实 Provider 的证据范围、失败责任与未知能力；[实施依据](design-and-implementation_CN.md)说明契约，[token 报告](token-usage-report_CN.md)提供详细用量。

## 1. 结论及证据级别

生产代码基准 `743a5e0a` 的协议修复保留；本轮修复三项 CI fixture 缺陷：上传收尾时序、usage 归属和重连握手顺序。冻结候选 `bfc5c62c45e8fd6b1ac08f7d9b62192ae76af17a` 的完整 CI 已通过，后续工程修订只追加 fixture 修复和文档，未改网关运行实现或性能工具。官方客户端最终复测的版本与结果独立记录于第 2.3 节。证据按具体路由、任务和版本解释，不认证普遍可靠率、长期任务质量或账单节省。

| 证据类别 | 本报告如何使用 |
| --- | --- |
| 当前源码及确定性 Go 回归 | 证明具体网关契约和已复现问题得到保护；不作为模型决策成功率。 |
| 官方客户端 + 脚本化本机模型 + 真实 MCP | 证明指定模式、调用入口和权限/取消边界；无真实 Provider token 或质量证据。 |
| 官方客户端 + 真实 Provider + MCP | 证明固定 revision、路由、任务的实际闭环和观察结果。 |
| 合成历史 + 真实 Provider | 证明目标接口接受指定 history；没有实际工具执行时不计为客户端任务成功。 |
| 历史报告与独立只读审计 | 保留原批分母、失败和标签，补充可核对事实；不修改原 journal、不池化不同版本。 |
| 未知 | 缺少有效身份、原始用量、终态或直接执行证据时保持未知；未知不填零。 |

历史 86 个 `ops/tool-loading` 文件均可从 Git 快照及原工作区恢复；仓库现保留 32 个官方客户端性能复现所需文件及回归。一次性探针、运行 journal、二进制和私有原始材料不纳入 PR，新产物写系统 tmp。没有客户端源码构建入口；自建客户端历史结果不列入官方客户端认证。

## 2. 最终代码工程验证

冻结候选 `bfc5c62c` 的 [完整 CI](https://github.com/hrygo/CLIProxyAPI/actions/runs/38490706422) 全部成功，使用 Go `1.26.4`，不联系真实 Provider。该候选的 Go 内容与本地完整门禁通过的 `eb5475e8` 相同。

最终追加的重连 fixture 修复已在 Go `1.26.4` 下重新通过全仓 test/vet/server build：102 个 package 返回 `ok`，29 个无测试文件。此次验证不发起 Provider 请求；冻结用量仍绑定 `bfc5c62c`，没有用测试修订冒充新的端到端候选。

| 检查 | 结果与口径 |
| --- | --- |
| `go test ./... -count=1` | 本地全套 102 个 package 返回 `ok`，包括恢复的 SDK 隔离 launcher 回归；29 个 package 为 `[no test files]`。CI 全仓测试通过。 |
| `go vet ./...` | 通过。 |
| server build | 通过。 |
| tracked gofmt | 通过；使用固定 Go toolchain。 |
| 离线 Codex model catalog | 通过。 |
| 既有维护工具回归 | 28 项通过。 |
| 复现工具回归 | 本地 Python 46 项通过，含真实 stdio MCP、独立代码判定、执行边界、计量、模式和结果绑定；CI 运行其中 21 项 stdlib 计量回归，无 Provider 或登录依赖。 |
| CI 定向稳定性检查 | 三个 WebSocket 上传测试、TTFT、usage 归属及重连状态测试共六个顶层测试各连续 50 次通过，原功能断言保留。重连测试含 Codex non-stream、Codex stream、xAI stream 三个子例；受控乱序探针修复前全部失败，修复后各重复 10 次通过。 |
| optional live/corpus/TLS | 既有条件跳过不代表对应外部能力通过；新增生产回归未以跳过代替执行。 |

本轮 Go 变化为三个 executor 测试文件和可复用 SDK launcher；网关协议运行实现未变。CI 增加离线计量检查，没有真实 Provider 请求。旧阶段报告的包数属于其各自 revision，不能替代当前门禁结果。

文档提交 `1559ce513937fe38339e42ee5c1bb5228437b532` 的 CI 两次尝试，以及后续文档提交 `6c999856c7c11ef76a74899c0af6c9c7c9b28b91` 的 CI，均在 Test 步骤失败，Build 未执行。失败记录保留，不标为通过：

| CI 尝试 | 实际失败 | 复核边界 |
| --- | --- | --- |
| [第一次](https://github.com/hrygo/CLIProxyAPI/actions/runs/38481971551/job/115586446466) | `TestCodexWebsockets_KeepalivePingDuringUpload_Sessionless`：上传时连接已关闭 | 受控调度复现：fixture 收到 Pong 后，在请求上传完成前回复并关闭连接。修复使服务器确认已读完请求后再回复；仍断言上传阻塞期间能收到 Pong。三个同类测试统一修复。 |
| [第二次](https://github.com/hrygo/CLIProxyAPI/actions/runs/38481971551/job/115588836183) | `TestAIStudioExecutorExecuteStartsTTFTBeforeRelayWait`：257.66µs，期望至少 40ms | 注入另一执行的同 provider/model usage record，旧 helper 可选错记录，受控测试失败。每次测试用唯一 AuthID，helper 同时匹配 provider/model/AuthID；新增错归属回归，保留 TTFT ≥40ms 断言。 |
| [后续文档提交](https://github.com/hrygo/CLIProxyAPI/actions/runs/38492630781/job/115621173646) | `TestWebsocketRetryBindFailureClearsActiveSessionState/xAI_stream`：第二次请求 unexpected EOF | fixture 在发送 Upgrade 握手后才分配连接序号；客户端快速重连可使恢复连接抢先取得序号 2，被当作拒绝连接关闭。强制该乱序时 Codex non-stream/stream、xAI stream 三子例均复现同类 EOF。修复在握手前预留序号，保留 Bind 拒绝、活跃状态清理、恢复调用及连接数断言。 |

三项失败均定位到既有 fixture 的时序或归属缺陷，未发现对应的网关运行故障证据。前两项修复提交 `66b59e7b` 的 [CI](https://github.com/hrygo/CLIProxyAPI/actions/runs/38485472454) 和冻结候选 CI 均通过；第三项修复的受控验证、定向重复和全仓本地门禁通过。最新提交的远端检查记录见 [PR #25 checks](https://github.com/hrygo/CLIProxyAPI/pull/25/checks)。原失败和修复前后证据均保留；有限重复验证不等于 CI 永不波动。

### 2.1 生产协议与发送保护

| 回归面 | 已验证结果 | 不能外推的能力 |
| --- | --- | --- |
| GPT Responses Lite / Muse | 精确搜索 alias、对象参数、buffered/SSE 恢复、分片参数、item 身份及结果回填通过。 | 不证明任意模型检索正确。 |
| Lite `additional_tools` | portable 路由提升、namespace 合并、去重、描述补齐、冲突拒绝与活跃预算通过；原生继承对照保留字节。 | 提升改变声明作用域；精确位置保真未认证。 |
| 累计激活 | 多轮 A→B 保留 A、后轮更新、别名与强制选择一致，累计超预算整体拒绝。 | 激活清单不认证执行授权。 |
| 原生多结果 | 合法多结果逐项重放；结果内容、metadata、身份、数量与顺序篡改本地拒绝。 | 限于显式支持的原生 route；跨协议一对多不普遍支持。 |
| 多结果调用参数 | HTTP/SSE 各 13 类篡改本地拒绝、零 mock 上游请求；合法及无参调用通过。 | 哈希保护范围不是所有 conversation 内容。 |
| Claude 活跃集合 | 五种 executor 的 HTTP/SSE 共 80 个子案例：58 个非法最终请求零上游发送；20 个正常及 2 个 xAI 合法修复对照允许发送。 | 2 个 xAI 对照保留既有孤立 choice→auto 修复，不能算作原强制选择被拒绝。 |
| Gemini 身份 | 正常/compat 请求、历史、choice、buffered/SSE 使用一致消歧映射；长名、字符替换、大小写与前导下划线对照通过。 | 最终指定 Gemini/Codex 编码闭环有实际执行和回填；Antigravity Code Mode 线包身份观察器未认证。 |
| Hosted choice / WS | 32 个 HTTP/SSE/WS 子案例：16 个非法选择零发送、16 个有效对照通过；另 6 个 hosted MCP server identity 子案例通过。 | 声明校验不认证 hosted 执行权限、费用或副作用。 |
| Claude 协议识别 | 普通文字、名称、schema/参数对照保持普通路径；重复字段与 Unicode 转义按协议位置检查。 | 非原生适配与 native/emergency bypass 分别定义。 |
| 大目录活跃预算 | 输入工具数组 262,617 B，裁剪后首轮 97 B、发现后 243 B；等于预算通过，小一字节 413。Execute/CountTokens/Stream 均覆盖。 | 合成字节证据不是真实任务 token 收益。 |
| 流终态 | 明确 reasoning-only finish 正确映射；缺终止信号、未闭合参数和真正截断保持错误，不补造 completed。 | 历史 GLM 缺终态的唯一根因未证明。 |
| 重试 / WS gate | 暴露调用后不跨凭据重放；语义 Prepare 决定 replay；native 后继帧 ID 修复、拒绝帧零发送、排队错误不被 EOF 覆盖。 | 没有全部 Provider 真正断流及 live duplex 的新实测。 |
| 诊断与释放 | typed error、固定 reason/phase、取消和迟到回调、attempt lease 与 HTTP body 关闭边界有确定性回归。 | 单一路径的诊断可达不等于整体取消生命周期认证。 |

子案例分别服务于具体缺陷，不合计为真实 Provider 成功率。localhost “零上游发送”与 MCP “零 dispatch”是不同层次的事实。

### 2.2 最终代码的解析与恢复审查

两次审查以 43 个生产 Go 文件的 PR diff、相关契约和回归为范围。索引覆盖不足的新增/修改内容以当前源码读取和实测复核，没有用图谱缺失证明代码不存在。

| 已复现问题 | 修复 | 验证 |
| --- | --- | --- |
| Claude discovery 的非法 UTF-8 / 孤立 surrogate 被替换为 `�`，改变工具身份 | `ce9288b8` 拒绝 Unicode 信息损失 | Execute/CountTokens/Stream 非法请求零 executor 调用，合法 Unicode 对照通过。 |
| 显式 native + custom:function、无 schema preparation 的路由漏判转义 `additional_tools` | `e880fb4d` 按 JSON 语义识别并进入 portable/replay 处理 | 声明提升、身份及原生透传对照通过；默认 Meta 先前已能处理，不扩大为默认 Muse 故障。 |
| 转义 custom 类型绕过 reject、多结果、孤立输出和 Unicode 校验 | `743a5e0a` 统一字面和转义类型检查 | 五个拒绝子案例修复前失败、修复后通过；拒绝后无预算 lease。 |
| buffered 的转义 `function_call` 未恢复，泄漏 custom bridge alias | `743a5e0a` 按解码后的类型恢复 | 原工具名、custom 类型、输入和 call ID 恢复；失败复现与正常对照通过。 |
| 普通 schema/参数中的 `type:custom*` 被递归识别为协议 | `743a5e0a` 只检查真实声明和顶层历史位置 | 字面、转义普通数据均保留原字节，不触发 managed replay。 |

五类新增缺陷均有先失败后通过的回归。最终候选全仓门禁与新增 Provider 用量独立记录于第 2.3 节；历史运行结果不替代最终实测。


### 2.3 最终候选与跨 Provider 实测

最终候选为 `bfc5c62c45e8fd6b1ac08f7d9b62192ae76af17a`，SDK binary SHA-256 `0bd462c759ede23d45f1330b520afcef6550097b55af7325f55f47592978fd58`；原生 Go build 信息为 `go1.26.4`、`vcs.modified=false`。后续仅修订文档和 executor 测试 fixture，不改变已测网关运行实现或 Python 复现工具。网关协议运行实现保留 `743a5e0a`；三项 CI fixture 修复见第 2 节。准入签名判读和目录就绪修复属于性能复现入口。

固定范围为 Codex `0.160.0` 的 `code_mode_only`、Claude Code `2.1.289` 的标准隔离模式和原生 StructuredOutput；四个既有路由 × 两客户端 × eager/lazy，预算函数与身份函数两个模板。70 个准入 MCP 工具，真实 read/replace/tests 三工具。顺序交叉安排，Gemini 优先；OpenCode 客户端不在本批，alias 中的 `opencode-go` 不是客户端身份。

| 冻结项 | 实际记录 |
| --- | --- |
| 配置 | 完整 operator source SHA-256 `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`，仅在临时 SDK 中隔离路由并禁用 hosted image generation；默认文件未写入。 |
| 客户端 | 官方已安装发行版；Codex SHA-256 `112fae7a5a1223e673c8a1791d32338f37df8b527ff1159bb8adac6c4dbf1b4b`，Claude SHA-256 `03d66745e3bb69ec727d66023696f3820bc0a00a8a5ba725eb6706d0c67cbe69`；未构建、升级或替换客户端。 |
| 身份 | OAuth 仅同一显式选定文件，以当前内容哈希冻结；API-key route 由原配置绑定；实际 attempt 的 executor/model/auth 摘要均校验。未刷新、写入或自动换账号。 |
| 单案预算 | 120 秒，包含目录就绪等待；最多 16 次物理 HTTP attempts、16 次 MCP dispatch，受整批剩余预算进一步限制。 |
| 最终批 | 16 planned，15 started，15 finished，7 严格通过；76 次物理请求，含 4 次辅助和 14 次 HTTP 429；`run_attempt_budget` 停止。Claude/GLM eager 未开始；lazy 仅剩一次物理请求预算，未完成任务。 |
| 本轮全量 | 52 planned、51 started/finished、160 次物理请求；此前 36 案、84 次与最终 15 案、76 次分批保留，不池化不同版本和材料。 |
| 执行边界 | 15/15 案有 MCP 边界观测，共 34 dispatch；forbidden/effects/final active 均为 0。限于 MCP fixture，不外推 hosted 或所有取消入口。 |
| 任务质量 | 10/15 案代码修改及独立行为检查通过；代码通过不能替代模式、加载顺序、真实执行、回填和终态判据。 |
| 材料保全 | config/client/fixture Python/binary 首尾摘要一致；credential 变化另按实际启动拒绝记录。 |

| 客户端 / 路由 / 任务 | 严格通过 E/L | 模式判读 E/L | 代码行为 E/L | 物理请求 E/L |
| --- | --- | --- | --- | --- |
| codex / Gemini / identity | 0/0 | 未认证/未认证 | 10/10 · 10/10 | 4/9 |
| claude / Gemini / identity | 0/0 | 通过/未完成 | 5/10 · 5/10 | 8/8 |
| codex / GPT / budget | 1/1 | 通过/通过 | 11/11 · 11/11 | 6/5 |
| claude / GPT / budget | 1/1 | 通过/通过 | 11/11 · 11/11 | 4/5 |
| codex / Muse / identity | 1/0 | 通过/未完成 | 10/10 · 10/10 | 5/10 |
| claude / Muse / identity | 1/1 | 通过/通过 | 10/10 · 10/10 | 4/5 |
| codex / GLM / budget | 0/0 | 通过/未完成 | 4/11 · 4/11 | 1/1 |
| claude / GLM / budget | 未开始/0 | 未开始/未完成 | 未开始 · 4/11 | 未开始/1 |

7/15 是这批的严格结果计数，不能解释为总体可靠率或网关故障率。Gemini/Codex 两臂有真实执行、10/10 代码行为检查、nonce/hash 回填和合法终态；当前 Code Mode 观察器仅覆盖 Responses/Chat，无法认证 Antigravity，原 `mode=false` 不证明网关或模型故障。Muse/Codex lazy 也通过代码及回填，但 read/replace 的调用发生在观察到相应 schema 前，不满足冻结的加载顺序判据，首轮省略声明不能替代该判据。Gemini/Claude 两案各七次实际上游 429，没有 MCP dispatch，最终达到过程预算；确切配额原因未知。GLM/Codex 两案各一个合法结束的模型请求、零 dispatch、代码未改，不构成工具闭环。

#### 本轮保留的准备与失败记录

| 范围 / revision | started/finished · 严格通过 | 物理请求 | 责任与复用边界 |
| --- | --- | --- | --- |
| 首轮 / `eb5475e8` | 16/16 · 3 | 24 | Codex-only source 未覆盖其余路由；一个 eager 判读误计禁止工具，70 个准入签名回归已修复。原 journal 不改写。 |
| 完整配置 / `433b93c7` | 16/16 · 9 | 56 | GPT/Codex lazy 10/11 行为检查；GLM/Codex 零 dispatch；Gemini 四案 binding_content_mismatch，零上游请求。 |
| Gemini 定向 / `433b93c7` | 4/4 · 0 | 4 | 只有辅助发现请求；首次空目录被过早判失败并取消发现。复现入口已恢复同进程 pid/nonce/active 校验及有预算的真实目录等待。 |
| 最终 / `bfc5c62c` | 15/15 · 7 | 76 | 一案未开始及一案预算不足均保留；上表为最终结果，不覆盖前批质量失败。 |

目录等待只重读本机真实 catalog，不发起新的上游发现或填造 metadata；工作结束仍无模型立即拒绝。该修复及七项既有就绪回归保存在同一复现模块。运行期间未改写已测源码或 capture；各准备批不升级为最终版本认证。

保存审计确认历史 86 个工具文件在 Git 快照和原工作区均完整存在，6 个本地改动保留；43 个一次性 Python 脚本另有系统 tmp 副本和 SHA-256 清单。仓库只保留性能复现所需的 32 个文件及回归，原批次编排、认证台账、Docker 和客户端源码探针留在历史。新二进制、清单、日志、journal、原始请求/响应和一次性提取脚本均在系统 tmp。

复现入口见 [ops/tool-loading](../../ops/tool-loading/README.md)。完整数值见[token 报告第 11 节](token-usage-report_CN.md#11-最终候选的完整用量记录)。本轮完成的是有预算的有限实测，未完成全部 16 格认证。

## 3. 客户端与模型范围

版本来自 2026-10-12 至 2026-10-14 的本机实测；版本之外的行为为未知。表中的“支持”按配置、协议、闭环和实测范围分别解释。

| 客户端 / 模式 | 已观察行为 | 适用边界 |
| --- | --- | --- |
| Codex `0.160.0` native tool search | 模型 metadata 与 provider 能力满足时，实际首包省略 MCP namespace 并声明检索；后续 reference 与调用闭环有实测。 | `supports_search_tool` 和 provider 的 namespace 能力共同相关；旧 `features.tool_search` 在该 profile 不能单独证明生效。 |
| Codex `0.160.0` `code_mode_only` | `functions.exec` 可从 `ALL_TOOLS` 选择 metadata/工具，实际加载调用 coding 工具，并可发出同 call 的通知和最终结果。 | Code Mode 的本地目录过滤与 native ToolSearch 是不同模式；有指导 exact-name 的成功不认证自然语言检索。 |
| Claude Code `2.1.289` 标准隔离模式 | 实际 ToolSearch、`tool_reference`、延迟声明、loaded tools 和调用结果闭环通过；编码对有实际执行。 | 固定 mode、允许工具、最终输出配置与实际路由；开关被接受不替代线包证据。 |
| Claude Code `2.1.289` `--bare` | 即使显式 `ENABLE_TOOL_SEARCH=true`，本机探针仍加载完整 67 个 MCP 工具，未观察 ToolSearch。 | 不是按需加载实测 profile；隔离配置使用独立 home/MCP/权限而不是该模式。 |
| Claude Code `--json-schema` | 该版本实际产生 `StructuredOutput` 工具及最终结构化结果；Qwen/GLM 指定编码/非编码样本通过。 | 与旧纯 JSON 要求不同；不能把后者失败用新格式追溯改判，也不是自动给 Provider 添加 response_format。 |
| OpenCode `1.19.0` Code Mode | eager 完整声明与 lazy execute 首包、真实调用及 session abort 有本机证据。 | 非重点；与 `1.18.32` 和 Code Mode V2 分开，不能沿用旧版本配置或证书。 |

模型 alias 中出现 `opencode-go` 只是路由名称，本文 Codex/Claude 案例不因这个字符串变为 OpenCode 案例。

### 3.1 已测上游路由

| 声明模型 | 实际 executor / 协议 | 已确认事实 |
| --- | --- | --- |
| `gpt-6-luna` | Codex OAuth / Responses | 历史官方客户端对照和指定原生多结果接受性成立。 |
| `muse-spark-1.3-contributor` | Meta / Responses | Lite/portable 激活有实测；目标要求 tool choice auto，一调用多输出被明确拒绝。 |
| `glm-5.3-flash` | OpenAI-compatible Chat | 工具调用及 Claude StructuredOutput 有有限实测；模型会出现格式/回执偏差，Codex 路由含预算失败。 |
| `deepseek-v4.1-flash` | Qwen endpoint / OpenAI-compatible Chat | 声明名称是路由范围而非权重身份鉴定；结构化输出和工具闭环有有限实测，native JSON 路径曾 400。 |
| `gemini-3.5-flash-lite` | Antigravity / generate wrapper | 部分 Codex 实际闭环成功；Claude 有 429，启动材料有漂移，用量权威性未知。 |
| Gemini / Vertex / AI Studio 其他入口 | 各自 executor | 部分 translator/SDK 回归通过；不能从 Antigravity 或本地回归外推全部 live route。 |

原生 Anthropic 的透传边界与非原生 Claude 客户端适配分开；本报告不据非原生 GPT 对照认证真实 Anthropic route。

## 4. 真实 Provider 证据台账

批次标签用于定位固定历史材料，不是当前阶段待办。不同 candidate、配置、输出 profile、任务和 mode 不合并为最终版本通过率。

| 证据范围 / candidate | 已开始与结果 | 已记录请求 / dispatch | 事实边界 |
| --- | --- | --- | --- |
| 初始跨 Provider；多个 revision | 正式核心样本最终 32/39；组合覆盖达到 20/20 | 原始各批保留 | 组合覆盖来自修复前后批次；不是单一版本新跑 20/20。硬纯 JSON 仅 16/32 最后成功，不能被 nonce 执行成功替代。 |
| 早期 Qwen/GLM 校准；不同修订 | 12 开始，4 完整任务通过 | 25 实际请求 | 采集修订及客户端输出格式分别影响判定；回放不算新增样本，旧缺用量不补写。 |
| 三场景 / `3de2aee8` | Codex 6/6，OpenCode 4/6 | 16+22 请求，6+6 dispatch | 同一候选的两个 plan；OpenCode 后续另查 workspace 隔离，不能据环境变量隔离宣称全部配置隔离。 |
| 固定标题 / `4e68b510` | Qwen 11/12、GLM 6/12 | 30+35 请求，12+10 dispatch | OpenCode 两模式固定标题，改变首请求边界；不与含自动标题的旧计量合并。 |
| Native 输出 / `732e73a1` | Qwen 5/12、GLM 7/12 | 23+17 请求，7+4 dispatch | Qwen 六例缺完整用量；unavailable 的单值 enum 约束期望答案，不能证明正确语义判断。 |
| Shape-only 输出 / `06e7b3ee` | OpenCode Qwen 6/6、GLM 5/6 | 独立批记录 | 去除答案 enum，格式与 gold 分开；与前 profile 独立，不追溯升级。 |
| 早期 coding / `ff28319c` | 23/24 严格成功；24 代码检查通过 | 109 请求、78 dispatch | Qwen/GLM×Codex/OpenCode×三纯函数；严格结果与代码部分通过分开。 |
| Claude 普通输出 / `5f0f186a` | Qwen 5/12、GLM 3/12 | 43+50 请求 | 两路线的编码行为分别 6/6、4/6；编码严格结果各 1/6，不能以代码通过覆盖最终格式失败。 |
| Claude StructuredOutput / `1677af58` | Qwen 12/12、GLM 12/12 | 44+50 请求，26+28 dispatch | 显式新输出 profile；每路线六个工具任务、六个 coding，全部固定模板，无 Provider HTTP≥400。 |
| T9 / `a2f7c8b3` | 84 started/finished，53 safe、31 非成功 | 191 记录，含 4 辅助；81 已知 dispatch | 14 例请求未知、18 例工具边界未知，真实总量未知；31 例为 1 budget、6 Provider HTTP、6 task/terminal、18 unknown。 |
| T13 原配置批次 | 10 started，0 严格成功 | 3 已知请求、1 dispatch；9 例未知 | 冻结配置失效，原分母保留；用户确认由其他任务预期修改，未回退配置。 |
| T13 新基线 / `4b428edf` | 10 started，8 safe | 28 已知请求、12 dispatch；2 例未知 | Gemini 在启动前失败；范围与旧基线不同。 |
| T13 Gemini 单独批 / `4b428edf` | 4 started，2 safe | 23 请求、2 dispatch | Codex 两例实际成功但 usage 未知；Claude 两例含上游 429 和预算失败。 |
| T14 主矩阵 / `4b428edf` | 48/48 finished，39 safe | 142 请求、48 dispatch | 8 model、1 protocol；同 call 多输出失败保留。 |
| T14 Gemini / `4b428edf` | 12/12 finished，5 safe | 68 请求、7 dispatch | Claude 六例 429；Codex 5/6 成功，另一个任务额外 confirm。 |
| T15 coding / `4b428edf` | 24/24 safe，24 有代码修改 | 123 请求、81 dispatch | 三个纯函数任务，host gold 288/288 次执行；不是 288 个独立编码任务。 |
| T17 发现策略 / `ca0758f4` | 24 finished，20 safe；22 gold 通过、23 有代码修改 | 123 请求、75 dispatch、85 MCP attempts | 六个选择性 lazy 均通过；四个失败和已有输入保留。 |
| T19 响应诊断 / `58f5523b` | 4/4 safe | 20 请求、12 dispatch | 44/44 gold 执行；只观察正常响应，未触发真实响应拒绝。 |
| T20 跨 Provider / `58f5523b` | 60 finished，41 safe、19 非成功 | 161 已记录、46 已知 dispatch、47 MCP attempts | 8 例缺请求/执行计数，实际总量未知；三个固定非编码模板。 |
| T23 多结果 / `832c8fbf` | 最后两个 synthetic bridge 请求合法完成 | 2 实际请求，零重试 | 合成历史，没有客户端工具执行；整个诊断历史另有 7 次物理请求及 2 次本地拒绝，均保留。 |
| T24 Codex 通知 coding / `832c8fbf` | 原 verdict 失败；独立审计确认闭环、代码及 11/11 gold | 7 请求、5 MCP dispatch | 原观测器漏识别 namespace/多输出，不改写旧判定；没有 eager 对照。 |
| T26 官方 Codex pair / `0efa8a28` | 两臂代码及各 11/11 gold；独立审计结果均 true | 10 请求、6 MCP dispatch | 原 journal 标签与审计存在差异，外层脚本退出 1；见第 5 节。 |
| T27 官方 Claude pair / `e202aaa8` | 两臂代码及各 11/11 gold；独立审计结果均 true | 9 请求、6 MCP dispatch | 原 journal 标签与审计存在差异；见第 5 节。 |
| 最终候选 / `bfc5c62c` | 15/15 finished，7 严格通过；1 planned 未开始 | 76 物理请求、34 MCP dispatch | 有预算的固定编码任务；模式、代码、外部可用性和失败分母见第 2.3 节。 |

T14 两份 plan 同 revision、binary 和配置材料，覆盖计数为 44/60 safe、210 请求、55 dispatch；两个 run 边界保持独立。T9、T14 和 T20 的分母不能累计成某个通用可靠率。

### 4.1 T20 完整 route 结果

candidate 为 `58f5523b90d0561ff7812ad8da8257f14acb4ef5`；Codex `0.160.0`、Claude Code `2.1.289`；五 route × 三个 lookup/multitool/unavailable 模板 × eager/lazy。Codex GPT/Muse/GLM/Qwen 为 Code Mode，Codex Gemini 为 native JSON schema，Claude 为 native StructuredOutput。每格分母为 3。

| 客户端 / 模型 | safe E | safe L | 主要失败边界 |
| --- | ---: | ---: | --- |
| Codex / GPT | 3 | 3 | 此有限样本未发生任务失败。 |
| Codex / Muse | 3 | 2 | lazy lookup 的答案/规定模式未通过。 |
| Codex / GLM | 1 | 0 | 格式、回填和八请求预算。 |
| Codex / Qwen route | 1 | 2 | 格式或中间回执。 |
| Codex / Gemini | 1 | 1 | 部分启动材料漂移；输入未知。 |
| Claude / GPT | 3 | 3 | 此有限样本未发生任务失败。 |
| Claude / Muse | 3 | 3 | 此有限样本未发生任务失败。 |
| Claude / GLM | 3 | 3 | 此有限样本未发生任务失败。 |
| Claude / Qwen route | 3 | 3 | 此有限样本未发生任务失败。 |
| Claude / Gemini | 0 | 0 | 启动材料漂移；实际 multitool 两臂各六次 429。 |

19 个非成功记录为 8 model、3 budget、8 unknown。八份启动回执记录 `credentials / binding_content_mismatch`，配置摘要未变；credential 内容来源及刷新机制未知，未自动换绑定或删除失败。161 次已记录请求为 149 HTTP 200、12 HTTP 429，包含重试及辅助请求。八例计数缺失，预算全量是否满足保持未知。

52 例的禁止 dispatch、副作用和最终 active 观测为 0，八例未知；网关退出为 52 个 0 和 8 个 1。52 份响应诊断正常封存、没有转换失败记录；这证明正常采集可达，不证明所有协议无误。

## 5. 官方客户端编码对照的实测范围

两组都使用 `gpt-6-luna` / Codex OAuth、隔离配置和真实 MCP coding 工具，单个固定 budget 函数任务。未刷新凭据、未安装候选或切换默认策略。

| 指标 | Codex `0.160.0` / `0efa8a28` | Claude Code `2.1.289` / `e202aaa8` |
| --- | --- | --- |
| 顺序 | eager→lazy，固定 | lazy→eager，固定 |
| 开始 / 结束 | 2 / 2 | 2 / 2 |
| 请求 E/L | 5 / 5 | 4 / 5 |
| MCP dispatch E/L | 3 / 3 | 3 / 3 |
| 代码与 gold | 两臂修改代码，各 11/11 | 两臂修改代码，各 11/11 |
| 发现/加载/回填 | 首轮无 fixture 签名；加载三工具，调用前声明有效；通知和最终结果保留 | 首轮 fixture 声明为 0，后续三工具；ToolSearch 返回三个引用，实际 read/replace/tests 并关联回填 |
| 可观测 MCP 边界 | forbidden dispatch/effect/final active 各 0 | forbidden dispatch/effect/final active 各 0 |
| 独立审计 case result | 两个 true | 两个 true |
| 原始 case journal | 两条 `failure_class=model`；六项 checks 全 true | 两条 `failure_class=model`；六项 checks 全 true |
| 外层 wrapper | 退出 1，排他写入汇总错误；部分汇总和错误保留 | 退出 0 |
| Provider 重跑 | 无 | 无 |

原始 journal 的分类标签与 checks/独立审计不一致。本报告保留这项证据差异：代码修改、执行与终态的直接检查和独立审计支持任务结果，但不把原标签改为 none、不把 wrapper 失败改为退出 0，也不把四个案例纳入“最终版本全通过”的汇总。标签不一致的确切内部成因未在本次文档整合中验证。

Claude eager 每请求 fixture 声明数量为 `[70,70,70,70]`，lazy 为 `[0,3,3,3,3]`。两臂均存在既有 operator 策略注入的 `image_generation` 声明，未观察到执行；MCP 计数不能认证 hosted 权限、费用或副作用。

详细 19 次请求的输入、输出、缓存、reasoning、总量和摘要在[token 报告](token-usage-report_CN.md)。固定任务与顺序、未受控缓存及不同 candidate 只支持点估计，不提供稳定延迟或跨 route 质量区间。

## 6. 权限、非法参数、撤权、取消与释放

### 6.1 脚本化模型的执行边界

以下运行采用官方客户端和真实 MCP SDK 服务，模型选择由本机 HTTP/SSE fixture 固定。成功属于执行边界证据；没有实际 Provider 或账单，不计入第 4 节的 live 分母。

| 范围 | 已测结果 | 精确限制 |
| --- | --- | --- |
| Codex `0.160.0` + OpenCode `1.19.0`，eager/lazy 四场景 | 最终 16/16：授权调用有真实 nonce；客户端禁止且服务允许时 0 MCP 请求/dispatch；非法参数有 MCP 请求但 0 dispatch；generation 2 撤权后 0 dispatch；effects/active 均 0。 | 此入口之前的 wrapper/时序诊断另保留，总历史 56 案、168 收到的请求；不池化成任务可靠率。 |
| Claude Code `2.1.289` 的 eager/lazy 执行边界 | 八例通过，禁止和非法参数执行前拒绝、零副作用；撤权在实际 dispatch 前生效。 | 固定版本和 permission profile；不认证所有动态授权变化。 |
| Claude SIGINT 取消 | MCP 实际 wait 开始后观察 cancelled/released，active=0，记录发生在强制清理前。 | 指定本机脚本化入口；不能外推所有 MCP transport、并发调用和真实 Provider 任务。 |
| 本机首包探针 | Codex eager/lazy 工具声明数组 195,587→9,984 B；OpenCode 199,295→10,912 B。 | 只证明首个带工具请求的字节变化；预设 HTTP 400 结束推理，任务不成功、Provider tokens 未知。 |
| 真实客户端→隔离网关→合成 Chat/Responses→MCP | 最后八例闭环通过，24 次收到的模型请求；每例一次 dispatch，nonce 回填、合法终态、零效果/active。 | 全诊断历史 24 案/65 请求/18 闭环，保留 fixture 修订边界；合成 usage 31/2/33 不是模型计费。 |

### 6.2 官方 Codex 取消缺口

官方 Codex `0.160.0` 的 Code Mode / app-server 探针显式设置 `features.code_mode_interrupt=true`，并以 `features list` 核对接受。四例中两个普通调用 completed、实际回填并释放；两个 dispatch 后 `turn/interrupt` 收到 ack 和 matching interrupted，但 MCP 取消通知为 0，未观察 cancelled/released，清理前 active=1。

这四例使用本机脚本化模型，没有网关和真实 Provider。显式开关缺失不能解释这两次失败；开关属于 UnderDevelopment，接受配置不代表稳定产品保证。证据证明指定客户端入口的取消传播缺口，未证明唯一 Rust 代码根因，也不证明进程终止后持续泄漏。

网关不拥有客户端的本机 MCP request ID，不能替它发送取消或伪造 released。客户端源码修改、构建和 fork 不属于生产交付。此前自建候选只保留在历史，未替换安装客户端，不计入官方兼容结果。

### 6.3 OpenCode 的补充边界

官方 `1.19.0` 的 session abort 在 eager/lazy 均观察到匹配 abort 终态、MCP 通知与释放、active/effects=0；单纯非交互 CLI SIGINT 探针没有取得同等证据。两个入口不可互相覆盖。

Code Mode 无界 CPU 循环的本机探针需要外部 20 秒预算终止；显式 SDK timeout/tool-call limit 回归可工作，但 maxOutputBytes=256 的样例实际 serialized value 为 348 B。客户端入口未传 SDK limits 的历史 scope 与 MCP dispatch/result 服务预算不同。该兼容问题非 CLIProxyAPI 的主要实施范围，不以网关裁剪结果弥补。

完整取消后新任务恢复、并发不同 request ID、运行中权限变更和跨事务原子撤权尚无全面认证。服务在某校验时点拒绝，不等于任意时刻撤权都能阻止已经合法提交的操作。

## 7. 失败责任和未验证能力

| 问题 | 已确认边界 | 当前状态 / 所有者 |
| --- | --- | --- |
| GLM 缺终止事件 | 明确 source finish 的 Chat→Responses reasoning-only 映射已修复；缺真实 finish/DONE 不补造完成。旧标题请求还存在读取收尾/采集不完整可能，后续关闭屏障没有复现旧时序。 | 网关已修复可证明的转换缺口；历史异常唯一根因未知。 |
| GLM tool result `name` | 指定 glm-* Chat 目标拒绝非标准 tool-result name，适配移除此字段并保留 tool_call_id/顺序。 | 定向兼容；不全局删除其他目标的扩展字段。 |
| Gemini 检索偏选 | 早期 decoy 选择已发生在客户端生成的请求/模型调用之前；选中 schema 的传递有 translator 回归。 | 有证据部分是客户端/模型检索决策，未证明网关排名故障；网关不另建检索器。 |
| Gemini 名称碰撞 | 长名/字符替换后同名、canonical fallback 串名已复现并修复。 | 网关生产回归通过；最终指定 Gemini/Codex 编码闭环成立，名称碰撞真实场景和 Antigravity Code Mode 身份观察器未全面认证。 |
| Gemini / Claude 429 | 实际 upstream HTTP 429 有捕获；Codex 同声明模型有部分成功。 | Provider 响应可用性边界；配额条件、持续性及 thinking 因果未知。 |
| 冻结材料漂移 | 默认配置变更被用户确认是其他任务预期操作；另有 credential binding 内容变动导致本批拒绝。 | 运维/运行材料边界；不回退用户配置，不自动刷新、换账号或更换失败分母。 |
| Muse 空 custom 参数 | 实际函数 required input，模型返回 `{}`；网关 `upstream_contract`，零 MCP attempts/dispatch。原框架分类保留。 | 模型违反出站契约；新增固定响应诊断已回归，真实拒绝回执和 WS 诊断未新增 live 验证。 |
| Muse 一对多结果 | 目标要求每个 function_call 一个输出；auto choice 限制有实际 400 证据。 | 限用；默认有损路径明确拒绝。 |
| Lite 声明位置 | portable 提升到整请求作用域，身份和累计激活保护存在。 | 精确时序可见性未认证，没有 strict position 配置；不能写成位置无损。 |
| Grammar / flatten | grammar 仅描述、循环引用变宽。 | 显式兼容损失，原始参数校验由执行端负责。 |
| Hosted 工具 | final choice 声明存在性及 server identity 已校验。 | 真实授权、副作用、operator 注入和费用没有全量证据；MCP 零 dispatch 不覆盖 hosted。 |
| Provider 用量 | Gemini wrapper snapshots 有回退与修正；其他 route 的 cache-write 部分缺字段。 | 权威计量/实际账单未知，见 token 报告。 |
| 最终代码端到端覆盖 | 冻结候选 15 案结束、7 严格通过，1 planned 未开始；模式观察器、模型、上游 429 和预算限制分别记录。 | 有限实测，不认证未完成或未覆盖的模式、长任务和传输。 |
| 任务质量与长期净收益 | 固定非编码模板和单函数 coding pilot 有数据；代表性多文件、长会话、故障切换样本不足。 | 结论为有限实测；缺口不自动产生新测试设施或大规模抽样交付。 |

## 8. 证据范围与最小验证入口

有效运行结论同时依赖 revision、实际 route、客户端版本/build/mode、source/target 协议、transport、模型声明、工具/权限目录和采集契约。环境或材料变化使对应结论需要重新验证；有效策略与实测真实性是不同问题。

生产回归使用仓库现有工具即可运行，按具体改动选择必要范围：

```sh
GOTOOLCHAIN=go1.26.4 go test ./internal/responsestools ./sdk/cliproxy/auth ./test -count=1
GOTOOLCHAIN=go1.26.4 go test ./... -count=1
GOTOOLCHAIN=go1.26.4 go vet ./...
GOTOOLCHAIN=go1.26.4 go build -o /tmp/cliproxyapi-tool-loading ./cmd/server
```

命令验证生产实现，不修改默认配置或启动服务；全仓结果不能自动证明运行态客户端、模型或权限。没有代码变动时不以重复 Go 门禁替代文档校验。

原始配置、credential 路径、请求和响应正文仅留本机私有材料。公开报告使用数值、固定 revision、hash 和脱敏序号。当前报告没有补写历史用量、将失败移出分母或用新候选替换旧证据。

## 9. 来源与文档整合

本报告吸收整合前 acceptance、compatibility 和三份工具加载计划中的长期事实。流程指令、待办清单、重复状态汇报及过期远端状态不作为当前结论。

[整合前文档快照](https://github.com/hrygo/CLIProxyAPI/tree/743a5e0ae14d100b2c1c8f2101b93adfa420269e/docs)保留原报告、候选版本、失败和旧门槛；[实验代码快照](https://github.com/hrygo/CLIProxyAPI/tree/d9e64b8eff81414244703220d44d651d195ee428/ops/tool-loading)保留已移出的实施实验。Git 历史是旧原文入口，当前组三份报告是长期参考入口。

没有合并、发布、安装、默认配置切换、客户端升级或服务重启。
