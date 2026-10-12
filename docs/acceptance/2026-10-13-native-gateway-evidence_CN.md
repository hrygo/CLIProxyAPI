# 真实客户端经隔离网关的协议与采集链路验收

核验日期：2026-10-13。此报告覆盖真实 Codex CLI、OpenCode、CLIProxyAPI handler/auth/executor 与 MCP 执行端；上游为合成本机模型，**不属于真实 Provider 验收，不证明任务可靠率、上下文 token 或账单节省**。

## 本轮交付

- `ops/tool-loading/gateway/` 提供仅接受 loopback 合成上游的独立候选入口，监听系统分配的临时端口，使用合成凭据及新建私有状态目录。
- `gateway_clients.py` 启动真实客户端并在启动前登记 case；验证子进程 PID、地址、执行器、协议和二进制摘要；任务结束后关闭本轮网关并核对实际退出结果。
- HTTP attempt journal v2 记录实际执行器及 model/auth record ID 的 SHA-256。导入可指定 expected executor，缺失身份或执行器不符时拒绝。历史 v1 仍可读取，但不能通过此身份门槛。执行器身份不推导协议，协议由独立 fixture constructor 固定。
- 模型 fixture 只选择实际声明；从发现清单学习 `cts_*` 别名，从实际 MCP 结果学习 nonce；不能从 fixture 内存直接获得 nonce。

启动器过滤子进程环境，不加载用户配置或 token store。共享 API constructor 在直接调用时可能读取管理环境变量，因此应使用文档启动器。原有服务、默认配置及工具目录没有修改；候选未安装、未发布。

## 保留的诊断分母

原始证据位于仓库私有目录 `.omo/tool-loading-completion/`，不提交合成请求正文和客户端数据库。

| 批次（`t6-native-gateway-*`） | 已开始 case | 实际 HTTP / 采集 attempts | 闭环通过 | 说明 |
| --- | ---: | ---: | ---: | --- |
| `initial` | 4 | 11 / 11 | 2 | Codex 结果包装及发现别名解码缺口 |
| `decoder-fixed` | 4 | 12 / 12 | 4 | 修正后首次 Chat 验证 |
| `chat-final` | 4 | 12 / 12 | 4 | 使用 case replay 判闭环 |
| `responses-final` | 4 | 6 / 6 | 0 | 合成 usage 缺字段；标题请求误推进工具状态 |
| `responses-wire-fixed` | 4 | 12 / 12 | 4 | 修正 fixture 后 Responses 复测 |
| `chat-wire-fixed` | 4 | 12 / 12 | 4 | 同一 fixture 修订的 Chat 复测 |
| 合计 | 24 | 65 / 65 | 18 | 跨 fixture 修订诊断记录，不是最终版本质量样本 |

失败责任已从相邻边界证据定位：Codex 的 Chat 结果保留 `Wall time/Output` 包装，发现工具变为清单中的 `cts_*` 别名；验收器原先未正确解码或选择。Responses 首轮实际 MCP 执行后，Codex 报 `ResponseCompleted` 缺 `total_tokens`；该字段由本轮合成模型遗漏。OpenCode 的标题请求在 Codex executor 下被加入 `image_generation` 声明，原先以“存在 tools”判工具轮次不成立。以上修正在验收器/合成上游，没有为通过而伪造网关终态或放宽真实客户端检查。

## 最终协议矩阵

两个最终批次使用同一 gateway binary SHA-256：`2c428a77b25f5bf9410f2c3bc4e16c0d9bb5d0578f2f156d92c96ba50092fd5f`。
客户端版本为 Codex `0.160.0`、OpenCode `1.19.0`，MCP Python SDK `1.26.0`。

| 上游契约 | 客户端 | eager requests | lazy requests | 闭环 |
| --- | --- | ---: | ---: | --- |
| `openai-chat-v1` | Codex | 2 | 3 | 2/2 |
| `openai-chat-v1` | OpenCode | 3 | 4 | 2/2 |
| `openai-responses-v1` | Codex | 2 | 3 | 2/2 |
| `openai-responses-v1` | OpenCode | 3 | 4 | 2/2 |

最终 8/8 每例均为一次 MCP request/dispatch、零禁止 dispatch、零副作用、最终 active=0，真实结果 nonce 出现在客户端最终答案，终态合法、网关退出码为 0。24 个模型 HTTP 尝试全部有实际执行器身份及配对采集记录。OpenCode 标题请求包含在分母内。

## 传输观测与上下文边界

以下仅为 Gateway→合成上游的 `tools` canonical JSON UTF-8 字节，不是 tokenizer 估计或 Provider input usage。首个工具任务请求单独展示；标题请求仍计入上述 HTTP 分母。

| 上游 / 客户端 | eager 首个工具请求 / 峰值 | lazy 首个工具请求 / 峰值 |
| --- | ---: | ---: |
| Chat / Codex | 196,208 / 196,208 | 9,930 / 37,648 |
| Responses / Codex | 195,637 / 195,637 | 10,031 / 37,765 |
| Chat / OpenCode | 199,295 / 199,295 | 10,912 / 10,912 |
| Responses / OpenCode | 199,477 / 199,477 | 10,964 / 10,964 |

这证明指定链路减少了发送的完整工具定义。合成 usage `31/2/33` 仅验证解析，不能用来计算上下文节省；模型输入、缓存、费用与收益置信区间仍未知。报告和 replay 保持 `gate_status: unverified`。

## 回归与剩余交付

本轮完整 Go gate `verify-absorb.sh` PASS；gateway/acceptance/helps 相关 race 检查 PASS；最终 Python 离线测试 97/97 PASS。新增测试覆盖真实执行器身份、导入身份缺失/不符、合成 gateway handler、外部上游拒绝、清单别名、结果包装、Responses total usage 及带 built-in 工具的标题请求。失败及修复日志均保留。

真实 Provider 的候选 bootstrap、不可变 scope manifest、均衡配对调度、实际 usage 与失败/重试归集、按路由聚类统计、最终 60-case 矩阵和代表性 Coding 任务仍需完成。客户端取消与 runtime/output limits 缺口沿用独立交付范围，本轮没有认证其修复。

复现见 [验收工具 README](../../ops/tool-loading/README.md)。完整门槛见 [实施方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)。本轮结论限于协议与采集链路，不宣称全目标完成或已经上线。
