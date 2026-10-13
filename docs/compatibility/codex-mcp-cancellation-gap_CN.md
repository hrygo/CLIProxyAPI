# Codex 0.160.0 Code Mode MCP 取消传播缺口

状态：**待实施的客户端交付方案，不是已修复补丁。** 本文只界定 Codex 客户端的候选修复路径与验收条件，不代表已有 Rust 改动、编译结果或取消修复认证。

核验日期：2026-10-13（Asia/Shanghai）。目标客户端为 Codex CLI `0.160.0`，Rust 源码 ref 为 `rust-v0.160.0`。本地缓存 `.omo/tool-loading-completion/client-sources/codex-v0160-tree.json` 的顶层 `sha` 为 `a956835d020762cb2b570053af06f643a11c0ecc`，这是 tree SHA。原缓存只含选定摘录；本轮在 `client-sources/codex-v0160-extra/` 补充实际调用链及 RMCP 源码，仍不是完整 Codex checkout。网页文本缓存与 Git blob 原字节分别记录，不能把文本行号完整误称为 byte-exact。

新增来源、原始行号、Git blob 与本地 SHA-256 见[缓存清单](../../.omo/tool-loading-completion/client-sources/codex-v0160-extra/MANIFEST.md)；RMCP 版本来自[锁文件 stanza](../../.omo/tool-loading-completion/client-sources/codex-v0160-extra/codex-rs__Cargo.lock__rmcp-3.2.0-entry.txt)。完整 lock 文本副本有字节差异，不能据此宣称整个 checkout 完整或逐字匹配。

## 已有失败证据

[客户端原生取消验收](../acceptance/2026-10-12-client-native-cancellation_CN.md)和[Code Mode 验收](../acceptance/2026-10-13-codex-code-mode_CN.md)记录了两例 Codex app-server Code Mode 取消：两例均收到 `turn/interrupt` 成功应答并进入匹配的 `interrupted` 终态，但 MCP 取消通知为 `0/2`，也没有 `cancelled` / `released` 回调。两例都等待了完整的 20 秒任务预算；任务及进程清理前的最后 MCP 记录仍为 `active=1`。

`active=1` 是清理前最后一次协议观测，不能表述成“杀掉进程后仍泄漏”，也不证明清理后仍有资源泄漏。OpenCode 的本机 abort 对照在相同验收框架中记录到取消通知、释放和 `active=0`。这些案例没有经过 CLIProxyAPI 或真实 Provider；它们只证明指定客户端版本、app-server 取消接口与本机 stdio MCP fixture 下的行为。

## 源码边界

以下源码证据来自上述 pinned 缓存，并由树 manifest 的路径/blob SHA 对应到 Codex tree；主代理已对图谱未收录的缓存文件做源码回退核验。版本固定链接：[`delegate.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/code_mode/delegate.rs)、[`execute_handler.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/code_mode/execute_handler.rs)、[`mcp_tool_call.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/mcp_tool_call.rs)、[`binding.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/codex-mcp/src/binding.rs)、[`connection_manager.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/codex-mcp/src/connection_manager.rs)、[`rmcp_client.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/rmcp-client/src/rmcp_client.rs)、[`tool_input.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/rmcp-client/src/tool_input.rs)。源码定位、网页可读与编译认证是不同证据。

已能确认的路径和断点：

- `CodeModeCellDelegate::invoke_tool` 接收 `CancellationToken`，将其放入 nested dispatch；`CoreTurnHost::submit_tool` 再将 token 传给 `submit_nested_tool`。worker 的取消分支目前仍执行 `invocation.await`，所以单改此分支为丢弃 future 不能证明底层 MCP request 已收到取消，也可能丢掉清理机会。
- 新增源码显示 `tasks/mod.rs:880–913` 在 task abort 时取消 task token；`CodeModeInterrupt` 生效时调用 `interrupt_active_cells`。Code Mode `submit_nested_tool` 将 token 传到 `ToolCallRuntime`，MCP handler 再传给 `handle_mcp_tool_call`。因此“没有 execute 参数就没有取消通路”不能作为结论。
- `handle_mcp_tool_call` 接收 `CancellationToken`，并把它传给审批逻辑。审批后的 `handle_approved_mcp_tool_call` 签名没有 token，调用 `PreparedMcpCall::call_with_preparation` 时也未传 token。这是摘录中可见的 MCP dispatch 取消传播断点。
- 新增 `binding.rs:279–350` 显示，主路径由 `PreparedMcpCall` 在 captured catalog/permission authority 下完成准备及 timeout 计算，再直接调用其 managed client's `RmcpClient::call_tool`；不能把 `ConnectionManager::call_tool` 当作这一主路径的必经节点。该 legacy/general 入口仍须按真实调用点单独复核。
- `RmcpClient::call_tool` 的兼容路径取得 request handle 后直接 `await_response`；现代 `tool_input::call_tool` 逐轮保存 handle ID 后同样等待。两条路径均没有 turn cancellation token 参数。
- `parallel.rs:235–247` 在取消时会 `dispatch_handle.abort()` 并等待 task 退出。这会 drop handler future；只在 handler 内增加 token `select!` 不保证它能在外层 abort 前执行取消和清理，必须覆盖 future drop/abort 的请求生命周期。
- pinned `Cargo.lock` 的 RMCP stanza 为 **3.2.0**。该版本 `RequestHandle::await_response(self)` 与 `cancel(self, reason)` 都消费同一个 handle；`cancel` 用 handle 自己的真实 request ID 发 `notifications/cancelled` 并清理 subscription/progress watcher，server 侧按同 ID 取消 callback token。不能把两个消费同一值的方法直接放进 `select!`，也不能假定 drop 自动发协议取消。

因此，证据支持首先修通客户端自己的 MCP request 生命周期；关键调用点和依赖取消 API 已从新增源码确认。仍需设计并编译验证 abort/drop 安全的 request owner，保持原有 timeout、进度 watcher、session recovery、input-required proof 与响应/取消竞态语义。以上断点是源码证据；哪个断点构成 2/2 实测失败的唯一根因尚未由编译后的补丁证明。

## 候选改动范围

客户端补丁应从调用链最窄处开始传递 token，不改变网关协议或代发客户端通知：

1. `codex-rs/core/src/mcp_tool_call.rs`：两处获准调用点均把 token 传入 `handle_approved_mcp_tool_call`，再传到 `PreparedMcpCall`。取消已先到时，应在不可逆准备和 dispatch 前拒绝；保留原有审批/权限 authority。
2. `codex-rs/codex-mcp/src/binding.rs`：在实际 `PreparedMcpCall::call_with_preparation` 边界接入取消上下文并向其 managed client 传递；保留 captured catalog、trusted access context 和 server/tool timeout。`ConnectionManager::call_tool` 及其他 caller 按完整 checkout 的真实调用点复核，而不是只改这一入口。
3. `codex-rs/rmcp-client/src/rmcp_client.rs`：在实际 `tools/call` request handle 上实现请求级取消，并由可跨 caller abort/drop 完成清理的 owner 持有；发送通知必须使用该次已发出请求的真实 JSON-RPC ID。正常响应先到时不得发迟到取消，不能为取消 A 关闭共享 peer 而影响 B。保留响应解析、timeout/progress 清理和 session recovery。
4. `codex-rs/rmcp-client/src/tool_input.rs`：现代 input-required 调用有逐轮请求及输入处理，需单独把取消绑定到当前活跃轮次的 request ID，并释放该轮未完成的提示/处理器；不得重放已提交的有副作用调用或 proof。
5. 复核 `codex-rs/core/src/tools/code_mode/delegate.rs` 的 `CodeModeDispatchWorker` 取消分支，以及 `codex-rs/core/src/tools/code_mode/execute_handler.rs` 到 `CodeModeService::execute` 的 turn 生命周期。先由完整源码确认 token 来源与 worker 等待语义，再决定是否改动；不能把“让 delegate 立即返回错误”当成已取消 MCP request。

底层 RMCP 已提供 `RequestHandle::cancel`，应优先复用其真实 ID 和清理行为；但它不等价于已提供 caller token 或 cancel-on-drop。实现需要先解决 handle 所有权、完成竞态和外层 abort，再决定等待封装。不得只按名称替换 `send_request_with_option`。

## 客户端回归矩阵

所有取消案例都应从实际 MCP dispatch 后触发，并独立观测 app-server 终态、协议通知、callback 生命周期与副作用。MCP 取消通知必须来自被测 Codex 客户端；fixture 只记录并把原消息交给官方 SDK，不合成取消或释放事件。

| 场景 | 必须核对的结果 |
| --- | --- |
| 普通成功对照 | 单次 dispatch 正常返回；不得发送取消通知；正常 completion 与回执真实匹配。 |
| Code Mode 中途 `turn/interrupt` | 使用服务真实返回的 `threadId` / `turnId`；请求进入 matching `interrupted`；收到 MCP 取消通知，且 `requestId` 与仍活跃的原 `tools/call` request ID 完全一致。 |
| 取消后的释放 | fixture 记录取消通知后，官方 SDK 回调出现 `cancelled`、`released`；在客户端/fixture 清理前核对 `active=0`、`effects=0`。通知或回调缺失即失败，不以进程退出推断释放。 |
| 响应与取消竞态 | 对每个 request ID 只接受一个真实终态；响应先完成时不发迟到取消，取消先胜出时不再把后来的结果伪装成成功 completion。重复通知、重复回执或跨 ID 取消均失败。 |
| 并发不同 request ID | 取消 A 只能通知 A 的实际 request ID；B 仍可正常完成；分别核对 A/B 的回调、释放和副作用计数。 |
| future drop 与既有 timeout | 在调用已 dispatch 后分别触发调用 future drop 和原有 timeout；核对请求级取消、原 timeout 语义、最终释放，且不得重放调用。 |
| input-required | 在每轮等待中取消；通知只对应当前活跃轮次 ID，输入提示与 timeout pause guard 释放；已提交的 proof 或有副作用调用不能重放。 |
| 取消后恢复 | 同一客户端取消并确认释放后，可开始新的独立任务并完成；不得把前一请求的晚到结果回填到新任务。 |

失败路径不得伪造 `completed` / 成功回执，也不得把 HTTP 200、interrupt ack、UI turn 结束或进程清理当成 MCP 取消和释放的替代证据。

## 网关边界与交付条件

不能只在 CLIProxyAPI 网关修复：失败复现没有经过网关；Codex 进程持有自己的 MCP peer、stdio 子进程和 `tools/call` JSON-RPC request ID。网关可以影响它发起的模型请求或响应流，却没有被测客户端本机 MCP peer 上的 request handle，无法替客户端向该连接发送正确 request ID 的取消通知。网关代发还会混淆协议连接与调用身份。因此，这项修复应作为 Codex 客户端交付单独实现和验收；网关不得代替客户端发送 MCP 控制消息。

开始实现前仍需完整 `rust-v0.160.0` checkout 和可用 Rust toolchain，以核对所有 caller 并编译；本轮新增缓存已确认主调用点、RMCP 版本和 request-handle 取消 API。当前 PATH 及 `~/.cargo/bin`、Homebrew、`/usr/local/bin` 的常见路径未发现可用 cargo/rustc，未安装工具链或升级客户端。补丁完成后需通过 pinned Rust 单测/构建，再运行本机 fixture 原生 app-server Code Mode 取消及成功对照，证明通知 ID 匹配、清理前释放、并发隔离、竞态与恢复。未完成前，状态保持待实施/未认证。
