# T13 primary routes 冻结验收记录

核验日期：2026-10-13。此记录把初始漂移批与 v2 候选分开保留。v2 当前候选为 `4b428edfa70e98df3780f4850ab1d7129b95ed14`。本报告只引用公开模型 alias、非凭据 summary、paired diagnostics、代码 diff 与退出回执；不包含账号、凭据路径或秘密。

## 批次与配置快照

| 批次 | plan SHA / run | source-config fingerprint | 已开始 / 严格通过 | journaled requests / known dispatch / unknown dispatch | 原判 |
| --- | --- | --- | ---: | ---: | --- |
| T13 初始漂移批 | `adb97761ef6f7957a8f049ded3d45839a86a1c12d1e304a86c7abd20d15772d9` / `live-matrix-f51b20e025df44368aed21c69889f2a2` | `43dc5a62…` | 10 / 0 | 3 / 1 / 9 | `unverified`；request/dispatch 总数未知。保留原判，不与 v2 合并。 |
| T13 v2 冻结批 | `41fe0a8e91cb4411df6af8b2e4011208a6273a7ae83821238f6c6f408b28fd73` / `live-matrix-2aeee3a041b54b3299d1763fd3802093` | `a746ab767d3e1ab33ed5e513f61dd119854b5ebc17db9f1a7e2053f0976a31f7` | 10 / 8 | 28 / 12 / 2 | `unverified`；10/10 started，0 unfinished。 |

旧批依据 [原 batch audit](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot/batch-audit.json) 与 [原 plan](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot/batch-plan.json)；v2 依据 [v2 audit](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/batch-audit.json)、[v2 plan](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/batch-plan.json) 和 [v2 case summary](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/summary.json)。用户已确认 `43dc…→a746…` 是其他并行任务预期的配置变化；这不是 T13 批次自行声明或造成的全局状态。

`source_config_unchanged` 只表示对应候选进程生命周期内的单批观测：初始批 10 份 gateway receipt 中 9 份为 `true`，Codex Muse lookup lazy 一份为 `false`；v2 批 10 份 receipt 均为 `true`。这不代表两批之间、并行任务期间或整台机器的用户配置一直未变。摘要中的配置指纹仅作批次识别，不展开配置内容。基线指纹也见[此前 Codex Code Mode 记录](2026-10-13-codex-code-mode_CN.md#L57)。

## Gemini candidate 启动边界

v2 Gemini eager/lazy 两例在 credentials binding 校验阶段得到 `binding_content_mismatch`，并以 `candidate_start_failure` 结束；候选路径读取绑定对象字节用于内容 hash 比对，但在不匹配后未进入凭据合成或服务启动。两例的退出码为 1、源配置未变；summary 标注 `no_gateway_attempt_capture`。相应 `dispatch`、`forbidden_effect`、`active` 计数为 `null`，而不是零；不能把 `upstream_attempts=0` 或 `usage.request_count=0` 当作完整边界证明。

来源为 [v2 summary 的 Gemini case diagnostics](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/summary.json)，以及两例的 `gateway.exit.json` 与 [case journal](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/cases.jsonl#L23)。此处仅记录 hash 比对的失败原因与启动边界，不记录绑定内容或其定位信息。

## 四个有完整用量的 eager/lazy 配对

新 paired 文件的 run ID 与 plan SHA 均匹配 v2；它含四个有完整原始用量的配对，以及一个 Gemini 无 usage 配对。下表的首轮、峰值、全任务 token 取自 v2 `.summary.json`；百分比是同组 eager 相对 lazy 的观察减少率。全任务输入包含 cache-read，不把 cache-read 另加一次。

| 客户端 / 路由 / 模板 | 首轮输入 E→L（减少） | 峰值输入 E→L（减少） | 全任务输入 E→L（减少） | cache-read E→L；cache-write | case elapsed E→L；请求数 E→L |
| --- | ---: | ---: | ---: | ---: | ---: |
| Claude / `gpt-6-luna` / `ledger-json-lookup-read-repeats-v2` | 39,630→2,422（93.89%） | 39,784→4,276（89.25%） | 79,414→10,875（86.31%） | 39,424→0；0→0 | 8.493→10.500 s；2→3 |
| Claude / Meta `muse-spark-1.3-contributor` / `ledger-json-lookup-read-repeats-v2` | 48,208→2,696（94.41%） | 48,373→4,962（89.74%） | 96,581→12,441（87.12%） | 47,985→4,834；未知→未知 | 7.676→9.914 s；2→3 |
| Codex Code Mode / Meta `muse-spark-1.3-contributor` / `ledger-json-lookup-read-repeats-v2` | 54,536→16,658（69.46%） | 54,895→24,910（54.62%） | 109,431→127,341（−16.37%；lazy 多 16.37%） | 54,129→101,173；未知→未知 | 9.924→20.180 s；2→6 |
| Codex Code Mode / Meta `muse-spark-1.3-contributor` / `coding-pure-python-budget` | 54,855→16,735（69.49%） | 56,525→23,662（58.14%） | 223,175→125,037（43.97%） | 165,971→99,637；未知→未知 | 21.823→29.865 s；4→6 |

每个模型组只有一个 template cluster；region 与 cache condition 未知。四组 paired diagnostics 的 observed reductions 与表中相符，但 95% 收益下界均为 `null`（`insufficient_clusters`）；质量差值虽观测为 0，conditional one-sided 95% lower bound 为 −0.95，仍不足以证明非劣。整体 gate 保持 `unverified`，且 billing 未评估。依据：[与 v2 run/plan SHA 匹配的 paired diagnostics](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2-pairs.json)、[summary usage](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/summary.json) 与 [case elapsed](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/cases.jsonl)。

Gemini 配对的首轮/峰值/全任务输入、缓存及节省值全部保留为未知；两例在启动前失败，不能拿它们填零或排除后宣称总体认证通过。初始批的 [旧 paired 文件](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-pairs.json) 使用 plan SHA `adb97761…`，对应 0/10、3 journaled requests、1 known dispatch、9 unknown 的漂移批；它不是 v2 paired 文件，也不回写旧结果。

## Codex Muse coding host：完整 diff 与验证

`coding-pure-python-budget` 的 Codex eager/lazy 两例均 `safe_task_success=true`、`checks.code=true`，其余 answer/dispatch/nonce/format/terminal checks 也通过；每例 3 dispatch，`forbidden_dispatch=0`、`forbidden_effect=0`、`active=0`。两份 `code-outcome.json` 均记录 `passed=true`、`tests=11`、`passed_tests=11`、`changed=true`。eager source SHA 为 `d44d5b2de322bed080ea57adbce567ad08c3dd24db96ff5cfaf781081734f1e3`，lazy 为 `be4cdbab465e9e1de90db51eb525394e77255a5823f61bfcdb7f347fdee8c346`。完整原始 diff 也保存在[eager source.diff](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-eager/source.diff)与[lazy source.diff](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-lazy/source.diff)。

eager host test record：[code-outcome.json](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-eager/code-outcome.json)；lazy host test record：[code-outcome.json](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/codex-opencode-go-muse-spark-1.3-code-mode-only-coding-budget-lazy/code-outcome.json)。对应 case journal 在 [eager `case_finished`](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/cases.jsonl#L35) 与 [lazy `case_finished`](../../.omo/tool-loading-completion/t13-primary-route-coding-pilot-v2/cases.jsonl#L49)。

eager diff：

```diff
--- baseline/module.py
+++ candidate/module.py
@@ -1,5 +1,14 @@
 def remaining_budget(limit, entries):
+    if type(limit) != int:
+        raise ValueError
+    if type(entries) != list:
+        raise ValueError
     total = 0
     for entry in entries:
-        total += entry['amount']
+        if type(entry) != dict:
+            raise ValueError
+        if type(entry['amount']) != int:
+            raise ValueError
+        if entry['state'] == 'posted':
+            total += entry['amount']
     return limit - total
```

lazy diff：

```diff
--- baseline/module.py
+++ candidate/module.py
@@ -1,5 +1,16 @@
 def remaining_budget(limit, entries):
+    if type(limit) is not int:
+        raise ValueError
+    if type(entries) is not list:
+        raise ValueError
     total = 0
     for entry in entries:
-        total += entry['amount']
+        if type(entry) is not dict:
+            raise ValueError
+        amount = entry['amount']
+        state = entry['state']
+        if type(amount) is not int:
+            raise ValueError
+        if state == 'posted':
+            total = total + amount
     return limit - total
```

## 冻结结论与待证项

本批证明的是一个有效 Codex Muse coding template 的 eager/lazy host 验证，不等于 60 个最终用例、广泛代表性编码或跨模板质量认证。取消传播仍是独立未完成项，见[Codex MCP cancellation gap](../compatibility/codex-mcp-cancellation-gap_CN.md)。T14 的 48-case 与 Gemini 12-case 分别冻结、分别审计，其结果独立交付，不回填本批。

## Gemini v3：当前凭据基线的独立复测

只读重算原先选定文件的原始字节摘要后，新建 v3 声明，未更换文件选择、模型或 endpoint，未刷新或写回凭据。文件名摘要保持相同，内容摘要变为 `114e703ad31c781274dff66069b26362ffa31009f39fc05cbf7d74cbe0483048`；这只证明本次选定字节，不证明此前账号内容未改变，也不解释变化来源。候选仍为 `4b428edfa70e98df3780f4850ab1d7129b95ed14`、source-config 仍为 `a746…`。

v3 plan SHA 为 `5b4349c28bdf4dac3e18c7c44ece4d1e9631c00bea7e9d19fe5e54ce7168cdf4`。四例全部 started/finished，严格通过 **2/4**，共 **23** 个实际请求（包括模型发现等辅助请求）与 **2** 次 dispatch，未知请求/dispatch 案例为 0。四例均 `forbidden_dispatch=0`、`forbidden_effect=0`、`active=0`，候选退出码 0，生命周期内源配置未变。来源：[v3 audit](../../.omo/tool-loading-completion/t13-gemini-primary-pilot-v3/batch-audit.json)、[v3 summaries](../../.omo/tool-loading-completion/t13-gemini-primary-pilot-v3/summary.json)、[v3 paired diagnostics](../../.omo/tool-loading-completion/t13-gemini-primary-pilot-v3-pairs.json)。

| 客户端 / 模式 | 实际请求 / dispatch | 严格通过 | 已观察边界 |
| --- | ---: | --- | --- |
| Claude eager | 7 / 0 | 否 | 模型请求返回 `429 RESOURCE_EXHAUSTED`，原文提示检查 quota；客户端重试后任务预算耗尽。没有成功工具执行或合法成功终态。 |
| Claude lazy | 7 / 0 | 否 | 同样收到 429；保留 provider 拒绝及任务时间预算影响，不称为工具检索缺陷的证明。 |
| Codex lazy | 6 / 1 | 是 | 真实查询、nonce、dispatch、JSON format 和合法终态均通过。 |
| Codex eager | 3 / 1 | 是 | 同上。 |

原始上游包中，Claude 的 `generationConfig.thinkingConfig.thinkingLevel=high`，Codex 为 `medium`；请求内容及工具策略也不同。这里只记录差异，未通过受控配对证明 thinking 导致 429。Codex 随后两例成功，因此不能把 Claude 的拒绝推断为全部 Gemini 请求不可用或整个账号配额已耗尽。429 是上游响应边界，不证明具体额度类别或永久限制；此轮不修改默认 thinking 配置。

Codex 两例的模型响应有 `conflicting_usage_snapshots`；Claude 被拒绝的模型请求未提供可信 usage。首轮、峰值、全任务输入及节省率保持 **未知**，辅助请求的成本也未知，不以 tokenizer 估算、较小值或最后一个快照替代真实 usage。成功的工具闭环与未验证的上下文收益分别报告；本批仍为 `unverified`。
