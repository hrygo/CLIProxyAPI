# 本地请求预算与上游错误的诊断边界

核验日期：2026-10-13。承接[原生输出复测](2026-10-13-native-structured-output_CN.md)及[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。本轮补齐隔离验收候选的请求预算归因；没有新增真实 Provider 任务或上下文节省数据，整体目标仍未完成。

## 已修复的诊断歧义

原 `HTTPAttemptJournal.Observe` 将记录器关闭、记录失败和尝试上限耗尽合并为同一错误，客户端再显示泛化错误。旧 GLM Codex 多工具案例虽观察到 8 个 HTTP 请求，不能只凭“high demand”或数量到上限认定 Provider 过载，也不能证明已经发生本地预算拒绝。

现按顺序判定关闭、记录失败、预算耗尽。只有真实预算拒绝返回 `ErrHTTPAttemptBudgetExhausted`；三类本地拒绝分别计数，不调用下游 transport，不新增物理请求记录。`reserved_attempts` 表示占用的记录槽位，可能包含出站前的记录准备失败，不能改名为已发出请求。

候选在 SDK 返回及 response-body release barrier 完成后，写入独占、0600 的 `capture/admission.json`：version、max_attempts、reserved_attempts、budget_rejections、closed_rejections、failed_rejections、active_bodies、journal_closed、journal_failed。该同步快照没有 URL、请求正文、凭据或错误消息，也不证明任务/流终态成功。

Python 校验固定字段、整数/布尔类型、锁定上限、原始 journal 数量、关闭和资源状态；拒绝重复字段、软链接及矛盾计数。没有旧快照时保留未知。新 runner 将快照保留在案例 summary；缺失或非法证据不能升级为成功。

## 判定与责任边界

| 证据 | 分类 | 限制 |
| --- | --- | --- |
| 关闭/记录失败拒绝，或 journal_failed | framework | 记录准备失败与已发出请求分开 |
| 有实际 budget_rejections，且槽位已到锁定上限 | budget | 不把拒绝次数计入物理 HTTP 或 usage |
| 无上述本地失败，实际捕获 HTTP 状态 ≥400 | provider | 只定位上游响应边界；可能源自配置、模型契约、权限或 Provider 自身 |
| 恰好达到上限但拒绝计数为 0 | 不推断预算耗尽 | 允许正常完成在最后一次请求上 |
| 无有效快照 | framework / 未知证据 | 不填 0，不改写旧案例 |

原始客户端错误消息不用于此分类。HTTP 200 中的错误、客户端时间预算、MCP dispatch 预算和生产声明预算分别处理；本轮不声称已经统一全部错误来源。案例的权限、资源和时间证据缺失时，既有评分器仍可将主分类保守降为 unknown，独立诊断快照不会因此删除。

## 验证

- Go 失败回归：未新增 sentinel/snapshot/writer 时编译失败；随后三种本地拒绝分开、真实 loopback HTTP 429 不误归为本地预算、快照私有/独占/不覆盖测试通过。两个相关 package 的 race 测试通过。
- Python 回归覆盖缺失快照、只到上限而未拒绝、类型/数量/状态/字段矛盾和软链接，以及上游 429 状态保留且不导出错误正文。任务级导入验证 8 个请求记录、160 input tokens；3 次本地拒绝保持独立，案例失败记 budget，不虚增 3 次 HTTP 或填入 usage。
- 全套 Python 170 项通过（10.795 秒），日志 `t6-admission-python-final-green.log`。全 Go 1.26.4 maintenance regression gate 通过，包括 race-sensitive packages 和 server build，日志 `t6-admission-full-gate.log`；相关 race 日志 `t6-admission-diagnostic-green.log`。
- 真实隔离 SDK + loopback HTTP 探针：上限 2，连续三次下游调用返回 200/200/500；上游收到 2 个 HTTP，原 journal 2 个物理记录，budget_rejections=1，closed/failed_rejections=0，active_bodies=0，journal_closed=true、journal_failed=false；评分分类为 budget。证据 `t6-admission-sdk-http-v2/`，不含真实 Provider 推理或 MCP，不计入 60 个案例。探针首轮临时配置错误地放在 upstream 层级，selected_auth_count=0 被启动校验拦住，未发出上游请求；原失败证据 `t6-admission-sdk-http/` 保留。

旧 GLM 案例没有该快照，仍不能追认其真实拒绝次数或“high demand”根因。以上 SDK 探针在未提交候选上执行，只证明本轮源码的本地接口闭环，不冒称干净最终 revision 的真实 Provider 认证。

## 后续与授权范围

整体仍缺单一最终 revision 的 60 案例、代表性 Coding、客户端取消/撤权/重复终结调用/历史恢复交付、原生/OAuth 路由覆盖、完整 scope 与足量统计证据。下一阶段优先扩展真实编码结果的验收，不把重复 ledger 模板凑作独立 Coding 样本。

本轮没有新增生产配置项、网络超时或工具执行入口；不发布、不安装、不改默认配置/catalog、不重启原服务。CLIProxyAPI 网关继续负责适配与诊断，客户端继续负责检索、授权、执行。图谱 generation 为 2026-10-01，新验收文件未被跟踪，相应结论以当前定向源码和实际测试为准，未重建索引。
