# Claude 激活声明发送保护与 Gemini 工具身份修复

核验日期：2026-10-13（Asia/Shanghai）。本轮交付两项 CLIProxyAPI 生产修复，官方 Claude Code 编码对通过；整体目标和高置信净收益仍未完成。使用既有官方客户端，不构建客户端 fork，不发布、切换默认配置或重启原服务。

## 生产问题与修复

Claude 的客户端 ToolSearch 结果原先已转换为普通工具声明，但随后绕过 Responses-family attempt 的 wire contract。真实 Manager→executor→localhost 上游复现显示，HTTP/SSE 下删除激活声明、重命名或扩大声明超过预算仍发送。失败复现保留于私有 `t27-claude-outbound-red.log`。

提交 `e202aaa89f5f70fd13eb7d9808842cbdd41fbea1` 将准备后的激活函数集合、传输身份和字节上限接入既有只读 `WireContract`，由 Execute、CountTokens、ExecuteStream 传递。最终发送层拒绝丢失、新增未加载函数、重复身份、最终强制选择未激活函数及超预算声明；身份问题本地 422，超预算 413。context 不保留 schema、参数或结果正文。原生 Anthropic 和总开关关闭路径沿用原逻辑。

名称映射复用已有 translator 机制：Codex 使用同一活动目录的截短消歧，Gemini/Antigravity 使用现有 `SanitizedFunctionNameMap`。没有重复翻译、再次调用 plugin 或另建检索/执行子系统。本轮 Claude 声明集合保护不等于跨协议完整历史内容摘要认证；T26 原生多结果保护仍按原精确范围生效。

提交 `4a747f7f` 修复 Gemini 两类已复现身份错误：不同长名/字符替换后同名，及响应 canonical fallback 将 `Read/read`、`_lookup/lookup` 串名。声明、历史调用/结果和强制选择统一使用已有消歧映射；buffered/SSE 回应优先精确映射，再保留原 fallback。正常和 compat translator 均覆盖。

## 工程回归

- 80 个实际 executor 本地子案例：Codex、Chat、Meta、xAI、Gemini，HTTP/SSE 各覆盖正常、长名称、删除声明、重命名、超预算、增加未加载函数、重复声明和强制选择。58 个不合法最终请求在 localhost 上游零发送；20 个正常对照和两个 xAI 合法修复对照允许发送。
- xAI 会清除 post-rule 孤立 choice，采用默认 auto；测试核对最终合法形态，未改动既有修复。这两例不计为“原始 post-rule 强制选择被拒绝”。
- Gemini 四种身份对覆盖正常/compat 请求、声明/历史/选择及 buffered/SSE 精确回填；失败日志和后续完整包通过记录保留。
- Go 1.26.4：全仓 `go test -json ./... -count=1` 的 103 个有测试包通过、29 个包无测试文件、零 package/test fail。8 项既有可选实时探测/原生语料/TLS 测试跳过，新增回归无跳过；`go vet ./...`、server build、whitespace 检查通过。
- 首次细分筛选未命中子案例，不计验证；更正筛选后的失败复现保留。早期 xAI 断言过严、沙箱缓存/localhost 限制和错误 SDK 构建路径均保留，未改判其原始运行。

精确配置、凭据绑定、原始线包和日志保留在 `.omo/tool-loading-completion/`，不纳入远端交付。覆盖以实际回归范围为准，未测路径仍未知。

## 官方 Claude Code 编码对照

固定候选 `e202aaa8` 的隔离 SDK binary 从干净 revision 构建，`vcs.modified=false`。本机官方 Claude Code `2.1.289` 使用独立 home、配置、workspace 和 MCP fixture；路由沿用冻结的 `gpt-6-luna` / Codex OAuth，启动校验配置和凭据绑定，不刷新或更换身份。此前 T26 属于另一候选，不能合并认证。

预登记一个 `coding-budget-selective` 任务族，lazy→eager 固定顺序，两案例，每案最多 8 次上游请求、8 次 MCP dispatch、120 秒，总上限 16/16。显式结构化最终输出用于结果验收；ToolSearch 是否生效另外核对实际检索、加载、调用和回填。启动前默认 Python 缺 `jsonschema`，尚未创建 case/journal 或发送 Provider 请求；失败日志保留，后续复用已有验收环境，未安装依赖。首次实际批次完整结束，未追加或重跑 Provider 案例。

| 指标 | eager | lazy |
| --- | ---: | ---: |
| started / finished | 1 / 1 | 1 / 1 |
| 独立行为测试 | 11 / 11 | 11 / 11 |
| 代码修改及最终结果 | 通过 | 通过 |
| 上游请求 | 4 | 5 |
| 实际 MCP dispatch | 3 | 3 |
| 禁止 MCP dispatch / 副作用 / 最终 active | 0 / 0 / 0 | 0 / 0 / 0 |
| 首轮输入 tokens | 39,965 | 2,629 |
| 峰值输入 tokens | 40,959 | 3,952 |
| 全任务输入 tokens | 162,062 | 16,563 |
| 全任务输出 tokens | 597 | 686 |
| cache read tokens（已含于输入） | 119,296 | 6,144 |
| cache write tokens（实际报告） | 0 | 0 |
| 包含准备/验证/清理的耗时 ms | 36,651 | 28,711 |

lazy 多一次检索，已计入全任务输入；全任务输入点估计减少 **89.78%**，首轮 **93.42%**，峰值 **90.35%**。9 个实际请求的输入计量均可解析，另报告输出增加；账单和可信费用仍未知，不从 cache 或 token 比例推断付费节省。一个固定任务族、固定顺序和观察到的缓存差异，不提供质量非劣、稳定延迟或净收益单侧 95% 下界。

lazy 首个上游请求没有 fixture MCP 参数 schema，只含 ToolSearch、StructuredOutput 两个普通函数及既有 image_generation built-in。官方客户端确实调用 ToolSearch 并返回三个 `tool_reference`，之后加载、执行 read/replace/tests 三个 coding 工具并回填。eager 首包有 70 个 fixture MCP 声明。配置、客户端 hash 不变，两隔离网关均退出 0，最终 MCP active=0。

独立只读核对 `independent-pair-audit.json` 验证两份 started spec 与预登记逐项相同、2 started/2 finished、9 个实际请求/响应均有合法终态，输入逐请求求和与全任务值一致；fixture schema 在首次调用前已声明，三个调用各有一条关联结果，客户端原始引用与网关加载清单一致。lazy 的逐请求 fixture 声明数量为 `[0,3,3,3,3]`，eager 为 `[70,70,70,70]`。审计脚本首次误用字符串调用 bytes-only parser 的失败记录保留，修复后只读重核通过，未重跑 Provider 或增加案例分母。

**权限证据范围：** 本轮零禁止 dispatch 指 MCP 夹具观测；两臂均存在网关既有 image_generation 声明，未见该工具执行，但不能据 MCP 计数认证 Provider hosted 工具的授权、副作用或费用。函数激活集合的严格检查不改变 hosted built-in 策略。这是后续要单独定位的责任边界，不隐去该声明，也不写成全部工具权限已验。

## 对完整目标的影响

本轮增强网关生产路径的声明、身份和预算约束。官方客户端取消/动态权限证据、Muse 一对多结果、Gemini 真实身份和计量边界、hosted 工具授权，以及代表性多文件任务的高置信全任务收益仍需独立完成。真实结果不转移为未测模型、transport 或客户端模式的认证。

此前 PR #25 的授权更新已完成；本轮新增修复为本地提交，不混入先前仅三个提交的远端记录。参见[当前状态](2026-10-13-tool-loading-current-status_CN.md)及[全任务净收益方案](../superpowers/plans/2026-10-13-tool-loading-net-benefit-pilot_CN.md)。
