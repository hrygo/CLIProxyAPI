# AGENTS.md

Go 1.26+ proxy server providing OpenAI/Gemini/Claude/Codex compatible APIs with OAuth and round-robin load balancing.

## Repository
- Upstream: https://github.com/router-for-me/CLIProxyAPI
- Our fork (source of truth): https://github.com/hrygo/CLIProxyAPI

## Maintenance Model
This repository is a self-maintained fork. `main` is the trunk; `codex/*` are task
branches that must merge back into `main`. Preserve `main` history when
integrating an upstream-based tree; do not wait for upstream review before
shipping our own work.

## Fork purpose and convergence
This fork exists only to fix problems upstream has not solved: the
response-side integral-float canonicalization for strict Codex clients
(`sdk/translator/codex_tool_arguments.go`, tracked upstream as
`router-for-me/CLIProxyAPI#6255`), the `local-refs: "flatten"` mode for
recursive tool schemas (`docs/responses-tools.md`), and the responses-tools
pipeline (`internal/responsestools/`). The Gemini/Vertex/AI Studio streaming
repairs shipped in `v1.1.0-upstream8.0.7` were retired when upstream's
streaming path replaced them.

Upstream wins every overlap. At each intake, look for an upstream replacement
before deciding to keep a fork behavior. When upstream implements the same
behavior, delete the fork implementation and its fork-only tests and take
upstream's; record the upstream file or commit and the evidence that the
behavior survives. Keep a fork implementation only where upstream has no
replacement or its replacement is not equivalent for a behavior we depend on,
then as the smallest delta on top of upstream's path, re-checked every intake.
A parallel implementation is never maintained "until we are sure", and a fork
patch upstream replaced is never re-applied: removal is convergence, not a
regression to repair later. The fork is retired once nothing it fixes remains
unfixed upstream.

The opt-in bootstrap timeout is a fork divergence. Upstream closed
`router-for-me/CLIProxyAPI#6930` as not planned because its design policy allows
timeouts only during credential acquisition, so the opt-in
`requests.streaming.bootstrap-timeout` bound in
`sdk/api/handlers/handlers_stream.go` is retained while upstream lacks equivalent
behavior; reevaluate equivalence at every intake. It is not upstream's
`codex.stream-bootstrap-timeout`, which covers Codex bootstrap buffering only and
releases buffered frames instead of abandoning the attempt; abandoning a silent
attempt on the general SSE path is the part upstream declined. The timeout
policy below carries the matching exception.

- **Remotes:** `origin` is our fork (`hrygo/CLIProxyAPI`), `upstream` is
  `router-for-me/CLIProxyAPI`.
- **Upstream intake is release-only.** Track `upstream` releases, not its `dev` or
  `main` branches. Use each accepted newest stable release as the complete
  source baseline; selection takes the newest upstream release at assessment
  time and applies no age threshold, so regression risk is carried by the replay
  gate and the post-install canary with a prepared rollback formula. Preserve
  every fork capability in atomic commits, and take upstream's replacement
  instead of the fork's implementation whenever upstream provides one.
  Integrate the
  reviewed replay with a protected PR merge commit preserving upstream and
  existing `main` history; see the branch governance prerequisites below. Fetch
  upstream tags into the local-only `refs/upstream/tags/*` namespace and never
  push them to `origin`: `release.yaml` triggers a full
  multi-platform build on a `vX.Y.Z-upstream*` tag push, so pushing upstream
  tags here would publish spurious releases from our fork.
- **Our releases carry their own `vX.Y.Z-upstreamA.B.C` tags**, where the suffix
  records the aligned upstream release. A release that ports no new upstream work
  keeps the previous suffix; the `Ported` field records that nothing new was
  taken. The suffix separates our tags from upstream's identical-looking names
  and must agree with the note's upstream-alignment statement. Push only the
  specific release ref, never `--tags` or `--mirror`. Release CI checks tag
  syntax and main ancestry, invokes the release guard, requires a curated
  `docs/releases/<tag>.md`, runs the regression gate, builds a draft, and
  publishes only after all archives and checksums are verified. Before release,
  verify the guard and workflow satisfy the behavioral contract in the
  [maintenance guide](docs/maintenance.md); stop if the contract is not proven.
- **Module path stays upstream.** `go.mod` keeps
  `module github.com/router-for-me/CLIProxyAPI/v8` so upstream releases can be ported
  without rewriting imports across the tree. Do not rename it.
- **Installation consumes our release artifacts** through the Homebrew tap
  `hrygo/homebrew-cliproxyapi`, tapped as `hrygo/cliproxyapi`
  (`brew install hrygo/cliproxyapi/cli-proxy-api`), which
  pins the release tarball's `sha256`. Do not build a binary by hand and `mv` it
  over a Homebrew-managed path; that leaves a stale regular file outside Cellar
  management. Homebrew has no `brew rollback` command; prepare a versioned
  formula before upgrading if rollback is required.
- **The tap formula is generated, not hand-maintained.** Run
  `ops/homebrew/render-formula.py <tag> <checksums.txt>` and apply the generated
  formula to the tap; edit `ops/homebrew/cli-proxy-api.rb` only to change the
  template itself, and never hand-edit `Formula/cli-proxy-api.rb` in the tap. The
  formula's `version` is the version in the asset filenames, which for an
  upstream-aligned release equals the full tag minus `v`, including the
  `-upstream` suffix. The download URL uses the full tag.
- [Maintenance guide](docs/maintenance.md) is the workflow entrypoint.
  Detailed intake and installation procedures live under `ops/`.
- Project skills live in `.agents/skills/`; use `cliproxyapi-fork-maintenance` for intake.
  Load only the procedure needed by the current task. Auditing a skill does
  not authorize its release or deployment steps.

### Branch governance

- `main` is protected. Direct pushes are rejected; open a PR from a `codex/*`
  branch and let the `build` check pass. Ordinary changes may merge by rebase or
  squash when permitted by branch protection. Approval is not required. Force
  pushes and branch deletion are disabled, including for admins, and unresolved
  review threads block the merge.
- Integrate upstream replays with a protected PR merge commit. Its first parent
  is the recorded prior `main`; its second parent is the reviewed integration
  branch, whose merge commit has that `main` and the replay candidate as its
  parents. Verify the resulting tree and upstream and fork commit ancestry.
  If remote protection forbids merge commits, stop until the necessary rule
  change is explicitly authorized and verified. Keep all other protections;
  do not bypass them or substitute a squash or rebase merge.
- `codex/codex-tool-search-shim` is a locked read-only archive branch that is
  never merged into `main` and must not be built on. The tool-search work it
  explored shipped through the responses-tools pipeline instead, so the branch
  is retained purely as a recoverable archive, not as pending work.
- Tag pushes are not affected by branch protection, so release tags can still be
  pushed from a local tag ref.

### CI

Only `release.yaml` and `pr-test-build.yml` are kept. Both use Go `1.26.10`.
`pr-test-build.yml` runs on pull requests and pushes to `main`, and gates on
`gofmt`, an offline validation of the tracked Codex model catalog,
`go vet ./...`, `go test ./... -count=1`, and a server build. Run the
maintenance gate with the same Go release; `gofmt` output can differ between
Go versions, so the workflow resolves it from the pinned toolchain's `GOROOT`
and checks only tracked sources (same as `verify-absorb.sh`).

Upstream-only workflows were removed and must not be reintroduced:
`docker-image.yml` (pushed to a third-party Docker Hub org),
`auto-retarget-main-pr-to-dev.yml`, `agents-md-guard.yml` (auto-closed PRs touching
`AGENTS.md`), and `pr-path-guard.yml` (blocked `internal/translator/` changes, a
restriction that no longer applies now that we own this fork).

## Commands
```bash
gofmt -w path/to/changed.go # Format changed Go files only
go build -o cli-proxy-api ./cmd/server # Build
go run ./cmd/server # Run dev server
go test ./... # Run all tests
go test -v -run TestName ./path/to/pkg # Run single test
ops/upstream-intake/verify-absorb.sh # Full release/intake gate, including build
python3 ops/tests/test_maintenance.py # Offline maintenance tooling checks
```
- Common flags: `--config <path>`, `--tui`, `--standalone`, `--local-model`, `--no-browser`, `--oauth-callback-port <port>`

For Go changes, run focused tests and a server build; broaden to the full gate
for release/intake or cross-module behavior changes. For docs and tooling, check
links, syntax, and isolated fixtures. Do not restart the service to test docs.
Preserve unrelated untracked files and user configuration.

## Config
- Default config: `config.yaml` (template: `config.example.yaml`)
- `.env` is auto-loaded from the working directory
- Auth material defaults under `auths/`
- Storage backends: file-based default; optional Postgres/git/object store (`PGSTORE_*`, `GITSTORE_*`, `OBJECTSTORE_*`)

## Architecture
- `cmd/server/` — Server entrypoint
- `internal/api/` — Gin HTTP API (routes, middleware, modules)
- `internal/api/modules/amp/` — Amp integration (Amp-style routes + reverse proxy)
- `internal/thinking/` — Main thinking/reasoning pipeline. `ApplyThinking()` (apply.go) parses suffixes (`suffix.go`, suffix overrides body), normalizes config to canonical `ThinkingConfig` (`types.go`), normalizes and validates centrally (`validate.go`/`convert.go`), then applies provider-specific output via `ProviderApplier`. Do not break this "canonical representation → per-provider translation" architecture.
- `internal/runtime/executor/` — Per-provider runtime executors (incl. Codex WebSocket)
- `internal/translator/` — Provider protocol translators (and shared `common`)
- `internal/registry/` — Model registry + remote updater (`StartModelsUpdater`); `--local-model` disables remote updates
- `internal/store/` — Storage implementations and secret resolution
- `internal/managementasset/` — Config snapshots and management assets
- `internal/cache/` — Request signature caching
- `internal/watcher/` — Config hot-reload and watchers
- `internal/wsrelay/` — WebSocket relay sessions
- `internal/usage/` — Usage and token accounting
- `internal/home/` — CLIProxyAPIHome control plane integration (bootstrap, RESP communication, dispatch coordination)
- `internal/tui/` — Bubbletea terminal UI (`--tui`, `--standalone`)
- `sdk/cliproxy/` — Embeddable SDK entry (service/builder/watchers/pipeline)
- `test/` — Cross-module integration tests

## Code Conventions
- Keep changes small and simple (KISS)
- Comments in English only
- If editing code that already contains non-English comments, translate them to English (don’t add new non-English comments)
- For user-visible strings, keep the existing language used in that file/area
- New Markdown docs should be in English unless the file is explicitly language-specific (e.g. `README_CN.md`)
- Standalone `internal/translator/` changes are allowed when the task requires
  them. Explain translator-only scope in the commit message; no upstream
  permission check is needed.
- `internal/runtime/executor/` should contain executors and their unit tests only. Place any helper/supporting files under `internal/runtime/executor/helps/`.
- Follow `gofmt`; keep imports goimports-style; wrap errors with context where helpful
- Do not use `log.Fatal`/`log.Fatalf` (terminates the process); prefer returning errors and logging via logrus
- Shadowed variables: use method suffix (`errStart := server.Start()`)
- Wrap defer errors: `defer func() { if err := f.Close(); err != nil { log.Errorf(...) } }()`
- Use logrus structured logging; avoid leaking secrets/tokens in logs
- Avoid panics in HTTP handlers; prefer logged errors and meaningful HTTP status codes
- Timeouts are allowed only during credential acquisition; after an upstream connection is established, do not set timeouts for any subsequent network behavior. Intentional exceptions that must remain allowed are the Codex websocket liveness deadlines in `internal/runtime/executor/codex_websockets_executor.go`, the wsrelay session deadlines in `internal/wsrelay/session.go`, the management APICall timeout in `internal/api/handlers/management/api_tools.go`, the `cmd/fetch_antigravity_models` utility timeouts, and the opt-in `requests.streaming.bootstrap-timeout` bound in `sdk/api/handlers/handlers_stream.go`, which is armed only until an attempt's first frame exists (default `0` leaves it unbounded, and it is disarmed before any later network behavior)
- Avoid wall-clock `time.Sleep` in TTL, expiration, ordering, or cache-eviction unit tests due to platform timer granularity (e.g. Windows default timer resolution of ~15.6ms) and CI jitter under load; prefer controllable clocks (`nowFunc` / mock clock), explicit timestamp manipulation, or deterministic synchronization primitives.
- Note: if modifying features that involve CLIProxyAPIHome, check if corresponding updates are needed in the CLIProxyAPIHome repository.
- Endpoints under the `/v0/management` base URL are deprecated and no longer maintained. For any feature changes, do not modify endpoints under `/v0/management` unless necessary to fix compilation errors.
