# Claude Code 工具检索与跨协议闭环实测

核验日期：2026-10-13。范围：Claude Code 2.1.289、synthetic MCP fixture、本地脚本化模型、
独立 CLIProxyAPI 候选 **243cd59ea64529ffe9f98560a9f4d33380d729df**，
Go 1.26.4，`vcs.modified=false`。状态：协议验证；完整目标仍未认证。

## 结论

当前客户端确实支持按需加载，但隔离启动方式会改变有效模式。
`--bare` 下即使设置 `ENABLE_TOOL_SEARCH=true`，实测首包仍包含 67 个完整 MCP 声明，
没有 `ToolSearch`。去掉该模式，使用 fresh home/config、显式 MCP、过滤环境、关闭 hooks、
禁用用户/项目/本地 settings 来源、显式权限和非必要流量后，lazy 首包只有两个声明：
`ToolSearch` 与 `DeferredToolPlaceholder`。这由线包确认，不由配置键推断。

后续检索结果使用 `tool_result.content[].tool_reference`，对应命中的完整 schema 出现在
下一轮 `tools`，并仍带 `defer_loading:true`。本版本的这个客户端模式首包不发送完整目录。
不能将服务端原生工具检索 API 的目录传输规则直接当作该客户端的实测行为。

四个脚本化 loopback 案例通过：直接 Messages、Messages→CLIProxyAPI→Chat，各有 eager/lazy。
每例均由实际 Claude CLI 触发真实 MCP lookup 和 confirmation，模型只能从回填历史读取随机
verification_code 与 confirmation_code，最终输出与工具服务的独立 gold 一致，客户端合法结束。
这证明限定协议闭环，不证明真实模型检索质量或跨 Provider 的最终任务通过率。

## 本轮实施

- 固定版本的 Claude adapter，独立 home/config/workspace，显式 fixture MCP 与 exact tool rules，
  `dontAsk` 和 `permission-prompts=none`；不绕过权限，不继承操作员凭据或 Provider 环境开关。
  不加入现有 Codex/OpenCode live batch，避免未验收客户端自动混入旧矩阵。
- 独立候选接纳 `/v1/messages` 和本地 `X-Api-Key` 占位认证。认证头冲突时拒绝。
  `/v1/messages/count_tokens` 未在本轮准入；不制造 token count。
- 非 Anthropic 路由在真实路由/auth 选择后、Execute/CountTokens/ExecuteStream 前统一准备：
  校验成功客户端 ToolSearch 的调用/结果关联与当前有效 schema；仅激活引用命中的工具，
  丢弃未加载工具及 placeholder，去掉 deferral 标记，将引用回填成 `loaded_tools` 清单。
  原生 Messages 路由不改动；responses-tools 总开关关闭时保持原行为。
- 未知引用、无 schema、伪造/重复 identity、重复 JSON key、失败搜索、错误角色、非法 flag、
  mixed result、深度/声明字节超限、强制调用未激活工具均在上游 dispatch 前拒绝。
  数字精度、schema 约束及调用方原始 body 保留。

## 计量边界

Gateway→Chat 的干净提交复测记录如下；用于理解传输优化，不是 Provider token 指标。

| 模式 | 物理上游请求 | 首轮声明字节 | 后续每轮声明字节 | MCP dispatch |
|---|---:|---:|---:|---:|
| eager | 3 | 200,182 | 200,182 | 2 |
| lazy | 4 | 1,481 | 10,248 | 2 |

首轮声明减少约 **99.26%**，峰值声明减少约 **94.88%**。
lazy 多一个检索请求，因此全任务收益必须按全部请求重新测量。
该表仅计算 `tools` 数组；system prompt、目录名称提醒、历史、缓存、延迟和账单不包含在内。
脚本中的 token usage 是明确的合成 fixture 值，真实 Provider input 与 billing 保持 `null`。
直接 Messages 首包声明 198,239→1,657 字节，同样只有传输意义。

Gateway 两例的物理 HTTP 次数分别为 3 与 4，实际接收、journal starts 和 admission reservation
一致；最终 active response bodies=0、journal closed、auxiliary owner sealed/inactive。
两例均零副作用，MCP active_count 在结束时归零。
独立 config 及原有服务没有改动。

## 复现与证据

```sh
go build -o "$task_validation/live-gateway" ./ops/tool-loading/live-gateway
python3 ops/tool-loading/claude_client_probe.py \
  --gateway-binary "$task_validation/live-gateway" \
  --output-dir "$task_validation/claude-protocol"
```

依赖沿用已有 fixture Python 环境，输出目录必须是新的。
原始 body、随机回执、客户端输出、MCP event journal 与 HTTP capture 只保存在私有输出目录。
最终四例均通过，私有证据为 `.omo/tool-loading-completion/t7-claude-probe-final/`；
`probe-events.jsonl` 保留 4 starts/4 finishes，整个 summary 仍标 `unverified`，不取得任务认证。
初始 `--bare` 和标准模式故障首包探针均保留；两组每模式各启动 2 个请求，第二个为
客户端能力降级重试，不能按单请求处理，也不进入真实模型成功率样本。
脚本化协议通过不覆盖这些故障记录。

代码入口：`ops/tool-loading/client_adapters.py`、`claude_client_probe.py`、
`internal/responsestools/claude_deferred.go`、`sdk/cliproxy/auth/claude_deferred_execution.go`。

同源码验证：Go 全量 103 个测试包通过；Python 全量 229 tests 通过；
responsestools/auth/live-gateway 三包 race 通过；`go vet ./...` 和 server build 通过。
所有长脚本异步启动，等待期间推进 adapter、回归、边界复核和文档；结论以原 handle 的终态为准。
默认 config SHA-256 仍为
`3a6ab28f1605437c2cd9358b7a5320781718f30266353c93953882469b9f46fe`。
本轮仅本地提交，未发布、未更新远端 PR、未切换默认配置、未重启原服务。

## 剩余认证条件

1. 将 Claude 适配到正式 CaseJournal/paired batch 和真实 Provider scope；客户端输出、
   Anthropic 计数端点、旁路推理、thinking/cache/signature、重试及流终态独立验证。
2. 在真实客户端上验证 forbidden/invalid/cancel/revoke/release，检索许可不能扩大执行许可；
   本轮回归及 lookup 的零副作用不代替这些完整执行边界案例。
3. 对 Responses、Gemini、native Antigravity 和 native Anthropic 实际路由复测，
   不将本地 Chat 的成功推广到其他格式或服务端执行的 Anthropic tool search。
4. 最终单一 revision 完成原 60-case 及新增客户端范围和代表性编码任务，保留全部 starts/fails/retries，
   逐路由满足 −2pp 单侧 95% 非劣及真实 token/成本收益门槛；样本不足继续标 bounded-live。

官方资料核验于同日：
[MCP 工具检索](https://code.claude.com/docs/en/mcp#configure-tool-search)说明 Gateway 默认回退及显式 opt-in；
[环境变量](https://code.claude.com/docs/en/env-vars)描述 simple/bare 和独立配置目录；
[Gateway 契约](https://code.claude.com/docs/en/llm-gateway-protocol)用于后续 beta/reference、计数和失败语义验收。
实际模式与行为以上述同版本线包为准。
