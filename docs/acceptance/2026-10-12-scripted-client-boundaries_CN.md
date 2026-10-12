# 真实客户端调用与权限边界验收

核验日期：2026-10-12。范围为 Codex CLI `0.160.0`、OpenCode `1.19.0` 访问本地脚本化 HTTP/SSE fixture 与真实 MCP SDK 服务。没有经过 CLIProxyAPI，也没有调用真实 Provider；整体认证状态仍为 `unverified`。

## 结果

同一代码候选上，两个客户端 × eager/lazy × 四种场景共 **16 个边界案例全部通过**，收到 48 个 HTTP 请求，包含 OpenCode 标题请求。所有案例都有真实客户端合法终态；模型决策由 fixture 固定，不构成任务质量或可靠率证据。

| 场景 | 四个客户端/模式组合的实测 | 拒绝/执行位置 |
| --- | --- | --- |
| 授权调用 | 各 1 次 MCP 请求、1 次 dispatch；随机 nonce 经工具结果回填并出现在最终回答 | MCP 服务执行 |
| 客户端禁止、服务允许 | 各 0 次 MCP 请求、0 次 dispatch | 客户端目录过滤及调用入口拒绝 |
| 非法参数 `project:"beta"` | 各 1 次 MCP 请求、0 次 dispatch；`invalid_arguments` | MCP 服务的原始 Draft 2020-12 schema validator |
| 声明/发现后撤权 | 各 1 次 MCP 请求、0 次 dispatch；`permission_denied` | MCP 服务读取 generation 2 的权限文件 |

所有案例副作用计数和最终 active count 均为零。服务的 `tools/list` 事件独立证明初始允许目标工具，避免用“服务也禁止”或“服务没有启动”掩盖客户端授权问题。客户端禁止案例在服务端允许 ledger 的条件下，仍强制模型尝试同一目标路径并得到错误回执。

lazy 授权案例先检索得到 ledger 声明/签名，再调用并读取结果：Codex 使用 `tool_search_call`/`tool_search_output`，OpenCode 使用 `execute` 内的 `tools.$codemode.search`。本地模型仅从客户端回填的调用结果读取 nonce，不读取预设 nonce 或用户提示。最终回答中的 nonce、MCP dispatch、资源释放和客户端终态共同判定，退出码 0 不单独代表通过。

撤权发生在 invocation 之前；lazy 案例已收到目标搜索结果。撤权 artifact 记录 `call_id="invoke"` 和 generation 2。该实测证明服务端能拒绝旧声明的调用，没有证明客户端索引自动刷新或客户端权限在执行中变更。

## 责任边界与剩余问题

两种客户端/模式均将 `project:"beta"` 发到了 MCP 服务，不能依赖本轮客户端提前验证 `const:"alpha"`。保留执行端对原始 schema 的校验才满足此案例的零副作用要求；这是验证位置的实测，不将所有 schema 约束概括为客户端均不校验。

客户端禁止案例证明禁止的目录项无法通过实际调用入口发出 MCP 请求；不代表所有嵌套调用、执行期间权限变更或 runtime 限额均已认证。原生取消传播、资源清理与累计 Code Mode 预算仍需单独验收。

网关转换、多路由切换、Provider 策略、真实检索决策、编码任务与长会话均未被本报告覆盖。CLI 的 usage 在 fixture 缺少 usage 时自动显示零，本报告保持 Provider token/成本未知，不把这些零用于节省计算。正式 60-case、逐请求计量、配对质量及收益置信区间仍待完成。

## 证据与诊断记录

复现入口：`ops/tool-loading/scripted_clients.py`，详见同目录 README。最终 artifacts 位于 `.omo/tool-loading-completion/t5-scripted-authorized/`：每案的 case-started、请求、响应、MCP events、客户端 stdout/stderr、revocation（对应案例）及 summary 均保留。

诊断过程中全部已启动案例保留：首批 4 案，Codex 两案真实执行成功但验收器漏读 `Wall time` 回执包装，旧判定为失败；已补回归。后续 16 案暴露 Code Mode 撤权过早作用于 search 的测试时序缺口；修正为仅作用于 `invoke`，另测 4 案。补齐真实客户端终态判定后测 16 案，再加入服务初始允许的独立证据，最终测上述 16 案。合计 **56 个本地诊断案例、168 个收到的请求**，均未计入正式 Provider live 样本。

74 个离线测试通过，包含回执来源、包装读取、事件身份与合法终态、截断输出拒绝，以及 service catalog 授权证据。原始失败不是 Provider/Gateway 缺陷，而是验收器读取和测试时序问题。

协议 fixture 参考 [OpenAI client-executed tool search 契约](https://developers.openai.com/api/docs/guides/tools-tool-search)，并以当前安装客户端的实际回填形式验证。未发布、未修改默认配置/catalog、未重启原服务。
