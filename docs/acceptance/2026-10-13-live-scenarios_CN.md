# 真实客户端三场景验收入口

核验日期：2026-10-13。承接[预先锁定批次及固定候选实测](2026-10-13-predeclared-batches_CN.md)，对应[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) L2/T6。本报告先记录可执行场景与回归；未运行的在线场景不计入真实案例。

## 场景与 gold

`live_clients.py --scenarios lookup unavailable multitool` 为每客户端产生三对、六任务，沿用事前锁定、平衡顺序、完整预算预留及所有已开始任务入分母。不同场景具有不同 template/pair/prompt，eager/lazy 同对 gold 相同。

| 场景 | 必须满足 |
| --- | --- |
| lookup | 一次实际 ledger dispatch，完成并释放；最终 plain JSON 使用 fresh receipt |
| unavailable | 请求 omega，所有声明仅允许 alpha；观察到目录、零 dispatch，准确报告不可用，不替代项目或编造金额 |
| multitool | ledger 完成后另次执行确认工具；参数使用实际 receipt；两次执行均完成释放，最终准确使用两个结果 |

确认工具在原始 JSON Schema 校验及权限检查后，对“尚未成功查询”和“错误 receipt”执行前拒绝。查询结果超过输出预算也不建立可确认状态。它是本地合成只读服务；事件只记录身份、阶段和结果摘要，不记录参数或 receipt。

multitool 还检查真实出站 history：某次请求只有查询结果，后续请求再包含确认结果。两次调用塞进同一 Code Mode 执行不冒充跨轮回填；prompt、assistant text 和 call arguments 不作为结果证据。unavailable 是“无受支持工具”的 gold，不声称客户端 search 返回了空列表；歧义检索仍需独立扩展。

catalog 新增确认工具，注册总数 68、默认可见 67，hash 已变；旧版本在线证据不能并入本轮分母。first-request probe 按当前可见目录核对声明数，未裁剪注册项。

## 离线验证与剩余工作

新增 10 项回归：三场景锁定、精确答案与 fresh receipts、有序执行与释放、跨轮 replay、结果包装及伪造来源排除、pre-dispatch 拒绝、失败查询不激活确认、真实 MCP stdio 确认与撤权、六任务调度。全套 136 项 Python 测试通过，未跳过；日志 `t6-scenarios-final-green.log`。CI 已使用 `unittest discover` 自动纳入新增测试。本轮未修改 Go 源码，未复跑完整 Go gate。

目前仍是显式 Chat profile 的校准入口，尚未实现全部 Provider 的最终统一矩阵、完整 immutable runtime scope、代表性 Coding 和按 route/template 的统计认证。模型 metadata、依赖内容、OAuth/account/region/cache 控制未知部分保持未知。未发布、未修改默认配置、未重启原服务。
