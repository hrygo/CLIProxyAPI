# Claude Code 跨 Provider 成对实测与执行边界

核验日期：2026-10-13。客户端固定 Claude Code 2.1.289；网关候选最终源码
`5f0f186aaeba6be872d703344a085e50fd279742`，Go 1.26.4。
本报告属于有限实测，完整目标未认证。原始请求、响应、随机回执和进程输出保存在私有验收目录。

## 实施结果

正式 `live_clients.py` 接纳显式 `--clients claude`，默认仍为 Codex/OpenCode。
Claude 的 client version/revision、Messages source format、direct/ToolSearch 模式、
权限和 fixture 源码进入预声明的成对作用域。每个任务在候选启动前进入 CaseJournal，
所有失败、重试、预算耗尽及未完成任务保留；不把独立 loopback 成功当作真实 Provider 成功。

Claude JSONL 解析必须以无错误的成功 result 结束。历史 assistant 中较早的正确答案不能
代替最后结果；代码围栏可证明内容和回执，但仍违反 plain JSON 格式门槛。
多轮 Messages 回填需要先前匹配的 assistant tool_use，再出现成功的 user tool_result；
错误调用 ID、ToolSearch 伪装的业务回执、失败结果及普通 prompt 都不能满足实际工具回填。

本轮新发现并修复两处问题：

1. 验收 Claude 命令末尾的变长 `--disallowedTools` 吞掉追加的 prompt，导致 CLI 在推理前退出。
   将其置于明确的后续选项之前，禁止项、允许项及执行权限不变。
2. 当前 GLM Chat 端点返回 HTTP 400，指出 `messages[4].name` 不受支持。
   Claude→Chat 翻译对 `glm-*` 上游模型省略 tool result 上的非标准 `name`，
   保留 `tool_call_id`、先前 function.name、参数、顺序及回执；buffer/stream 和 compatibility
   路径均有回归。其他模型保留已有 name 扩展，避免破坏 Gemini 兼容上游。
   这是明确模型范围的修复，不是所有严格端点已完成能力协商的证明。

[OpenAI Chat 契约](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
将现代 tool message 定义为 content/role/tool_call_id；
[上游 #5859](https://github.com/router-for-me/CLIProxyAPI/issues/5859)记录了 Gemini 兼容上游需要 name 扩展的反例。
两者核验于本报告日期。调用身份不能因删除扩展字段而丢失。

## 配置漂移与失败留存

首轮两条路由共 12 个已启动任务，源配置由 SHA-256
`3a6ab28f1605437c2cd9358b7a5320781718f30266353c93953882469b9f46fe`
变为 `43dc5a62be638bfeda68a2c588b9f4bbe03d0c96a5d39d83031fc7840ff1e394`。
两例在候选正常退出后因 source_config_unchanged=false 拒绝，后三例材料校验失败，未发上游请求。
不能删除它们或把缺失请求的 token 用零补齐。

本任务未写入、恢复或回退默认配置，无法仅凭哈希确定外部改动来源。
候选代码只读源配置，SDK 使用独立状态、内存配置、空 watcher 和另一个不存在的配置路径。
后续按当前实际配置建立新批次和新作用域，保留旧批次，不跨配置合并认证。

## 修复前的稳定配置批次

候选 `c81dfd536e8fba416f01db7efc2b290cceadf0bd`，每路由三个工具场景×两模式。
这两个批次全部正常完成配置和材料校验，但严格任务质量仍不通过：

| 实际 Provider 路由 | eager 成功 | lazy 成功 | 物理 HTTP | 首轮输入中位数减少 | 峰值输入减少 | 全任务输入减少 |
|---|---:|---:|---:|---:|---:|---:|
| Qwen / deepseek-v4.1-flash | 2/3 | 1/3 | 16 | 94.86% | 89.67% | 87.61% |
| OpenCode Go / glm-5.3-flash | 1/3 | 0/3 | 17 | 95.25% | 未知 | 未知 |

Qwen 三个失败均为 strict JSON 格式失败，实际工具执行、回执和客户端/上游终态成立。
GLM 三例为 Provider HTTP 400、另两例为格式失败。400 后的能力回退请求也进入计数；
缺少失败请求 usage 时不报告峰值、全任务或账单收益。
只有三个模板簇，收益区间没有有限的 95% 下界；质量条件下界约 −86.46pp，远低于 −2pp。
这些描述性中位数不是质量非劣或成本认证。

## 实际客户端执行边界

公开探针 `claude_boundary_probe.py` 使用实际 Claude CLI 和实际 fixture MCP，
模型为本地脚本化 Messages 服务，无真实 Provider 推理或账单。
修复前私有诊断 6/8 通过，两个 client-denied 例因 prompt 解析错误没有启动，均保留。
修复后公开探针 8/8 通过，覆盖每种 eager/lazy：

| 场景 | 验证结果 |
|---|---|
| 客户端禁止 ledger lookup | 模型实际发出该调用；客户端拒绝，零 MCP request/dispatch/副作用，错误真实回填 |
| 非法 project 参数 | 实际 MCP 服务返回 invalid_arguments，零 dispatch/副作用，错误真实回填 |
| 检索后撤销权限 | generation=2 在调用前生效；实际服务返回 permission_denied，零 dispatch/副作用 |
| 正在执行时取消 | 先观察 fixture_wait dispatch，再向实际客户端发送 SIGINT；cancelled/released 事件成立，active=0，无强制客户端清理 |

取消例没有合法完成终态，这是预期的取消状态，不能伪装成任务成功。
结果及 8 starts/8 finishes 保留在私有 journal；summary 仍为 unverified。

## 最终候选复测

最终两条路由各预声明 12 个任务：lookup/unavailable/multitool 和 coding-budget/retry/identity，
每场景 eager/lazy。编码要求实际 read/replace/test、修改后的独立 host-owned 行为测试、
最终 source hash 和随机回执；不能只凭模型声称修复。
两批均完成，12 starts/12 finishes，候选均正常退出，配置与材料校验成立；构建的
vcs.revision 与上述 commit 一致，vcs.modified=false。全部 24 例观察到请求的 eager/lazy
实际声明模式，MCP 禁止 dispatch/副作用及最终 active count 均为零。

| 实际 Provider 路由 | 全部严格成功 | 工具场景严格成功 | 编码行为通过 | 编码严格成功 | 物理 HTTP | Provider HTTP ≥400 |
|---|---:|---:|---:|---:|---:|---:|
| Qwen / deepseek-v4.1-flash | 5/12 | 4/6 | 6/6 | 1/6 | 43 | 0 |
| OpenCode Go / glm-5.3-flash | 3/12 | 2/6 | 4/6 | 1/6 | 50 | 0 |

GLM 先前的字段拒绝消失，实际工具结果通过新翻译继续多轮回填。
但这不等于质量恢复：GLM 两个 coding-budget 例反复修改后达到 8 次物理请求上限，
独立编码行为验证失败；其余七个严格失败为格式失败。Qwen 六个严格失败仅因格式，
另一个 coding-budget lazy 同时格式失败和上游终态未验证：第三次物理尝试响应 capture
为 0 字节，后续请求恢复并完成编码，所有尝试仍保留。该空响应的具体根因未知，
不能把任务最后成功或客户端退出码用来补出缺失的上游终态。

下面的收益只针对三个工具场景的匹配模板簇，不包括编码任务、不包括私有故障首包、
不跨候选版本或 source config 合并：

| 路由 | eager 严格成功 | lazy 严格成功 | 首轮输入中位数减少 | 峰值输入减少 | 全任务输入减少 | 质量条件下界 |
|---|---:|---:|---:|---:|---:|---:|
| Qwen | 3/3 | 1/3 | 94.86% | 89.66% | 85.10% | −98.30pp |
| GLM | 1/3 | 1/3 | 95.26% | 90.56% | 86.06% | −63.16pp |

Provider input 包含 cached input，不能乘未知单价变成账单；全部收益区间下界因只有
三个模板簇而未知。每种编码任务绑定独立任务配置、catalog/permission/fixture 作用域，
各只有一个成对样本，不合并成三个独立同作用域样本。Qwen coding-budget 的峰值及全任务
input 因空响应的 usage 缺失而未知；其他编码对的已知输入减少仍只是描述性观测。

截至本轮，真实 Provider 新增 48 个已启动案例：漂移批次 12、修复前稳定批次 12、
最终候选 24。它们分属不同 revision/config，不能把 48 计入单一最终版本的 60-case 门禁，
更不能把独立 scripted boundary/protocol 探针补进这个分母。

最终私有证据为 `t7-claude-qwen-final/`、`t7-claude-glm-final/` 下的 batch-plan、
CaseJournal、batch-audit、paired-analysis 和逐例 summary；根目录为本仓库
`.omo/tool-loading-completion/`。失败没有重新跑到成功后覆盖原记录。

最终提交同源码的边界复测 8/8、四个 Messages/Chat 客户端协议探针 4/4 通过。
这些都是 scripted loopback 验证；Provider usage/cost 不使用其合成数值。

### 下一步结构化输出验证路径

当前默认验收保持 plain JSON，未剥离代码围栏。只读 `claude --help` 和
[官方 CLI 文档](https://code.claude.com/docs/en/cli-reference)确认 `--json-schema` 存在。
同版本本地故障首包实测：加这个 flag 后 eager 有 68 个声明，lazy 有 3 个，
新增 builtin `StructuredOutput`；没有 response_format，output_config 仅带 effort。
每模式均因故意 HTTP 400 发生 2 个物理请求，不是执行成功，也没有真实 Provider token。

这提供了通过客户端原生 StructuredOutput 改善格式的可验证路径，不是简单要求所有
Provider 支持服务端 response_format。必须先验证实际 StructuredOutput 调用、成功 result
中的 structured_output、schema 校验、真实 MCP gold、权限变化和合法终态，再作为显式新
profile 接入正式验收；旧默认失败仍保留，两个 profile 不混合。当前 runner 仍拒绝 Claude
structured-output，避免凭一个 flag 或首包就宣称支持。

## 验证与复现

同修复源码：Go 全量 103 个测试包、go vet 和 server build 通过，Python 237 tests 通过。
Go 回归同时保留原 Gemini name 扩展测试；GLM 新增四种 stream/compat 组合，检查字段、身份及回执。
长任务均以异步 handle 启动，等待期间推进独立代码、执行边界探针、统计核查及本报告。

```sh
GOTOOLCHAIN=go1.26.4 go build -o "$task_validation/live-gateway" ./ops/tool-loading/live-gateway
python3 ops/tool-loading/claude_boundary_probe.py \
  --output-dir "$task_validation/claude-boundaries"
python3 ops/tool-loading/live_clients.py \
  --gateway-binary "$task_validation/live-gateway" \
  --config "$task_operator_config" --output-dir "$task_validation/claude-live" \
  --gateway-revision "$task_gateway_revision" --endpoint-sha256 "$task_endpoint_sha256" \
  --client-model "$task_model_id" --upstream-model "$task_upstream_model_id" \
  --executor "$task_executor_id" --clients claude \
  --scenarios lookup unavailable multitool coding-budget coding-retry coding-identity \
  --task-seconds 90 --batch-max-requests 96 --batch-max-dispatches 96
python3 ops/tool-loading/analyze_pairs.py \
  --journal "$task_validation/claude-live/cases.jsonl" \
  --plan "$task_validation/claude-live/batch-plan.json"
```

命令只应在预声明、隔离且已授权的真实 Provider 额度范围执行。私有证据不提交仓库。
本轮仅本地提交；未发布、未更新远端 PR、未切换默认配置、未重启原服务。

## 剩余条件

- Claude native structured output 未认证；当前格式失败保留，不通过剥离代码围栏提高成功率。
- name 等扩展字段的 Provider 能力选择仍需统一契约；本轮 glm-* 的限定适配不解决未知端点。
- native Responses、Gemini/Antigravity、Anthropic 与实际计数、thinking/cache/signature 路径仍需同版本验证。
- 原有 Codex/OpenCode 路由、单一最终版本的 ≥60 案例、代表性模板与逐路由质量非劣门槛仍未整体满足。
- 冷/热缓存、凭据/区域等完整不可变作用域及实际价格/账单未认证；未知成本不填零。

完整目标见[实施方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)，
协议前置实测见[Claude Code 协议报告](2026-10-13-claude-code-protocol_CN.md)。
