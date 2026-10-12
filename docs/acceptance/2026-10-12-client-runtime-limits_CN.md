# 原生客户端取消与 Code Mode 执行限额验收

核验日期：2026-10-12（Asia/Shanghai）。本报告补充[完整方案 T5](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md#t5客户端发现目录与底层权限第三批r1r2r5r6)，不构成最终跨 Provider 认证。

后续[官方取消接口实测](2026-10-12-client-native-cancellation_CN.md)已补充 OpenCode session abort 的通过证据和 Codex turn/interrupt 的传播缺口；本报告保留原非交互 CLI SIGINT 结论，不将不同入口的结果相互覆盖。

## 结论与范围

实际 Codex CLI `0.160.0`、OpenCode `1.19.0` 的非交互 CLI 取消探针，尚不能证明取消传到 MCP 并完成资源释放。验收器已修复直接杀进程组的问题：先向主 CLI 发送 SIGINT，等待并保留退出输出，再清理进程组；新的原生复测仍未取得 MCP cancelled/released 事件。

OpenCode Code Mode 无界循环在 20 秒内没有返回，需要验收器进程预算终止。SDK 显式限额独立测试证明其超时、调用次数和释放机制可以工作，同时复现输出字节限额缺陷。网关不能代替客户端中断 CPU 循环，也不能用服务端 dispatch 预算冒充客户端执行预算。

测试使用真实客户端与合成 MCP，模型回复由临时本机 HTTP/SSE 服务脚本控制；没有经过 CLIProxyAPI，没有实际 Provider 请求。SDK 测试直接调用官方 tag 提取的外部源文件，没有修改或安装客户端候选。Provider usage 为未知；没有新增上下文 token、账单或任务质量结论。

## 原生诊断结果

| 范围 | 已开始案例 | 边界通过 | 实际观察 |
| --- | ---: | ---: | --- |
| 原先整组终止的取消基线，两个客户端 × eager/lazy | 4 | 0 | 每例一次 wait dispatch；缺少取消/释放事件 |
| 主 CLI SIGINT 后的取消复测，同一四种模式 | 4 | 0 | Codex 主进程退出码 1；OpenCode 两例需要宽限期后的强制终止；仍缺少取消/释放事件 |
| OpenCode lazy CPU 无界循环 | 1 | 0 | 20 秒外部进程预算终止；零 MCP dispatch，缺少合法客户端终态 |
| OpenCode lazy 单次 execute 中 12 次工具调用 | 1 | 1 | 9 次 MCP 请求、8 次 dispatch，第 9 次由服务 dispatch_budget 拒绝；合法终态 |
| OpenCode lazy 请求 1 MiB 结果 | 1 | 1 | 一次 dispatch，服务 result_budget 拒绝；合法终态 |

共 11 个原生诊断案例、27 个收到的 HTTP 请求（包括 OpenCode title 请求）。全部失败保留，不能合并为正式 60-case live 样本，也不能将重复诊断当作独立任务质量样本。

八个取消案例最后记录的 active_final 均为 1。这是被终止前最后一个回调计数，**不证明进程结束后仍存在泄漏**。当前缺口是协议取消及释放证据；不能伪造 released 事件，也不能将进程消失转写为 active=0。SIGINT 的结果只覆盖这些非交互 CLI 启动方式，不能推断交互界面、Codex app-server `turn/interrupt` 或 OpenCode session abort 的行为。

原始私有证据位于仓库忽略目录 `.omo/tool-loading-completion/`：`t5-cancel-baseline/`、`t5-cancel-interrupt/`、`t5-execution-budget/`；每例保留 case-started、完整请求、模型回复、MCP 事件、客户端输出及摘要。不将原始客户端请求提交仓库。

## 外部 CodeMode SDK 显式限额

来源固定为 [OpenCode v1.19.0 CodeMode](https://github.com/anomalyco/opencode/tree/v1.19.0/packages/codemode)，运行环境 Bun `1.4.2`，依赖为 acorn `8.15.0`、effect `4.0.0-beta.83`、typescript `5.8.2`。探针先校验[25 个源文件及依赖锁定清单](../../ops/tool-loading/opencode-codemode-source.json)，再导入执行。修改过的源文件在依赖加载前拒绝；此负例有 RED/GREEN 回归。

| 测试 | 显式限额 | 实测 | 判定 |
| --- | --- | --- | --- |
| CPU 无界循环 | timeoutMs=100 | TimeoutExceeded，约 123 ms | 通过该 SDK 场景 |
| 尝试三次工具调用 | maxToolCalls=2 | 实际执行两次，ToolCallLimitExceeded | 通过该 SDK 场景 |
| 等待工具的超时释放 | timeoutMs=100 | active=0、released=1 | 通过该 SDK 场景 |
| 大字符串结果 | maxOutputBytes=256 | 序列化 value 实际 348 字节 | 失败 |

输出失败来自[interpreter 的 boundOutput](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/codemode/src/interpreter/runtime.ts)：按预算截取后追加提示文字，valueBytes 未计算最终 JSON 转义与提示开销；日志截断提示也没有计入剩余预算。零字节、单字节等极小预算与合法 JSON value 的接口语义需要明确，不能只减少切片长度就宣布完整修复。

官方同 tag 的[客户端 Code Mode 入口](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/tool/code-mode.ts)调用 CodeMode.make 时未传 limits；[SDK 契约](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/codemode/src/codemode.ts)明确未传限额时不启用对应限制。入口同时存在 ctx.abort 与 SDK 执行的竞争取消路径，因此源码有取消机制不等于当前 SIGINT 启动方式已触发它。

SDK 曾有一次因合成 Tool 缺少 description 而异常退出的诊断，未生成完整摘要；修正测试输入后，两次完整 SDK 批次均为 3 通过、1 失败。初次异常、后续完整批次均保留，不覆盖。最新带全量源文件 pin 的证据为 `t5-sdk-limits-pinned/`；之前两个目录为 `t5-sdk-limits/` 和 `t5-sdk-limits-complete/`。

## 客户端侧独立交付

1. **取消适配**：为 Codex app-server、OpenCode session abort 建立独立版本 profile；在实际 MCP dispatch 后调用官方取消接口，验证通知、合法取消状态、资源释放与后续新任务可运行。与 CLI SIGINT 分开认证，避免进程组清理掩盖协议取消。
2. **执行预算接入**：OpenCode host 显式提供 CPU 执行时间、调用数和输出预算；参数须由运行策略决定，示例探针的 100 ms/2 次/256 字节不是生产默认值。单次 execute、整个任务、多次 execute、MCP 服务与 Provider 请求分别计数，不能重置预算绕过限制。
3. **输出预算修复**：在客户端 SDK 计算最终序列化 value 与日志的总开销，将截断提示纳入预算；定义极小预算的明确失败契约，补充 JSON 转义、中文、多字节边界、日志和失败结果回归。网关不得无声裁剪工具结果来遮蔽该缺陷。
4. **权限与目录更新**：已有实际客户端拒绝、原始 schema 校验和服务撤权证据；客户端权限 generation 更新、索引刷新、会话恢复仍需独立实测。旧声明和 Code Mode execute 入口不得扩大底层权限。

目前单独交付可重现探针、源文件锁定清单及责任边界，未声称客户端修复已经落入安装版本。无需修改用户默认配置或重启原服务即可继续在隔离环境验证上述能力。

## 当前验证与未完成门禁

Python 离线 suite 新鲜运行通过 **77 tests**，包括主进程 SIGINT 后输出保留、取消目标/模式限制及外部源文件拒绝回归。SDK 负例继续失败是被记录的客户端缺陷；不能把它描述为全部回归已绿。没有 Go 源码改动，本轮未重跑 Go 全量门禁。

总目标仍未完成：真实客户端协议取消、客户端累计执行预算、网关 attempt/usage 来源及不可变证据整合、最终版本跨 Provider 60-case 与代表性 coding 任务，以及按路由的成功率非劣和上下文/成本收益区间仍待验证。实现与测试均不包含发布、默认配置切换或原服务重启。
