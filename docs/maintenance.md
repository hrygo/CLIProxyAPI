# Fork maintenance

`hrygo/CLIProxyAPI` owns `main`, release tags and release assets. The Go module
path remains upstream so upstream baselines require no import rewrite. Procedures below describe
operations; a request to review them does not authorize publishing or deployment.

## Choose the workflow

| Task | Authoritative procedure | Completion evidence |
| --- | --- | --- |
| Develop | [AGENTS.md](../AGENTS.md) | Relevant tests and server build for Go changes |
| Assess or replay onto upstream | [Intake](../ops/upstream-intake/README.md) | Replay and equivalence ledger, baseline and candidate gate results |
| Release | This document | Successful release workflow, complete public assets |
| Install, upgrade or roll back | [Homebrew](../ops/homebrew/README.md) | Formula, installed binary, running service verified separately |
| Agent-assisted intake | [Project skill](../.agents/skills/cliproxyapi-fork-maintenance/SKILL.md) | Same intake evidence; skill discovery grants no deployment authority |

## Release a version

1. When integration is authorized, merge the reviewed task branch into our
   `main`. For a replay, follow the [intake procedure](../ops/upstream-intake/README.md)
   and [branch governance](../AGENTS.md#branch-governance), including the stop
   condition if remote protection does not permit merge commits. Resolve
   compatibility changes and choose our semantic version: patch for compatible
   fixes, minor for compatible additions, major for breaking changes.
2. Build the release tag as `vX.Y.Z-upstreamA.B.C`, where `A.B.C` is the
   upstream release this version is aligned to. A version without a new upstream replay keeps the previous release's suffix; the `Ported` field records that no new upstream release was taken. The
   suffix is what distinguishes our tags from upstream's identical-looking
   `vX.Y.Z` names. The release workflow requires the suffix even though the
   standalone note validator also accepts some historical bare tags.
3. Copy `docs/releases/RELEASE_TEMPLATE.md` to `docs/releases/<tag>.md` and
   write the release from the user's perspective. Explain the compatibility or
   migration impact, validation actually performed, known limitations, and the
   exact upstream release aligned with this version, which must match the tag
   suffix. Validate it locally:

   ```bash
   python3 ops/release-notes/validate.py validate \
     v1.2.3-upstream8.0.6 docs/releases/v1.2.3-upstream8.0.6.md
   ```

   Commit the curated note before tagging. Generated commit summaries do not
   explain compatibility and are not a substitute for this file.
4. Run `ops/upstream-intake/verify-absorb.sh` on the candidate. For protocol
   changes, include the relevant client replay evidence and its coverage limits.
5. For a release based on a replay, compare the aligned upstream release's exact
   SHA with the accepted baseline and confirm that commit is an ancestor of the
   reviewed replay candidate. Verify the integration commit has the recorded
   prior `main` and the replay candidate as parents; the PR merge has the prior
   `main` and that integration commit as parents. Both integration trees must
   match the accepted candidate tree. Any subsequent changes in the release
   commit, including a fork hotfix or release note, need their own review,
   inventory and applicable validation. The
   [intake procedure](../ops/upstream-intake/README.md) records the evidence.

   Before tagging a release, verify that `guard-release-tag.sh` and its CI
   integration satisfy the [intake release-guard contract](../ops/upstream-intake/README.md#procedure)
   with isolated behavioral tests. The guard must verify the suffix's upstream
   release and SHA, the accepted baseline registry and eligible fork `main`
   commit, and their documented relationship; historical selective baselines
   need their actual evidence. Check that the registry agrees with the human
   ledger. The guard must reject an upstream commit, fail closed on lookup or
   authentication errors, and accept a valid fork release. The release workflow
   must provide the read-only authentication needed for the check. Machine
   validation does not prove human review or behavioral tests occurred. Stop
   before creating or pushing any release tag unless the guard, CI integration,
   behavioral tests and review evidence meet the contract.
6. Confirm a clean checkout, exact release commit on the `origin/main`
   first-parent chain, and absence of the proposed tag both locally and remotely.
   Both tag listings must be empty; a failed command blocks release:

   ```bash
   git status --short
   git fetch --no-tags origin main
   git rev-parse HEAD
   git rev-list --first-parent origin/main | grep -Fx "$(git rev-parse HEAD)"
   git tag --list v1.2.3-upstream8.0.6
   git ls-remote --tags origin refs/tags/v1.2.3-upstream8.0.6
   ```

7. When release execution has explicit authorization, create an annotated tag.
   Replace `v1.2.3-upstream8.0.6` with the reviewed tag:

   ```bash
   git tag -a v1.2.3-upstream8.0.6 -m "Release v1.2.3-upstream8.0.6"
   ```

   Run the verified guard against the locally created tag. A failure blocks the
   push:

   ```bash
   ops/upstream-intake/guard-release-tag.sh v1.2.3-upstream8.0.6
   ```

   Only after the guard succeeds, push the single release ref:

   ```bash
   git push origin refs/tags/v1.2.3-upstream8.0.6:refs/tags/v1.2.3-upstream8.0.6
   ```

   Never push `--tags`, `--mirror` or upstream refs. Do not move a published tag.

   A tag push can run the workflow and guard from the tagged commit. A tag
   pointing to an older commit may therefore run older checks; the current
   scripts do not protect every possible tag push. Enforcing a repository-wide
   boundary, such as a server-side tag ruleset, requires separate assessment
   and authorization.

8. `release.yaml` validates stable tag syntax, ancestry on `main`, the curated
   release note and its upstream-alignment statement, invokes the guard, then
   runs the full regression gate. It creates a draft and builds ten platform
   archives from the tagged source. One final job verifies the complete archive set, downloads the
   uploaded assets and checks their hashes, uploads `checksums.txt`, then
   publishes the draft. A failed build leaves the release unpublished.
9. Verify workflow success and the public assets before updating the tap.
   Manual workflow dispatch accepts an existing tag and resumes an incomplete
   draft; published releases are refused. Fix a published regression with a new
   version rather than replacing its binaries.

The catalog bundled in a release comes from the tag. Update and review model
catalog data as a normal source change before tagging. PR and release builds do
not fetch a floating model catalog. This avoids passing tests against source
that differs from the release commit.

## Verify installation and operation

The tap is a separate repository. Updating `ops/homebrew/cli-proxy-api.rb` here
does not update users' Homebrew metadata. Generate and review the formula, test
it in the tap, then publish that exact formula as a separately authorized step.

Before an upgrade, preserve a tested rollback formula and the existing config.
Afterward verify four separate facts: public Release, tap version and checksum,
installed binary banner, and restarted process behavior. A successful
`brew upgrade` does not establish that a process has restarted.

## Research and local choices

Official documents checked on **2026-10-01**:

- [Codex AGENTS.md](https://developers.openai.com/codex/guides/agents-md/) and
  [skills](https://developers.openai.com/codex/skills/): scoped instructions and
  progressive disclosure. Keep project invariants in `AGENTS.md`, specific
  procedures in `ops/`, and decision guidance in the skill.
- [GitHub immutable releases](https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/immutable-releases):
  create a draft, attach assets, then publish. The workflow checks for a
  published release before building and uploads draft assets with `--clobber`.
  Without repository-level immutability, that initial check alone does not
  prevent a concurrent publication during later uploads. Do not publish the
  draft separately while the workflow runs or modify a published release.
- [GitHub Actions security](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions):
  minimum token permissions and full commit SHA pins. Workflows use read
  permissions by default, write only where assets are published, and pin
  official actions to resolved full commit SHAs (with major-version comments).
- [Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook):
  versioned URLs, SHA256, and a formula test.
- [Homebrew Versions](https://docs.brew.sh/Versions) and
  [manual](https://docs.brew.sh/Manpage): use a maintained versioned formula or
  `brew extract` for an older version. `brew rollback` is not a supported command.

The 72-hour settling rule and release-tree replay are fork policy, not
requirements imposed by GitHub or Homebrew.

Repository-level immutable releases, tag protection and artifact attestations
need separate configuration or a further reviewed workflow change. SHA256 verifies
integrity; it is not a substitute for provenance.
