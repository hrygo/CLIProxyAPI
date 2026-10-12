# 跨 Provider 工具按需加载：Muse 原生 Responses 试点

核验日期：2026-10-13（Asia/Shanghai）。本报告记录两个实现候选的四个独立批次，
以及随后对声明计量的修正。它是有限实测报告，完整目标仍按
[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) 验收。

## 结论与候选

Muse 原生 Responses 的 eager namespace 身份恢复缺口已修复并获得真实执行证据。
修复候选的 Codex 批次严格通过 **4/6**，OpenCode 默认输出 profile 通过 **6/6**。
OpenCode 原生 StructuredOutput profile 仍受上游 `tool_choice` 限制，不能用默认输出
profile 的通过结果替代。每条路线只有三个任务模板，质量和收益统计门禁尚未通过。

| revision | 交付 |
| --- | --- |
| `2c072be744898424d2947f1d0b14d03f215a2ae9` | 原生 Responses 验收线包；显式、只读的 OAuth 文件绑定与内存快照 |
| `4b2ad3dbb66ae4432401cf2ab6a5b3e3f5fe33a3` | portable native eager namespace 响应身份恢复及 buffered/SSE/WebSocket 回归 |
| `e6b1e096507fad645690db1a5f91809ad3df79ee` | 正确计量 Lite `additional_tools` 与原生检索历史声明；离线重算，不冒充新 live 批次 |

当前报告不声称后两个候选之间完成了新的 live 复测，也不把旧候选的案例计入
最终版本 60-case 门槛。所有改动仅在本地提交，未更新远端 PR、发布、安装、修改默认配置
或重启原服务。

## 锁定范围与分母

客户端：Codex CLI `0.160.0`、OpenCode `1.19.0`；Go `1.26.4`。
路由 alias：`opencode-go-muse-spark-1.3`；实际 upstream model：
`muse-spark-1.3-contributor`；executor：`meta`；原生 `openai-responses-v1` HTTP/SSE。
这不代表 Chat、WebSocket live、其他 Muse 模型或 OAuth 账户已经认证。

每批启动前固定 plan、材料指纹、prompt/gold、权限及执行预算；每批六例：
64 工具目录、lookup/unavailable/dependent-confirmation 三场景，各 eager/lazy 一例。
每例最多八次物理上游请求、八次 dispatch、90 秒。所有任务使用 fresh 私有客户端 home、
隔离候选与 MCP fixture；确认任务必须回放 lookup 实际产生的随机回执。
空结果任务的正确不可用回答可成功，零 dispatch 是其预期行为。

| 批次 | 候选 | 客户端输出 profile | 严格通过 | eager / lazy 通过 | 物理请求 | 实际 MCP dispatch |
| --- | --- | --- | ---: | --- | ---: | ---: |
| `t6-native-meta-codex` | `2c072be` | Codex 原有输出 | 4/6 | 1/3、3/3 | 26 | 3 |
| `t6-native-meta-opencode` | `2c072be` | 原生 JSON schema、固定标题 | 0/6 | 0/3、0/3 | 6 | 0 |
| `t6-native-meta-retest-codex` | `4b2ad3d` | Codex 原有输出 | 4/6 | 2/3、2/3 | 20 | 5 |
| `t6-native-meta-retest-opencode` | `4b2ad3d` | 默认输出、固定标题 | 6/6 | 3/3、3/3 | 19 | 6 |

全部 **24 started / 24 finished / 14 strict successes / 71 requests / 14 dispatches**；
`4b2ad3d` 的两批合计 **12 started / 10 strict successes / 39 requests / 11 dispatches**。
没有覆盖失败、追加成功替代原分母或混合不同输出 profile 作因果比较。
全部批次 plan/journal 校验通过，未开始和未结束案例均为零，物理请求证据未知数为零。

## 身份缺口及修复边界

初始 Codex eager lookup 收到 qualified function name（例如
`mcp__fixture.ledger_budget_lookup`）而没有 namespace，客户端随后记录 unsupported call，
八次请求耗尽后仍无 MCP 执行。原生请求未改写时，准备阶段过早释放响应身份适配器，
因此既有 canonical identity resolver 没有运行。

修复仅为 portable native、需要 function 转换的 upfront namespace 保留 response-only
适配器；请求字节、call ID 和参数保持原有语义。Codex transparent、普通顶层 function
和尚未激活的 deferred namespace 不因此启用适配器。
原候选的 Go overlay 使 buffered/SSE 的实际 Meta executor 回归失败；修复候选通过。
真实复测 eager lookup 两次请求、一次 MCP dispatch，并正确使用真实回执。

WebSocket 对该响应修复路径需要 managed replay，不能同时宣称原生 raw duplex/steering
保真。回归期发现旧测试仍要求该路径 passthrough，已按照实际准备结果修正；其他既有
passthrough 场景继续保留。协议边界见 [Responses tools](../responses-tools.md)。

## 保留的失败与责任归属

| 现象 | 已确认事实 | 责任与处理 |
| --- | --- | --- |
| 初始 eager namespace 不可调用 | 上游 qualified name；客户端 unsupported call；零 MCP 执行 | Gateway 响应身份适配缺口，已修复；旧失败仍保留 |
| OpenCode StructuredOutput 六例全部 HTTP 400 | 上游仅支持 `tool_choice: auto`，拒绝 required/named/none；零 dispatch，usage 缺失 | Provider 契约限制与客户端 structured-output 要求冲突；不得静默把 required 改成 auto。默认输出另行验收 |
| 修复候选 Codex lazy unavailable 返回本地 413 | `declaration_budget: cumulative discoveries do not fit: 270783 > 262144`；两次实际请求、零 dispatch | 累计声明预算护栏生效，任务仍失败；客户端发现量/重试策略需协同。runner 原分类为 framework，属于诊断分类缺口，不改写原证据 |
| 修复候选 Codex eager dependent-confirmation 耗尽预算 | 八次上游请求；lookup 一次执行；七次 confirm 在执行前拒绝 invalid_arguments | 原始上游 `response.function_call_arguments.done` 连续返回 `{}`，但出站 schema 要求 project、verification_code；属于 Provider 参数生成失败，Gateway 不得凭空补回执 |

最后一例同时有原生事件的快照不一致：`function_call_arguments.done.arguments` 为 `{}`，
而 `output_item.done.item.arguments` 为空字符串。此报告能证明上游没有提供必填值，不能
证明所有事件的一致性已经修复。MCP 的参数校验阻止了七次非法确认，确认 dispatch 为零。
初版诊断脚本遗漏 arguments.done 事件，把这七例称作 malformed；核对全部事件后，准确
结论是有效 JSON 空对象、缺必填参数。原始 Provider/MCP 记录均未改写。

全部 24 例禁止 dispatch、禁止效果和最终 active callback 均为零。
HTTP admission 全部封存，active bodies 为零、journal closed、无 journal failure；
两个 Codex eager 多工具失败各保留一次超额请求 admission 拒绝。
每例候选正常退出、源配置哈希未变。本轮 Muse 使用既有 API-key route，未读取 OAuth
凭据正文；显式 OAuth 文件绑定只有离线隔离证据，尚未宣称真实 OAuth 跨 Provider 认证。

## 上下文、声明占用与成本

下表逐批次、逐客户端路线，在三个配对任务上计算 `1 - lazy/eager` 的中位数。
采用 Provider 实际 total input usage，额外调用及失败消耗均保留。
不同 revision、不同 profile 和不同模型的 raw tokens 不合并成统一节省率。

| 批次 | 首轮输入降幅中位数 | 峰值输入降幅中位数 | 全任务输入降幅中位数 |
| --- | ---: | ---: | ---: |
| 初始 Codex | 73.43% | 30.20% | 70.10% |
| 初始 OpenCode StructuredOutput | 未知 | 未知 | 未知 |
| 修复候选 Codex | 73.42% | 53.85% | 40.77% |
| 修复候选 OpenCode 默认输出 | 90.69% | 76.94% | 72.76% |

初始 Codex 的失败 eager 基线同样计入；不能据此声称质量合格的输入收益。
OpenCode 默认输出 unavailable 的 lazy 使用六次请求、eager 一次；该单对峰值输入增加
36.60%、全任务输入增加 234.98%。中位收益不得掩盖空结果任务的成本反例。

三个有 usage 的批次均六例 input 完整，初始 OpenCode 六例 usage 全未知。
前者 cache-read 字段已知，cache-write 未知；cache condition、价格/实际账单和充分的
延迟分布尚未认证。input 包含缓存输入，不能将 token 降幅称为已验证省钱。

声明计量修正单独重算已有 71 个 request captures：12 个 Codex 后续请求遗漏了原生
`tool_search_output.tools` 的历史位置；OpenCode 请求无此差异，所有首轮均无差异。
例如修复候选 Codex lazy lookup 后续请求的声明数组序列化占用从 41,329 修正为
66,731 bytes，多工具后续请求从 38,823 修正为 61,721 bytes。
这是声明容器的规范化序列化字节数，既不是完整 HTTP body 字节数，也不是模型 token。
重复出现在顶层和历史中的定义均计入传输占用，不能因 active-set 去重而消失。
原始 summary 的 declaration_bytes 未覆盖修改；修正审计另存
`.omo/tool-loading-completion/t6-native-history-observation-audit.json`。
上表实际 usage 和严格成功判定不受此计量修正影响。

## 验证与证据入口

`4b2ad3d` 的完整 Go 测试：103 个 package 通过、零失败；core/auth/test 与 WebSocket
相关 race 回归、全量 vet 和 server build 通过。测试修复前的失败日志也保留。
随后仅修改 Python 验收计量和文档：新增回归先出现五个失败，修复后完整 Python suite
**200 tests / OK**。没有把这次离线通过记成新的 Provider live 成功。

本地原始证据位于 `.omo/tool-loading-completion/`，未将凭据或原始客户端私有 home 入库：

- 四个表中批次目录：`batch-plan.json`、journal/replay、逐例 `summary.json`、MCP 事件、
  实际 HTTP captures、candidate exit，以及各批次 `-validation.json`、`-paired-analysis.json`。
- `t6-native-eager-executor-red.log`：原候选 overlay 的真实 Meta executor 回归。
- `t6-native-eager-full-go-v2.log`、`t6-native-eager-final-race.log`、
  `t6-native-eager-websocket-race.log`、`t6-native-eager-full-vet.log`、
  `t6-native-eager-server-build.log`：修复候选工程验证。
- `t6-native-history-declarations-red.log`、`t6-native-history-declarations-full-python.log`、
  `t6-native-history-observation-audit.json`：计量回归和独立 capture 重算。

## 未达到的完整目标

每条最终 live 路线仅三个模板。按分析器的独立同分布模板假设作探索性计算，Codex 质量
差值单侧 95% 下界为 −86.46 个百分点，OpenCode 为 −63.16 个百分点；这些条件值
不是已认证统计结论，收益区间下界也因模板不足而未知。不能宣布 −2pp 非劣
或收益区间下界大于零，所有配对分析 gate_status 保持 unverified。

当前仍需：

1. 为本地准备/预算拒绝增加结构化诊断证据，并分别处理客户端检索量和 Provider 参数失败。
2. 完成真实 Gemini Antigravity 的独立 bootstrap、包装协议 usage/终态计量和全部物理请求
   admission；不能把普通 Gemini、Gemini CLI/plugin 或 AI Studio relay 当成同一传输。
3. 锁定新的单一最终候选，执行全部 60 个跨 Provider 案例，并重跑代表性编码任务。
   [此前编码试点](2026-10-13-coding-pilot_CN.md) 的 24 个案例属于另一 revision，不拼入本轮。
4. 按独立任务模板收样，分别完成质量、收益、延迟及已知成本门禁；部署仍需另行授权。

因此，本报告证明一个 Gateway 身份修复、计量缺口修复和有限 Muse 闭环实测，
完整跨 Provider 目标继续保持未完成。
