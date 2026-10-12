# OpenCode 显式标题验收配置

核验日期：2026-10-13。承接[三场景及隔离修复](2026-10-13-live-scenarios_CN.md)，对应[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) 的客户端控制和计量验收。本次控制只用于显式选择的临时 profile，不改变默认配置。

## 隔离修复后的在线复测

干净候选 `098ebf9df15fc397f3e810771d43d0172f04e05e`、Go `1.26.4`、OpenCode `1.19.0`，`qwen-ds41-flash` → `deepseek-v4.1-flash` / `openai-compatible-qwen`，原三场景/预算/硬格式标准未改。六任务全部开始、完成记录，严格通过 **4/6**，22 次真实请求、6 次 dispatch，禁止执行/副作用/active 均为 0；六候选退出 0、source config 未变，批次因严格失败退出 1。

实际 wire 六例均不再包含仓库政策标记；因此工作目录修复的目标有在线证据。lookup lazy 最终仍有 JSON fence，format=false；unavailable eager 客户端最终答案和终态正确，但第一个上游请求没有合法终态，整体 terminal=false。不能把隔离修复解释成全部格式/终态问题已解决。

unavailable eager 第一次为 title 请求，finish 记录 `read_error`、HTTP 200、实际观察/保留 42,111 字节、complete=false，缺合法终态及 usage。首轮、峰值及全任务输入保持 **未知**；其他已知请求输入 subtotal 50,191 不能当成完整成本。具体故障原因仍需独立同步/取消诊断，不归罪 Provider。旧失败、请求和成本全部保留。计划摘要 `c63786acdbcf15a15872402d15f48ca4d5ffaec32a7713b47d0f41224602dbf0`，证据 `t6-opencode-private-worktrees-live/`，journal/plan 校验结果 `t6-opencode-private-worktrees-live-validation.json`。

## 显式标题控制

当前安装 CLI 的 `opencode run --help` 支持 `--title`；`--format json` 表示事件格式，并不保证最终文本为 JSON。适配器新增可选 `session_title`；live runner 与首包 probe 新增 `--opencode-title`，eager/lazy 使用同一显式标题，默认仍自动生成。

标题策略进入 fixture hash，并为固定标题生成独立 pair ID。fixture hash 同时绑定 fixture、client adapter 和场景源码；不同配置或材料不能合并成同一对样本，运行参数偏离计划时发送前失败。标题文本不写入运营配置，公开 CaseSpec 仅含摘要。

真实客户端本地首包探针：Codex 两模式仍各一请求；OpenCode 两模式从默认“标题+任务”两请求变为一请求，并观察到 eager 完整声明、lazy execute 入口。这里没有 Provider 推理，不能用字节数宣布上下文节省。正式实测仍记录每个实际请求，若出现标题请求不会过滤它。

新增回归覆盖默认不变、仅支持 OpenCode、非法标题发送前拒绝、profile 分离与配对一致、锁定标题漂移零候选启动。该 savepoint 全套 Python **141 项通过**，日志 `t6-title-profile-savepoint-green.log`。以下补充同一候选的在线实测；不将显式标题的结果推广到默认 OpenCode 行为。

## 同一候选跨路由实测

候选 revision `4e68b5107f2b02b4ec927b74748f58fc5cdc8f97`，Go `1.26.4`，构建记录 `vcs.modified=false`。客户端 Codex `0.160.0`、OpenCode `1.19.0`；OpenCode 两模式固定相同标题 `Tool loading acceptance`，Codex 无标题参数。每例 90 秒、最多 8 次上游请求/8 次 MCP dispatch；每批上限 96 次请求/96 次 dispatch。三场景与硬 JSON gold 没有改变。

| 实际模型 / executor | 全部开始 / 严格通过 | 真实 HTTP 请求 | MCP dispatch | 候选退出 / 配置未变 |
| --- | ---: | ---: | ---: | --- |
| `deepseek-v4.1-flash` / `openai-compatible-qwen` | 12 / 11 | 30 | 12 | 12 个 exit 0 / 全部成立 |
| `glm-5.3-flash` / `openai-compatible-opencode-go` | 12 / 6 | 35 | 10 | 12 个 exit 0 / 全部成立 |

24 个开始全部有完成记录，无未开始或未完成案例；所有尝试的输入 usage 已知。两批预算均未超限，禁止 dispatch、副作用、结束时 active 均为 0。两批因严格失败退出 1，不能只报成功子集。实际 OpenCode 各 15 个请求均携带任务工具，未观察到独立标题请求；实际 system 未含仓库政策标记，不能以事后过滤请求解释节省。

计划 SHA：Qwen `e253c4894cb976328d92179c07661716f20ae173b4514674184cba2330ee0f88`，GLM `ef206df12b64cc70aec7c649c94d6511372322a724c1901d025a06bbf8863d99`。本地原始证据分别位于 `.omo/tool-loading-completion/t6-title-profile-qwen-live/` 和 `t6-title-profile-glm-live/`；对应 `*-validation.json` 已核验 journal 与锁定计划，`*-paired-analysis.json` 为只读分析，不包含认证结论。

## 输入计量及质量

表中数值为 eager → lazy 的实际 Provider 输入，包含缓存输入、失败任务及全部重试。每行只有一对观察，不是可靠率样本；跨模型不汇总 raw tokens 为统一成本。

| 路由 / 客户端 / 场景 | 首次请求输入 | 峰值输入 | 全任务输入 | 请求数 |
| --- | ---: | ---: | ---: | ---: |
| Qwen / Codex / lookup | 53,849 → 12,235 | 54,008 → 18,572 | 107,857 → 49,199 | 2 → 3 |
| Qwen / Codex / unavailable | 53,879 → 12,265 | 53,879 → 17,821 | 53,879 → 30,086 | 1 → 2 |
| Qwen / Codex / multitool | 53,896 → 12,282 | 54,250 → 18,704 | 162,218 → 67,918 | 3 → 4 |
| Qwen / OpenCode / lookup | 50,175 → 4,315 | 50,359 → 11,110 | 100,534 → 26,420 | 2 → 3 |
| Qwen / OpenCode / unavailable | 50,211 → 4,351 | 50,211 → 11,044 | 50,211 → 15,395 | 1 → 2 |
| Qwen / OpenCode / multitool | 50,221 → 4,361 | 50,510 → 11,452 | 151,089 → 38,105 | 3 → 4 |
| GLM / Codex / lookup | 53,225 → 11,752 | 53,300 → 17,986 | 106,525 → 47,625 | 2 → 3 |
| GLM / Codex / unavailable | 53,256 → 11,783 | 53,256 → 17,390 | 53,256 → 40,972 | 1 → 3 |
| GLM / Codex / multitool | 53,269 → 11,797 | 53,468 → 20,010 | 160,089 → 149,654 | 3 → 8 |
| GLM / OpenCode / lookup | 49,054 → 4,076 | 49,118 → 10,719 | 98,172 → 25,424 | 2 → 3 |
| GLM / OpenCode / unavailable | 48,298 → 4,115 | 48,298 → 16,062 | 48,298 → 20,177 | 1 → 2 |
| GLM / OpenCode / multitool | 49,098 → 4,125 | 49,261 → 10,881 | 147,522 → 36,444 | 3 → 4 |

| 路由 / 客户端 | eager / lazy 严格成功 | 首轮节省中位数 | 峰值节省中位数 | 全任务节省中位数 | 保守条件性质量差下界 |
| --- | --- | ---: | ---: | ---: | ---: |
| Qwen / Codex | 3/3 / 3/3 | 77.24% | 65.61% | 54.38% | −63.16 个百分点 |
| Qwen / OpenCode | 3/3 / 2/3 | 91.33% | 77.94% | 73.72% | −86.46 个百分点 |
| GLM / Codex | 3/3 / 1/3 | 77.87% | 66.26% | 23.07% | −98.30 个百分点 |
| GLM / OpenCode | 2/3 / 0/3 | 91.60% | 77.91% | 74.10% | −98.30 个百分点 |

这些是每条路线三个模板的描述性结果。地域与缓存条件仍 `unknown`，现有 replay 的配对仍 `comparable=false`；新分析只在未知字段相等且其他配对条件一致时展示观察值，不改变该判定。四条路线均未证明 −2pp 非劣，收益均没有有限的单侧 95% 下界。输入下降不意味着失败任务有价值，也不证明账单节省；缓存写入、价格和账单未验证。小样本延迟完整保留在 case journal，尚不发布 p95 结论。

## 剩余失败及责任边界

- Qwen：OpenCode multitool lazy 真实执行两个工具、回填并合法结束，但最终 JSON fence 违反硬格式标准，保留为失败。
- GLM：Codex lookup lazy、OpenCode lookup lazy 及 multitool 两模式违反最终格式；OpenCode unavailable lazy 的答案与格式均失败，零 MCP dispatch。
- GLM Codex multitool lazy：8 次真实请求、0 次 MCP dispatch；第一次检索后的下一请求中，lookup 和 confirmation 均有有效声明。随后模型连续用 `exec_command` 输出提示语，没有调用已加载 MCP。最终客户端报泛化“high demand”，不能据此判断 Provider 过载。现有报告分类为 `framework`，缺中间 receipt；请求预算达到上限的精确错误归因还需补充边界诊断。没有伪造任务终态。

上述格式/调用失败首先指向客户端输出约束、模型策略和错误诊断；本轮没有证明 Gateway 丢失声明、错配身份或遗漏工具回填。没有重写答案、放宽 gold 或用后来成功覆盖这些失败。Codex/OpenCode 的取消与权限生命周期缺口仍须单独交付和实测。

## 统计方法及回归

新增 `ops/tool-loading/analyze_pairs.py`，先验证 journal，提供计划时同时验证绑定；仅分析单一 run/revision，按客户端、实际 route、版本、profile、缓存和预算分组。同模板的重复不增加独立 cluster 数；所有失败、未完成、缺配对和缺 usage 均保留，缺失不能用已知子集代替，负收益不裁剪。

质量采用保守条件性诊断：令模板内平均成功差为 D，若该模板任何一对发生 eager 成功/lazy 失败，则 I=1，否则 I=0；逐模板 D ≥ −I。因此用 I 的单侧 exact binomial 上界的负数约束等权模板平均差。忽略改善，可能较宽；独立、同分布且代表性的模板抽样和预先固定重复设计均未验证，不能把该诊断当正式认证。计算依据为 [NIST 精确二项区间](https://www.itl.nist.gov/div898/handbook/prc/section2/prc241.htm) 的尾概率反演，单侧采用 alpha=0.05。

收益采用每模板成对收益中位数，再取模板中位数；条件性下界选择最大阶次 j，使 `P(Binomial(n,0.5) ≤ j−1) ≤ 0.05`。n=3 时最小值覆盖率也只有 87.5%，没有有限的 95% 下界；同三个模板重复二十次仍为三个 cluster。该推导不证明本批三个固定 fixture 代表真实 Coding 任务。

回归覆盖零损失不产生零宽区间、NIST 数值及闭式边界、非法计数/概率、非有限收益、重复聚类、失败/未完成分母、未知与缺失、scope 漂移、路由隔离、负收益及 CLI 的 journal/plan 拒绝。全套 Python **155 项通过**，日志 `t6-pair-analysis-final-green.log`；新增 14 项。NIST 展示上界 0.401029 为近似数，本实现尾概率求根为 0.4010281173，回归使用 1e−6 展示误差容限，并以两个独立闭式边界交叉核验。分析器是后续只读代码；真实候选仍为以上 `4e68b51`，没有声称新分析器 commit 已在线复测。完整目标保持未完成。

本轮未改 Go 源码，未复跑完整 Go gate。未发布、未改默认配置、未重启原服务；全部路由、最终 60 案例、Coding、权限生命周期与统计认证仍按原目标继续。
