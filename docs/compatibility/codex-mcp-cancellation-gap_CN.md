# Codex 0.160.0 Code Mode MCP 取消传播缺口

状态：**隔离客户端候选已有局部实现及回归证据，原生 Code Mode 取消仍失败，未交付为已修复客户端。** T25 曾在任务目录修补并构建 Codex；该支线停止扩展，实施重新聚焦 CLIProxyAPI。未安装或替换现有客户端。

**2026-10-13 用户明确的交付边界：** 实际使用官方发行的 Codex CLI、Claude Code 等客户端，不走自建、源码修补或维护客户端 fork 的路线。下文源码候选与回归矩阵保留为历史诊断及外部上游参考，不是本任务后续执行计划。客户端缺口以兼容限制、责任边界和外部依赖报告交付；仅通过现有受支持能力接入，不将自建客户端作为验收前置。默认配置和原服务继续保持不变。

核验日期：2026-10-13（Asia/Shanghai）。目标客户端为 Codex CLI `0.160.0`，Rust 源码 ref 为 `rust-v0.160.0`。更正此前 provenance：缓存 `.omo/tool-loading-completion/client-sources/codex-v0160-tree.json` 的顶层 `sha` 为 **commit SHA** `a956835d020762cb2b570053af06f643a11c0ecc`，实际 tree SHA 为 `c09968bc06fc543c9d8bd9ffbaaa72829b31f5d9`。T25 已取得完整官方 checkout，位于任务私有的 `client-sources/codex-v0160-checkout/`；原有摘录与网页文本缓存仍不能误称为 byte-exact 完整源码。

新增来源、原始行号、Git blob 与本地 SHA-256 见[缓存清单](../../.omo/tool-loading-completion/client-sources/codex-v0160-extra/MANIFEST.md)；RMCP 版本来自[锁文件 stanza](../../.omo/tool-loading-completion/client-sources/codex-v0160-extra/codex-rs__Cargo.lock__rmcp-3.2.0-entry.txt)。完整 lock 文本副本有字节差异，不能据此宣称整个 checkout 完整或逐字匹配。

## 已有失败证据

[客户端原生取消验收](../acceptance/2026-10-12-client-native-cancellation_CN.md)和[Code Mode 验收](../acceptance/2026-10-13-codex-code-mode_CN.md)记录了两例 Codex app-server Code Mode 取消：两例均收到 `turn/interrupt` 成功应答并进入匹配的 `interrupted` 终态，但 MCP 取消通知为 `0/2`，也没有 `cancelled` / `released` 回调。两例都等待了完整的 20 秒任务预算；任务及进程清理前的最后 MCP 记录仍为 `active=1`。

`active=1` 是清理前最后一次协议观测，不能表述成“杀掉进程后仍泄漏”，也不证明清理后仍有资源泄漏。OpenCode 的本机 abort 对照在相同验收框架中记录到取消通知、释放和 `active=0`。这些案例没有经过 CLIProxyAPI 或真实 Provider；它们只证明指定客户端版本、app-server 取消接口与本机 stdio MCP fixture 下的行为。

## 源码边界

T25 的新增证据来自固定 commit 的完整 checkout 和实际编译使用的 RMCP 3.2.0 registry 源码；原有摘录只是历史定位依据。以下断点仍须区分局部源码事实与原生 interrupt 的实测责任。

以下源码证据来自上述 pinned 缓存，并由树 manifest 的路径/blob SHA 对应到 Codex tree；主代理已对图谱未收录的缓存文件做源码回退核验。版本固定链接：[`delegate.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/code_mode/delegate.rs)、[`execute_handler.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/code_mode/execute_handler.rs)、[`mcp_tool_call.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/mcp_tool_call.rs)、[`binding.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/codex-mcp/src/binding.rs)、[`connection_manager.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/codex-mcp/src/connection_manager.rs)、[`rmcp_client.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/rmcp-client/src/rmcp_client.rs)、[`tool_input.rs`](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/rmcp-client/src/tool_input.rs)。源码定位、网页可读与编译认证是不同证据。

已能确认的路径和断点：

- `CodeModeCellDelegate::invoke_tool` 接收 `CancellationToken`，将其放入 nested dispatch；`CoreTurnHost::submit_tool` 再将 token 传给 `submit_nested_tool`。worker 的取消分支目前仍执行 `invocation.await`，所以单改此分支为丢弃 future 不能证明底层 MCP request 已收到取消，也可能丢掉清理机会。
- 新增源码显示 `tasks/mod.rs:880–913` 在 task abort 时取消 task token；`CodeModeInterrupt` 生效时调用 `interrupt_active_cells`。Code Mode `submit_nested_tool` 将 token 传到 `ToolCallRuntime`，MCP handler 再传给 `handle_mcp_tool_call`。因此“没有 execute 参数就没有取消通路”不能作为结论。
- `handle_mcp_tool_call` 接收 `CancellationToken`，并把它传给审批逻辑。审批后的 `handle_approved_mcp_tool_call` 签名没有 token，调用 `PreparedMcpCall::call_with_preparation` 时也未传 token。这是摘录中可见的 MCP dispatch 取消传播断点。
- 新增 `binding.rs:279–350` 显示，主路径由 `PreparedMcpCall` 在 captured catalog/permission authority 下完成准备及 timeout 计算，再直接调用其 managed client's `RmcpClient::call_tool`；不能把 `ConnectionManager::call_tool` 当作这一主路径的必经节点。该 legacy/general 入口仍须按真实调用点单独复核。
- `RmcpClient::call_tool` 的兼容路径取得 request handle 后直接 `await_response`；现代 `tool_input::call_tool` 逐轮保存 handle ID 后同样等待。两条路径均没有 turn cancellation token 参数。
- `parallel.rs:235–247` 在取消时会 `dispatch_handle.abort()` 并等待 task 退出。这会 drop handler future；只在 handler 内增加 token `select!` 不保证它能在外层 abort 前执行取消和清理，必须覆盖 future drop/abort 的请求生命周期。
- pinned `Cargo.lock` 的 RMCP stanza 为 **3.2.0**。该版本 `RequestHandle::await_response(self)` 与 `cancel(self, reason)` 都消费同一个 handle；`cancel` 用 handle 自己的真实 request ID 发 `notifications/cancelled` 并清理 subscription/progress watcher，server 侧按同 ID 取消 callback token。不能把两个消费同一值的方法直接放进 `select!`，也不能假定 drop 自动发协议取消。

这些源码断点属于客户端内部；哪个断点构成 2/2 实测失败的唯一根因仍未证明，也未排除 feature 配置条件。若最终需要改变 MCP request 生命周期，应由客户端官方项目解决。本任务保留诊断，不继续设计或编译客户端补丁。

## 历史候选改动范围（停止实施）

客户端补丁应从调用链最窄处开始传递 token，不改变网关协议或代发客户端通知：

1. `codex-rs/core/src/mcp_tool_call.rs`：两处获准调用点均把 token 传入 `handle_approved_mcp_tool_call`，再传到 `PreparedMcpCall`。取消已先到时，应在不可逆准备和 dispatch 前拒绝；保留原有审批/权限 authority。
2. `codex-rs/codex-mcp/src/binding.rs`：在实际 `PreparedMcpCall::call_with_preparation` 边界接入取消上下文并向其 managed client 传递；保留 captured catalog、trusted access context 和 server/tool timeout。`ConnectionManager::call_tool` 及其他 caller 按完整 checkout 的真实调用点复核，而不是只改这一入口。
3. `codex-rs/rmcp-client/src/rmcp_client.rs`：在实际 `tools/call` request handle 上实现请求级取消，并由可跨 caller abort/drop 完成清理的 owner 持有；发送通知必须使用该次已发出请求的真实 JSON-RPC ID。正常响应先到时不得发迟到取消，不能为取消 A 关闭共享 peer 而影响 B。保留响应解析、timeout/progress 清理和 session recovery。
4. `codex-rs/rmcp-client/src/tool_input.rs`：现代 input-required 调用有逐轮请求及输入处理，需单独把取消绑定到当前活跃轮次的 request ID，并释放该轮未完成的提示/处理器；不得重放已提交的有副作用调用或 proof。
5. 复核 `codex-rs/core/src/tools/code_mode/delegate.rs` 的 `CodeModeDispatchWorker` 取消分支，以及 `codex-rs/core/src/tools/code_mode/execute_handler.rs` 到 `CodeModeService::execute` 的 turn 生命周期。先由完整源码确认 token 来源与 worker 等待语义，再决定是否改动；不能把“让 delegate 立即返回错误”当成已取消 MCP request。

底层 RMCP 已提供 `RequestHandle::cancel`，应优先复用其真实 ID 和清理行为；但它不等价于已提供 caller token 或 cancel-on-drop。实现需要先解决 handle 所有权、完成竞态和外层 abort，再决定等待封装。不得只按名称替换 `send_request_with_option`。

## 客户端回归矩阵（历史诊断参考）

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

不能只在 CLIProxyAPI 网关修复：失败复现没有经过网关；Codex 进程持有自己的 MCP peer、stdio 子进程和 `tools/call` JSON-RPC request ID。网关可以影响它发起的模型请求或响应流，却没有被测客户端本机 MCP peer 上的 request handle，无法替客户端向该连接发送正确 request ID 的取消通知。网关代发还会混淆协议连接与调用身份。因此，该项列为客户端兼容边界及潜在外部上游依赖，不在本任务实现或交付客户端补丁；网关不得代替客户端发送 MCP 控制消息。官方发行版本及受支持配置能否满足取消要求尚未认证，不能用自建候选代替。

T25 在任务目录准备了隔离 Rust 1.95.0、just 和 nextest；未修改 shell profile、系统 PATH 或已安装客户端。候选实现把获准 MCP 调用的取消上下文接到 `PreparedMcpCall`，并由可跨 caller drop 存活的 owner 持有真实 RMCP request handle。Legacy、modern、已有 timeout、并发隔离和 pending input 清理取得局部证据，但这不等于原生 Code Mode interrupt 已修复。

## T25 的结果与收束

- 底层失败复现：原有等待方式下成功对照通过，abort 后取消等待超时；有效基线为 1 passed / 1 failed。之前两次测试准备/断言问题单列保留。
- 两个修改 crate 的最终 scoped 回归为 **483 passed / 1 skipped**；三 crate 的 scoped `just fix` 返回 0。`just fmt` 的 Rust formatter 完成，但整体因缺 `dotslash` 返回 1，不称全格式门禁通过。core 的新增 interrupt 集成用例完成编译，但环境的网络标志使其提前跳过，不能计为真实协议通过。未运行 Codex 全工作区测试。
- 隔离 CLI 构建成功。Code Mode host 的源码构建失败：固定 V8 150.4.0 的 Apple arm64 ptrcomp/sandbox prebuilt URL 返回 404。首次四个 app-server 案例均因缺 companion binary 而无 MCP dispatch，失败保留。
- 随后只在任务目录复制现有 `0.160.0-aarch64-apple-darwin` 包中未改动的 companion，SHA-256 为 `679eedaea70529aa1cffc9bc0a0788c186412663544fa76c09d63b57f383a65a`。包 manifest 可确认版本及 target，不能确认该 companion 的 commit；这一混合制品边界明确保留。
- 完整运行包下四例复测：两个成功对照通过，各有一次真实 MCP dispatch 并释放；两个取消案例仍为 interrupt ack 与 `interrupted`，取消通知 **0/2**，清理前最后 `active=1`，均耗尽 20 秒预算。**不能称客户端取消缺口已闭合。** 原始记录见[本轮汇总](../../.omo/tool-loading-completion/t25-codex-cancel-v2/summary.json)。这些案例没有经过网关或真实 Provider，没有新增上下文收益证据。
- 新确认的配置边界：官方 `features/src/lib.rs` 将 `code_mode_interrupt` 标为 `UnderDevelopment` 且默认 `false`；`core/src/tasks/mod.rs` 只在该 feature 启用时调用 `interrupt_active_cells`。本轮 fixture 未显式启用它，因此仍未排除配置条件，不能把上述失败唯一归因于 request handle 断点。未修改默认配置，也未继续扩大客户端实现。

本轮源码构建曾用于验证客户端候选修复能否改变实际取消行为；它不是 CLIProxyAPI 实现的必需步骤，扩大到客户端实现偏离实际交付范围。用户已明确排除自建客户端路线，该支线停止；保留隔离源码、制品和失败证据，客户端问题作为兼容边界报告。网关生产修复继续在 CLIProxyAPI 内实施。
