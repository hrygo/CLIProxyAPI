# Claude 延迟工具协议识别修复

核验时间：2026-10-13，北京时间。生产修复分支：`codex/tool-loading-gateway-production`；基于 `cb494e54`，未包含后续 repository runner 实验提交。

## 问题与改动

普通 Messages 请求可以合法地在文字、工具名、schema 或参数中包含 `tool_reference`、`defer_loading`。旧实现用字节子串触发桥接，随后要求存在工具声明或活跃 `ToolSearch`，导致无关内容被拒绝。例如：

```json
{"messages":[{"role":"user","content":"tool_reference"}]}
```

本轮先复现六种普通请求的错误拒绝，再将判断改为检查协议位置：工具声明中的 deferral flag、消息内容块及 `tool_result.content` 中的 reference block。普通请求保持原始字节、原始请求视图和普通执行上下文。

识别阶段保留重复 JSON 成员，避免后一个 `messages`、`content`、`type` 或 deferral 字段覆盖真实发现状态，从而绕过已有重复字段校验。首次回归实际复现了重复根级 `messages` 的漏判。

另一次失败复现确认，合法 Unicode 转义（例如 `defer_\u006coading`、`tool_refer\u0065nce`）可绕过旧字节快速判断，包括禁用 client search 的路由。两处入口现将这类输入交给结构识别；普通转义文本仍保持透传。

改动仅涉及 `internal/responsestools/claude_deferred.go`、`sdk/cliproxy/auth/claude_deferred_execution.go` 及其现有回归文件。真实发现仍经过声明、身份、历史绑定、预算和强制 tool choice 校验；原生 Claude 路由和总开关绕过行为沿用现有契约。

## 验证与证据边界

Go `1.26.4` 的现有全套回归通过：103 个带测试包通过，29 个包没有测试。`go vet ./...` 和服务器构建通过。既有 Execute、CountTokens、Stream 入口、原生绕过、禁用路由、声明预算及无效引用回归包含在测试范围内。

```bash
GOTOOLCHAIN=go1.26.4 go test ./internal/responsestools ./sdk/cliproxy/auth -run TestClaudeDeferred -count=1
GOTOOLCHAIN=go1.26.4 go test ./... -count=1
GOTOOLCHAIN=go1.26.4 go vet ./...
GOTOOLCHAIN=go1.26.4 go build -o /tmp/cliproxyapi-claude-detection ./cmd/server
```

本机主工作区的 `.omo/tool-loading-completion/t31-*` 保留失败复现与最终验证日志；首轮因 Go cache 写权限未启动的结果单独保留，没有算作缺陷复现或通过。

这是网关请求准备阶段的确定性兼容修复。本轮没有发送真实 Provider 请求、进行官方客户端任务对照或新增上下文/成本节省测量，不能由这些回归外推跨 Provider 成功率或收益比例。是否在实际客户端负载中触发、收益大小及新增结构检查的性能代价，尚无本轮测量。

未发布、安装、修改默认配置或重启服务；尚未将本轮生产修复更新到远端 PR。此前测试设施实验留在原工作区。
