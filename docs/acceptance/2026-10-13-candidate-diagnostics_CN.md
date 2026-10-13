# 工具按需加载：安全诊断与 Gemini 目录就绪复测

核验日期：2026-10-13。本轮完成安全诊断及隔离验收的启动竞态修复，Gemini 已进入真实工具执行；整体目标仍未达标。未发布、未修改默认配置、未重启原服务，也未刷新操作方 OAuth 凭据。长时间构建、测试和实测均异步启动，期间推进独立源码核查和文档工作。

沿用[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)的门槛。[上轮 84 例矩阵](2026-10-13-attested-matrix_CN.md)保持原样，本轮四例使用新候选及新冻结绑定，不能与历史候选混合认证。

## 本轮实现

安全诊断提交：`1d1f64b1ba4e0969f83ddf32a64d717438f69a1d`。

- 候选子进程仅输出固定 `stage` / `reason` 枚举，不导出异常原文、凭据、私人文件名或 URL。递归错误树及输出数量有界。
- 区分绑定缺失、内容漂移、读取失败、身份变化、快照不可用和配置错误。客户端以有界、拒绝符号链接和重复 JSON 键的读取方式验证诊断。
- 清理错误覆盖早先目录错误时，两个安全原因都保留。进程退出回执与源配置校验继续独立记录；缺失请求捕获仍为未知，不能据启动原因补成零请求。
- 目录模式不匹配与搜索能力未准入分别记录。GPT 的 `code_mode_only` 不再只得到通用启动错误，但仍不放宽原 direct profile。

目录等待提交及本轮正式候选：`35ef9672cdab2822333c8b02dff90fe0b8df708b`。

- 隔离候选新增本地鉴权 `/acceptance/catalog-state`，只返回 PID、readiness nonce 和 active auxiliary-work 计数。
- 实际模型目录缺少 Antigravity 模型、同一候选仍有在途辅助工作时，验收在原任务总期限内重新查询。耗时扣除后才启动客户端。
- 工作结束时再次查询一次，覆盖“读取目录后、读取工作状态前完成发布”的竞态。模式不匹配、状态身份不符、已结束但模型仍缺失及预算耗尽继续失败，不合成模型元数据。
- 保留目录等待记录；辅助发现请求照常计入物理请求预算。仅修改隔离验收代码，没有改变生产 SDK 的异步模型发现行为或添加上游网络超时。

## 竞态的实测证据

独立工程探测使用安全诊断候选及同一精确指定的 OAuth 文件：旧绑定被真实进程拒绝，原因是 `credentials / binding_content_mismatch`；新绑定对应的现有 access token 有效期超过五分钟，未刷新凭据。

新候选在 147 ms 就绪；查询在 153、659、1,171 ms 得到 `model_unavailable`，到 1,684 ms 才观察到真实模型。捕获的一次 `model_discovery` 返回 HTTP 200、EOF，响应体 93,383 字节；辅助工作最终 started/completed 为 2/2、active/rejected 为 0，证据 sealed。清理退出 0、配置及绑定未变。

因此，进程就绪与模型目录就绪是不同状态。旧验收立即查询并关闭候选，会取消仍在进行的发现请求；本轮确认该竞态实际存在，但不能逐项追溯认定上轮所有 `transport_error` 或通用启动失败均由它造成。

首版临时工程脚本重复创建汇总文件，汇总中断，原目录和捕获已保留；修正后在新目录完整重跑。该工程探测不计入正式四例，也不计入上轮矩阵。

## 四例预声明复测

客户端为 Codex CLI `0.160.0`、OpenCode `1.19.0`；原生路线为 `antigravity / gemini-3.5-flash-lite`，契约 `antigravity-generate-v1`。两个客户端各执行 lookup eager/lazy 一对，保留原生 JSON-schema 输出；OpenCode 使用显式固定标题。

候选使用 Go `1.26.4` 构建；启动前核对真实构建提交与 `vcs.modified=false`。计划摘要为 `64de51feaec928df1128eb42e7fc0a89264ee9f39d1b20bbb0ef9c725d75a0f9`。每任务 90 秒、8 次物理请求和 8 次 dispatch；全批上限分别为 32。新凭据绑定在启动前冻结，没有替换旧矩阵绑定。

| 客户端 / 模式 | 安全成功 | 请求数，含发现 | dispatch | 总耗时 | 结果 |
| --- | --- | --- | --- | --- | --- |
| Codex eager | 是 | 3 | 1 | 8.053 s | 答案、执行、nonce、输出格式及终态通过 |
| Codex lazy | 是 | 6 | 1 | 12.247 s | 按需加载及执行闭环通过 |
| OpenCode eager | 否 | 4 | 2 | 8.664 s | 查询后额外调用 `ledger_budget_confirm` |
| OpenCode lazy | 否 | 5 | 3 | 10.115 s | 先调用 `collection_00_read` / `collection_01_read`，随后正确查询 |

四例全部开始并结束，安全成功 **2/4**。18 次物理请求包含 4 次模型发现、14 次推理；总 dispatch 7。没有未知请求或 dispatch 案例，预算合规。禁止工具 dispatch、副作用和清理后 active 均为 0；四个候选退出 0，源配置保持不变。默认配置 SHA-256 仍为 `43dc5a62be638bfeda68a2c588b9f4bbe03d0c96a5d39d83031fc7840ff1e394`。

四例均先观察到模型缺失及 active auxiliary-work=2，再等待真实目录出现，等待约 616–969 ms，随后进入客户端执行。这验证了修复，不只是声明配置已启用。

OpenCode eager 的额外 confirm 调用已出现在原始 Provider 响应中；lazy 的三个独立 `execute` 响应对应上述三个 MCP dispatch。现有证据支持工具选择偏离任务，尚不能泛化为整个 OpenCode 或 Gemini 的能力限制；未发现本例由目录等待修复重复注入同一调用的证据。不修改精确 dispatch 标准来抬高通过率。

## 输入与质量结论

| 路线 | eager 首轮 / 峰值 / 全任务输入 | lazy 首轮 / 峰值 / 全任务输入 | 对应减少 |
| --- | --- | --- | --- |
| Codex / Gemini | 未知 | 未知 | 未知 |
| OpenCode / Gemini | 53,535 / 53,762 / 160,920 tokens | 5,994 / 13,242 / 38,504 tokens | 88.8% / 75.4% / 76.1% |

Codex 的七次推理都存在 `conflicting_usage_snapshots`：流式快照的计数变化不满足当前计量契约。例如 eager 首次响应的 `thoughtsTokenCount` 从 236 降到 118，部分响应的最终 `promptTokenCount` 也改变。保留原始捕获及未知值，不取较小快照冒充节省；需要核对原生流式 usage 契约及最终权威计量来源。

OpenCode 的数值是一个失败任务对的描述，不能证明完成任务的收益达标。每路线仅一个任务簇，质量差值的条件单侧 95% 下界为 −95pp，收益下界未知；region、缓存条件和账单仍未验证。全部 `gate_status=unverified`。

## 工程验证与剩余工作

265 项 Python 回归通过；隔离候选 Go 包测试、race 和干净候选构建通过。新增测试覆盖安全诊断、清理原因保留、异步目录发布、settled 后复查、不同 Provider/模式错误不等待、身份/严格计数验证及任务预算耗尽。生产 server 构建已在本轮安全诊断阶段通过；后续只修改隔离验收代码，生产 SDK 没有再变更。

仍需：明确 Codex Code Mode profile；处理 Qwen 路线原生结构化输出能力；继续查明 GLM 行为与终态、OpenCode/Gemini 工具选择，以及原生 Gemini 流式计量边界；完成同一最终候选 60+ 案例与代表性编码任务，并按独立任务簇验证质量和经济性。四例通过与否都保留，不替代完整矩阵，也不意味着整体目标完成。

复现方法及约束见[验收工具 README](../../ops/tool-loading/README.md)。本机私有证据为 `.omo/tool-loading-completion/t10-gemini-pilot/`，含冻结计划、append-only journal、原始 HTTP/MCP 捕获、退出回执、批次 audit、replay、summary 和 pair analysis。工程探测另存 `t10-bootstrap-probe/` 与 `t10-bootstrap-probe-v2/`；不进入发布产物。
