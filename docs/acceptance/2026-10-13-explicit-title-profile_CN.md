# OpenCode 显式标题验收配置

核验日期：2026-10-13。承接[三场景及隔离修复](2026-10-13-live-scenarios_CN.md)，对应[完整方案](../superpowers/plans/2026-10-12-cross-provider-tool-loading-completion_CN.md) 的客户端控制和计量验收。本次控制只用于显式选择的临时 profile，不改变默认配置。

## 隔离修复后的在线复测

干净候选 `098ebf9df15fc397f3e810771d43d0172f04e05e`、Go `1.26.4`、OpenCode `1.19.0`，`qwen-ds41-flash` → `deepseek-v4.1-flash` / `openai-compatible-qwen`，原三场景/预算/硬格式标准未改。六任务全部开始、完成记录，严格通过 **4/6**，22 次真实请求、6 次 dispatch，禁止执行/副作用/active 均为 0；六候选退出 0、source config 未变，批次因严格失败退出 1。

实际 wire 六例均不再包含仓库政策标记；因此工作目录修复的目标有在线证据。lookup lazy 最终仍有 JSON fence，format=false；unavailable eager 客户端最终答案和终态正确，但第一个上游请求没有合法终态，整体 terminal=false。不能把隔离修复解释成全部格式/终态问题已解决。

unavailable eager 第一次为 title 请求，finish 记录 `read_error`、HTTP 200、实际观察/保留 42,111 字节、complete=false，缺合法终态及 usage。首轮、峰值及全任务输入保持 **未知**；其他已知请求输入 subtotal 50,191 不能当成完整成本。具体故障原因仍需独立同步/取消诊断，不归罪 Provider。旧失败、请求和成本全部保留。计划摘要 `c63786acdbcf15a15872402d15f48ca4d5ffaec32a7713b47d0f41224602dbf0`，证据 `t6-opencode-private-worktrees-live/`，journal/plan 校验结果 `t6-opencode-private-worktrees-live-validation.json`。

## 显式标题控制

当前安装 CLI 的 `opencode run --help` 支持 `--title`；`--format json` 表示事件格式，并不保证最终文本为 JSON。适配器新增可选 `session_title`；live runner 与首包 probe 新增 `--opencode-title`，eager/lazy 使用同一显式标题，默认仍自动生成。

标题策略进入 fixture hash，并为固定标题生成独立 pair ID。fixture hash 同时绑定 fixture、client adapter 和场景源码；不同配置或材料不能合并成同一对样本，运行参数偏离计划时发送前失败。标题文本不写入运营配置，公开 CaseSpec 仅含摘要。

真实客户端本地首包探针：Codex 两模式仍各一请求；OpenCode 两模式从默认“标题+任务”两请求变为一请求，并观察到 eager 完整声明、lazy execute 入口。这里没有 Provider 推理，不能用字节数宣布上下文节省。正式实测仍记录每个实际请求，若出现标题请求不会过滤它。

新增回归覆盖默认不变、仅支持 OpenCode、非法标题发送前拒绝、profile 分离与配对一致、锁定标题漂移零候选启动。全套 Python **141 项通过**，日志 `t6-title-policy-final-green.log`。在线收益和硬格式质量尚待该新 profile 复测，整体 gate 仍 `unverified`；不将显式标题的结果推广到默认 OpenCode 行为。

本轮未改 Go 源码，未复跑完整 Go gate。未发布、未改默认配置、未重启原服务；全部路由、最终 60 案例、Coding、权限生命周期与统计认证仍按原目标继续。
