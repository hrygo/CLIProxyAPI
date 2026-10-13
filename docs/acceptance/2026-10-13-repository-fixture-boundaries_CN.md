# 多文件 MCP fixture：提交回执与验收边界

核验日期：2026-10-13，Asia/Shanghai。状态：**仓库读写基础与回执回归已交付，隔离代码执行、代表性编码质量及净收益尚未验证。** 本轮没有 Provider 实测、发布、安装、默认配置变更或原服务重启。

现行目标为[高置信全任务净收益](../superpowers/plans/2026-10-13-tool-loading-net-benefit-pilot_CN.md)。[T20 六十例](2026-10-13-final-candidate-60-retest_CN.md)继续作为历史覆盖证据：41/60 safe，不因新目标或本轮离线修复追溯改判。网关 Go 源码与 `58f5523b` 的 live 执行二进制未变；本轮改变的是验收 fixture，尚未用于任何新的 Provider 认证。

## 已交付能力

- 新增[四模块 repository storage](../../ops/tool-loading/repository_project.py)：固定 `ledger_model.py`、`validation.py`、`ledger.py`、`api.py`，每文件最多 8,192 UTF-8 字节、每树最多 16,384 字节。它是一套 synthetic ledger 编码基线，不代表独立抽样的真实工作负载。
- [MCP catalog](../../ops/tool-loading/catalog_fixture.py)仅在 opt-in repository profile 声明 `repository_read` 与 `repository_patch`；真实 stdio MCP client 已验证发现、读取、修改、回填 revision 与后续撤权。旧 fixture 默认目录不变；没有 `repository_tests_run` 声明。
- patch 校验固定路径、重复字段/路径、UTF-8 字节、当前 tree/file revision；拒绝 symlink、hardlink、根目录替换与内容漂移。源文件变更先写完整 snapshot，再发布一个 state 指针；旧 generation 保留。
- 参数在 schema 校验前快照，避免调用方修改已检查对象。权限在 preflight 后、写 staging 前、临时 state fsync 后且替换指针前复核。授权更新与 filesystem commit 不共享原子锁：这不是“任何时刻撤权都必然阻止提交”的保证。

## 修复及回执语义

| 情况 | 当前行为 | 验收含义 |
| --- | --- | --- |
| patch 结果预算不足 | dispatch 前预留成功及不确定回执，包含 `evidence_complete=false`；不足直接拒绝，无写入 | 不能提交后返回 `result_budget` 冒充未执行。预算口径是 fixture 的 MCP content/structured result 表示。 |
| 写 generation 中断 | 先写随机私有 staging，完整校验/fsync 后 rename；同目标 revision 可重试 | 当前 state 不变，不再由残缺目标目录永久阻断重试。失败 staging 可能保留。 |
| state 已替换，父目录 fsync 失败 | `repository_commit_uncertain`，携带 revision 与 changed files | 新指针已可见，crash durability 未确认；调用方应读回核对，不能当未提交自动重做。 |
| 初始化发生上述不确定提交 | 异常给 host 附带 project 恢复句柄 | host 可恢复读取；owner 不进入面向模型的 MCP 回执。 |
| 提交后 completed / commit_uncertain / released 日志写失败 | 保留操作回执，`evidence_complete=false` | 操作结果与证据完整性分别处理；缺审计记录不能认证。正常回执中的 `true` 也不等于任务通过。 |
| 文件锁竞争 | `LOCK_NB`，返回 `repository_busy` | 不在 MCP event loop 同步等待锁；调用方重试仍计入任务预算。 |
| preflight 内撤权 | 执行前拒绝，零 dispatch，当前树不变 | 只覆盖实际校验的权限时点。 |
| staging / state fsync 后撤权 | publish 前拒绝，当前树不变 | 已发生合法 dispatch，可能留下 host-owned staging/snapshot；不宣称零字节写入。 |

历史 snapshot 与失败残留在 host-owned workspace 内保留，本模块没有自动删除或垃圾回收。独立权限文件、同 UID 的任意外部篡改、进程 crash、磁盘故障和跨事务撤权有各自边界；文件 API 测试不能证明任意代码执行隔离，也不能证明所有存储资源的生命周期已闭合。

## 验证证据

- 初始 storage/MCP 回归 12 项；复现预算、重试、持久化、参数快照与权限问题后 20 项通过；最后补日志故障、初始化恢复和发布时点撤权，**23 项定向通过**。
- **327 项完整 Python 回归通过**。首次默认沙箱跑 324 项时，两个本地 HTTP fixture 因 `127.0.0.1` bind 被拒绝报错，第三个 fake OpenCode HTTP child 提前退出；保留该失败记录。核准本地端口测试权限后，全套 327 项通过，没有跳过这些案例或修改原有 oracle。
- 撤权测试精确在临时 state fsync 后更新权限，再检查未替换当前 state；日志故障覆盖 completed、released、commit_uncertain 三条路径。真实 MCP stdio 测试验证工具发现与调用，新增 storage 测试验证同 revision 竞争、异常提交恢复及非法输入零写入。
- `git diff --check` 通过；此次不涉及网关 Go 模块，未重复 Go 全仓门禁。新文件尚未进入当前图谱，结论依据当前源码与测试，未重建索引。

本机日志：[边界 RED](../../.omo/tool-loading-completion/t21-repository-boundary-red.log)、[边界 GREEN](../../.omo/tool-loading-completion/t21-repository-boundary-green.log)、[回执 RED](../../.omo/tool-loading-completion/t21-repository-audit-red.log)、[回执 GREEN](../../.omo/tool-loading-completion/t21-repository-audit-green.log)、[首次沙箱失败](../../.omo/tool-loading-completion/t21-python-all-final.log)、[完整回归](../../.omo/tool-loading-completion/t21-python-all-unsandboxed.log)。`.omo` 日志仅留本机；公开材料不含凭据路径或请求明文。

可移植复现命令（使用已具备 MCP/jsonschema 的项目 Python 环境）：

```sh
python3 -m unittest discover -s ops/tool-loading/tests -p 'test_repository*.py' -v
python3 -m unittest discover -s ops/tool-loading/tests
git diff --check
```

## 下一阶段的关键缺口

1. host-owned gold、模型无法修改的独立测试入口及可靠隔离后端。Docker daemon 的只读检查可用，但本轮 `docker image ls python` 未找到缓存条目；没有拉取镜像、创建容器或验证隔离。
2. 实际 MCP 客户端取消正在执行的测试、进程/连接/callback 释放及资源回执。当前 read/patch 无执行 worker，不能替代[Codex 取消缺口](../compatibility/codex-mcp-cancellation-gap_CN.md)的修复与复测。
3. repository case 与 live runner 的登记、gold 判定、回执不完整失效及预算集成；目前不能从 `evidence_complete=true` 或测试代码存在推导 ready/certified。
4. 代表性任务框、任务族独立性、双向质量差、最小有意义净收益与区间方法的冻结。既有分析器的 conservative-any-loss 质量界不直接估计平均成功差，不静默改释旧字段。

先完成所选协议/route 的离线门槛，再锁定并至多运行 16-case 校准；它用于识别预算、波动和计量，不能宣称可靠率。输入资源可有独立硬预算，账单未知保持未知，不机械阻断输入校准；货币 ROI 需要额外可核实费用边界。正式样本量根据任务分布、校准与经济意义设计，不照搬 149 簇或固定 45%／10%。
