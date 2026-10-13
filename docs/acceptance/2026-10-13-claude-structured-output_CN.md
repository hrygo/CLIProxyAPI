# Claude Code 原生 StructuredOutput 与跨 Provider 验收

核验日期：2026-10-13。Claude Code 2.1.289；候选代码
`1677af587d6e25baff48393898191a44ba9562fb`，Go 1.26.4。
本报告记录显式新 profile，完整目标仍未认证。

## 原生输出的证据与实施

上一轮仅确认 `--json-schema` 改变首包，不能证明客户端执行或结果正确。
本轮先加失败测试，再以实际客户端验证 `StructuredOutput` 调用及最后的
`result.structured_output`，通过后才接入正式 runner 的显式 `--structured-output`。
现有 plain JSON 门槛、旧批次失败及默认客户端选择均保留。

[Claude 官方非交互文档](https://code.claude.com/docs/en/headless)说明 schema 输出出现在
structured_output 字段；具体执行方式和 ToolSearch 的共存，以本轮同版本请求/JSONL
实测为准。未要求兼容 Provider 支持 response_format，没有网关合成答案。

新 profile 验证最后成功 result、完整 JSONL、无 assistant error、有效 schema，以及
最后实际 assistant StructuredOutput 的 input 与最终对象严格一致。较早的正确调用不能
替代较晚的错误调用；无调用的结构化对象、重复 JSON member/调用 ID、非有限数字、
终态错误与截断输出均不能通过。参数声明 schema 只给形状，不泄露实际 gold/随机回执。
MCP dispatch、回执、独立编码行为与所有上游终态继续单独验证。

适配器仅在显式 schema 时添加 --json-schema；允许/禁止工具、permission-mode 和
permission-prompts 不变，未加入权限绕过。输出 profile 进入 fixture/permission hash、
pair ID 和预声明 plan；不同 profile、revision 或配置的结果不合并认证。

## 实际客户端协议与执行边界

同候选原生 Messages、Messages→Chat 的 eager/lazy 四个 scripted loopback 案例全部通过。
每个案例执行实际 lookup→confirm，模型从实际 history 获得两份回执，再调用 builtin
StructuredOutput；最后结构化对象与实际调用、schema 和独立 gold 一致。
eager 3 个物理请求、lazy 4 个；实际客户端成功退出，候选物理请求记录和收到的请求数一致。
原生首轮 lazy 为 ToolSearch/DeferredToolPlaceholder/StructuredOutput 三个声明；
经网关的 Chat 首轮只剩 ToolSearch/StructuredOutput，两次检索出的业务声明随后加载。
计数只描述本 fixture。合成 usage 不报告 Provider 上下文或账单收益。

同原生输出 profile 的执行边界 8/8 通过：两种模式分别验证实际客户端禁止调用
零 MCP request/dispatch、MCP 非法参数零 dispatch、检索后 generation=2 撤销权限
零 dispatch，以及观察真实 fixture_wait dispatch 后 SIGINT 的 cancelled/released/active=0。
全部 effects=0，无 forced_process_cleanup。前三类错误真实回填后执行原生输出；
取消例没有成功终态，这是预期的取消状态。全部 starts/finishes 保留。

这些是实际客户端/工具服务的 scripted 证据，不能计入真实 Provider 60-case 分母。

## 真实 Provider 复测

两个批次均在候选启动前锁定：每条路由六场景×eager/lazy，共 24 个任务，
涵盖 lookup/unavailable/multitool 与 coding-budget/retry/identity。
每任务 90 秒、8 次物理请求、8 次 dispatch；每路由 96 请求/96 dispatch 总上限。
候选使用独立状态和只读源配置，原服务不重启。源配置 SHA-256：
`43dc5a62be638bfeda68a2c588b9f4bbe03d0c96a5d39d83031fc7840ff1e394`。

真实路由：qwen-ds41-flash→deepseek-v4.1-flash/openai-compatible-qwen；
opencode-go-glm53-flash→glm-5.3-flash/openai-compatible-opencode-go。
两个批次均正常结束，全部 starts/finishes 和物理请求保留：

| 路由 | 严格任务成功 | 工具任务 | 独立编码行为/严格输出 | 物理 HTTP | MCP dispatch | Provider HTTP ≥400 |
|---|---:|---:|---:|---:|---:|---:|
| Qwen / deepseek-v4.1-flash | 12/12 | 6/6 | 6/6 | 44 | 26 | 0 |
| OpenCode Go / glm-5.3-flash | 12/12 | 6/6 | 6/6 | 50 | 28 | 0 |

24 例全部观察到请求的 eager/lazy 声明模式；实际 final structured_output、schema、
工具回填及上游终态均满足严格 task 判定。24 个候选退出码均为零，source config
未变化；禁止 dispatch/effect 及最终 active 均为零，预算未超限，没有未完成任务。
Qwen coding-budget eager、GLM coding-budget lazy 各有一次追加修改和测试；
不能因最终成功而省略中间执行，全部保留在 dispatch 和请求总数内。

下面仅聚合 lookup/unavailable/multitool 三个相同作用域的工具模板对，Provider input
包含 cached input，未知价格不能用于换算账单：

| 路由 | 首轮输入中位数减少 | 峰值输入减少 | 全任务输入减少 | 质量单侧 95% 条件下界 | 收益区间下界 |
|---|---:|---:|---:|---:|---|
| Qwen | 94.65% | 89.44% | 84.66% | −63.16pp | 样本不足 |
| GLM | 95.04% | 90.12% | 88.66% | −63.16pp | 样本不足 |

三个编码任务分别绑定独立 coding fixture/catalog/permission 作用域，每个只有一对，
不能合并成三个独立同作用域模板。各编码对首轮输入减少约 94.46%/94.86%；
峰值减少范围 Qwen 92.09%–92.19%、GLM 91.90%–92.72%；全任务减少范围
Qwen 91.42%–94.24%、GLM 88.05%–93.56%。这些仍是逐任务描述性结果，
不把编码与工具任务合成一个整体收益率。

延迟与缓存单列，仅取上述每模式三个工具任务。耗时是 CaseJournal 的任务墙钟时间，
含候选/客户端准备、MCP 与网关清理，不是纯模型延迟，也没有可靠的 p95 估计：

| 路由/模式 | 工具任务耗时 p50 | cached-input / inclusive-input | cache-write |
|---|---:|---:|---|
| Qwen eager | 7.731 秒 | 50.92% | 未知 |
| Qwen lazy | 9.284 秒 | 53.73% | 未知 |
| GLM eager | 7.810 秒 | 53.56% | 实际报告 0 |
| GLM lazy | 7.419 秒 | 22.52% | 至少一份响应未报告，整体未知 |

Qwen lazy 的样本 p50 更长，不能把 input 节省称为全方位提速。缓存条件未受控，
上表是实际 usage 份额，不是严格 cold/warm 实验或缓存效果因果估计。
实际价格和账单均未知，客户端针对其他模型的估算成本不作为真实 Provider 账单。

与[上一轮默认 profile](2026-10-13-claude-code-live_CN.md)的 5/12、3/12 相比，本轮
显式 native profile 观测为 12/12、12/12。两轮 revision/fixture/profile 不同，
这是新 profile 的有限实测，不能当作同条件的因果比较、运行态推广或可靠率证明。
原始失败和默认配置均未变更。当前最终版本仅 24 个新 Claude 实测，原有客户端/路线
尚未在此版本完整复测，不能把此前不同版本的案例拼成 60-case。

私有证据：`.omo/tool-loading-completion/t8-claude-qwen-structured/` 与
`t8-claude-glm-structured/` 的 batch-plan、CaseJournal、batch-audit、paired-analysis
及逐例 source diff/code-outcome、MCP events、HTTP capture、client stdout 和 gateway exit。
两个 batch-audit 均报告未知作用域 12 例，gate_status=unverified；region/cache 等完整
作用域、独立代表性模板抽样和真实账单未认证，不能只因通过率高而覆盖这个状态。

## 统计门槛与后续工作

60-case 是覆盖门槛，不能自动满足逐路由 −2pp 的单侧 95% 质量门槛。
当前 analyze_pairs.py 的保守方法忽略改善，把模板簇中任意 eager-success/lazy-failure
视为退化簇；零退化时下界为 `-(1 - 0.05^(1/n))`。
三个独立模板簇仍为 −63.16pp；即使同一路由有 30 个独立簇仍为 −9.50pp。
按这个方法，至少 149 个独立、代表性的零退化簇才可达到 −2pp，即每个作用域至少
298 个成对任务。重复原来三个模板、跨 profile 或跨 coding fixture 拼接，不会增加这个 n。

这是当前诊断方法的零退化样本下限，不是 power calculation，不保证实际会通过。
收益区间至少需要五个完整独立模板簇才有有限单侧 95% 中位数下界；出现缺失 usage
仍不可计算完整收益。下一步须先扩展代表性任务语料、补齐不可变作用域、根据 pilot
discordance 做正式设计和额度估算；不直接把 60-case smoke 或高 token 降幅作为认证。

原有 Codex/OpenCode 路由、native Responses/Gemini/Antigravity/Anthropic、计数/
thinking/cache/signature、单一最终版本 ≥60-case 及逐路由质量/收益门槛仍需继续验证。
账单、缓存、延迟与 input 单独报告，未知不填零。

## 回归与复现

Python 全量 244 tests 通过；同源码候选 build 成功，vcs.modified=false。
本轮只修改验收 Python/README；生产 Go 修复继承上一轮验证，未新增 Go 行为。

```sh
GOTOOLCHAIN=go1.26.4 go build -o "$task_validation/live-gateway" ./ops/tool-loading/live-gateway
python3 ops/tool-loading/claude_client_probe.py \
  --gateway-binary "$task_validation/live-gateway" --structured-output \
  --output-dir "$task_validation/claude-structured-protocol"
python3 ops/tool-loading/claude_boundary_probe.py --structured-output \
  --output-dir "$task_validation/claude-structured-boundaries"
```

真实路由命令在[上一轮验收报告](2026-10-13-claude-code-live_CN.md)基础上添加
`--structured-output`，固定本轮 revision 并使用新 output directory。
完整门槛见[实施方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。
本任务未发布、更新远端 PR、写默认配置或重启原服务。
