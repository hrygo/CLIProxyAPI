# Responses 工具响应失败诊断：实施边界与回归要求

核验日期：2026-10-13。**最小响应诊断已实现并通过本地回归；新候选四例正常路径 live 校准通过，尚未获得完整认证。** 范围仅是本地工具响应转换诊断；不增加自动重试，不修改权限、调用参数或流终态，不要求改默认配置或重启服务。

## 触发案例与当前实现

T17 Muse / selective budget eager 的实际 upstream 状态为 HTTP 200，桥接函数声明 `parameters.required=["input"]`、`additionalProperties=false`，响应却生成 `arguments="{}"`。本地转换返回 `upstream_contract`，随后结束上游读取；零 MCP attempts、零 dispatch。原始 harness verdict 为 framework，因为客户端非零退出且响应侧原因没有结构化回执。原始 capture SHA-256 为 `70894daf1a9f9f1f0e4a127de5cb24b359506683d1bc171d2b2bdcb18f6a7789`，详见[T17 报告](../acceptance/2026-10-13-codex-selective-discovery_CN.md)。

改动前 [`failure_observer.go`](../../internal/responsestools/failure_observer.go) 只定义准备阶段的 observer，`RewriteResponse`、流 `Feed` 和 `Finish` 错误仅返回/发出错误，没有响应侧回执。现在新增 [`response_failure_observer.go`](../../internal/responsestools/response_failure_observer.go)，在 [`responses_tools_execution.go`](../../sdk/cliproxy/auth/responses_tools_execution.go) 的三个本地转换边界调用；[`handlers.go`](../../sdk/api/handlers/handlers.go) 向执行 context 传递独立 observer。隔离 candidate 新增 [`response_evidence.go`](../../ops/tool-loading/live-gateway/response_evidence.go)，原 [`preparation_evidence.go`](../../ops/tool-loading/live-gateway/preparation_evidence.go) 不变。以上依据源码读取及回归；图谱相应路径 not_tracked/metadata_changed，未用索引缺失证明不存在。

## 最小改动与责任分工

| 层次 | 已实现 | 保持的边界 |
| --- | --- | --- |
| `internal/responsestools` | 新增可选 response-failure observer，仅导出固定 phase、Reason allowlist 与 StatusCode | 保留准备 observer 的原契约；不导出 Err、正文、tool 名、call_id、模型或凭据 |
| `sdk/cliproxy/auth` | 在本地 `RewriteResponse` / `Feed` / `Finish` 返回 typed `ToolCompatibilityError` 时同步报告一次 | 不把 executor 的任意 error 或 error 文本冒充本地转换错误；错误、取消和 Close 路径保持原行为 |
| `sdk/api/handlers` | HTTP request context 向实际执行 context 传递该可选 observer | 保留显式 parent observer、取消、deadline 与其他值；WS 生命周期同样核验，不能假定 HTTP 覆盖 WS |
| 隔离验收 Gateway | 独立 `response-failures.json`，请求序号与固定字段、版本化 sealed snapshot、排他创建及 0600 权限 | 原 `preparation.json` v1 不改含义；有限记录预算；请求 finish 等待其同步 callback 后关闭该 scope，迟到报告不再写入；重复/超额/未结束/写入失败标证据失败 |
| Python 验收 | 严格读取固定 schema、加 `response_diagnostics`；未来批次在已验证本地转换错误时归为 protocol，已确认预算错误仍归 budget | 历史缺文件标未知，原 journal 不重写；诊断只定位响应转换边界，不直接断言 Gateway bug 或 Provider HTTP failure |

phase 限定 `response_restore`、`stream_feed`、`stream_finish`。不复用 `prepare` 来掩盖不同错误发生位置。`ReasonUpstreamContract` 可以表示模型生成违反声明的结果；单凭本地 Reason 不能证明 Gateway 有缺陷，仍须比对实际出站声明、上游响应与协议契约。消费者已取消时不生成迟到诊断；取消中未完成转换的原因仍未知，不能将空 snapshot 推论为取消或资源生命周期已认证。

失败分类优先保留证据完整性/预算与准备阶段错误，再判断明确的本地响应转换失败；没有该诊断时才按实际 HTTP 错误分类为 Provider。早期重试 HTTP 429 与最终转换错误可以同时存在，前者仍保留在逐请求记录，不能抢占后者的主要终止归因。

## 必须覆盖的回归

1. **身份与隐私。** wrapped typed error 输出固定字段；同名普通错误文本不产生可信诊断；未知 reason 只输出 unknown；任何 error payload、receipt 或路由内容均不进入回执。
2. **准备与响应分离。** Prepare 拒绝零出站，只记录准备阶段；模型返回空 `{}` / 错误 `input` 类型 / 多字段 custom arguments 时记录响应转换失败。合法响应不增加失败记录。
3. **流语义。** 出错前已有 frame 时仍只发一次失败，取消上游并释放 attempt；不补 completed，不修补 arguments，不添加重试。真正 EOF 缺终态在 Finish 保持原错误；取消与断流不被升级成完成。
4. **Context 与资源。** HTTP 与 WS 实际入口 observer 可达；parent observer 不被覆盖；并发请求记录正确归属。结果被取消、下游不再消费、回调迟到和证据封存先后顺序均有确定性同步测试，不用 `time.Sleep` 猜顺序。
5. **验收读入。** 旧文件缺失不记零错误；新回执未知 phase、重复字段、非法请求序号、符号链接、未 sealed、active 非零、过量或额外敏感字段均拒绝。回调 owner 停用后不能改写封存 snapshot；这不证明停用后上游再无工作。证据失败不被 protocol 分类覆盖；请求与任务失败的原分母保持完整。
6. **真实案例复核。** 使用脱敏合成声明与 `{}` 复现 T17 错误，证明保持 fail-closed 与零执行；新 live 批次需锁定新材料，不能把新诊断写回旧 run 冒充当时采集。

## 交付与回退

实现前新增测试因缺少 observer/reader/recorder 失败；实现后定向测试通过。审查时补了取消中转换的回归：移除取消检查的 overlay 会导致两个测试失败，当前实现通过 race 检查。最终 Go 1.26.4 全仓 103 个测试 package、vet、server build 均通过；四个相关 package 的完整 race 及取消修复后的定向 race 通过；Python 288 tests 通过。回执见[本地验证](../../.omo/tool-loading-completion/t19-response-diagnostic-verification.json)。

HTTP 执行 context 的传播有新增回归；WebSocket 使用相同 `GetContextWithCancel` 入口已有源码证据，既有全仓回归通过，但尚没有新增的真实 WS 响应诊断实测，不能据此宣称 WS 诊断已认证。新增观测不成为适配成功的前置业务条件；仅隔离验收将缺失/失败证据保留为未知或 framework。关闭 observer 即不收集诊断，原适配与错误语义不变；原服务配置、运行进程和默认客户端策略不参与此次实施。

该改动完善归因，不解决 Codex 多输出保真、MCP 请求级取消、Gemini 权威计量或统计样本不足。后者分别保留独立交付与未知标签，见[当前状态](../acceptance/2026-10-13-tool-loading-current-status_CN.md)。

[四例真实校准](../acceptance/2026-10-13-response-diagnostics-calibration_CN.md)验证了新 `isolation_version=7` 候选的回执可达、封存及请求数匹配；模型均生成合法参数，没有真实复现拒绝事件，不能把正常路径通过算成异常实测。
