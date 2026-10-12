# 跨 Provider 验收的只读候选 bootstrap

核验日期：2026-10-13。本轮推进 T6 的真实 Provider 验收前置基础设施；**推理上游仍为本机合成 fixture，另已只读解析实际配置并验证两个 API-key 路由的候选启动；没有读取真实 OAuth 文件或执行真实 Provider 推理**。

## 交付和责任边界

新增 `ops/tool-loading/live-gateway/`，复用官方 SDK 的配置解析、API-key 凭据合成、执行器注册及模型别名初始化。源配置只读；HTTP bootstrap 使用合成源配置，实际配置用于后述只读路由核验。候选状态目录新建且私有，日志、插件、Home、发现、控制面、配置 watcher 与冷却状态持久化关闭；候选不安装，原服务及默认配置不改动。

新增 `live_gateway.py` 管理独立进程生命周期及 readiness；`probe_live_gateway.py` 使用本机合成配置、合成 API key 与合成上游验证完整 bootstrap。SDK 自己绑定端口，启动器先取得可用端口再释放；存在竞争窗口，不能宣称无竞争。实际 PID 和随机实例 nonce 的 HTTP 校验确保竞争/启动失败不会被当作本轮候选启动成功；仅终止本轮子进程组。

当前构造范围为 Codex Responses、Gemini GenerateContent、Claude Messages，以及配置中的 `openai-compatible-*` Chat 执行器。未知执行器/协议不猜测。发送前检查实际 executor、model 指纹及冻结的初始 auth-ID 集合；不符时零上游 dispatch。原始源配置、响应工具策略与 auth-ID 集合有指纹；启动器核对二进制、子进程身份、profile、指纹格式和集合摘要。

## 已发现并修复的前置问题

1. 首个合成配置把 provider 放在 `credentials` 下，候选实际选中凭据数为 0，启动器拒绝。依据当前模板改为 `api-keys.openai-compatibility`，没有放宽启动检查。
2. 仅内存 Save 并不能阻止共享 OAuth 的远端刷新。最终隔离版本 3 清除候选快照的 refresh token并设置后台刷新门槛；API-key 凭据由 SDK 调度器排除。复用 SDK 创建的实际 auth manager，保留原配置的选择策略和 session-affinity 生命周期，避免过早 StopAutoRefresh 同时停止 selector。到期访问凭据属于不可用/失败，不自动刷新共享凭据。此结论来自源码及合成测试，真实到期 OAuth 尚未实测。
3. 更严格的源对象不变测试发现 `Auth.Clone()` 只拷贝顶层 metadata。已在隔离入口深拷贝 JSON 容器，再清除 refresh token；每次 Store.List 也独立深拷贝。测试验证顶层/嵌套源 token 不变，并通过 Manager.Load 后检查刷新门槛仍保留。未改生产通用 Clone 语义。

相关失败日志与早期候选证据保留在 `.omo/tool-loading-completion/t6-live-*`。全量 gate 曾因源对象不变回归失败，修复后的最终 gate 与 race 检查另存；不使用早期绿色覆盖后续失败。

## 实际 bootstrap 验证

最终证据目录：`.omo/tool-loading-completion/t6-live-bootstrap-sdk-final/`。

- 源为私有合成 config；public model `fixture-alias` 实际解析为 upstream `fixture-model`。
- 实际 SDK 执行器为 `openai-compatible-fixture`，契约为 `openai-chat-v1`。
- 一个真实本机 HTTP 请求进入实际 executor；一个 attempt 被采集，合成返回内容验证通过。
- 初始 auth-ID 指纹集合与实际执行匹配，源文件指纹前后相同，候选干净退出。
- 合成 usage `31/2/33` 不代表真实 Provider token，也不用于节省率。

最终 Go 完整维护 gate、候选 package 的 race 检查及构建通过；Python 离线 suite 99/99 通过。测试覆盖源隔离、未知契约、实际 executor/model/auth-set 错配拒绝、原始及嵌套刷新凭据不变、SDK Load、readiness PID/nonce、指纹与集合摘要。

## 实际配置的候选启动核验

使用当前 Go 配置解析器读取 `/opt/homebrew/etc/cliproxyapi.conf`，仅导出目标别名、上游模型、executor、协议及 endpoint/config 摘要，不输出 API key、header 或原始地址。五个历史目标中，显式映射命中 Qwen 与 GLM；其余未命中不能证明动态 OAuth/catalog 路由不存在。

| client model | upstream model | executor | 实际初始 auth 数 | 候选启动/退出 |
| --- | --- | --- | --- | --- |
| `qwen-ds41-flash` | `deepseek-v4.1-flash` | `openai-compatible-qwen` | 2 | PID/nonce 校验通过，退出 0 |
| `opencode-go-glm53-flash` | `glm-5.3-flash` | `openai-compatible-opencode-go` | 1 | PID/nonce 校验通过，退出 0 |

两次 source config SHA 相同且前后未变化；均无推理请求，不能计入 60 个正式案例或成功率。证据分别为 `.omo/tool-loading-completion/t6-live-operator-{qwen,glm}-startup/`，只读目录核验为 `t6-configured-route-inventory.json`。启动证据还不能证明模型 alias 实际被 executor 采用，须后续实际 HTTP attempt 核对。

## 未完成项

本轮仅证明正式 SDK bootstrap 与采集约束在合成配置下成立。没有完成真实客户端经此入口的真实 Provider 闭环。auth-ID 集合指纹不是完整凭据材料、endpoint 或 account/region 指纹；完整不可变 scope manifest、真实客户端 model metadata、目录/权限代次、配对调度、usage/失败归集及聚类统计仍待补齐。

下一步先用实际配置和选定 route 建立这些指纹并验证真实候选 readiness，随后把已有 native client/MCP CaseJournal 接入同一入口，执行校准批次。最终 60-case、代表性 Coding、质量非劣与收益区间尚未达到；客户端取消/runtime/output 限制仍独立交付。

复现命令见 [验收工具 README](../../ops/tool-loading/README.md)。验收标准见 [完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。本轮无发布、安装、默认配置切换或原服务重启，整体目标继续进行。

后续更新：同日已接入实际客户端与 Provider，结果见 [真实校准报告](2026-10-13-live-calibration-evidence_CN.md)。本文件记录 bootstrap 阶段，其启动观察不重算为真实推理样本。
