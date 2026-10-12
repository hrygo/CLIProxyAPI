# Antigravity 线包与辅助 HTTP 观测回归

核验日期：2026-10-13（Asia/Shanghai）。本轮完成两个前置实施切片：包装线包的
确定性读取，以及辅助 HTTP 请求的可选观测。没有启用 Antigravity live profile，
没有增加真实 Provider 样本；[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)
的最终 60-case、编码任务、质量与收益门槛仍未满足。

## 包装线包的读取

本地提交 `8f70d46` 增加独立 `antigravity-generate-v1` 观测契约，读取
`request.tools` 和 `response.usageMetadata`，不按普通 Gemini 或模型名称猜测线包。
源码允许的 `functionDeclarations`、`function_declarations` 两种拼写均计量，
包括同一容器同时存在两者的情况；声明字节保留完整容器，函数计数只统计声明叶子。
usage 的多次累计快照合并，不相加；输入计数冲突、错误包装和异常身份保留为未知。
缺少的 cache-write 和账单字段不补零。

明确终态须保持 response identity 一致，且所有已观察候选都结束；只有 `STOP`
可作为成功。失败终态可用于完整计量，但不能成为成功任务。未知枚举、重复结束、
尾随错误和截断不认证终态。缺少 finishReason 的既有健康 EOF 修复路径，额外要求
验收器的传输封存证明完整、存在实质内容、response ID 和有效输入/候选计数；
`closed`、`read_error` 或未结束的捕获不能冒充 EOF。

候选结束枚举核对了 [Google 官方 GenerateContent 文档](https://ai.google.dev/api/generate-content#FinishReason)。
该文档支持内部候选字段的解释；私有包装形式及健康 EOF 兼容来自本仓库 executor
和已有夹具，不能由公开 API 文档推断整条 Antigravity 路线已受支持。

新增线包/usage 回归先在旧实现失败；审查时进一步修复同一容器两种拼写漏计、未知
finishReason 被当成确定终态两个问题。最终完整 Python suite 209 项通过。

## 辅助请求进入观测边界

当前源码显示生成请求使用 model-bound usage reporter，而异步 `loadCodeAssist`
查询使用独立 context，旧路径未把 HTTP observer 一并复制。回归实际进入 timed
credits queue 后复现 observer=0、identity=nil；修复后 observer=1，查询 task 可等待释放。

本地核心提交 `b5ef4d45471d2b411a790da6984d7a03ad7df822` 的可选 observer
覆盖固定辅助 operation：`credits_query`、`oauth_refresh`、
`project_discovery`、`http_request`。身份包含实际 executor 和凭据指纹，不将辅助
请求伪装为模型推理；model fingerprint 留空，operation 只在这些固定入口产生。
独立 context 仅复制 observer，保留原 lifetime、transport 与已有定时行为；没有
增加网络 timeout。默认不注册 observer，没有默认配置切换。

回归证明：真实 credits 查询可在 transport 前拒绝、零 dispatch；嵌套包装与
redirect 合计两个物理 dispatch 和两个观测；另行 parent context 的选择、显式禁用
和 Gin 请求 observer 传播均保留。源请求取消没有改变 warm-hint 原有独立生命周期。
这验证的是 fixture 中的 task 释放，不替代整个候选服务的 shutdown 验收。

Go 1.26.4 全量测试 103 个 package 通过，executor 与 helps 的完整 race、全量 vet、
server build 通过；tracked gofmt 与离线 Codex catalog 校验通过。
完整日志位于 Git 忽略的 `.omo/tool-loading-completion/t6-antigravity-*`。
源配置指纹未变；没有 push、PR 更新、发布、安装、默认配置/catalog 修改或原服务重启。

## 尚需完成的连接

隔离 Go profile 仍拒绝 `antigravity`。下一步必须把辅助 identity 接入固定 journal
契约、实际请求预算与 importer，分别报告辅助请求和模型输入，保持缺失账单为未知，
再验证异步工作与 journal/candidate shutdown 的衔接。不能仅删除辅助请求或关闭原有
credits 功能来获得表面输入收益。

随后使用只读、明确绑定的 OAuth snapshot 和单一最终 revision 跑完整矩阵与代表性
编码任务；不同旧 revision 的数据不合并认证。本轮没有新的通过率、上下文节省或
统计置信结论，完整目标保持未完成。
