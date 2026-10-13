# 原生多结果回填：调用参数保护与官方 Codex 复测

核验日期：2026-10-13（Asia/Shanghai）。**网关生产修复已本地提交；官方客户端取得一组有限成对实测，整体目标与高置信净收益尚未完成。** 未构建或修改客户端，未发布、改默认配置或重启原服务。远端更新曾被自动审批拒绝；用户随后明确授权，PR #25 已同步本轮三个提交，远端 `c093ef73` 的 `build` 于 2026-10-13 20:01（北京时间）通过，仍未合并。

## 生产修复

候选 `0efa8a28bc8e68bf5f13293b17d424c7a9065f7c` 修复 CLIProxyAPI 最终发送保护的调用参数缺口：原先绑定了多结果内容、身份与顺序，却没有绑定这些历史调用的参数。对已转换的调用改写 `arguments`、删除参数或改成对象，旧实现均仍发出上游请求；失败复现日志为 `.omo/tool-loading-completion/t26-native-history-red.log`。

`sdk/cliproxy/executor/wire_history.go` 现在同时记录调用输入和结果的规范化 SHA-256；context 不保存源代码、参数或结果正文。已有精确原生 `custom-output-replay: multiple` 路径在最终发送前比较摘要，历史参数变动返回本地 422。没有新增配置键或扩大 Provider 支持范围。既有空白 function arguments → `"{}"` 归一化仍可通过；`null`、缺省、对象与字符串保持区别，custom source 不做此归一化。

Go `1.26.4` 的定向、全仓测试、`go vet ./...` 和 server build 通过。全仓 JSON 日志有 103 个 package pass，29 个 package skip，零 package/test fail；skip package 均为无测试文件。HTTP 与 SSE 各覆盖正常多结果及无参调用，另各有 13 种篡改在实际发送前拒绝，mock 上游请求为零。新增调用输入回归覆盖外层 JSON 转义、缺省/null、结构化精确整数与 custom source。server build 退出 0，另有沙箱限制写 Go module stat cache 的警告，保留于日志；干净 SDK 候选构建退出 0。

## 官方客户端与执行范围

- 客户端：现有官方 `0.160.0-aarch64-apple-darwin` 包，实际 `codex-cli 0.160.0`，客户端 binary hash 与 manifest hash 在私有冻结材料中；未采用 T25 自建候选。
- 网关：上面的单一生产候选，隔离 SDK binary SHA-256 为 `c31e0e76fea60db8f0b358cff6a512e3a7ac94ac2ab02ce38a7bc6fc1067ce4b`，Go build metadata 为同 revision / `vcs.modified=false`。
- 路由：沿用已测的 `gpt-6-luna` / Codex OAuth 原生 Responses；精确配置及凭据材料在候选启动时校验，未刷新或更换身份。
- 客户端模式：在隔离 home 使用 `code_mode_only`。custom grammar 为 `described`，不是原生采样语法约束认证；官方版本也不代表该模式在默认配置中开启。
- 任务：一个固定单函数编码模板，读、修改代码并运行独立测试。每次工具执行在同一次 exec 先通知再回填结果。预登记 eager、lazy 两臂，顺序固定，未随机化；每 case 最多 8 次请求、8 次 dispatch、120 秒，总上限 2 cases / 16 请求 / 16 dispatch，无追加样本或重跑。

## 实际结果与输入

| 指标 | eager | lazy |
| --- | ---: | ---: |
| case started / finished | 1 / 1 | 1 / 1 |
| case 质量与合法终态 | 通过 | 通过 |
| 独立行为测试 | 11 / 11 | 11 / 11 |
| 真实 Provider 请求 | 5 | 5 |
| 真实 MCP dispatch | 3 | 3 |
| 禁止 dispatch / 副作用 / 最终 active | 0 / 0 / 0 | 0 / 0 / 0 |
| 首轮输入 tokens | 50,024 | 15,153 |
| 峰值输入 tokens | 52,255 | 17,273 |
| 全任务输入 tokens | 255,874 | 81,343 |
| cache-read tokens | 200,704 | 61,440 |
| cache-write tokens | 0 | 0 |
| 任务耗时（含准备、验证与清理） | 23.292 秒 | 22.038 秒 |

本组输入降幅：首轮 **69.71%**、峰值 **66.94%**、全任务 **68.21%**。Provider 输入计量包含 cached input；10 次请求全部纳入，未知/无效 input 请求均为零，未将缓存输入当作免费或账单节省。缓存状态仅为 observed/unknown，账单与费率未验证，货币收益保持未知。一个固定顺序的任务对不能估计质量非劣或收益单侧 95% 下界，也不能宣称稳定延迟收益。

lazy 首轮没有 fixture 参数签名；实际只加载并调用 `coding_task_read`、`coding_source_replace`、`coding_tests_run`，未发现 schema 加载前调用，回填身份一致。两臂各三个真实执行在最后上游历史中均有独立通知和最终结果，结果 item ID 唯一且关联原 call ID。两个候选网关均退出 0。源配置、默认配置和官方客户端 binary 前后摘要未变。

## 汇总脚本错误与证据保留

两个 case 的独立结果及 journal 均已正常落盘，但外层一次性脚本误用 `write_private` 的排他创建接口重复写 `summary.json`，最终 **退出 1**。原部分汇总、失败及实际执行脚本副本均保留，不能称整次脚本或完整验收通过。脚本已改为不同文件名，**没有重新发送 Provider 请求**。

另用独立只读核对恢复两臂汇总：确认两个预登记 case 均 started/finished，10 份真实请求与 usage 数量一致、6 次真实 dispatch、通知/终态关联、网关退出与配置保全。结果位于 `.omo/tool-loading-completion/t26-official-codex-pair-v1/independent-pair-audit.json`；该文件补充解释证据，不覆盖原部分汇总或制造新案例。所有私有 traces、凭据绑定及原始配置继续留在本机。

## 剩余边界

这是本轮网关具体修复及一个官方客户端模式的有限实测。Muse 一对多结果仍不接受；其他协议的通知语义、官方客户端取消与权限生命周期、Gemini 绑定/计量，以及代表性多文件任务的质量与收益区间均未完成。旧批次的失败和收益不迁移到此候选，也不合并成可靠率分母。后续继续修复网关可解决的问题，客户端缺口交付兼容报告，不回到自建客户端路线。
