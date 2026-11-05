---
name: cliproxyapi-fork-maintenance
description: >
  维护 hrygo/CLIProxyAPI 自有 fork 的上游 release 评估与完整树重放、fork 差异登记、
  自有版本发布及 Homebrew 安装、升级和回退，并校验或修复这些流程。非 fork 特有的普通
  API 功能开发、一般运行故障排查和其他仓库同步不触发。
---

# CLIProxyAPI fork maintenance

Maintain the `hrygo/CLIProxyAPI` fork with `main` as trunk and independent
`vX.Y.Z-upstreamA.B.C` releases. Keep `module github.com/router-for-me/CLIProxyAPI/v8`
unchanged.
The requested operation determines which procedure to load:

| Request | Read | Expected result |
| --- | --- | --- |
| Assess or replay onto upstream releases; maintain the fork inventory | [Intake](../../../ops/upstream-intake/README.md), [inventory](../../../ops/upstream-intake/fork-capabilities.md) and [ledger](../../../ops/upstream-intake/absorbed.md) | Mature release baseline, fork capability and patch inventory, equivalence decisions and verification evidence |
| Prepare or execute our release | [Maintenance](../../../docs/maintenance.md), release section | Reviewed version/commit, successful complete release |
| Install, upgrade, roll back or update the tap | [Homebrew](../../../ops/homebrew/README.md) | Verified formula, binary and service state |
| Audit or repair these processes | The affected procedure and script/workflow | Evidence-backed correction and isolated verification |

Read only the branch needed for the task. The documents and this skill describe
operations; they do not authorize remote branch or protection changes, pushes,
PR merges, publication, installation, or service restarts. These actions require
explicit user authorization for their targets. Existing authorization continues
to apply; do not ask again for routine steps it covers.

## Upstream and fork decisions

- Monitor upstream releases and security reports daily and assess stable releases
  weekly. A read-only schedule is configured separately when requested; do not
  assume one exists. Accept published stable releases after at least 72 hours,
  measured from `publishedAt`. The window lets regressions and hurried follow-up
  fixes surface upstream before we adopt a release. Upstream publishes
  frequently, so a fixed wait costs little; do not shorten it, tier it by
  commit prefix, or lower it through an environment override.
- Use the explicitly accepted comparison baseline in the ledger; a fetched tag
  or an assessment does not establish a completed replay. Ancestry can identify
  inherited history, but cannot establish the contents of a later replayed tree.
- Fetch only into `refs/upstream/tags/*` with `--no-tags`. Never push those refs.
- Use `check-tag-namespace.sh` as a diagnostic when upstream tags may have leaked
  into `refs/tags/*`. It can suppress failed remote lookups, so independently
  verify both remotes and tag ownership before acting on its output. Never delete
  or move a tag based on an incomplete comparison.
- Keep the capability and patch inventory current as part of each fork-specific
  feature, fix, or operational and release-governance change. Record what it
  preserves, its implementation and regression evidence, and its upstream status;
  do not wait for the next intake to reconstruct the delta.
- Use the complete mature upstream release tree as the new baseline. Review its
  changes and retain every fork capability, including required operational and
  release governance. Reconcile conflicts without silently dropping patches,
  upstream behavior, user data, configuration, or fork requirements. For each
  capability, retire the fork implementation only after verifying that upstream
  is equivalent; record the evidence and limits, not just commit similarity.
- When committing fork-specific work or replaying it, organize atomic,
  reviewable commits by capability, including their tests and supporting changes.
  Prepare and verify the candidate in an isolated worktree, with the full upstream
  history and fork commits traceable.
- After the candidate is fully verified, the intended integration is a protected
  `main` PR merge commit that preserves both upstream ancestry and the fork patch
  history; verify the merged tree and ancestry. Check the actual branch protection
  before integration. If it forbids merge commits, stop until the necessary
  remote rule change is explicitly authorized and applied; do not bypass it.
- Verify the current baseline and the replay candidate. A baseline failure blocks
  intake; diagnose it without silently expanding scope or granting a waiver.
- Record release and trunk SHAs, fork delta and equivalence decisions,
  baseline/candidate gate results and client replay coverage. An assessment or
  fetched tag is not a completed replay or a new baseline.

## Release and installation decisions

Release only reviewed commits on the `origin/main` first-parent chain. Tag releases
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

Upgrade through Homebrew. Before an upgrade, prepare a versioned rollback formula
and assess whether existing configuration and data are compatible with the older
version. Verify public assets, tap metadata, installed binary and running process
separately. Preserve existing config and credentials; do not replace a
Homebrew-managed executable with a hand-built file.

If completing the task requires an uncovered configuration migration, public
interface change or deployment decision, explain the concrete impact and ask for
that decision while continuing independent work. Report failed and unverified
checks, rather than converting them into completion claims.
