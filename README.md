# CLIProxyAPI

[English](README.md) | [中文](README_CN.md) | [日本語](README_JA.md)

CLIProxyAPI is an open-source Go proxy that exposes OpenAI-compatible,
Gemini-compatible and Claude-compatible APIs for CLI-oriented accounts and
models. It supports OAuth and API-key authentication, multi-account routing,
streaming responses and tool calls, and provides a reusable Go SDK.

This repository is the self-maintained
[`hrygo/CLIProxyAPI`](https://github.com/hrygo/CLIProxyAPI) fork. It tracks
selected upstream releases from
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI)
and publishes independent `vX.Y.Z-upstreamA.B.C` releases.

## Project Status

| Item | Value |
| --- | --- |
| Development branch | `main` |
| Published release | [v1.3.0-upstream8.0.16](https://github.com/hrygo/CLIProxyAPI/releases/tag/v1.3.0-upstream8.0.16) |
| Go toolchain | `1.26.4` |
| Module path | `github.com/router-for-me/CLIProxyAPI/v8` |
| Upstream policy | Released upstream versions only; individual commits are assessed |
| Release channel | [GitHub Releases](https://github.com/hrygo/CLIProxyAPI/releases) |
| Homebrew tap | `hrygo/cliproxyapi` |

The exact upstream release aligned with each fork release is recorded in that
release's notes. See [Maintenance and releases](docs/maintenance.md).

## Capabilities

- OpenAI, Gemini, Claude and Grok compatible API surfaces.
- Streaming, non-streaming and WebSocket responses where supported.
- Function calling, tool search and multimodal inputs.
- Deferred tool declaration and activation across supported provider routes;
  clients retain tool discovery, authorization and execution.
- OAuth authentication and API-key authentication.
- Multiple accounts with round-robin scheduling and route policies.
- Pluggable OpenAI-compatible upstream providers.
- File-based, PostgreSQL, Git and object-store persistence backends.
- Management API, configuration hot reload and observability.
- Reusable Go SDK for embedding the proxy in another service.

## Deferred Tool Loading: Measured Results

`v1.3.0-upstream8.0.16` includes cross-provider tool-loading fixes for official
Codex and Claude Code. The frozen candidate `bfc5c62c` was tested with Codex
`0.160.0` and Claude Code `2.1.289`; the following three comparable fixed-task
pairs passed in both eager (full declarations) and lazy (on-demand) modes.
Figures were verified on 2026-10-14.

| Client / route / task | First input reduction | Peak input reduction | Cumulative input tokens, eager → lazy | Cumulative input reduction |
| --- | ---: | ---: | ---: | ---: |
| Codex / GPT / budget | 67.22% | 65.28% | 317,874 → 89,527 | 71.84% |
| Claude Code / GPT / budget | 94.40% | 91.26% | 160,384 → 14,490 | 90.97% |
| Claude Code / Muse / identity | 94.25% | 91.72% | 195,991 → 17,601 | 91.02% |

**Metric definitions:** first input counts input tokens in the first actual
model request; peak input is the largest single-request input; cumulative
input sums all model requests, including discovery, retransmitted history,
failures and retries.
Reported cached input is included; output is separate. Each reduction uses
its own eager baseline, so cumulative reduction can exceed first or peak
reduction. Missing usage stays unknown. Input reduction is not billing savings.

**Coverage and limits:** the final batch finished 15 of 16 planned cases,
with 7 strict passes and 10 independent code-check passes; one case was not
started when the budget ran out. These are one fixed-task pair per listed
route, not a general success rate or a confidence interval. Gemini usage
remains unverified; Codex/Muse loading order and GLM execution did not meet all
acceptance checks. Some historical tasks increased cumulative input, and lower
input does not guarantee lower latency or cost.

See the [implementation report](docs/tool-loading/design-and-implementation_CN.md),
[verification and compatibility report](docs/tool-loading/verification-and-compatibility_CN.md),
[complete per-request token report](docs/tool-loading/token-usage-report_CN.md)
and [reproduction tools](ops/tool-loading/README.md). The reports are in Chinese
and retain failures, retries, unknown usage and client/provider boundaries.

## Quick Start

### Prerequisites

- Go `1.26.4` or a compatible newer Go toolchain.
- A provider account or an API key that can access the selected model.

### Build

```bash
git clone https://github.com/hrygo/CLIProxyAPI.git
cd CLIProxyAPI
go build -o cli-proxy-api ./cmd/server
```

### Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` for the server address, providers, routing and management
settings. `.env` in the working directory is loaded automatically; credential
material defaults to `auths/`.

### Run

```bash
./cli-proxy-api --config config.yaml
```

Useful flags include `--tui`, `--standalone`, `--local-model`,
`--no-browser` and `--oauth-callback-port <port>`.

### Homebrew

Install the binary built from this fork's published release:

```bash
brew tap hrygo/cliproxyapi
brew install hrygo/cliproxyapi/cli-proxy-api
cliproxyapi -h
```

Homebrew installs the executable and a configuration example; it does not
manage an existing configuration file or start a service. Upgrade and rollback
procedures are documented in [Homebrew maintenance](ops/homebrew/README.md).

## Configuration

| Path or variable | Purpose |
| --- | --- |
| `config.yaml` | Primary server, provider, routing and management configuration |
| `config.example.yaml` | Annotated starting point |
| `.env` | Optional environment values loaded from the working directory |
| `auths/` | Default OAuth credential storage |
| `PGSTORE_*` | PostgreSQL storage backend settings |
| `GITSTORE_*` | Git storage backend settings |
| `OBJECTSTORE_*` | Object-store storage backend settings |

The management interface is documented in
[Management API](docs/management-api-v8.md). Protocol and SDK references are
listed under [Documentation](#documentation).

## Development

```bash
gofmt -w path/to/changed.go
go vet ./...
go test ./... -count=1
go build -o cli-proxy-api ./cmd/server
```

Run the complete maintenance gate for release, intake or cross-module changes:

```bash
ops/upstream-intake/verify-absorb.sh
python3 ops/tests/test_maintenance.py
```

The gate uses `gofmt` from its active Go toolchain. Keep PR CI and local runs on
the same Go release because formatting can differ between Go versions.

Contributions target `hrygo/CLIProxyAPI:main`. Create short-lived
`codex/<task>` branches and follow [AGENTS.md](AGENTS.md).

## Maintenance and Releases

- [Maintenance, release and rollback](docs/maintenance.md)
- [Upstream release intake](ops/upstream-intake/README.md)
- [Upstream intake ledger](ops/upstream-intake/absorbed.md)
- [Release-note template](docs/releases/RELEASE_TEMPLATE.md)
- [Homebrew tap maintenance](ops/homebrew/README.md)

Every release requires a curated `docs/releases/<tag>.md`. It explains the
user-visible change, validation and known limitations, and names the exact
upstream release aligned with that fork release.

## Documentation

- [Configuration example](config.example.yaml)
- [Management API](docs/management-api-v8.md)
- [Responses tools](docs/responses-tools.md)
- [SDK usage](docs/sdk-usage.md)
- [SDK access](docs/sdk-access.md)
- [SDK advanced](docs/sdk-advanced.md)
- [SDK watcher](docs/sdk-watcher.md)
- [Custom provider example](examples/custom-provider)

## License

[MIT](LICENSE)

## Acknowledgements

CLIProxyAPI originates from
[`router-for-me/CLIProxyAPI`](https://github.com/router-for-me/CLIProxyAPI).
This fork preserves the upstream module path to keep selective release ports
compatible and thanks the upstream maintainers and contributors.
