# 当前响应诊断候选：60 例复测与剩余边界

> **历史冻结批次说明：** 本报告保持 T20 原始分母、数据和旧门槛判定。用户随后明确采用[全任务净收益目标](../superpowers/plans/2026-10-13-tool-loading-net-benefit-pilot_CN.md)，60 例／45%／10% 不再是各路统一通过线；新目标不把旧结果改判为通过。报告中的“现行门槛”指本批冻结时的规则。

核验日期：2026-10-13，Asia/Shanghai。状态：**数量覆盖完成，41/60 safe，完整验收未通过。** CLIProxyAPI 为实施主体，Codex 与 Claude Code 为重点；模型别名中的 `opencode-go` 不表示使用 OpenCode 客户端。本轮不发布、不修改默认配置、不重启原服务。

## 冻结范围和全部分母

- 执行候选：`58f5523b90d0561ff7812ad8da8257f14acb4ef5`；binary SHA-256：`42920710b5c0feda73458abb4bf16e79b57989fdcd8a89b726b8bac59e8e3c9c`。沿用 [T19 四例校准](2026-10-13-response-diagnostics-calibration_CN.md)的网关二进制。
- Codex CLI `0.160.0`、Claude CLI `2.1.289`，五条声明模型路由，三个固定非编码任务 `lookup` / `multitool` / `unavailable`，各 eager/lazy：共 60 例。
- Codex GPT/Muse/GLM/Qwen 使用 Code Mode；Codex Gemini 使用 native JSON schema，Claude 五路均使用 native structured output。不同客户端模式与源协议分别锁定，不混为原生 `tool_search` 认证。
- run：`live-matrix-75ec264e0e654220bdba65c5d760d064`；plan SHA-256：`21652bc7d34a26128c16a9ccce5ce50378e660d016e28e053dfe1edffd6fb430`。所有 CaseSpec、执行材料和顺序在首个任务前冻结。
- 默认配置 SHA-256：`a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`。用户已确认该配置属于其他任务的预期改动；本任务保留它。60 份退出回执均记录 `source_config_unchanged=true`，复核摘要也未变。
- 每 case 上限 90 秒、8 次物理请求、8 次 dispatch；批次上限请求/dispatch 各 480。长脚本异步执行。到达声明终点结束，未增加配额、刷新 OAuth 或自动换账号。

60 planned / 60 started / 60 finished，**41 safe、19 非成功**：8 model、3 budget、8 unknown。已开始任务全部保留；没有尚未开始或未封存案例。未知类的具体启动原因另有证据，但不重写原 journal 分类。

161 次已记录物理请求中，149 次 HTTP 200、12 次 HTTP 429；这些次数包含重试与辅助请求。8 例启动失败缺少请求/执行计数，因此**实际请求总量、dispatch 总量和全部预算是否满足均为未知**。已知 dispatch 为 46，MCP attempts 为 47；52 例可观测 forbidden dispatch / forbidden effect / 最终 active 为 0，另外 8 例未知，不补零。网关退出回执：52 次为 0，8 次为 1。

52 份独立响应诊断均封存、无 active、无 collector failure，响应转换失败事件为 0。8 例没有该回执。正常封存不证明所有协议语义正确，也不覆盖尚未实测的响应拒绝与 WebSocket 诊断。

## 按 client/route 配对结果

每行 3 个预声明固定模板配对；成功列为 eager / lazy，各分母为 3。输入列为三个模板的成对减少率中位数，**包含失败任务实际产生的输入**，不挑选成功子集、不汇总不同 Provider tokenizer。负值表示增加。输入包括 cache-read，不等于账单节省。

| Client / 声明上游模型 | Safe E / L | 首轮输入减少 | 峰值输入减少 | 全任务输入减少 |
| --- | --- | --- | --- | --- |
| Codex / `gpt-6-luna` | 3 / 3 | 67.13% | 49.35% | 33.03% |
| Codex / `muse-spark-1.3-contributor` | 3 / 2 | 69.42% | 52.77% | 43.03% |
| Codex / `glm-5.3-flash` | 1 / 0 | 73.95% | 73.50% | **−5.02%** |
| Codex / `deepseek-v4.1-flash`（Qwen endpoint） | 1 / 2 | 73.40% | 55.10% | 42.54% |
| Codex / `gemini-3.5-flash-lite` | 1 / 1 | 未知 | 未知 | 未知 |
| Claude / `gpt-6-luna` | 3 / 3 | 93.82% | 88.65% | 86.31% |
| Claude / `muse-spark-1.3-contributor` | 3 / 3 | 94.35% | 89.08% | 84.09% |
| Claude / `glm-5.3-flash` | 3 / 3 | 95.05% | 90.44% | 88.04% |
| Claude / `deepseek-v4.1-flash`（Qwen endpoint） | 3 / 3 | 94.65% | 89.33% | 87.27% |
| Claude / `gemini-3.5-flash-lite` | 0 / 0 | 未知 | 未知 | 未知 |

非 Gemini 八行的首轮、峰值点估计超过现行 45% 数值门槛；Codex GLM 全任务输入收益不满足 10%。所有行只有三个固定模板，收益单侧 95% 下界均缺失；质量的条件性下界为 −63.16pp，Codex Muse/GLM 有回归簇时为 −86.46pp，均不能满足 −2pp。全部案例仍有未知 scope，模板的代表性与独立性未证实，不能宣称可靠率或通过正式认证。

延迟与缓存分开报告。审计产物保留每行 E/L 的 elapsed 中位数和 cache-read 总量；elapsed 包含准备、执行及清理，不等于 TTFT。冷热缓存条件、TTFT、region、货币成本与实际账单未知，不能填零。Gemini 因输入计量冲突及辅助调用缺成本契约，继续保持输入收益未知，详见[计量边界](2026-10-13-gemini-usage-boundary_CN.md)。

## 失败责任边界

| 观察 | 责任与可证明范围 | 不作的推断 |
| --- | --- | --- |
| 8 例 Gemini 启动失败均有 `credentials / binding_content_mismatch`；配置摘要未变 | 运行材料漂移与冻结保护：同名凭据内容不再符合本轮只读预检摘要，隔离候选拒绝启动 | 来源、刷新机制及账号身份仍未知；不能说是网关工具转换 bug，也不能自动换绑定删除旧失败。 |
| Claude Gemini multitool E/L 各记录 6 次上游 HTTP 429，最终耗尽任务时间 | Provider HTTP 响应边界及重试/任务预算；均保留 `budget` 主分类与 HTTP 错误证据 | 配额根因、持续性、是否 thinking 导致尚未证明；不能因两模式都失败而称该路线质量可用。 |
| Codex Muse lazy lookup 答案检查失败且未观察到规定工具模式 | 模型/客户端闭环质量待进一步对照；实际 dispatch 与合法终态分别记录 | 零响应诊断事件不能证明整个 Gateway 绝无语义问题；不伪造正确答案或执行。 |
| Codex GLM lookup lazy 耗尽 8 次 HTTP admission；其他 GLM、Qwen 失败涉及格式或中间回执 | 客户端策略、模型输出和任务 oracle 边界；相关合法终态不替代格式/回填正确性 | 不放宽 oracle、不删除失败，也不把输入降低直接当任务成功。 |

本轮没有新增已证实的响应转换失败，因此没有基于这些失败修改 Gateway 执行逻辑。请求级取消、多输出保真、真实响应拒绝、WS 诊断与代表性 repository 编码仍有单独缺口。

## 继续交付与验收结论

本轮新增[任务族登记分析模块](../../ops/tool-loading/task_registry.py)与 16 项回归。它在首个任务之前通过 batch materials 绑定 sampling frame、完整 registry、精确 CaseSpec 与随机 seed；仅允许预登记 fixture/catalog/permission 差异，固定 client/provider/model/协议/路由与双模式身份。重复任务、同一 fixture 或 route 的伪拆分、材料篡改、缺失配对和未知 usage 均有负向覆盖。完整验收 Python **304 项测试通过**；网关 Go 源码和执行二进制未变，本轮未重复 Go 全仓门禁。

模块提供 opt-in 离线分析，**尚未接入 live runner 的自动登记执行，也没有 repository fixture、独立性证明或可认证标签**。本批次未预登记新任务族，不能事后套用新模块升级证书。工作流见 [README](../../ops/tool-loading/README.md#preregistered-task-clusters)，正式设计见[两层验收方案](../superpowers/plans/2026-10-13-tool-loading-formal-acceptance-design_CN.md)。

G1 部分完成；G2 未闭合且本批 8 例计数未知；G3 当前候选数量覆盖达到 60，但代表性编码未完成；G4 未通过；G5 未通过。优先继续预登记 repository fixture 与 runner 集成，再做 bounded pilot；Codex 取消交付与 Gemini 材料/计量问题并行保留。未启动 2,980 或 5,260 案例的大规模统计试验。

## 本机复现证据

- [冻结 plan](../../.omo/tool-loading-completion/t20-primary-60-v1/batch-plan.json)、[完整 journal](../../.omo/tool-loading-completion/t20-primary-60-v1/cases.jsonl)、[成对分析](../../.omo/tool-loading-completion/t20-primary-60-v1/pairs.json)、[独立核对审计](../../.omo/tool-loading-completion/t20-live-audit.json)。
- [只读绑定预检](../../.omo/tool-loading-completion/t20-readonly-binding-preflight.json)、[registry 初始 RED](../../.omo/tool-loading-completion/t20-registry-red.log)、[Python 全套 GREEN](../../.omo/tool-loading-completion/t20-python-all.log)。

私有原始请求、响应及凭据路径不进入公开报告；上述 `.omo` 证据仅留本机。完整 journal 可以按原冻结 plan 重放，不能用新 registry 事后改判：

```sh
python3 ops/tool-loading/analyze_pairs.py \
  --journal "$task_validation/cases.jsonl" \
  --plan "$task_validation/batch-plan.json"
python3 -m unittest discover -s ops/tool-loading/tests
```
