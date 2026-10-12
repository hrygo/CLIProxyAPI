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

## 在线验收状态

待在固定干净候选上，用 Qwen 与 GLM 的原三场景、同一预算、同一 gold 完成两客户端 eager/lazy 配对。此前普通输出 profile 的失败和成本保留，不将不同 profile/revision 混合为通过率。地域、缓存、账单和正式统计未验证。

本轮没有 Go 源码修改；候选构建用于固定真实 SDK revision，不代表完整 Go gate 重新运行。未发布、未改默认配置/catalog、未重启原服务。剩余原生路由、最终 60 案例、代表性 Coding、取消/权限生命周期和请求预算拒绝的精确诊断继续按完整目标推进。
