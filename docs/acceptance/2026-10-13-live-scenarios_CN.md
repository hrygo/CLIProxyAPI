# 真实客户端三场景验收入口

核验日期：2026-10-13。承接[预先锁定批次及固定候选实测](2026-10-13-predeclared-batches_CN.md)，对应[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) L2/T6。本报告先记录可执行场景与回归；未运行的在线场景不计入真实案例。

## 场景与 gold

`live_clients.py --scenarios lookup unavailable multitool` 为每客户端产生三对、六任务，沿用事前锁定、平衡顺序、完整预算预留及所有已开始任务入分母。不同场景具有不同 template/pair/prompt，eager/lazy 同对 gold 相同。

| 场景 | 必须满足 |
| --- | --- |
| lookup | 一次实际 ledger dispatch，完成并释放；最终 plain JSON 使用 fresh receipt |
| unavailable | 请求 omega，所有声明仅允许 alpha；观察到目录、零 dispatch，准确报告不可用，不替代项目或编造金额 |
| multitool | ledger 完成后另次执行确认工具；参数使用实际 receipt；两次执行均完成释放，最终准确使用两个结果 |

确认工具在原始 JSON Schema 校验及权限检查后，对“尚未成功查询”和“错误 receipt”执行前拒绝。查询结果超过输出预算也不建立可确认状态。它是本地合成只读服务；事件只记录身份、阶段和结果摘要，不记录参数或 receipt。

multitool 还检查真实出站 history：某次请求只有查询结果，后续请求再包含确认结果。两次调用塞进同一 Code Mode 执行不冒充跨轮回填；prompt、assistant text 和 call arguments 不作为结果证据。unavailable 是“无受支持工具”的 gold，不声称客户端 search 返回了空列表；歧义检索仍需独立扩展。

catalog 新增确认工具，注册总数 68、默认可见 67，hash 已变；旧版本在线证据不能并入本轮分母。first-request probe 按当前可见目录核对声明数，未裁剪注册项。

## 离线验证与剩余工作

新增 10 项回归：三场景锁定、精确答案与 fresh receipts、有序执行与释放、跨轮 replay、结果包装及伪造来源排除、pre-dispatch 拒绝、失败查询不激活确认、真实 MCP stdio 确认与撤权、六任务调度。全套 136 项 Python 测试通过，未跳过；日志 `t6-scenarios-final-green.log`。CI 已使用 `unittest discover` 自动纳入新增测试。本轮未修改 Go 源码，未复跑完整 Go gate。

目前仍是显式 Chat profile 的校准入口，尚未实现全部 Provider 的最终统一矩阵、完整 immutable runtime scope、代表性 Coding 和按 route/template 的统计认证。模型 metadata、依赖内容、OAuth/account/region/cache 控制未知部分保持未知。未发布、未修改默认配置、未重启原服务。

## 固定版本在线结果

候选 `3de2aee8e240c6459b6202773dc011342a29ff9a`（Go `1.26.4`，`vcs.modified=false`），路由 `qwen-ds41-flash` → `deepseek-v4.1-flash` / `openai-compatible-qwen`。两个独立事前锁定批次、每批请求/dispatch 预算 48/48、每任务 90 秒，未补跑替换失败。

| 客户端 | 严格通过 | 实际 HTTP | 实际 dispatch | 禁止执行/副作用/结束 active |
| --- | ---: | ---: | ---: | --- |
| Codex CLI 0.160.0 | 6/6 | 16 | 6 | 均为 0 |
| OpenCode 1.19.0 | 4/6 | 22 | 6 | 均为 0 |

所有 12 个任务开始并完成记录，12 个候选退出 0，source config 均未变；plan/journal 离线绑定校验通过。Codex 批次退出 0，OpenCode 因两个严格失败退出 1。无匹配场景均零 dispatch；多工具场景均有查询→确认实际执行和分轮结果回填。OpenCode 的 lookup/multitool lazy 虽答案、receipt、执行和终态均正确，最终 JSON 包在 Markdown fence 中，format=false；按模型任务格式失败保留，未放宽 gold。这只能定位失败表现，不能单凭它确定模型/客户端提示的根因。

| 客户端/场景 | 首次输入 eager→lazy | 峰值 eager→lazy | 全任务 eager→lazy | 全任务减少 |
| --- | ---: | ---: | ---: | ---: |
| Codex lookup | 53,873→12,259 | 54,044→18,575 | 107,917→49,237 | 54.38% |
| Codex unavailable | 53,902→12,288 | 53,902→28,299 | 53,902→62,821 | **−16.55%** |
| Codex multitool | 53,919→12,306 | 54,291→18,939 | 162,296→68,526 | 57.78% |
| OpenCode lookup | 582→582 | 52,949→13,817 | 106,357→35,053 | 67.04% |
| OpenCode unavailable | 614→614 | 52,860→20,455 | 53,474→41,750 | 21.92% |
| OpenCode multitool | 620→620 | 53,174→13,964 | 159,682→49,097 | 69.25% |

全部 usage 为 Provider 实测 input，含 cache；两例格式失败的成本仍计入。每客户端三个任务对的首轮/峰值/全任务降幅中位数：Codex **77.20% / 65.12% / 54.38%**，OpenCode **0% / 73.74% / 67.04%**。OpenCode 第一请求是 title，未移出指标或改称后续主任务请求。其首轮门槛未达到，未知账单不宣称省钱。三个模板不足以计算正式非劣或收益认证，未提供窄置信区间。

Codex unavailable 的 eager/lazy 请求为 1/3。实际 lazy 出站历史有四个搜索调用，声明数 17→32→40；查询/额外轮次扩大总输入，即使结论正确也不能宣布普遍省上下文。OpenCode unavailable 为 2/4 请求，含标题。总请求与时长分别报告，失败不删。

| 客户端/场景 | 请求 eager/lazy | 整任务毫秒 eager/lazy | cache read tokens eager/lazy |
| --- | --- | --- | --- |
| Codex lookup | 2/3 | 7,958/7,052 | 98,816/25,472 |
| Codex unavailable | 1/3 | 5,965/11,368 | 0/9,216 |
| Codex multitool | 3/4 | 9,946/8,983 | 152,320/44,288 |
| OpenCode lookup | 3/4 | 6,814/7,333 | 53,760/20,736 |
| OpenCode unavailable | 2/4 | 5,312/8,373 | 0/20,736 |
| OpenCode multitool | 4/5 | 9,868/8,869 | 106,112/35,200 |

时长包含候选启动/执行/清理，不冒充纯 Provider 延迟；样本不足不报正式 p95。cache write、实际账单与 account/region 不明，保留未知。

原始证据分别保存于 `.omo/tool-loading-completion/t6-live-qwen-three-scenarios/` 与 `t6-live-qwen-opencode-three-scenarios/`；计划摘要分别为 `8b6df688e581f0fc4ba790a2de932f0fd6e9f77f14afbac10d68cecab9738989`、`695341482939964cec451425732df41d7462ff7a5b433ccc11bac9e00466f57c`。验证产物在同名前缀 `-validation.json`。

## 新发现：OpenCode 工作目录隔离不足

实际出站系统提示包含当前仓库 `AGENTS.md`；`opencode run --help` 明确 `--pure` 仅禁用外部插件。相同客户端/模式在仓库内与仓库外做本地首包对照（无 Provider 推理）：仓库政策标记分别存在/不存在，系统提示长度 eager 19,776→8,915、lazy 19,775→8,914 字符。路径外移可以排除此处祖先指令，但尚不构成所有运行时指令都冻结的证明。

因此上述 OpenCode 在线结果只作为实际环境的有限实测，不能认证为无项目指令影响的隔离 benchmark，也不把其格式失败直接归罪 Provider。下一步需修复客户端工作目录边界、补回归与实际 wire 检查，再以新 scope 复测；旧失败和旧 input 继续保留。

随后同位置建立独立 Git 根的本地诊断排除了项目政策标记，eager/lazy 模式仍被实际观察到。适配器已改为为每个私有 workspace 初始化独立仓库；禁用 operator Git system/global config 与 templates，不读取或改写原仓库政策。新增回归使用真实 `git rev-parse` 验证 worktree 根归属。该修复需新的客户端 harness scope 和在线复测，不事后改写上述 12 例。

修复后全套 Python **137 项通过**，日志 `t6-workspace-git-boundary-green.log`。生产适配器本地首包复测四个 client/mode 均观察到正确模式，均无仓库政策标记；产物 `t6-native-private-git-workspace/`。此阶段无新增 Provider 推理，只证明工作目录边界及首包模式；在线格式质量、收益和完整运行时隔离仍需复测。
