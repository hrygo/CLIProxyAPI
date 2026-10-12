# 上游 HTTP 尝试与 usage 证据链

核验时间：2026-10-13，Asia/Shanghai。承接[完整方案 T6](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)及[客户端取消报告](2026-10-12-client-native-cancellation_CN.md)。

## 本轮实现与证据

增加显式启用的请求级 HTTP observer，以及独立验收用 `internal/acceptance.HTTPAttemptJournal`。没有生产配置开关或全局采集器。observer 位于既有 usage transport 中，在代理/TLS 选择之后、实际 transport 调用之前执行；嵌套 usage 包装不重复统计一次实际 dispatch。

真实 Responses handler 集成测试首次失败：buffered/SSE 请求均完成，但 journal 为空。源码确认 handler 使用 `context.Background()` 作为执行 parent，保留 Gin context，并独立转发请求取消；任意 request-context 值不会自动继承。修正 observer 只从既有 Gin request 取回显式采集 hook，保留原来的 deadline、值隔离与取消语义。修正后，两种 handler 场景均贯通实际 auth manager、Codex executor、工具 bridge 与临时本机 HTTP 上游。

每次 dispatch 前，request body 与 start 记录均完成写入及 `fsync`。原始请求/响应使用独占文件、0600 权限与 SHA-256；不采集 headers、URL 或错误消息。请求不可复制、请求体超限、请求次数耗尽或采集初始化失败时阻止发送。响应保存受字节预算约束，但不裁剪执行器实际收到的字节。EOF、提前 close、传输错误、读取错误和采集错误分别记录；EOF 只是传输观测，不证明协议合法完成。

Python `http_attempts.py` 校验顺序、身份、协议契约、metadata allowlist、大小与摘要，拒绝 symlink artifact、重复 finish 和被修改的原始文件。四种显式 wire contract 的 usage 样例覆盖 buffered/SSE、累计 snapshot 与 Anthropic 分阶段 usage；不将累计值相加，冲突或缺失标为未知。所有开始的 HTTP 尝试均导入已经开始的 `CaseJournal`，无 finish 的尝试保持未完成。Python 导入发生在读取快照后；发送前持久化的证明来自上游 journal，不伪装成 Python 在发送前记账。

## 验证范围

- observer 回归证明：start 早于 transport 调用，失败重试保留，嵌套包装只计一次，observer 拒绝不误标上游发送。
- 实际本机 HTTP 回归证明：请求与响应字节不变、发送前已有 durable start、headers/地址未进入 artifacts、提前关闭与超限不误报完整、预算耗尽阻止后续 dispatch。
- 实际 gateway handler 的 buffered/SSE 两个场景通过；另将 Go 导出的真实本机 HTTP artifacts 交由 Python 校验并导入任务台账，读出 fixture 指定的 input=31/output=2。`gate_status` 仍为 `unverified`。
- 新鲜 Python suite：89 tests 通过。Go 全量测试、相关包 race、vet 与 server build 通过；`GOTOOLCHAIN=go1.26.4 ops/upstream-intake/verify-absorb.sh` 完整维护 gate 最终为 `REGRESSION GATE: PASS`，仅证明工程门禁，不是 live 认证。

原始记录保存在 Git 忽略目录 `.omo/tool-loading-completion/`：`t6-observer-red/green.log`、`t6-journal-red/green.log`、`t6-import-red/green.log`、`t6-handler-green.log`（保留首次失败）、`t6-handler-fixed.log`、`t6-handler-export-20261013/`、`t6-full-final.log`、`t6-race-final.log`、`t6-python-final.log`、`t6-gate.log`。复现步骤见[验收工具 README](../../ops/tool-loading/README.md)。

## 剩余工作与限制

本轮未调用真实 Provider；31/2 是受控响应指定的计数，不是实际模型 tokens，也不是上下文节省。四种协议的 usage 解析样例不是四家 Provider 的运行认证。当前已验证的完整入口只有 Responses→Codex HTTP bridge；WebSocket、其它 handler/provider route、动态路由选择下的实际契约与身份仍需分别验证。

尚需把 collector 接入隔离真实客户端 runner，关联最终版本、配置/catalog/权限指纹，完成不可变 run manifest 与脱敏导出、平衡调度、批次预算及成对统计。最终版本 60 个真实跨 Provider 案例、代表性 Coding 任务、−2pp 非劣区间、50% 首轮/峰值及 10% 全任务收益门槛均未达成。已有客户端取消与 runtime 限额缺口仍独立保留。

没有发布、候选安装、默认配置/catalog 切换或原服务重启；本轮仅构建了忽略目录内的候选测试二进制。
