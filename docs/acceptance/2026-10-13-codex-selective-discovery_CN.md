# Codex 选择性工具发现：同版本 24-case 复测

核验日期：2026-10-13（Asia/Shanghai）。**本轮 20/24 safe successes；选择性发现的六个 lazy 案例全部通过，只加载三个所需工具。** 这证明有指导的 Code Mode 发现策略能改善峰值输入，不证明任意自然语言检索质量、原生 `tool_search` 支持或可靠率认证。

## 冻结范围与回执

- Gateway revision：`ca0758f4943baa97e651545a56c376adcdcb8e3d`；隔离 binary SHA-256：`f1d0214f3e75808fd457f2d06a2fe9852b05ae4988b9b037aff9bcd73a1fb278`，Go 1.26.4，`vcs.modified=false`。
- Codex CLI `0.160.0`，`code_mode_only`；GPT `gpt-6-luna` 与 Muse `muse-spark-1.3-contributor`，模型菜单 alias `opencode-go-muse-spark-1.3`。后者仍是 Codex 案例，不是 OpenCode 客户端。
- 配置 SHA-256：`a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7`；只读凭据绑定，不刷新、不自动切换账号。每个任务八请求/八 dispatch、90 秒；整批上限 192 请求/192 dispatch。
- 两 route × 三个单函数 coding task × 原始/选择性 prompt × E/L，共 24 cases。选择性 prompt 明确三个工具的实际全名；E/L 使用相同 prompt、fixture、权限、gold 和预算。
- 冻结 plan SHA-256：`ba0aac1016ac12a3aadac52cd2d649e06bf0661a5285fbe252585497185b70c3`；run：`live-matrix-7456762986cf4af38b720a5f12a9fc38`。

24 started / 24 finished；123 个实际出站请求，75 dispatch、85 MCP attempts；请求和 dispatch 无未知案例，均在预算内。24 例记录的 forbidden dispatch、forbidden effect、最终 active 均为零，24 个隔离网关退出回执为零。22/24 的代码通过 host gold，23/24 有代码修改，host 测试执行 280/288 通过；这些是测试执行次数，不是独立任务样本。代码已通过也不能替代最终格式、发现顺序或合法终态。

| Route | 原始 prompt safe E+L | 选择性 prompt safe E+L | 原始请求 / dispatch | 选择性请求 / dispatch |
| --- | --- | --- | --- | --- |
| Codex / GPT | 5/6 | 6/6 | 27 / 18 | 32 / 20 |
| Codex / Muse | 4/6 | 5/6 | 37 / 21 | 27 / 16 |

本轮 Python 回归 285 tests 通过；Go source 与此前全仓 103 个测试 package、vet、build 通过的版本一致。候选独立构建无 VCS 警告。四个 live 失败全部保留，脚本退出 1 是包含失败的批次结果。

## 成对上下文收益

下表按同 route、同任务、同 prompt 变体的 E/L 实际 Provider usage 配对。包含失败案例已经产生的输入；未知值不记零。百分比不是请求 body 大小或账单减少率。

| Route | Task / prompt | Safe E / L | 首轮降幅 | 峰值降幅 | 全任务降幅 |
| --- | --- | --- | --- | --- | --- |
| GPT | budget / 原始 | 1 / 0 | 67.19% | 48.33% | 40.81% |
| GPT | budget / 选择性 | 1 / 1 | 67.09% | 63.05% | 51.01% |
| GPT | identity / 原始 | 1 / 1 | 67.19% | 48.38% | 40.84% |
| GPT | identity / 选择性 | 1 / 1 | 67.08% | 64.88% | 65.91% |
| GPT | retry / 原始 | 1 / 1 | 67.19% | 48.60% | 40.94% |
| GPT | retry / 选择性 | 1 / 1 | 67.08% | 64.93% | 65.95% |
| Muse | budget / 原始 | 1 / 0 | 69.49% | 59.75% | 41.41% |
| Muse | budget / 选择性 | 0 / 1 | 未知 | 未知 | 未知 |
| Muse | identity / 原始 | 1 / 1 | 69.49% | 50.84% | 55.06% |
| Muse | identity / 选择性 | 1 / 1 | 69.39% | 66.55% | 67.87% |
| Muse | retry / 原始 | 1 / 0 | 69.49% | 60.08% | 53.85% |
| Muse | retry / 选择性 | 1 / 1 | 69.39% | 66.55% | 73.19% |

GPT 三个固定任务的峰值降幅中位数原始 48.38% → 选择性 64.88%，全任务降幅中位数 40.84% → 65.91%。选择性 Muse budget 的 eager 在首个响应失败，没有可用输入计量，因此不计算该配对，不能用其他两个已知配对补出完整三任务中位数。

真实历史观察：六个选择性 lazy 只加载 `coding_task_read`、`coding_source_replace`、`coding_tests_run`，没有先调用后加载，身份链一致。GPT 原始 lazy 使用宽泛 `ALL_TOOLS.filter`，三个任务均打印 19 个 fixture 工具；选择性只打印三个工具。按工具结果 text 拼接计量，发现输出约 40,155 字节 → 1,047–1,049 字节；这是文本字节，不等于 token，也不同于包含外层 JSON 的历史字节指标。Gateway 没有截断结果。

选择性 eager 也会执行发现，属于同 prompt 的公平 E/L 对照；不能将它与原始 eager 跨 prompt 合成新的“节省率”。Muse 原始 budget/retry 有 schema 加载前的实际调用，尽管最终代码通过，仍不能判定完整闭环通过。

## 四个失败与责任边界

1. **GPT / 原始 budget lazy：model。** 实际改了代码，host gold 10/11；最终回执/dispatch oracle 也失败。保留已经产生的五请求、三 dispatch 和输入，不用代码部分通过代替任务成功。
2. **Muse / 原始 budget lazy、retry lazy：budget。** 各八请求耗尽；代码都通过，但最终合法回执/终态缺失，且观察到先调用后发现。属于模型/客户端执行策略与预算边界，不应由 Gateway 伪造完成。
3. **Muse / 选择性 budget eager：原始分类 framework。** 首个原始 upstream function call 的 `arguments` 是空对象 `{}`，未提供 custom bridge 必需的 `input` 字段。Gateway 报 `upstream_contract` 并使流失败，MCP attempts、dispatch 与 forbidden effect 均为零。实际捕获支持“上游模型生成不合规参数、网关执行前拒绝”，不证明 Provider HTTP 可用性错误，也不证明 Gateway 误拒绝。现有 harness 未采集响应转换边界的结构化原因，非零客户端退出被归为 framework；原始 verdict 不修改，诊断另记。后续补观测应使用 typed error 与固定字段，不能从任意客户端错误文本猜分类。

非法对象不能通过默认填 `input`、丢字段或臆造代码“修好”。本轮没有新增 Provider 自动重试来覆盖失败。

## 延迟、缓存与认证边界

每 case 的 elapsed、cache-read、cache-write 原值见审计回执；elapsed 包含准备、执行、host gold 与清理，不等于 TTFT。GPT cache-write 记录为 0，Muse 缺少该字段，仍为未知；缓存冷热未受控，费用、region 和账单没有证据。全部输入包含 cache-read，不重复加总。

原始与选择性模板高度相关，每个独立 fixture scope 只有两个模板变体、各一对 E/L。自动分析返回 `unknown_scope` / `unverified`，收益置信下界缺失；质量条件性下界也未达到 −2pp，不可把有指导的六例通过宣称可靠率。用户将门槛改为 45% 后，原始 GPT 点估计也达到该数值门槛；**置信区间、代表性和最终版全路由覆盖依然未通过**。

## 复现与证据

[冻结 plan](../../.omo/tool-loading-completion/t17-codex-discovery-24-v1/batch-plan.json)、[原始 journal](../../.omo/tool-loading-completion/t17-codex-discovery-24-v1/cases.jsonl)、[配对分析](../../.omo/tool-loading-completion/t17-codex-discovery-24-v1-pairs.json)、[逐案审计](../../.omo/tool-loading-completion/t17-summary-audit.json)、[候选构建回执](../../.omo/tool-loading-completion/t17-candidate-attestation.json)。这些本机私有证据不包含在公开交付目录，不应原样上传 capture 或凭据材料。

```bash
python3 ops/tool-loading/analyze_pairs.py \
  --journal .omo/tool-loading-completion/t17-codex-discovery-24-v1/cases.jsonl \
  --plan .omo/tool-loading-completion/t17-codex-discovery-24-v1/batch-plan.json
```

没有发布、默认配置切换、客户端升级或原服务重启。详见[当前状态](2026-10-13-tool-loading-current-status_CN.md)。
