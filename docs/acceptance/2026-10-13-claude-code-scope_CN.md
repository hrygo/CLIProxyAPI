# Claude Code 纳入跨 Provider 工具按需加载验收

核验日期：2026-10-13。状态：范围已接受，客户端端到端证据尚未建立。
依据用户更新的目标，客户端范围为 Codex、OpenCode、Claude Code 与 MCP；
完整目标及五项门禁沿用 [完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。

## 已核验事实

- 本机 `claude --version` 为 **2.1.289**。这是版本实测，不表示该客户端检索或任一路由通过。
- 同版本 `--help` 提供 `--bare`、`--setting-sources`、`--strict-mcp-config`、
  `--mcp-config`、`--tools`、`--allowedTools`、`--disallowedTools`、
  `--no-session-persistence`、`--output-format stream-json` 与 `--permission-mode`。
  `--bare` 帮助说明跳过 hooks、插件同步、后台预取及钥匙串读取；这些开关的组合效果仍需隔离实测。
- 官方 MCP 文档说明，非第一方 `ANTHROPIC_BASE_URL` 默认关闭工具检索；
  显式 `ENABLE_TOOL_SEARCH` 可以覆盖这一默认回退，但实验能力总开关与供应商限制仍会影响它。
  `true` 与 `false` 作为候选 eager/lazy 配置，不能直接当成模式生效证据。
  [官方 MCP 文档](https://code.claude.com/docs/en/mcp#configure-tool-search)
- 官方 Gateway 契约将 `defer_loading`、`tool_reference` 与相应 beta header 视作成组能力；
  跨格式转换、缓存、错误及终态需分别验证。不能通过禁用所有实验能力取得通过，再声称检索有效。
  [官方 Gateway 契约](https://code.claude.com/docs/en/llm-gateway-protocol)

## 独立实施与验收交付

1. 增加固定 2.1.289 的隔离 adapter。使用 fresh home/workspace、显式 fixture MCP、过滤环境、
   显式权限与任务预算；先验证隔离有效，不加载用户现有 MCP 或修改默认设置。
2. 候选前端需接纳 Anthropic Messages 及所需计数请求；锁定实际出站 executor、模型、wire contract
   与 credential scope，区分本地计数估计和上游物理请求。所有失败、重试和旁路推理保留。
3. 直接读取 eager/lazy 的线包：首轮工具目录、`defer_loading`、检索入口、工具引用、后续有效声明，
   再与实际 MCP dispatch、随机回执及后续结果回放配对。发现成功和配置开关均不替代实际执行。
4. 用同样的禁止工具、非法参数、取消、撤权及释放 fixture 检查原始工具 identity。获准 ToolSearch
   不授予发现工具执行权；禁止工具必须零 dispatch、零效果。
5. 覆盖 lookup、空结果、多轮依赖及真实编码，保持与其他客户端相同任务、gold、计量和质量门槛。
   每个客户端/模型/模式独立 scope，不将其成功合并到旧版本或旧客户端的认证。
6. 在最终单一 revision 重新锁定完整矩阵和样本设计。新增客户端不降低原 60-case、代表性编码、
   逐路由 −2pp 非劣及上下文收益门槛；模板不足仍为 bounded-live。

## 当前证据与限制

CLI help 原始证据保存在本地 `.omo/tool-loading-completion/t6-claude-help.txt`；
尚未启动 Claude Code 推理或真实 MCP 验收，无其通过率、上下文节省或账单结论。
SDK native Antigravity 的 loopback 测试只证明 Gateway 集成，不覆盖 Claude Code 客户端。
没有变更默认配置、注册目录、安装版本或运行服务。
