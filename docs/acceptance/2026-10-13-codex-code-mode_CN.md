# Codex Code Mode：显式 profile、权限边界与真实 GPT 复测

核验日期：2026-10-13。沿用[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)的五项门槛。用户已明确后续投入优先 Codex、Claude Code；OpenCode 保留对照。本轮没有发布、升级客户端、修改默认配置/catalog、重启原服务或刷新 OAuth 凭据。

## 结论

**Codex CLI `0.160.0` 的 `code_mode_only` 已形成独立、可观测的验收 profile。真实 GPT 最新六例安全成功 5/6，取消传播与峰值收益仍未达标，整体目标未完成。** 新 profile 不覆盖旧 direct/native-search 证据，也不合并不同候选的通过率。

客户端实际执行的是 `functions.exec` / `ALL_TOOLS` 路径：模型检索元数据、取得 TypeScript 工具声明，再调用底层 MCP，并使用结果继续任务。它证明客户端 Code Mode 的发现闭环，不等于证明一个未声明的原生 `tool_search` 已可发现。网关仍负责协议适配与诊断，不替客户端检索或执行本机工具。

## 实现与证据范围

- `f46376353582aa6cd673e70f28e1b59eb07c7238`：显式 Code Mode profile、矩阵 schema v2、独立 pair/fixture 身份、两层权限过滤及发现顺序观测。保留实际模型 `tool_mode`；冲突模型拒绝，未声明模式时才由显式隔离 flags 选择。
- `b6072df6`：按实际初始声明解析省略 namespace 的 `exec`；要求名称唯一、声明为 custom/Lark，拒绝歧义或不同 namespace。多工具回填检查新增匹配 call ID 的 `custom_tool_call_output`，不读取 prompt、调用参数或无对应调用的输出作为回执。
- `b7fbad02e649b325759b2582405bdfaae40ab86a`：候选退出清理遇到 `PermissionError` 时，仅在 `wait()` 证实子进程已退出后容忍；活进程信号失败继续报错。该提交是最新正式六例的干净构建候选。

第二个提交曾在全量结果核对前提交；随后发现一项真实快速退出测试的清理错误，未将该提交构建为合格 live 候选。修复后重新验证，保留失败日志及后续证据。

Code Mode eager 检查权限过滤后全部合法 fixture 签名；本机默认服务拒绝 `fixture_mutation`，因此完整合法目录为 67 个，而非原始 68 个。客户端再拒绝 lookup 时为 66 个。lazy 首轮必须无 fixture 签名并提供 `ALL_TOOLS`；后续检查匹配发现结果、完整的目标 typed declaration、执行前加载和调用身份。发现结果的名称列表本身不证明 schema 已加载。

省略 namespace 不是按名称猜身份：真实 Lite 请求在 `additional_tools` 中包含 `functions` namespace 的 custom `exec`。唯一初始声明提供解析依据；同名多声明、无声明或显式其它 namespace 均拒绝。TypeScript 签名为多行，观测器据实际格式匹配；禁止跨另一条声明拼出伪签名。

服务端禁止工具可以在客户端因目录隐藏而被提前拒绝。本轮要求实际 exec 调用与匹配错误回填、无 MCP request/dispatch，再认可这个客户端拒绝层级；不伪造服务端 `permission_denied` 事件。撤销和非法参数场景仍要求真实 MCP 拒绝记录。

## 本地原生客户端边界

这些案例使用真实 Codex CLI / MCP、临时本机模型 SSE fixture；CLIProxyAPI 与真实 Provider 未参与，不能计入跨 Provider 六十例、token 节省或账单收益。

| 批次 | 已结束 | 严格通过 | 限制 |
| --- | ---: | ---: | --- |
| 初始工程 allow 探测 | 2 | 不作完整发现认证 | lazy 强制返回初始未声明的 native search；原证据保留 |
| 首轮完整 Code Mode 边界 | 12 | 4 | eager 权限目录计数及服务隐藏工具判定不完整；取消失败 |
| 修正观测后的 CLI 边界 | 12 | 10 | 正常、客户端禁止、服务禁止、非法参数、撤销各两例通过；取消两例失败 |
| Code Mode app-server 取消 | 2 | 0 | 两例都有 interrupt ack / matching interrupted，但无 MCP 取消或释放 |

最新十个非取消案例禁止工具 dispatch、副作用及最终 active 均为零，正常两例真实 nonce 回填。取消发生于已观察到的 `fixture_wait` dispatch 之后。CLI SIGINT 两例没有 `cancelled/released`；app-server 两例等待 20 秒预算后仍无通知或释放，最后 MCP 事件 active=1。进程清理不是协议取消成功，也不能据此声称清理后仍存在实际资源泄漏。

该缺口明确位于所测客户端/MCP 链：测试没有网关。指定版本的 [Code Mode delegate](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/tools/code_mode/delegate.rs)向 nested tool submission 传入 cancellation token，取消分支仍等待 invocation；这本身不能证明 MCP requestId 已被取消。后续客户端交付应连通 turn → cell/nested call → MCP request handle 的取消与释放，覆盖正常完成不取消、并发 ID、响应/取消竞态及取消后新任务。尚无已编译验证的 Codex Rust 补丁；不得以网关代发本机 MCP 控制消息。原 direct 模式的独立证据见[官方取消接口报告](2026-10-12-client-native-cancellation_CN.md)。

## 两次真实 GPT 六例

固定 Codex `0.160.0`，`codex / gpt-6-luna / openai-responses-v1`，原生 JSON-schema 输出。lookup、unavailable、dependent multitool 三个模板各 eager/lazy 一对；每任务 90 秒、8 次物理请求和 8 次 dispatch；全批上限各 48。只使用启动前校验仍匹配的精确 OAuth 文件绑定，未选择其它账号。

首批候选为 `f4637635`，六例全部开始并结束，严格安全成功 2/6，15 次物理请求、6 次 dispatch。四例失败包含 namespace/Code Mode 回填观测缺口；原 journal、判定及 paired analysis 不回写。后续源码解释不能追溯把它们改成通过或纳入新候选认证。

最新批次候选为 `b7fbad02e649b325759b2582405bdfaae40ab86a`，Go `1.26.4`，真实二进制 metadata 为该完整提交及 `vcs.modified=false`。冻结计划 SHA-256：`e3741bdab7820f565b84d0e14dad42b5c09e81ee772b759656893d4fd666f38d`。

| 模板 / 模式 | 安全成功 | 物理请求 | dispatch | 总耗时 | 首轮 / 峰值 / 全任务输入 tokens |
| --- | --- | ---: | ---: | ---: | --- |
| lookup eager | 是 | 2 | 1 | 6.753 s | 51,498 / 51,672 / 103,170 |
| lookup lazy | 是 | 3 | 1 | 8.452 s | 16,848 / 26,062 / 68,826 |
| multitool eager | 是 | 3 | 2 | 8.924 s | 51,556 / 51,923 / 155,211 |
| multitool lazy | 否 | 4 | 2 | 12.817 s | 16,906 / 26,388 / 95,544 |
| unavailable eager | 是 | 1 | 0 | 4.540 s | 51,529 / 51,529 / 51,529 |
| unavailable lazy | 是 | 3 | 0 | 9.975 s | 16,879 / 35,060 / 77,953 |

最新六例全部开始并结束，安全成功 **5/6**；16 次物理请求、6 次 dispatch，无未知请求/dispatch，预算合规。禁止 dispatch、副作用及最终 active 均为零；六个候选退出 0、源配置未变化。默认配置摘要仍为 `43dc5a62be638bfeda68a2c588b9f4bbe03d0c96a5d39d83031fc7840ff1e394`。

lazy multitool 的两个 MCP 调用及中间回填均通过。最终 verification code 与真实回执一致，但 confirmation code 从回执的 64 字符变成最终答案的 56 字符；Provider 原始 completed message 与 CLI 最终文本逐字相同。当前证据把错误定位到模型生成的最终答案，未发现网关丢失这些字符的证据。保留原生 schema、合法终态和实际执行的通过项，但整体任务仍失败。

## 收益及认证状态

按三个模板的成对节省率取簇中位数，包含所有已声明任务及失败：

| 指标 | 实测中位数减少 | 门槛状态 |
| --- | ---: | --- |
| 首轮输入 | 67.24% | 描述值超过 50%；收益下界未知 |
| 峰值输入 | **49.18%** | 未达 50% |
| 全任务输入 | 33.29% | 描述值超过 10%；收益下界未知 |

unavailable lazy 的全任务输入比 eager 增加；不能只展示首轮或成功模板。模型检索输出较宽，并存在额外回合，提示后续应在客户端限制相关 schema 返回量、处理无匹配与最终回执保真；这是基于捕获的改进方向，不是已经验证的修复。

三簇质量差值均值为 −33.33pp，条件单侧 95% 下界 −86.46pp，未达 −2pp。六例不足以认证质量非劣；三项收益区间下界均未知。cache-read 在各例有观测数值，但 cache 条件和 region 未锁定，账单未知，输入 tokens 不等于收费节省。全部 gate 仍为 `unverified`。

三例 lazy 总耗时均高于 eager；上述耗时包含候选准备、客户端启动与任务执行，不是纯模型首 token 延迟。分别保留耗时与缓存计数，不把节省 tokens 解释为更快或更便宜。

Gemini usage 冲突仍未改计量契约：[官方 usage 字段文档](https://ai.google.dev/api/generate-content#UsageMetadata)不能单独决定 Antigravity 私有冲突快照的权威顺序；保留上一轮未知值，不选较小值补收益。详见[Gemini 复测报告](2026-10-13-candidate-diagnostics_CN.md)。

## 工程验证与后续主线

最终 **277 项 Python 回归通过**，另 30 次真实快速退出/安全诊断探测通过；干净候选构建和矩阵 budget audit 通过。本轮只改验收 Python/文档，没有生产 Go 修改，不宣称重新完成全量 Go 门禁。新增负例覆盖 metadata 冲突、模式身份、矩阵 v1/v2 闭合字段、null 拒绝、权限泄露、typed schema、发现先后、重复身份、未声明/歧义 namespace 及 custom output 绑定。

后续按用户优先级：首先完成 Codex Code Mode 在非 GPT 路线的独立闭环与代表性编码，继续 Claude Code 跨 Provider；补齐客户端取消交付、检索规模和回执保真。网关只修复由原始边界证据支持的转换/激活/终态缺陷。最后在同一最终版本完成 60+ 跨 Provider 案例，并用独立模板簇验证质量与收益；不能用本轮六例或不同候选拼接认证。

复现入口为 [README](../../ops/tool-loading/README.md)，显式 `--codex-tool-mode code_mode_only` 或矩阵 schema v2 的同名字段。CLI SIGINT 与 `--transport codex-app-server` 分开运行。私有原始证据在 `.omo/tool-loading-completion/t11-codex-code-mode-{probe,boundaries,boundaries-v2,app-cancel}/` 与 `t11-gpt-code-mode-pilot{,-v2}/`；保留启动记录、失败、HTTP/MCP 捕获、退出回执、冻结计划、audit 和 pair analysis，不进入发布产物。
