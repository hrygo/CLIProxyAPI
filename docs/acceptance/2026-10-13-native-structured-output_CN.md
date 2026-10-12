# 原生结构化输出验收配置

核验日期：2026-10-13。承接[显式标题与跨路由报告](2026-10-13-explicit-title-profile_CN.md)，继续执行[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。这是独立、可选的输出 profile；尚未通过最终 60 案例及 Coding 认证。

## 原生控制与语义

当前 Codex CLI `0.160.0` 的 `exec --help` 支持 `--output-schema`；客户端实际首包携带 `text.format.type=json_schema` 和原始 schema。OpenCode `1.19.0` 的 CLI `--format json` 是事件编码；其 session API 支持 `format.type=json_schema`，向模型提供 `StructuredOutput` 和 `tool_choice=required`。schema 只声明答案字段和类型，不提供正例预算 424242、nonce 或 confirmation receipt。

新增 `live_clients.py --structured-output`，两模式采用同一输出控制，原 prompt 和硬 gold 不变。控制开关、native API adapter 源码及新增入口许可参与 fixture/permission 指纹与 pair ID；默认 CLI 配置不变。Codex 写私有 schema 文件；OpenCode 在私有 Git workspace/XDG 状态中启动临时服务，创建 fresh session，通过唯一同步 prompt 请求直接取该次原生 assistant 响应。

OpenCode 验收要求：session 匹配、assistant 有 parent ID 与完成时间、合法 finish、session idle、schema 合法、恰好一个完成的 `StructuredOutput` 调用且其输入等于 `info.structured`。实际 MCP dispatch、receipt 使用、多轮回填、全部上游终态和资源释放另行验证。不会取更早答案、剥离 fence 后伪装格式通过或制造 JSON 结果。服务正常 SIGTERM 退出与任务完成分开，不能把正常完成标成用户中断完成。

## 已确认的客户端缺口

先采用 OpenCode 的 `prompt_async` 再 `GET /session/<id>/message`，两模式均返回 HTTP 400：`Expected OutputFormatJsonSchema, got ... at [0]["info"]["format"]`。源码中的 Format 是 Schema.Class union；错误发生在本机服务对用户 format 对象的响应编码，不能归为 Provider 检索失败或 Gateway 格式丢失。

证据 `t6-native-structured-output-probe/` 的 `server-http.jsonl` 显示 POST 204，随后读取消息 400；eager 尚无模型请求，lazy 已有一个模型请求。该客户端历史读取缺口未修复、未修改安装的 OpenCode。同步 prompt API 则直接返回实际 assistant，避免依赖这条有缺陷的历史读取路径。它是独立 API 配置，不据此认证任意会话恢复或完整历史 API。

## 本地验证

- 四个真实客户端首包探针均观察到正确 eager/lazy 与原生输出约束；证据 `t6-native-structured-output-sync-probe/`。本地模型端故意返回 HTTP 400，无 Provider 推理、无 token/成本收益结论。
- 真实 OpenCode API + MCP + 脚本 loopback 模型，两模式均实际 dispatch 一次、完成/释放、active/effect 0；从实际工具结果获得新 nonce 后调用 `StructuredOutput`。eager 两请求、lazy 三请求，答案/硬格式/终态通过。证据 `t6-native-structured-task-scripted/`；早期结果误报 `interrupt_verified=true` 已保留，之后新增确定性 HTTP 服务回归复现并修复。最终代码独立复测 `t6-native-structured-task-final/`，两模式均 `interrupt_acknowledged=false`、`interrupt_verified=false`、无强制清理，实际 nonce 与 dispatch 验证通过。
- 回归覆盖默认控制不变、schema 私有与深复制、无引用/严格 schema 发送前校验、profile 分离与漂移零启动、原生结果/gold/终态，以及中断与正常完成区分。全套 165 项通过（10.459 秒），日志 `t6-native-structured-final-green.log`；正常完成误报中断的失败回归保留于 `t6-native-structured-interrupt-red.log`。

## 在线验收：首个原生输出配置

固定候选 `732e73a12e903acb01e8f87dbc374a4944a78766`，Go 1.26.4、`vcs.modified=false`。Qwen/GLM 各 12 案例：两客户端 × 三场景 × eager/lazy；同一 gold、90 秒任务预算、每例最多 8 个物理 HTTP 请求/8 个 dispatch，预先锁定 batch plan，显式固定 OpenCode 标题。两批均已结束并返回 exit 1，失败全部保留；所有 24 候选退出 0、源配置未变、forbidden/effect/active 均 0。

| 路由 | 启动/完成 | 严格成功 | HTTP / dispatch | input usage 不完整案例 |
| --- | ---: | ---: | ---: | ---: |
| Qwen / `deepseek-v4.1-flash` | 12 / 12 | 5 / 12 | 23 / 7 | 6 |
| OpenCode Go / `glm-5.3-flash` | 12 / 12 | 7 / 12 | 17 / 4 | 0 |

两个 journal/plan 均验证通过，无未开始或未完成案例，预算在锁定范围内。实际出站：Codex 共 12 请求全部携带 `response_format.type=json_schema`；OpenCode 共 28 请求全部携带 `StructuredOutput` 和 `tool_choice=required`；未发现仓库 AGENTS 的 `全局工作约定` 标记。这是线包观察，不能代替模型侧内部序列化证据。

| 路由 / 客户端 | eager 成功 | lazy 成功 | 首轮减少 | 峰值减少 | 全任务减少 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Qwen / Codex | 0 / 3 | 0 / 3 | 未知 | 未知 | 未知 |
| Qwen / OpenCode | 2 / 3 | 3 / 3 | 90.97% | 77.07% | 74.20% |
| GLM / Codex | 1 / 3 | 1 / 3 | 77.86% | 77.86% | 77.86% |
| GLM / OpenCode | 3 / 3 | 2 / 3 | 91.17% | 91.11% | 91.11% |

降幅为三个模板配对的中位数，失败成本保留。**低输入不代表有效收益**：GLM Codex 四个正例及 OpenCode 多工具 lazy 没有 MCP dispatch，输入少主要因为任务未做；Qwen Codex 全部被拒收，没有 usage，不能用 0 计算节省。三个模板不足以产生有限的 95% 收益下界；条件质量下界为无回退组 −63.16pp、GLM OpenCode −86.46pp，均不能通过 −2pp 门槛，抽样假设本身也未认证。地域、缓存、账单和正式统计保持未知。

责任边界：

- Qwen Codex 六个请求均由实际上游 HTTP 400 拒收，`invalid_parameter_error`、`This response_format type is unavailable now`。网关实际发送了 JSON-schema 格式；该路线不能靠开启 Codex `--output-schema` 修复，不能静默降级为普通文本并冒称约束保留。
- GLM Codex 四个正例 JSON 格式合规、终态合法但零 dispatch；OpenCode 多工具 lazy 也是零 dispatch，属于当前模型/输出配置下的任务失败。不能归为 MCP 拒绝或 Gateway 断流，也不能只凭一次观察证明模型永久不支持工具。
- Qwen OpenCode lookup eager 实际读取两次，答案/nonce/格式/终态通过，但违反预先锁定的恰好一次 dispatch 规则；原判定保留，不事后放宽 gold。

证据目录为 `.omo/tool-loading-completion/t6-native-structured-{qwen,glm}-live/`，终态复核为同前缀 `*-validation.json`、`*-paired-analysis.json`。此前普通输出 profile 的失败和成本保留，不混合不同 profile/revision 为通过率。

## 输出 schema 的语义审查

上述首个 profile 对 unavailable 的 `status`/`project` 使用单值 enum，直接约束为期望答案。原 prompt 本已提供无适用工具时的答案格式，因此不是泄露秘密回执；但 schema 使错误的语义答案无法被观测，不能据这批负例证明模型正确判断无可用工具。已新增失败回归：错误状态/项目应通过格式校验、被独立 gold 判错；随后去掉两个单值 enum，只保留非空字符串类型。原结果不改写，新源码和 fixture 指纹须单独固定、重新验收，不作为同版本补测合并。

失败回归 `t6-native-schema-negative-red.log` 已复现，修复后全套 166 项通过（11.014 秒），日志 `t6-native-schema-negative-green.log`。下一批只复测已能接收原生输出的 OpenCode 两条路线，预先保留三场景 eager/lazy、同一 gold 与预算；Codex 的格式不支持/任务失败证据保留，不反复启动已知不兼容配置以累加样本。

本轮没有 Go 源码修改；候选构建用于固定真实 SDK revision，不代表完整 Go gate 重新运行。未发布、未改默认配置/catalog、未重启原服务。剩余原生路由、最终 60 案例、代表性 Coding、取消/权限生命周期和请求预算拒绝的精确诊断继续按完整目标推进。
