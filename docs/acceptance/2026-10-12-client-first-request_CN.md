# 客户端隔离配置与首包验证

核验日期：2026-10-12。本报告仅覆盖真实客户端访问本地 HTTP fixture 的首个带工具声明请求；不覆盖真实 Provider 或 CLIProxyAPI 跨供应商闭环。

## 范围与方法

- 客户端实测版本：Codex CLI `0.160.0`、OpenCode `1.19.0`。旧 OpenCode `1.18.32` 实测不得沿用为当前版本证明。
- 使用同一注册目录、权限和 synthetic model；67 个注册工具中，`fixture_mutation` 在服务端默认禁止，暴露 66 个声明。
- 每次使用独立目录、Codex home、OpenCode XDG 状态、配置、模型 catalog 和 nonce；不继承用户凭据或广义实验标志。
- 真实客户端启动官方 MCP SDK fixture；临时 HTTP fixture 捕获请求并返回 HTTP 400，主动停止推理。所有推理任务均失败，这是预设探针行为。
- 未修改默认配置或默认 catalog，未安装候选、发布或重启原服务。

## 首个带工具声明请求的实测

| 客户端 | eager | lazy | eager 工具声明字节 | lazy 工具声明字节 | 字节降幅 |
| --- | --- | --- | ---: | ---: | ---: |
| Codex `0.160.0` | `mcp__fixture` namespace 内 66 个声明 | 无该 namespace；出现 `tool_search` | 195,587 | 9,984 | 94.90% |
| OpenCode `1.19.0` | 66 个 MCP 函数 | 仅 `execute` | 199,295 | 10,912 | 94.52% |

字节采用 UTF-8、排序键、紧凑 JSON 对完整 `tools` 数组计量。Codex 内建工具也计入该数组。这里证明配置能改变首包声明加载方式；不等同于 Provider 输入 token、全任务上下文或账单节省。Provider usage 为未知，没有填零。

最终四个探针共收到六个请求：Codex 各一个；OpenCode 各一个无工具的标题请求、一个带工具的任务请求。辅助请求没有被丢弃。

## 失败与修正记录

最初四个诊断探针全部保留：两个 Codex 探针因 synthetic catalog 缺少 `base_instructions` 或 `model_messages.instructions_template`，在出站前失败；已修正本地 fixture metadata。两个 OpenCode 探针面对预设 HTTP 500 各发起七个请求，最终耗尽 20 秒进程预算。后续改用 HTTP 400 避免探针引发重试。

修正后三批各四个探针的 eager/lazy 首包检查均通过；每批六个收到的请求，所有推理仍按设计失败。合计十六个诊断探针、三十二个收到的请求；这些是本地配置诊断，不是正式 60-case live 样本，不能作为任务成功率分母或质量认证证据。服务端只能证明收到的请求，未捕获的出站尝试数仍为未知。

## 复现与证据

入口为 `ops/tool-loading/probe_clients.py`，隔离 API 为 `ops/tool-loading/client_adapters.py`，运行方式见同目录 README。最终探针原始 artifacts 保存在 `.omo/tool-loading-completion/t5-native-probe-verified/`，包含每个请求、完整客户端输出和汇总；未提交含长系统提示的原始请求。早期诊断保存在同目录 `native-probe-*` 和 `t5-native-probe-final`。

适配测试覆盖版本/未知键拒绝、凭据隔离、eager/lazy 权限一致、基础工具 deny 配置、进程与输出预算、取消、MCP journal 序列完整性和首包模式判定。配置中出现 deny 不证明真实客户端执行前拦截；仍需构造服务端允许、客户端禁止的调用，并证实零 MCP 请求。

当前结果为 `local-first-request-only`，整体验收状态仍是 `unverified`。尚待真实发现与调用、原始 schema 拒绝、撤权与取消释放、逐请求 usage 与 journal 联动、跨 Provider 60 cases、编码任务及配对置信区间。
