---
name: cliproxyapi-fork-maintenance
description: >
  维护 hrygo/CLIProxyAPI 自有 fork 的上游 release 评估及完整基底重放、自有版本发布、
  Homebrew tap 安装、升级与回退，并校验或修复这些维护流程。用户请求上述工作时使用；
  普通 API 功能开发、一般运行故障排查和其他仓库同步不触发。
---

# CLIProxyAPI fork maintenance

Maintain the `hrygo/CLIProxyAPI` fork with `main` as trunk and independent
`vX.Y.Z-upstreamA.B.C` releases. Keep `module github.com/router-for-me/CLIProxyAPI/v8`
unchanged.
The requested operation determines which procedure to load:

| Request | Read | Expected result |
| --- | --- | --- |
| Assess or replay onto upstream releases | [Intake](../../../ops/upstream-intake/README.md) and [ledger](../../../ops/upstream-intake/absorbed.md) | Mature release baseline, fork delta and equivalence decisions, verification evidence |
| Prepare or execute our release | [Maintenance](../../../docs/maintenance.md), release section | Reviewed version/commit, successful complete release |
| Install, upgrade, roll back or update the tap | [Homebrew](../../../ops/homebrew/README.md) | Verified formula, binary and service state |
| Audit or repair these processes | The affected procedure and script/workflow | Evidence-backed correction and isolated verification |

Read only the branch needed for the task. The documents and this skill describe
operations; they do not grant permission to push tags, publish a tap, change
installed binaries, or restart a service. Existing explicit authorization
continues to apply to its target; do not ask again for routine steps it covers.

## Upstream decisions

- Accept published stable releases after at least 72 hours, measured from
  `publishedAt`. The window exists to let regressions and hurried follow-up
  fixes surface upstream before we adopt a release. Upstream publishes
  frequently, so a fixed wait costs little; do not shorten it, tier it by
  commit prefix, or lower it through an environment override.
- Use the explicitly accepted comparison baseline in the ledger; a fetched tag
  or an assessment does not establish a completed replay. Ancestry can identify
  inherited history, but cannot establish the contents of a later replayed tree.
- Fetch only into `refs/upstream/tags/*` with `--no-tags`. Never push those refs.
- Run `check-tag-namespace.sh` when unsure whether upstream tags leaked into
  `refs/tags/*`. It never proposes deleting a tag origin also publishes, because
  this fork's version numbers overlap upstream's historical ones.
- Use the complete mature upstream release tree as the new baseline. Review its
  changes and replay only necessary fork changes, including required operational
  and release governance. Reconcile conflicts without silently dropping upstream
  behavior, user data, configuration, or fork requirements.
- For every fork capability, compare upstream behavior and retire our version
  when equivalence is verified. Record evidence and limits; do not infer
  equivalence from commit messages or mere similarity.
- Preserve the existing `main` history and protected linear PR workflow. Prepare
  and verify the replay in an isolated worktree; integrate the reviewed resulting
  tree onto a `codex/*` branch rooted at `main`, then verify the resulting tree.
- Verify the current baseline and the replay candidate. A baseline failure blocks
  intake; diagnose it without silently expanding scope or granting a waiver.
- Record release and trunk SHAs, fork delta and equivalence decisions,
  baseline/candidate gate results and client replay coverage. An assessment or
  fetched tag is not a completed replay or a new baseline.

## Release and installation decisions

Release only reviewed commits reachable from our `main`. Tag releases
`vX.Y.Z-upstreamA.B.C`, where the suffix names the upstream release the tag is
aligned to and must match the note's upstream alignment. A release that ports no
new upstream work keeps the previous release's suffix, because it is still
built on that baseline; record that in `Ported` rather than declaring the
alignment absent. Push a single own tag, assemble complete assets as a draft,
and publish after final checksum validation. Do not move published tags or
overwrite published assets; fix forward with a new version. Every release
requires a curated `docs/releases/<tag>.md` that explains
user-visible changes, validation limits and the exact aligned upstream release.
Update the separate tap only after the public release is complete.

Upgrade through Homebrew. Prepare a versioned rollback formula first when a
rollback path is needed. Verify public assets, tap metadata, installed binary and
running process separately. Preserve existing config and credentials; do not
replace a Homebrew-managed executable with a hand-built file.

If completing the task requires an uncovered configuration migration, public
interface change or deployment decision, explain the concrete impact and ask for
that decision while continuing independent work. Report failed and unverified
checks, rather than converting them into completion claims.
