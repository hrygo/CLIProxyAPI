# 辅助 HTTP 请求计量与后台任务封存

核验日期：2026-10-13。代码 revision：`e51fa10baf0aaa6a04caedd05d4f7acad194afb0`。
本轮补齐验收日志、共享预算、模型输入统计与后台额度任务的关闭屏障。
这是确定性回归与本地 SDK 证据，完整目标仍按
[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)
执行；没有新增真实 Provider 认证或上下文收益结论。

## 已确认缺口及修复

1. `antigravity-generate-v1` 已加入 usage decoder，但 CaseJournal 的导出白名单缺少对应计数字段，完整导出触发 `KeyError`。现补齐与 GenerateContent 同语义的字段映射；任意正文和非计数字段仍不导出。
2. HTTP journal 原先对所有请求使用固定模型契约，无法表示实际额度查询。现仅依据真实 Antigravity executor identity，为允许的辅助操作选择 `antigravity-auxiliary-v1`，并将契约固定到每个 attempt 的开始和结束。缺失绑定、错误模型角色和跨契约辅助身份在 dispatch 前拒绝。
3. importer、CaseJournal 与聚合器现保留辅助角色及固定操作。所有物理 attempt 共用请求预算；模型请求和辅助请求分别计数。辅助正文即使包含 token-like 字段也不成为模型 usage，缺失账单保持未知。
4. 完整 `run_case` 原先把额度响应送入模型声明和终态解析，产生框架错误。现只对模型 attempt 检查声明、结果回放与模型终态；全部 attempt 仍保留在日志、预算、HTTP 错误和结算检查中。
5. 独立后台额度任务可能在 HTTP handler 返回后继续工作。新增可选的 owner scope，在启动 goroutine 前注册；关闭时停止准入、取消并等待实际 cleanup，再封存 HTTP 证据。无 owner scope 的生产请求保留已有生命周期；没有新增已建立上游连接的网络超时。

## 计量与证据契约

| 字段或证据 | 当前含义 |
| --- | --- |
| `request_count` | 全部已观察物理请求，包括辅助、失败和未完成 attempt；用于共享预算 |
| `model_request_count` | 模型推理 attempt；包括标题生成等辅助推理 |
| `auxiliary_request_count` | 明确辅助 HTTP 契约的 attempt |
| `unknown_auxiliary_cost_requests` | 无已验证账单的辅助 attempt，不填零成本 |
| `first/peak/total_input_tokens` | 对全部模型 attempt 计算；模型 usage 缺失仍使对应指标未知 |
| `auxiliary-work.json` | 版本 1、仅计数、私有且不可覆盖的 owner 结算记录 |
| Candidate isolation version 6 | 必须有 owner 结算证明；活动、被拒绝或不一致任务不能获得成功封存判定 |

固定辅助操作为 `credits_query`、`oauth_refresh`、`project_discovery`、`http_request`。
允许的记录必须有真实 Antigravity executor、非空 credential fingerprint 和空 model fingerprint。
开始与结束之间不得变更角色、契约或操作。只有辅助请求时，模型输入仍为 null；未完成辅助请求仍使任务结算失败。
旧证据不改写，缺少新的 owner snapshot 时保留未知。

## 验证结果

- 全量 Python：218 项通过。新增回归覆盖完整 export、混合预算、伪造角色/操作、未完成辅助请求、模型 usage 缺失、结算篡改/重复 JSON/符号链接以及完整 `run_case` 的模型视图。
- 全量 Go：103 个包通过，失败包为 0。
- `executor/helps`、`executor`、`acceptance`、`live-gateway` 四包 race 通过。
- `go vet ./...`、服务构建、候选构建、Go 1.26.4 的 tracked-source gofmt、离线模型目录校验及 28 项维护测试通过。
- 实际额度队列回归先复现 owner `Started=0/Active=0`，修复后验证 source cancellation 不改变默认独立生命周期、owner cancellation 能到达工作 context、底层 transport 未释放时 join 不提前完成、关闭后新任务不能启动。测试使用同步通道，不使用 sleep。

从干净的 `e51fa10` 构建候选，Go build metadata 确认为 `vcs.modified=false`。
实际 SDK 与本地 HTTP upstream 的复测如下；未调用真实 Provider，也未启动真实 Coding Agent：

| 合成案例 | Client HTTP status | 物理上游请求 | 归因 | 关闭证据 |
| --- | ---: | ---: | --- | --- |
| 本地声明预算拒绝 | 413 | 0 | budget；1 条 typed Prepare failure | 候选退出 0，HTTP/Prepare/owner 均封存 |
| 上游模仿预算错误文本 | 400 | 1 | provider；0 条 typed Prepare failure | 候选退出 0，HTTP/Prepare/owner 均封存 |

两个 SDK 案例的 owner `started/completed/active/rejected` 均为 0，`sealed=true`；
这只验证候选在零后台任务时的集成关闭链。
非零后台额度任务的取消与等待由真实 executor queue 的确定性回归证明，尚未替代 native Gemini 的 SDK/live 复测。

私有证据位于 `.omo/tool-loading-completion/`：
`t6-auxiliary-final-{full-go,vet,server-build,python,catalog,maintenance}.log`、
`t6-auxiliary-sealed-race.log`、`t6-auxiliary-sdk-final/`。
RED 证据保留在 `t6-auxiliary-{accounting,journal,http,live-view,scope,owner,seal}-red.log`。

## 剩余工作与边界

- Go live profile 仍未允许 Antigravity；还需将辅助 admission 与显式选择的 OAuth credential、固定路由绑定，验证 wrapped function-result 回放，再启用 native Gemini 候选。
- `oauth_refresh` 和 `project_discovery` 的契约名称可记录，不表示其完整 OAuth/live 流程已认证。候选继续禁止刷新原服务 credential。
- 后台额度任务 owner 已接入；不合作的底层 transport 仍可能耗尽 launcher cleanup budget，证据应保留未验证，不补造 finish。
- 本轮没有新增 eager/lazy 实测、通过率或 token 节省。最终单一 revision 的 60-case、代表性 Coding、逐路由质量非劣与收益置信门槛仍未完成。
- 原默认配置 SHA-256 复核仍为 `3a6ab28f1605437c2cd9358b7a5320781718f30266353c93953882469b9f46fe`。未发布、未推送、未修改默认配置、未重启原服务。
