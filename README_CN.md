# CLIProxyAPI

[English](README.md) | [中文](README_CN.md) | [日本語](README_JA.md)

CLIProxyAPI 是一个开源 Go 代理服务，为 CLI 账户和模型提供 OpenAI 兼容、
Gemini 兼容与 Claude 兼容 API。它支持 OAuth 和 API Key 认证、多账户路由、
流式响应、工具调用，并提供可嵌入其他服务的 Go SDK。

本仓库是自主维护的
[`hrygo/CLIProxyAPI`](https://github.com/hrygo/CLIProxyAPI) fork。我们按版本
评估并吸收 [`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI)
上游的发布版本，同时使用独立的 `vX.Y.Z-upstreamA.B.C` 版本发布本 fork。

## 项目状态

| 项目 | 当前值 |
| --- | --- |
| 开发主干 | `main` |
| 已发布版本 | [v1.3.0-upstream8.0.16](https://github.com/hrygo/CLIProxyAPI/releases/tag/v1.3.0-upstream8.0.16) |
| Go 工具链 | `1.26.4` |
| Module path | `github.com/router-for-me/CLIProxyAPI/v8` |
| 上游策略 | 只跟踪上游发布，逐条评估和移植提交 |
| 发布渠道 | [GitHub Releases](https://github.com/hrygo/CLIProxyAPI/releases) |
| Homebrew tap | `hrygo/cliproxyapi` |

每个 fork 版本对齐的具体上游 release 会写入该版本的发布说明。详见
[维护与发布](docs/maintenance.md)。

## 核心能力

- 提供 OpenAI、Gemini、Claude 和 Grok 兼容 API。
- 支持流式、非流式响应，以及受支持场景下的 WebSocket 响应。
- 支持函数调用、`tool_search` 和多模态输入。
- 在受支持的 Provider 路由间适配工具按需声明与激活；客户端负责工具发现、授权和执行。
- 支持 OAuth 认证与 API Key 认证。
- 支持多账户轮询调度和精确路由策略。
- 可通过配置接入 OpenAI 兼容上游提供商。
- 支持文件、PostgreSQL、Git 和对象存储后端。
- 提供管理 API、配置热重载和可观测性能力。
- 提供可复用的 Go SDK。

## 工具按需加载：实测指标

`v1.3.0-upstream8.0.16` 包含面向官方 Codex 和 Claude Code 的跨 Provider 工具加载修复。
冻结候选 `bfc5c62c` 使用 Codex `0.160.0`、Claude Code `2.1.289` 验证；下列三个
可比固定任务对在 eager（全量声明）和 lazy（按需加载）两臂均通过。数据核验日期为 2026-10-14。

| 客户端 / 路由 / 任务 | 首轮输入减少 | 峰值输入减少 | 全任务累计输入 tokens，eager → lazy | 累计输入减少 |
| --- | ---: | ---: | ---: | ---: |
| Codex / GPT / budget | 67.22% | 65.28% | 317,874 → 89,527 | 71.84% |
| Claude Code / GPT / budget | 94.40% | 91.26% | 160,384 → 14,490 | 90.97% |
| Claude Code / Muse / identity | 94.25% | 91.72% | 195,991 → 17,601 | 91.02% |

**统计口径**：首轮是第一个实际模型请求的输入；峰值是单任务最大的单次请求输入；
全任务累计是全部模型请求输入之和，包含检索、历史重传、失败和重试。输入包含已报告的
缓存输入，输出另计。三个减少率各用自己的 eager 基准，因此累计减少率可以高于首轮或峰值。
缺失用量保持未知，输入减少率不等于账单节省率。

**覆盖与边界**：最终批次计划 16 案，15 案结束，7 案严格通过，10 案通过独立代码检查；
1 案因预算耗尽未开始。表中每条仅一个固定任务对，不代表总体成功率或统计置信区间。
Gemini 用量仍未认证；Codex/Muse 加载顺序及 GLM 执行未满足全部验收条件。
部分历史任务累计输入反而增加，输入下降也不保证延迟或费用降低。

详见[实施报告](docs/tool-loading/design-and-implementation_CN.md)、
[验证与兼容报告](docs/tool-loading/verification-and-compatibility_CN.md)、
[完整逐请求 token 报告](docs/tool-loading/token-usage-report_CN.md)和
[复现工具](ops/tool-loading/README.md)；报告保留失败、重试、未知用量及客户端和 Provider 边界。

## 快速开始

### 前置条件

- Go `1.26.4` 或兼容的更新版本。
- 可访问目标模型的提供商账户或 API Key。

### 构建

```bash
git clone https://github.com/hrygo/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cli-proxy-api ./cmd/server
```

### 配置

```bash
cp config.example.yaml config.yaml
```

根据需要编辑 `config.yaml` 中的监听地址、提供商、路由和管理配置。工作目录下的
`.env` 会自动加载，凭据默认保存在 `auths/`。

### 运行

```bash
./cli-proxy-api --config config.yaml
```

常用参数包括 `--tui`、`--standalone`、`--local-model`、`--no-browser` 和
`--oauth-callback-port <port>`。

### Homebrew

安装本 fork 已发布 Release 中的二进制：

```bash
brew tap hrygo/cliproxyapi
brew install hrygo/cliproxyapi/cli-proxy-api
cliproxyapi -h
```

Homebrew 只安装可执行文件和配置示例，不接管已有配置文件，也不自动启动服务。
升级和回退流程见 [Homebrew 维护](ops/homebrew/README.md)。

## 配置

| 路径或变量 | 用途 |
| --- | --- |
| `config.yaml` | 服务、提供商、路由和管理配置 |
| `config.example.yaml` | 带示例的配置模板 |
| `.env` | 从工作目录自动加载的可选环境变量 |
| `auths/` | 默认 OAuth 凭据目录 |
| `PGSTORE_*` | PostgreSQL 存储后端配置 |
| `GITSTORE_*` | Git 存储后端配置 |
| `OBJECTSTORE_*` | 对象存储后端配置 |

管理接口见 [Management API](docs/management-api-v8.md)。其他协议和 SDK 文档见
[文档索引](#文档)。

## 开发

```bash
gofmt -w path/to/changed.go
go vet ./...
go test ./... -count=1
go build -o cli-proxy-api ./cmd/server
```

涉及发版、上游摄入或跨模块行为变化时运行完整维护关卡：

```bash
ops/upstream-intake/verify-absorb.sh
python3 ops/tests/test_maintenance.py
```

关卡使用当前 Go 工具链自带的 `gofmt`。本机检查和 PR CI 应使用相同 Go 版本，
因为不同 Go 版本的格式化结果可能不同。

贡献目标为 `hrygo/CLIProxyAPI:main`。请使用短期 `codex/<任务>` 分支，并遵循
[AGENTS.md](AGENTS.md)。

## 维护与发布

- [维护、发布与回退](docs/maintenance.md)
- [上游 release 摄入](ops/upstream-intake/README.md)
- [上游摄入记录](ops/upstream-intake/absorbed.md)
- [发布说明模板](docs/releases/RELEASE_TEMPLATE.md)
- [Homebrew tap 维护](ops/homebrew/README.md)

每个版本都必须提供人工整理的 `docs/releases/<tag>.md`，说明用户可见改动、
验证范围、已知问题，以及本版本对齐的具体上游 release。

## 文档

- [配置示例](config.example.yaml)
- [管理 API](docs/management-api-v8.md)
- [Responses 工具](docs/responses-tools_CN.md)
- [各端工具按需加载配置手册](docs/tool-loading/client-configuration_CN.md)
- [SDK 使用](docs/sdk-usage_CN.md)
- [SDK 认证](docs/sdk-access_CN.md)
- [SDK 进阶](docs/sdk-advanced_CN.md)
- [SDK Watcher](docs/sdk-watcher_CN.md)
- [自定义 Provider 示例](examples/custom-provider)

## 许可证

[MIT](LICENSE)

## 致谢

CLIProxyAPI 源自
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI)。
本 fork 保留上游 module path，以便按 release 移植时保持兼容，并感谢上游维护者
与贡献者。
