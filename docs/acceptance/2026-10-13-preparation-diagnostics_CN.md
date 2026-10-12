# 工具准备失败的可信诊断验收

核验日期：2026-10-13（Asia/Shanghai）。本报告覆盖本地准备阶段诊断和隔离验收器，
不增加真实 Provider 样本，不认证任务质量或上下文收益。完整目标继续按
[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) 验收。

## 问题与修复边界

[Muse 实测报告](2026-10-13-native-responses-muse_CN.md) 记录过本地累计声明预算
拒绝：HTTP 413，声明 270,783 字节超过 262,144 字节。旧验收器仅观察物理上游
HTTP 请求和 HTTP admission，缺少本地 Prepare 边界证据，原始结果保留为 framework。
本次没有改写旧结果，也没有通过匹配客户端错误文字重新分类历史样本。

本轮在 `internal/responsestools` 增加可选同步 observer，只记录本地 Prepare
返回的真实 `ToolCompatibilityError`：固定 phase、reason、status_code。
SDK 的 buffered、stream、count 三个入口在调用执行器之前报告；HTTP handler
在另行提供 parent context 时保留 observer，不改变原有取消生命周期。
错误文本、schema、参数、凭据标识均不进入这份诊断。

隔离候选将请求序号和诊断封存到私有 `capture/preparation.json`，限制 256 个请求，
要求零 active、无 recorder failure、禁止覆盖。readiness 升至 isolation_version 5，
新的 case 指纹包含诊断版本。Python 读取器拒绝重复 JSON 键、非法类型、额外字段、
符号链接及未封存证据。已知本地预算拒绝归为 budget，其他已知准备失败归为 protocol，
未知原因或诊断缺失归为 framework；精确 reason 保留供定位。

早期 handler 校验、路由 guard、执行器及 Prepare 之后的错误不属于本 recorder
范围，需使用其各自证据。诊断不产生虚构 HTTP attempt，既有请求与 usage 不被抹去。
observer 的使用契约是同步 Prepare 返回边界；本次没有扩展为任意异步错误审计接口。

## 回归与真实 SDK 夹具

SDK/handler 回归使用旧实现 overlay：三个入口均真实拒绝声明并保持零执行器调用，
但 observer 为空；另行 parent context 丢失 observer。四项断言先失败，修复后通过。
同时覆盖上游同名文字、未知 reason 脱敏、重复释放、未结束请求、并发 32 个请求、
请求释放后的非法 callback，以及诊断拒绝后保留此前八个请求的输入 usage。

真实 SDK server 使用独立合成配置和 loopback upstream，结果如下：

| 场景 | 客户端 HTTP | 物理上游请求 | 封存 Prepare 失败 | 归因 |
| --- | ---: | ---: | ---: | --- |
| 本地声明预算拒绝 | 413 | 0 | 1，declaration_budget / 413 | budget |
| 上游返回同名错误文字和 JSON code | 400 | 1 | 0 | provider |

两个候选均退出 0，源配置指纹未变。它们证明实际 SDK、HTTP context、执行入口、
封存文件与验收归因的连接；不使用真实凭据或真实 Provider，不能计入 60-case 矩阵。
提交后在干净 `76c77849f86871046ec7ecc95108887a6170d68e` 上重建候选并复跑两例，
结果一致；Go build metadata 为 `go1.26.4`、`vcs.modified=false`。

Go 1.26.4 全量测试 103 个 package 通过；全量 vet、server build 通过；
core、SDK auth、HTTP handler、隔离候选四个 package 的 race 通过。
新增并发回归后的隔离候选 race 复测通过，最终 Python suite 202 项通过。
本地完整日志与夹具证据保存在 `.omo/tool-loading-completion/t6-preparation-*`，Git 忽略。

## 完整目标仍未达到

本轮没有新的跨 Provider 通过率或 token 节省测量；此前不同 revision 的实测不能
合并成为当前版本认证。下一步仍需补齐 Antigravity 的线包、usage、终态及辅助请求
观测，锁定同一最终候选执行完整 60-case 矩阵和代表性编码任务，并取得逐路由的
质量非劣与收益置信区间。诊断修复只解决归因缺口，不保证模型检索策略或生成参数正确。

仅本地提交；没有 push、PR 更新、发布、安装、默认配置或 catalog 修改、原服务重启。
