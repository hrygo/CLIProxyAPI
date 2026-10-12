# 跨 Provider 工具按需加载：真实源码修改试点

核验日期：2026-10-13。实现候选：`ff28319c9f2e7d110bc9a6ffd1f8c21d1258a89f`。

## 结果与适用范围

同一候选完成 24 个真实 Provider 编码案例：2 个路由 × 2 个客户端 × 3 个任务 × eager/lazy。
实际源码修改及宿主独立行为测试 **24/24 通过**，包括真实工具执行、最终源码哈希和新鲜回执；
预先锁定的完整验收 **23/24 通过**。唯一失败是 GLM/Codex lazy 的重试任务最终答案带 Markdown
围栏，违反纯 JSON 标准；该例代码、工具执行和终态均通过，失败仍保留在原始分母中。

这是小模块纯函数编码试点，状态为 **bounded-live**。不代表任意 Python 项目、其他语言、所有
Provider 或完整 Coding 语料认证，也没有完成最终 60-case 矩阵及正式质量、收益置信门禁。
完整目标保持 [完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md)
第 3、11、12 节的范围。

本轮没有修改生产 Gateway 的适配逻辑；交付的是客户端/MCP 执行侧的隔离编码 profile、独立
评分与回归测试。Gateway 继续承担适配、实际请求计量和流终态检查，不执行客户端工具。

## 固定的任务与执行边界

客户端版本：Codex CLI `0.160.0`，OpenCode `1.19.0`。候选以 Go `1.26.4` 构建，构建信息
确认上述 revision、`vcs.modified=false`。Provider 路由固定为：

| 配置 alias | 上游模型 | executor |
| --- | --- | --- |
| `qwen-ds41-flash` | `deepseek-v4.1-flash` | `openai-compatible-qwen` |
| `opencode-go-glm53-flash` | `glm-5.3-flash` | `openai-compatible-opencode-go` |

Codex 使用原有 CLI 输出 profile，未启用原生 JSON schema；OpenCode 显式启用
`--structured-output`，固定任务标题为 `Tool loading coding pilot`。同一客户端/路由的 eager
和 lazy 使用相同 prompt、gold、权限及输出 profile；两客户端结果不拼成同一配对。

三个任务均在 fresh 私有 Git workspace 中修复 `module.py`：

| 任务 | 行为标准 | 独立测试数 |
| --- | --- | ---: |
| `coding-budget` | 只扣已入账金额；退款、pending/void、负余额与非法类型 | 11 |
| `coding-retry` | 输出暴露后禁止重试；429/5xx 边界和例外；拒绝 bool 状态码 | 15 |
| `coding-identity` | 忽略空 ID、按首次出现去重、保留顺序/大小写/空格；类型校验 | 10 |

三份基线代码均未通过行为测试。评分测试和实现位于宿主 harness，客户端 workspace 内只有
待修改源码，没有可以通过工具修改的测试、gold 或评分入口。仅显式编码 profile 增加：

- `coding_task_read`：读取任务需求、语言约束、源码和当前哈希。
- `coding_source_replace`：只替换该 workspace 的 `module.py`，必须提供当前哈希。
- `coding_tests_run`：执行宿主行为测试，返回结果、源码哈希与会话回执。

源码写入属于已声明允许的任务效果，与禁止的 `fixture_mutation` 效果计数分开。
Codex 内置 shell 仍为 read-only，OpenCode 内置工具仍为默认 deny。MCP 是独立执行边界，
不能把 shell 的 read-only 当成 MCP 也只读；编码权限及可写文件已进入 profile 指纹。
默认 ledger 场景不暴露编码工具。

测试运行器只接受文档明确的纯 Python 子集：拒绝 imports、attributes、动态调用、装饰器、
annotations、comprehensions、幂运算和字符串格式化扩容；编译后加入语句及容器预算。
worker 使用 `-I -S` 和空环境，具有 CPU 与宿主执行时限。Linux 另限制地址空间；本 Mac
运行时不支持降低 RLIMIT_DATA/AS，因此不声称 Darwin 上存在内核 heap 上限，也不把这个
profile 称为通用 Python sandbox。取消覆盖 worker 创建和运行两个阶段，必须 kill/join
后才能释放 MCP callback。

读写拒绝 symlink、hardlink、目录身份变化及超大源码；写入前在实际文件 descriptor 上
重新核对哈希，避免先读到旧版本再截断新版本。客户端退出后，runner 再独立评分最终源码，
保存 `code-outcome.json` 和 `source.diff`。成功还要求真实 sequential read/edit/test 的
完成与释放轨迹、最后测试对应最终哈希、源码确实改变以及最终答案使用真实回执。
只有正确 JSON、只有“fixed”声明、或只有模型自述测试通过均不能满足代码 gate。

## 全部已开始案例与上下文收益

四个批次均在启动前锁定 plan/material fingerprints，每批 6 cases、最多 48 次物理请求和
48 次工具 dispatch；每 case 最多 8 次请求、8 次 dispatch、90 秒。没有自动覆盖失败或
只保留后续成功。所有批次已结束，24 started/24 finished/0 unfinished。

| 路由 / 客户端 | 严格通过 | 独立代码通过 | 物理请求 | MCP dispatch | 首轮输入降幅中位数 | 峰值输入降幅中位数 | 全任务输入降幅中位数 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Qwen / Codex | 6/6 | 6/6 | 29 | 19 | 77.27% | 66.44% | 62.21% |
| GLM / Codex | 5/6 | 6/6 | 28 | 18 | 77.89% | 66.72% | 61.81% |
| Qwen / OpenCode | 6/6 | 6/6 | 24 | 18 | 90.59% | 88.44% | 89.43% |
| GLM / OpenCode | 6/6 | 6/6 | 28 | 23 | 90.88% | 87.20% | 88.69% |

合计 109 次实际上游请求、78 次 MCP dispatch；每例实际线包确认预期 eager/lazy 模式。
禁止 dispatch、禁止效果、最终 active callback 均为 0。24 个候选服务均正常退出，源码配置
哈希保持不变；HTTP 证据均完成封存、active bodies 为 0，没有预算 admission 拒绝。

表中每行是该客户端/模型三个任务的成对输入降幅描述性中位数：`1 - lazy/eager`。使用
Provider 实际 usage，全部 109 次请求 input 已知且无无效 usage；失败例和额外调用计入全
任务输入。不同模型 raw tokens 未合并成统一节省率。这是总 input（包含缓存输入）的降幅，
不是 schema token 的精确归因，也不是已验证账单节省。

两个需要保留的过程事实：Qwen/Codex lazy 重试任务重复执行一次已通过的测试，产生 7 次
物理请求、4 次 dispatch；GLM/OpenCode lazy 重试任务经历两个真实失败的行为测试后再次
修改，第三次测试通过，产生 7 次物理请求、7 次 dispatch。后者的两次失败测试和三次
源码替换均保留在 MCP trace，允许预算内修复，不能把中间失败抹掉。

缓存 read 有 Provider 实际计数，但 cold/warm 未受控；cache write 未披露，保持 unknown。
账单、价格与币种没有足够证据，保持 unknown。耗时保留在每例 journal，未以小样本认证
p95 或 latency 门禁。

当前编码 task 名被绑定进 fixture/permission scope，因此分析器保守地将三个任务保留为
三个独立 scope，每个仅一对样本。不能把描述性路由中位数当作跨 scope 认证：每个 scope
的质量条件下界为 −95pp（无退化）或 −100pp（唯一格式退化），收益没有有限的单侧 95%
下界；采样设计、region/cache scope 也未认证。G4/G5 均保持 unverified。

## 责任边界与剩余工作

- 本轮未发现 Gateway 协议或实际工具执行失败。GLM/Codex 的唯一严格失败属于模型最终
  输出格式，网关不得通过去掉 Markdown 围栏或伪造答案来使它通过。
- OpenCode 的原生结构化输出仍使用已验证的同步 session endpoint；此前历史读取 API
  缺口不因本轮通过而消失。此前 Codex/Qwen 原生 JSON schema 拒绝也没有重新认证。
- 三个纯函数任务补上了实际源码结果证据，仍需扩展文档/issue 查证、多个文件、真实项目
  测试与失败恢复等独立模板，按完整方案完成代表性 Coding 语料。
- 最终单一 revision 的完整 60-case、其他既定模型及 native/OAuth 路由、客户端取消与
  权限变化、长会话/恢复/WS 验收，以及正式逐路由质量与收益收样仍待完成。
- 正式收样前需定义统一编码执行 profile 和任务模板绑定方式，使任务变化不会被错误
  当成权限变化；必须新开锁定批次，不修改或放宽本轮证据。

本轮未发布、未安装、未部署、未更新 PR、未改变默认配置，也未重启原有服务。

## 复现与工程证据

入口：[harness README](../../ops/tool-loading/README.md)、
[编码任务与评分](../../ops/tool-loading/coding_tasks.py)、
[runner](../../ops/tool-loading/live_clients.py)。使用 README 的 bounded live 命令，选择
`--scenarios coding-budget coding-retry coding-identity`；OpenCode 显式增加上述输出 profile
和标题。Provider alias/model/executor/endpoint digest 必须按实际配置锁定，不复制凭据。

本机原始证据目录（包含受限私有捕获，不作为公开交付物）：

```text
.omo/tool-loading-completion/t6-coding-qwen-codex/
.omo/tool-loading-completion/t6-coding-glm-codex/
.omo/tool-loading-completion/t6-coding-qwen-opencode/
.omo/tool-loading-completion/t6-coding-glm-opencode/
```

每目录包含 `batch-plan.json`、`cases.jsonl`、`batch-audit.json`、`replay.json`、`summary.json`
和各 case 的私有 capture、MCP trace、源码及评分。四批均在确认进程 terminal 后通过
`validate_evidence.py --journal ... --plan ...` 和 `analyze_pairs.py --journal ... --plan ...`，
结构通过不替代正式质量或可靠率认证。

新增行为回归先失败再实现，覆盖基线/正确实现、固定答案、源码子集、扩容预算、错误哈希、
symlink/hardlink、写 descriptor 竞争、worker 创建期与运行期取消、真实 stdio MCP 读改测、
撤权、只读 profile、代码与 JSON/dispatch 的独立评分及预锁定 scope。
最终 Python suite **190 tests 通过**（12.554s）；日志
`.omo/tool-loading-completion/t6-coding-suite-final-v2.log`。`git diff --check` 通过。
本轮无 Go 源码改动，完成固定工具链候选构建，未把历史 full Go gate 计为本轮新结果。

源码/测试在 2026-10-01 generation 图谱中均尚未跟踪，已检查 coverage 并直接核对当前
源码；未重建图谱。
