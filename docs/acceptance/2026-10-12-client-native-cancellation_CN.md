# 官方客户端取消接口与 MCP 传播验收

核验日期：2026-10-12（Asia/Shanghai）。承接[运行限额与 CLI 取消报告](2026-10-12-client-runtime-limits_CN.md)，补充真正客户端 API 的取消证据，不覆盖原 CLI SIGINT 失败记录。

## 结论

**OpenCode `1.19.0` 的 session abort 在 eager/lazy 模式均通过本机受控取消场景；Codex CLI `0.160.0` 的 app-server turn/interrupt 能结束客户端 turn，但本场景仍未传播 MCP 取消通知。** 后者属于客户端 MCP 执行链的缺口，CLIProxyAPI 没有参与这些测试，也无法代替客户端向其本机 MCP 子进程发送取消。

MCP 服务使用官方 Python SDK `1.26.0`，服务协议版本为 `2025-11-25`。模型回复由临时本机 SSE fixture 控制，无真实 Provider 调用、无 token/账单/任务质量实测。结果仅覆盖指定版本、接口、stdio MCP 与单个已开始的 wait 调用；其它协议版本、HTTP MCP、多并发调用及取消后的新任务恢复尚未认证。

## 观测与通过条件

Codex 从已安装 CLI 生成 app-server JSON Schema，以其 v1 initialize、v2 thread/start、turn/start 与 turn/interrupt 契约实现独立 adapter。取消请求必须使用服务实际返回的 threadId/turnId；成功应答与匹配 interrupted 终态分别检查。未知的服务端请求只返回不支持，不自动审批。

OpenCode 使用现有隔离配置启动临时 `serve --pure --hostname 127.0.0.1`；只连接该子进程报告的本机监听地址。通过官方 `/session`、`prompt_async`、`abort`、status/message 接口操作新建会话。只有匹配 session 的 MessageAbortedError、idle 状态、abort=true 和 MCP 释放均具备时才通过；HTTP 200 或进程退出不能替代这些证据。[v1.19.0 API 契约](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/server/routes/instance/httpapi/groups/session.ts)、[abort handler](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts)

取消均在 MCP 实际 dispatch 后触发。fixture 在 stdio 接收端记录 `protocol_cancel_received`，随即将原消息交给官方 SDK，不修改通知、不合成回调事件。真实 SDK stdio 正例证明接收记录出现于 released 之前；callback cancelled/released 和最终计数仍独立核对。协议通知不存在时不把最后 active=1 猜成零，也不宣称进程退出后仍有资源泄漏。

## 原生结果与分母

| 批次 | 案例数 | 模型 HTTP 请求 | 接口终态 | MCP 取消 / 释放 | 边界判定 |
| --- | ---: | ---: | --- | --- | --- |
| Codex app-server 初测 eager/lazy | 2 | 3 | 两例 interrupt ack + interrupted | 两例均缺少 callback cancelled/released；收包探针尚未启用 | 0/2 |
| Codex app-server 收包复测 eager/lazy | 2 | 3 | 两例 interrupt ack + interrupted | 两例通知数 0；无 cancelled/released，最后 active=1 | 0/2 |
| OpenCode session abort eager/lazy | 2 | 3 | 两例 ack + matching MessageAbortedError + idle | 每例通知数 1，随后 cancelled/released，active=0、effects=0 | 2/2 |
| Codex app-server 普通调用正例 eager/lazy | 2 | 5 | 两例 completed | 每例一次 dispatch，nonce 实际回填，active=0、effects=0 | 2/2 |
| 最终 adapter 的 OpenCode abort 复测 eager/lazy | 2 | 3 | 两例取消终态完整 | 每例一次通知与释放，active/effects=0 | 2/2 |
| 最终 adapter 的 Codex 普通调用复测 eager/lazy | 2 | 5 | 两例 completed | nonce 实际回填，active/effects=0 | 2/2 |

共 12 个已开始原生诊断案例、22 个收到的模型 HTTP 请求；客户端控制 API 请求另存于各例日志，不计为 Provider 请求。初测与复测失败均保留，不能用后续模式成功替换分母；这些重复诊断不是正式跨 Provider 60-case 样本。Codex 普通调用正例证明 adapter 能完成执行、回填和正常终态，不能证明取消传播。

Codex 两次取消批次均等待完整 20 秒任务预算；客户端已结束 turn 时，MCP wait 回调仍没有释放记录。OpenCode 初测约 2 秒内满足取消与释放条件，随后验收器终止临时 server（进程退出码 -15）。该进程退出码不是失败依据：取消边界在进程清理前已经记录完整；整体 task_success_verified 和 live gate 仍为未知。

忽略目录 `.omo/tool-loading-completion/` 保存 `t5-app-cancel-initial/`、`t5-app-cancel-wire/`、`t5-server-cancel-initial/`、`t5-app-positive/`、`t5-server-cancel-final/`、`t5-app-positive-final/`。每例保存 case-started、模型请求/回复、MCP 事件、app-rpc-sent 或 server-http、客户端输出及摘要。原始内容不提交仓库；完整不可变证据导出仍属于 T6 待办。

## Codex 客户端修复边界

指定 [rust-v0.160.0 RmcpClient 源码](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/rmcp-client/src/rmcp_client.rs)显示：兼容协议的 tools/call 使用 send_request_with_option 后 await_response；事件流入口使用 send_cancellable_request。[connection manager](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/codex-mcp/src/connection_manager.rs)把 tool timeout 传给 RmcpClient；其 operation wrapper 通过 future 等待/超时控制调用。

结合收包实测，**推断**修复应从 MCP tools/call 的 request handle 生命周期入手：将外层 turn 取消或 future drop 绑定到实际 requestId 的 cancel-on-drop/显式取消机制，而非只结束 UI turn。具体 Rust 补丁尚未编译验证；不能仅替换 API 名称就宣布修复。

客户端独立回归须覆盖：dispatch 后 turn/interrupt、future drop、现有 tool timeout、响应与取消竞态、并发不同 requestId、正常完成不发送取消、收到 MCP cancel 后零 active/零副作用，以及取消后新任务可运行。不得重放已开始的有副作用 tools/call，也不得由 Gateway 代发客户端 MCP 控制消息。新协议 tools/call input-required 路径应单独验收。

## 当前工程验证与剩余工作

新鲜离线检查通过 **84 tests**：新增 app-server 身份/应答/终态负例、未发出的 RPC 应答与空身份拒绝、OpenCode abort ack/状态/会话错误负例，以及真实 MCP SDK stdio 取消观测回归。三个实现入口都有先失败后通过的测试日志；原生 Codex 取消的失败是被保留的产品缺口，不被离线绿灯掩盖。本轮没有 Go 源码变化，未重跑旧 Go 门禁。

后续继续 Codex 客户端修复交付、OpenCode host/SDK 执行预算缺口、权限与索引 generation 变更、新任务恢复，以及 Gateway attempt/usage/证据整合；最终 60-case、coding 质量非劣和上下文/成本收益区间仍未达成。没有发布、安装候选、修改默认配置/catalog 或重启原服务。
