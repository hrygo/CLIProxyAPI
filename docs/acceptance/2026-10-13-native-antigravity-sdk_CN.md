# Native Antigravity 准入与 SDK 集成回归

核验日期：2026-10-13。干净候选 revision：`9760511e6ab178a379bab0e5766840bcb9206e08`；
实现提交：`e191174`。Go 1.26.4 构建 metadata 为 `vcs.modified=false`。
状态为确定性协议/集成验证；没有新增真实 Provider、真实客户端通过率或上下文节省结论。

## 修复与已确认责任边界

1. 原 Go live profile 不接受 Antigravity，credential loader 对其却允许无显式绑定；
   原生候选现要求 filename/content SHA-256 绑定、已存在 project 和满足 executor 五分钟安全窗口的
   current access token。只使用内存 snapshot，仍剥离 refresh authority，不刷新或 bootstrap 原账户。
2. 原 observer guard 只接纳有 model fingerprint 的推理，因此实际额度查询被拒绝。
   新增每个实际 auth ID 的固定 POST endpoint 准入：`credits_query` 与 `model_discovery`；
   错误方法、地址、query、操作、credential 或模型身份在 dispatch 前拒绝。
   credits 默认 production；catalog 默认 daily，并复核 SDK 的首个 `base_urls`/`base_url` 规则。
3. 实际 SDK 启动需查询账户模型目录，原 `fetchAvailableModels` HTTP 绕过 observer。
   新回归先复现 **observed=0 / dispatched=1**，修复后发送前拒绝为 **observed=1 / dispatched=0**。
   该路径现携带 `model_discovery` identity，纳入辅助契约及与推理共享的物理预算。
   候选 service context 在启动前绑定 owner 和 observer，不能只在推理 middleware 注入。
4. 模型目录 worker 在启动 goroutine 前登记 owner；实际 HTTP 操作也持有 owner 到 body/transport cleanup，
   避免 singleflight 等待者提前返回使 HTTP 被误判为释放。关闭取消可以到达操作，底层 transport
   未释放时不能提前封存。无 opt-in owner/observer 的生产请求不因此获得新的生命周期或网络超时。
5. 原结果回放检查只读 Chat/Responses，遗漏 wrapped native `functionResponse`。
   当前 executor 会把纯结果 turn 改成 `model` role；这是本地源码和真实 SDK 出站线包的事实，
   本报告不将该私有 wire 行为解释成公开 Gemini role 契约。
   验收现接受 user-role result，以及与前序 call ID/name 匹配的纯 model-role result；
   不将 prompt、systemInstruction、call arguments、混合 model text 或未配对结果当作回执。

## 干净候选的实际 SDK loopback

复现入口：[sdk_antigravity_probe.py](../../ops/tool-loading/sdk_antigravity_probe.py)、
[README](../../ops/tool-loading/README.md)。fixture 使用 synthetic OAuth file、loopback catalog/credits/generate
server 和实际 SDK HTTP handler，不读取或调用真实 OAuth account，不启动真实 Coding Agent。

| 路径 | 物理 HTTP | 模型推理 | 模型目录 / 额度查询 | owner started/completed | active / rejected | 回填及两侧终态 |
| --- | ---: | ---: | --- | --- | --- | --- |
| buffered | 5 | 3 | 1 / 1 | 3 / 3 | 0 / 0 | 通过 |
| SSE | 5 | 3 | 1 / 1 | 3 / 3 | 0 / 0 | 通过 |

每例物理 budget 为 5、实际 reserved 为 5，无预算拒绝；journal closed、active bodies 为 0，
owner sealed。client buffered completed/SSE completed 与原生 wrapped candidate STOP 分别检查。
lookup→confirm 的随机回执保留在实际出站结果历史，并按调用 identity 核验。
原 fixture credential/config 均未变，候选退出 0。

每个模型响应的输入 100、全任务输入 300 是脚本写入的合成计数；目录与额度响应无模型 usage，
成本仍未知。这些数值只验证计量分流，不是实际模型 token 或收益数据。
早期 bootstrap/model-not-found、回放检查及 probe 修正失败均保留在本地，不替换为成功，也不计入最终 live 认证。

## 工程验证

- 全量 Go：103 个通过包，零失败；全量 Python：221 tests / OK。
- `sdk/cliproxy`、`live-gateway`、`internal/acceptance`、`executor/helps` 四包 race 通过，
  包括取消后等待真实模型目录 HTTP release 的新回归。
- 全量 vet、服务构建、固定工具链候选构建及 diff whitespace 检查通过。
- 私有证据：`.omo/tool-loading-completion/t6-native-{full-go,full-python-v2,race,vet,server-build}.log`、
  `t6-native-sdk-final/` 与 `t6-native-sdk-final.log`。SDK final 在 clean revision 上执行；
  各早期尝试另存，未覆盖。没有将旧版本工程门禁算作本轮新验证。

## 完整目标仍需完成

- 用显式当前 OAuth snapshot 跑真实 Antigravity eager/lazy；provider/账号/路由可用性未认证，
  不从普通 Gemini、AI Studio 或 Gemini CLI 路径外推。
- Claude Code 已纳入范围，本机版本为 2.1.289；隔离 adapter、Anthropic 前端检索/声明/执行/回填和
  独立质量收益证据待实施，见 [范围增补](2026-10-13-claude-code-scope_CN.md)。
- 最终同一 revision 的 60-case、代表性编码、逐路由 −2pp 非劣及收益区间下界仍未达成。
  不用本报告两例或不同旧 revision 的成功填补这些门槛。
- 默认配置 SHA-256 仍为 `3a6ab28f1605437c2cd9358b7a5320781718f30266353c93953882469b9f46fe`；
  本轮只做本地提交，未发布、未更新远端 PR、未安装、未改变默认配置或重启原服务。
