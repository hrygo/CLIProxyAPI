# Reproducing tool-loading performance

This directory runs installed **official Codex 0.160.0 and Claude Code 2.1.289**
against an isolated CLIProxyAPI SDK listener and a real stdio MCP coding fixture.
It does not build either client. OpenCode, batch certification, Docker workloads
and client-source probes are outside this utility.

## Entry points

- `reproduce.py`: run a declared eager/lazy pair, preserve every started case,
  verify actual route identity, and write a numeric report.
- `report.py` and `normalize_usage.py`: compare complete pairs and retain unknown
  counters, failed attempts and negative benefits.
- `live-gateway/`: the existing SDK isolation launcher and Go regressions.
  It reads selected credentials in memory, disables refresh and persistence,
  pins the route, records physical HTTP attempts and seals the capture on exit.
- `clients.py`, `catalog_fixture.py`, `coding_tasks.py`: isolated official-client
  settings, 70 exposed tools, and host-owned behavioral checks for one temporary
  Python source file.
- The remaining modules decode verified wire artifacts and observe effective
  tool mode. Requested settings alone do not establish deferred loading.

Historical experimental tools remain recoverable at
[`d9e64b8e`](https://github.com/hrygo/CLIProxyAPI/tree/d9e64b8eff81414244703220d44d651d195ee428/ops/tool-loading).
One-off probes, client builds, run journals, credentials and binaries are not
repository deliverables. Put new throwaway scripts and artifacts in system tmp.

## Build and run

Use a clean checkout and the CI Go toolchain. The launcher rejects a binary
whose VCS revision differs from the declared revision or whose build is dirty.

```sh
run_dir=$(mktemp -d)
python3 -m venv "$run_dir/venv"
"$run_dir/venv/bin/python" -m pip install -r ops/tool-loading/requirements.txt
export PYTHONPYCACHEPREFIX="$run_dir/pycache"
GOTOOLCHAIN=go1.26.4 go build -buildvcs=true -o "$run_dir/gateway" ./ops/tool-loading/live-gateway
git rev-parse HEAD
"$run_dir/venv/bin/python" ops/tool-loading/reproduce.py \
  --manifest /absolute/path/to/frozen-manifest.json \
  --binary "$run_dir/gateway" --output "$run_dir/results"
```

Supply an external manifest containing no secrets. The config path is read only;
use an explicitly prepared test source if testing a different route policy.
Credential bindings are hashes of an explicitly selected file name and contents
in that config's auth directory. Do not guess or select an arbitrary account.

Go 1.26.4 on this host does not stamp a managed worktree whose ".git" is a file.
For that case, build the exact frozen revision in a temporary local clone:

```sh
revision=$(git rev-parse HEAD)
git clone --shared --no-checkout . "$run_dir/source"
git -C "$run_dir/source" checkout --detach "$revision"
(cd "$run_dir/source" && GOTOOLCHAIN=go1.26.4 go build -buildvcs=true \
  -o "$run_dir/gateway" ./ops/tool-loading/live-gateway)
```

```json
{
  "version": 1,
  "gateway_revision": "<full clean checkout revision>",
  "config": "/absolute/path/to/operator-or-test-config.yaml",
  "codex": "/absolute/path/to/official/codex",
  "claude": "/absolute/path/to/official/claude",
  "fixture_python": "/absolute/path/to/venv/bin/python",
  "seconds": 120,
  "max_attempts": 16,
  "max_physical_attempts": 160,
  "routes": [{
    "client": "codex",
    "task": "budget",
    "modes": ["eager", "lazy"],
    "profile": {
      "client_model": "<public alias>",
      "upstream_model": "<actual executor model>",
      "executor": "<executor ID>",
      "contract": "openai-responses-v1",
      "auth_files": [{
        "name_sha256": "<64 hexadecimal characters>",
        "content_sha256": "<64 hexadecimal characters>"
      }]
    }
  }]
}
```

`client` is `codex` or `claude`; `task` is `budget`, `identity` or `retry`.
A pair has exactly one eager and one lazy case. Counterbalance their order across
pairs. An optional `notify: true` on a Codex route exercises progress plus final
output in the same exec invocation; enable it only with a supported explicit
native multi-output policy. API-key profiles omit `auth_files`; native OAuth
profiles require explicit bindings. Contract identifiers must match the actual
executor wire.

## Outputs and interpretation

`report.json` and `cases.jsonl` preserve all started cases, including startup
failures, quota failures, timeouts and interruptions. Every physical attempt
belongs to the denominator. An unfinished case or unknown accounting prevents a
complete-run pass. Config/binary drift stops the frozen run; selected credential
content is checked by the launcher. No automatic refresh, rebind or fallback
repairs the evidence.

A successful case needs an observed effective mode, real read/replace/test
dispatches, independent code correctness, matching nonce and source revision,
client and upstream terminal events, and no forbidden MCP dispatch or unreleased
fixture work. A nonzero CLI exit does not become a success because its source
file passes the checker.

For Antigravity, a missing model is re-read while the same listener's authenticated
catalog-state receipt reports pending native discovery. This consumes the case
deadline and does not initiate another upstream discovery. A settled missing model,
invalid readiness receipt or incompatible model capability is rejected.

The strict Code Mode history observer covers native and bridged Responses and
Chat. It does not yet normalize Antigravity Code Mode history. A Codex/Antigravity
case can complete its code task while its strict mode check remains unverified;
the generic `model_or_mode` journal label does not establish a model or gateway
fault. Do not certify a mode or paired saving from that label alone.

`per_request` exports allowlisted numeric usage, HTTP status, completion basis
and normalized counters. Raw bodies stay in the private capture. Input/cache and
output/reasoning counters retain their provider contract. Incomplete or conflicting
usage stays unknown; a known subtotal is not a full task total. Gemini cumulative
counters are observed wire evidence, not billing authority. Cost is null without
verified prices and billing. Missing cache-write counters stay null.

This is fixed-task reproduction, not a general repository sandbox, discovery
quality benchmark or reliability certification. Fixture tool names are supplied
in the prompt. Hosted image generation is disabled only in the isolated launcher;
operator settings are unchanged. Cancellation and permissions retain their
separately documented scope. One pair per route does not justify a population
success-rate claim, confidence interval, or long-task benefit.

## Offline checks

```sh
GOTOOLCHAIN=go1.26.4 go test ./ops/tool-loading/live-gateway -count=1
"$run_dir/venv/bin/python" -m unittest discover -s ops/tool-loading/tests -v
```

CI runs Go launcher regressions in its full Go test gate and the standard-library
accounting/usage tests without a Provider or login. The full local Python suite
also exercises real stdio MCP and bounded workers. See the
[verification report](../../docs/tool-loading/verification-and-compatibility_CN.md)
and [token report](../../docs/tool-loading/token-usage-report_CN.md) for frozen
versions, measured results and limitations.
