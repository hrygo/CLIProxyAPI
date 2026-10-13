# T28：hosted choice 网关修复与官方 Codex 取消边界

核验日期：2026-10-13（Asia/Shanghai）。网关生产提交 `42b3d5da8c314322600ce1f6270a896c3a8fbf58`，基于 PR #25 已同步的 `d9e64b8e`。本轮只做本地修复与验收；没有发布、安装、切默认配置、刷新凭据或重启原服务。

## 网关已修复：强制选择必须有最终声明

Claude 延迟工具的最终发送契约原来只检查普通函数 choice。后置 payload 规则若强制选择 `web_search`，而最终 `tools` 中没有对应声明，仍会发到上游。声明可以一开始就不存在，也可以被后置规则删除；`allowed_tools` 内的 hosted 选择也有相同缺口。

修复放在既有 `validateExactOutboundToolChoice`：对当前启用严格函数集合的请求，直接 choice 和 `allowed_tools` 中的 hosted 类型必须有同一最终请求中的有效类型声明。带 `server_label` 的 hosted MCP choice 还须匹配声明的 server。错误本地返回 422；不为缺失声明补造工具，不改变原生或未启用契约请求的行为。

这是**声明与选择的协议校验**，不是 hosted 执行授权：没有核验 hosted MCP 的逐工具权限、Provider 副作用、账单或任意 hosted 参数 schema。已有有效声明和 operator 图像注入策略仍保留。

回归证据：

- 修复前 HTTP/SSE 16 个真实 Manager→executor→localhost 子案例：8 个非法 hosted 选择实际发到 mock 上游，8 个有效对照通过；原始 RED 保留。
- 修复后 HTTP、SSE、WebSocket Execute/ExecuteStream 共 32 个子案例通过：16 个非法选择本地拒绝、零上游发送；其中 WebSocket 拒绝发生在握手前。16 个客户端声明/operator 注入的有效对照继续发送。
- hosted MCP server identity 的 6 个 guard 子案例通过，覆盖直接/allowed/wrapped 选择；这只是本地选择检查，不是 hosted MCP 执行认证。
- Go `1.26.4` 全仓测试：103 个有测试 package 通过、29 个无测试 package、零 package/test failure。8 个既有可选 live/corpus/TLS 测试跳过，32+6 个新增子案例以及既有 Claude 声明保护 80 个子案例实际执行。`go vet ./...` 和 server build 返回 0。

源码入口：[最终发送保护](../../internal/runtime/executor/helps/responses_tools.go)、[HTTP/SSE/WebSocket 回归](../../test/claude_deferred_outbound_guard_test.go)、[hosted MCP identity 回归](../../internal/runtime/executor/helps/responses_tools_test.go)。

## hosted 图像声明：配置边界与未知项

[T27 官方 Claude 对照](2026-10-13-claude-deferred-wire-contract_CN.md)的两臂均含 `image_generation`；它来自 Codex executor 既有 `ensureImageGenerationTool`，不是模型检索结果或 T27 新增行为。

当前配置契约中，`multimedia.disable-image-generation: false` 对应自动注入；`passthrough` 在非 images 端点既不注入也不剥离客户端声明；`chat`/`true` 进行已有剥离。该剥离发生在 payload rules 前，显式 operator override 仍可重新加入 hosted 声明。相关源码为 [Codex 请求规范化](../../internal/runtime/executor/codex_executor_request.go)、[payload 规则](../../internal/runtime/executor/helps/payload_helpers.go)，说明位于 [config.example.yaml](../../config.example.yaml)。

因此未来验收必须冻结这一策略及 payload rules，区分客户端提供、operator 注入和模型选择；仅有 MCP 禁止 dispatch=0 不认证 hosted 工具权限。可以在独立验收配置中用现有 `passthrough` 排除自动注入，再核对实际 final wire。本轮没有切换默认策略，也没有执行真实 hosted 工具；其授权、副作用、费用保持未知。

## 官方客户端实测：开关已被接受，取消仍失败

使用已安装官方 Codex CLI `0.160.0`，独立 `CODEX_HOME`、app-server、localhost scripted 模型与官方 MCP SDK fixture。仅在本次隔离配置显式设置 `features.code_mode_interrupt=true`，没有编译、替换或升级客户端。

第一例前登记四例：eager/lazy 各一个成功对照和一个中途取消；每例 20 秒、最多 16 个本机 scripted 请求、8 次 MCP dispatch。所有 4 例已开始并完成观测；总计 8 个 scripted 请求、4 次真实 MCP dispatch、**0 个真实 Provider 请求**。独立审计没有重跑模型/MCP 案例。

| 模式 | 场景 | 任务终态 | MCP 取消通知 | 清理前最后 active | 结果 |
| --- | --- | --- | ---: | ---: | --- |
| eager | 成功对照 | completed | 0 | 0 | 通过，真实回执、正常释放 |
| eager | 中途取消 | interrupted，收到匹配 ack | 0 | 1 | 20 秒预算耗尽，取消未通过 |
| lazy | 成功对照 | completed | 0 | 0 | 通过，先发现后加载、回执及释放 |
| lazy | 中途取消 | interrupted，收到匹配 ack | 0 | 1 | 20 秒预算耗尽，取消未通过 |

官方 `features list` 在相同隔离 home 下返回 `code_mode_interrupt / under development / true`，证明该二进制接受此开关。独立审计核对 predeclared/journal/summary 分母、实际 dispatch 后触发、interrupt RPC 的 thread/turn ID、对应 ack 和 `interrupted`，以及四例原始 MCP phases、配置与二进制 hash。

两例取消都没有 `protocol_cancel_received`、`cancelled` 或 `released`；这说明仅缺少此显式开关不能解释本轮失败。`active=1` 是进程清理前最后一次协议观测，不能说清理后仍泄漏。feature 属于 UnderDevelopment；被接受不等于生产稳定保证，也不证明客户端所有内部条件或唯一根因。范围仅限该官方版本、Code Mode、app-server 与 stdio MCP。

责任归客户端：这四例没有经过 CLIProxyAPI，网关不持有该 MCP peer 的 request handle，不能替客户端代发取消。交付为指定 profile 的限用/外部依赖记录，不继续自建客户端路线，不全局阻塞其他可用 route。详见[取消边界](../compatibility/codex-mcp-cancellation-gap_CN.md)。

## 证据与结论范围

本地原始目录 `.omo/tool-loading-completion/t28-codex-official-interrupt-feature-v1/` 包含冻结 scope、四例 journal、summary、客户端 stdout、MCP 事件和 `independent-audit.json`。RED/GREEN、32 transport 回归、full Go JSON、vet/build 及 `t28-gates-summary.json` 同样留在私有证据目录；不公开原始客户端历史或配置。

本轮增加网关生产修复，并把 Codex 取消的“开关未显式设置”变量缩小为有官方二进制实测的客户端边界。没有新增全任务收益样本；T26/T27 点估计不迁移到本候选，代表性多任务与统计置信区间仍未建立。当前整体目标继续执行，不能因本地回归通过而宣布完整验收完成。
