# Upstream release intake

Track published stable releases from `router-for-me/CLIProxyAPI`. Wait at least
**72 hours** after `publishedAt`, then use the complete release tree as the
baseline and replay the fork changes still needed. Preserve the existing `main`
history, user data and configuration. The window lets regressions and follow-up
fixes surface upstream; it cannot be lowered through an environment override.

## Procedure

1. Read [absorbed.md](absorbed.md) and record the accepted comparison baseline
   and exact current `main` commit. Earlier selective intake records describe a
   reviewed ceiling, not a complete upstream-based tree. Assessment or a fetched
   tag alone does not advance that baseline.
2. Assess a mature candidate, replacing both example tags:

   ```bash
   ops/upstream-intake/assess-release.sh v8.0.6 v8.0.4
   ```

   The script requires `git` and `gh` read access; it rejects drafts,
   prereleases, malformed versions, releases younger than 72 hours and a
   non-ancestor comparison baseline. It fetches only to `refs/upstream/tags/*`
   with `--no-tags`. Its log and diff are review aids, not a list of changes to
   select. `MIN_AGE_HOURS` may increase, but not lower, the gate.
3. Run `ops/upstream-intake/verify-absorb.sh` on the clean current baseline.
   Preserve unrelated files. Diagnose a baseline failure within authorized scope
   before intake; do not silently waive it.
4. In an isolated worktree, start the replay candidate from the exact upstream
   release commit. Inventory the fork's behavioral, configuration, build,
   release and maintenance changes. For every fork capability compare the
   upstream behavior against its requirements with relevant tests and client
   evidence. Retire a fork implementation only when equivalence is verified;
   otherwise replay the needed fork changes and their tests. Replay the required
   fork governance and release tooling as well, including the documented CI
   exclusions. Review upstream changes and conflict resolutions so that upstream
   content is retained except for explicitly documented, necessary fork deltas.
   Assess compatibility and migration impact; never discard user configuration,
   credentials or persisted data. Escalate uncovered migration decisions.
5. Run the regression gate on the replay candidate. For request/response changes,
   run representative requests against both baseline and candidate with isolated,
   equivalent configurations. Record actual client coverage and limitations.
6. Preserve trunk history: create a `codex/*` integration branch from the recorded
   `main` commit and apply the reviewed **complete resulting tree** as a change on
   that branch. For example, with a clean integration worktree and a committed
   candidate, `git diff --binary --full-index <main-sha> <candidate-sha> | git
   apply --index` applies tracked additions, modifications and deletions. Inspect
   the diff first, protect untracked and ignored files, and stop on any conflict.
   Verify `git write-tree` equals the candidate's `git rev-parse <candidate-sha>^{tree}`;
   then run the candidate gate on the integration branch. Use the normal
   protected PR and linear-history process. Never rewrite `main` history.
7. Record in [absorbed.md](absorbed.md) the upstream release and commit, prior
   baseline and trunk commit, retained and retired fork deltas, equivalence
   evidence and limits, integration tree, gate and client replay results. Mark
   the new baseline complete only after the reviewed tree reaches `main` with
   passing checks. Do not describe an assessment as a completed replay.
8. When releasing, the `Upstream release` and tag suffix name the accepted
   baseline; the previous baseline and `Ported` field must agree with the ledger.
   Release through [docs/maintenance.md](../../docs/maintenance.md) when requested.
   Assessment or replay alone does not authorize publishing or deployment.

## Tag namespace hygiene

Upstream tags belong in the local-only `refs/upstream/tags/*` namespace. A plain
`git fetch upstream --tags` writes them into `refs/tags/*` instead, and a later
`git push --tags` would then publish every one of them from this fork. Upstream's
whole release history is present locally this way today.

Stop it at the source once per clone:

```bash
git config remote.upstream.tagOpt --no-tags
```

With that set, a plain `git fetch upstream` no longer imports tags at all, and
only the explicit namespace-targeted fetches below bring any in.

```bash
ops/upstream-intake/check-tag-namespace.sh --remote
```

The check compares tag names, but never proposes deleting a tag that `origin`
also publishes: this fork numbers its releases independently and our `v1.0.0`
already shares a name with an upstream tag pointing at a different commit.
It also warns when `remote.upstream.tagOpt` is unset, since that is what makes
the leak recur.

At release time `ops/upstream-intake/guard-release-tag.sh <tag>` rejects any tag
that resolves to the same commit as the upstream tag of that name. The release
workflow runs it before building anything.

## Regression gate

The gate requires Python 3 and checks the offline maintenance fixtures, tracked
Go formatting with `gofmt` from the active `go env GOROOT` toolchain,
`go vet ./...`, focused responses-tools
invariants, all unit/integration tests, race-sensitive packages and a server build
in a unique temporary directory. Formatter failures propagate; the gate neither
formats files nor reads unrelated untracked source. It records all failed stages
and returns nonzero if any stage fails.

Passing means no regression detected by those checks. For release-sensitive
behavior, keep the additional acceptance evidence and its limits in the ledger.
