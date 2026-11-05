# Upstream release intake

Monitor upstream releases and security reports daily, preferably with a
separately authorized read-only schedule, and assess stable releases from
`router-for-me/CLIProxyAPI` weekly. Do not assume a schedule exists without
verifying it. A release is eligible only after **72 hours** since `publishedAt`.
Use its complete tree as the baseline and replay every fork change still needed.
The window lets regressions and follow-up fixes surface upstream; it cannot be
lowered through an environment override.

An urgent security fix that cannot wait is a separate hotfix against the
existing accepted baseline on `main`. Keep that baseline and its release suffix;
do not adopt a younger upstream release or port individual changes from it to
evade the waiting period. An actual release requires separate authorization and
the release checks below. Record the hotfix and its tests in the inventory as
part of the hotfix commit, then reconcile that entry at the next intake.

## Procedure

1. Read [absorbed.md](absorbed.md) and the [fork capability and patch
   inventory](fork-capabilities.md). Record the accepted comparison baseline,
   refresh `origin/main`, and fix its exact commit as **M**. Earlier selective
   intake records describe a reviewed ceiling, not a complete upstream-based
   tree. If the previous tag is absent locally, fetch it only into the local
   `refs/upstream/tags/*` namespace. Resolve it to its peeled commit, verify it
   against the official upstream API and the ledger's recorded SHA before
   assessment, and stop if any value is missing or mismatched. Assessment or a
   fetched tag alone does not advance the baseline.
2. Assess the eligible stable release, replacing both example tags:

   ```bash
   ops/upstream-intake/assess-release.sh v8.0.6 v8.0.4
   ```

   The script requires `git` and `gh` read access; it rejects drafts,
   prereleases, malformed versions, releases younger than 72 hours and a
   non-ancestor comparison baseline. It fetches only to `refs/upstream/tags/*`
   with `--no-tags`. Its log and diff are review aids, not a list of upstream
   changes to select. `MIN_AGE_HOURS` may increase, but not lower, the gate.
   Record the release, `publishedAt`, age-check time and exact peeled upstream
   commit as **U**. Resolve the same tag through the official upstream API,
   peeling annotated tags to a commit, and confirm it matches the fetched U;
   the script's configured Git remote and API repository are independent.
   Stop on an unresolved or mismatched tag. Confirm the tag still resolves to U
   before integration.
3. Run `ops/upstream-intake/verify-absorb.sh` on a clean worktree at M, using
   the toolchain pinned by M's CI. Use C's and I's own pinned toolchain when
   verifying those commits later. Preserve unrelated files. A baseline failure
   blocks intake; diagnose it within authorized scope without a silent waiver.
4. Reconcile the capability and patch inventory against M. Examine the history
   and tree differences since the last complete inventory and accepted baseline,
   including every later fork feature, repair, test, configuration, documentation,
   CI, release and governance change. Earlier selective baselines require review
   of their actual history and decisions; a single ancestry or diff command does
   not prove completeness. Map each change or coherent feature to its source
   commits and files, requirements, tests and intended disposition. Update the
   inventory with anything added to M since it was last maintained.
5. In an isolated worktree, start at U and replay the inventoried changes on the
   **complete** upstream tree. Keep the upstream history as an ancestor. Apply
   changes in independently reviewable, feature-atomic commits with their tests,
   configuration and documentation; record original-to-replay commit mappings.
   For each capability compare required behavior and test coverage with upstream.
   Retire its fork implementation only when both behavior and tests are verified
   equivalent, recording the evidence and limits. Otherwise retain or adapt the
   implementation and its tests. Do not automatically skip an empty or
   conflicting patch, silently drop a test, or infer equivalence from similar
   code or commit messages. Resolve each decision explicitly and account for all
   M changes before freezing the candidate.

   If rebasing a reviewed patch series, use `--reapply-cherry-picks` and
   `--empty=stop` so an apparently duplicate or empty patch is not silently
   omitted. Inspect each stop and compare the original and replayed series with
   `git range-diff`, recording the disposition of every difference. Range-diff is
   a review aid, not proof of behavior or test equivalence. Individual patch
   replay is also valid, with the same accounting and evidence requirements.

   Review upstream changes and each conflict resolution. Preserve upstream
   content except for explicitly documented, necessary fork deltas, including
   fork governance, release tooling and the documented CI exclusions. Assess
   compatibility and migration impact; never discard user configuration,
   credentials or persisted data. Stop for uncovered migration decisions.
6. Freeze the committed replay candidate as **C**. Verify U is an ancestor of C
   and that every inventory item, including later changes on M, has an evidenced
   disposition in C. Record C and its tree outside C, such as in the PR evidence;
   a commit cannot contain its own final SHA or tree ID. Run the regression gate
   on C. For request/response changes, also run representative requests against
   M and C with isolated, equivalent configurations; record actual client
   coverage and limitations.
7. Integrate only when the protected `main` rules explicitly permit PR **merge
   commits** while retaining the required build check, protected branch and
   no-force-push safeguards, and require the branch to be up to date before
   merging. The currently enforced linear-history rule rejects this method;
   changing remote rules requires separate authorization. Do not
   bypass protection, use rebase or squash, force push, or flatten the candidate
   into a single tree patch as a substitute. If the rules do not permit a merge
   commit, stop before integration.

   Refresh `origin/main` and confirm it is still M. Create a `codex/*`
   integration branch in a clean worktree from M and merge C into it. Review
   each upstream/fork difference and resolve each conflict deliberately. Since
   all changes on M were already incorporated or explicitly accounted for in C,
   the complete staged merge tree must be exactly C's tree. For example, with
   `C` set to the full candidate commit ID:

   ```bash
   git merge --no-ff --no-commit "$C"
   ```

   If the merge reports conflicts, resolve and stage each one before continuing.
   Then check the pending merge and its complete tree:

   ```bash
   test -f "$(git rev-parse --git-path MERGE_HEAD)" &&
   test "$(cat "$(git rev-parse --git-path MERGE_HEAD)")" = "$C" &&
   test -z "$(git diff --name-only --diff-filter=U)" &&
   git diff --cached --name-status "$C" &&
   test "$(git write-tree)" = "$(git rev-parse "$C^{tree}")"
   ```

   Each test must succeed, `MERGE_HEAD` must contain exactly C, the diff must be
   empty and the tree IDs must match. An `Already up to date` result is not a
   merge and must stop here. If a check fails, stop;
   reconcile the inventory and rebuild or revalidate C rather than concealing a
   missing main change or discarding a candidate change. Commit the merge as
   **I**, verify its parents are M and C and its tree still equals C, and run
   the regression gate on I. Preserve the exact gate results and review evidence.
8. With authorization for remote changes, open the protected PR at I. Record M,
   U, C, I, the candidate tree, the complete dispositions and exact gate/client
   evidence in its review material. Require the PR `build` check and resolve
   review threads. The current pull-request workflow tests GitHub's temporary
   merge commit, not I itself: record the tested commit and confirm its tree
   equals I's, as well as the PR head I and base M. Immediately before merging,
   confirm the target is still M, the PR head is still I, the upstream tag still
   resolves to U and all required checks passed for that PR state. Require the
   host to reject an out-of-date branch at merge time;
   checking the PR head alone does not protect the base. If `main` advances,
   return to step 1: inventory the new main
   changes, rebuild or revalidate C and repeat integration and gates. Do not
   merge a stale candidate.

   Merge the PR with a merge commit **J**. Fetch the actual result and verify
   that J has parents M and I, J is on `origin/main`, its entire tree equals C's
   tree, and both C and U are ancestors of J. Check J itself, not just the
   latest main tree, which may later receive other commits. If any check fails,
   do not claim intake completion or publish from it; investigate and fix
   forward through the protected workflow.
9. Record the decisions, exact commits and tree IDs, gate results and client
   coverage in [absorbed.md](absorbed.md). The actual J SHA and tree cannot be
   embedded in J's own tree: capture them in PR evidence first, then record them
   in a separate follow-up ledger commit and protected PR. Advance the accepted
   baseline only after J and its ancestry and tree have been verified, and the
   final ledger record has reached `main`. An assessment, candidate, or fetched
   tag is not a completed replay.
10. A release is a separate operation. The `Upstream release`, tag suffix,
    previous baseline and `Ported` field must agree with the accepted ledger;
    follow [docs/maintenance.md](../../docs/maintenance.md) when authorized.
    Before publishing, the release guard must resolve the suffix's upstream tag
    and commit, validate the accepted fork main commit and its documented
    relationship to that upstream commit, and confirm the release target is
    eligible on `main`. For a complete replay this includes U and C being
    ancestors of J, J being an ancestor of the release target, and the release
    target being an ancestor of `origin/main`. Historical
    selective baselines must be checked against their actual ledger evidence,
    not described as complete upstream ancestry.

    The current `guard-release-tag.sh` checks a same-name upstream tag but does
    not establish these conditions and treats any failed lookup as absence.
    Its success alone is insufficient. Stop until the guard is corrected and
    passes isolated behavioral tests for the required conditions. An unresolved
    upstream version, network or authentication failure, failed guard, failed
    validation or missing evidence blocks publishing, including a hotfix;
    never bypass the guard. Assessment or replay alone does not authorize
    publishing or deployment.

## Tag namespace hygiene

Upstream tags belong in the local-only `refs/upstream/tags/*` namespace. A plain
`git fetch upstream --tags` writes them into `refs/tags/*` instead, and a later
`git push --tags` would then publish every one of them from this fork.

Stop it at the source once per clone:

```bash
git config remote.upstream.tagOpt --no-tags
```

With that set, a plain `git fetch upstream` no longer imports tags at all, and
only the explicit namespace-targeted fetches below bring any in.

```bash
ops/upstream-intake/check-tag-namespace.sh --remote
```

The check is diagnostic and can suppress remote lookup failures. A successful
exit does not prove that either remote was compared. Independently verify that
both remote queries succeeded and compare each tag's owner and target before
acting on any suggestion. This fork numbers releases independently, so a name
can also exist upstream while pointing to a different commit. Never delete or
move a tag based on an incomplete comparison. The check also warns when
`remote.upstream.tagOpt` is unset, since that is what makes the leak recur.

At release time `ops/upstream-intake/guard-release-tag.sh <tag>` is intended to
reject a tag that resolves to the same commit as the upstream tag of that name.
The release workflow runs it before building, but failed initial API lookups
currently pass; the repair and release stop condition above still apply.

## Regression gate

The gate requires Python 3 and checks the offline maintenance fixtures, tracked
Go formatting with `gofmt` from the active `go env GOROOT` toolchain,
`go vet ./...`, focused responses-tools
invariants, all unit/integration tests, race-sensitive packages and a server build
in a unique temporary directory. The formatter inspects tracked Go files only;
`go vet`, tests and builds can also read untracked or ignored Go source in package
directories. Run the gate in an isolated worktree without such extra source,
preserving all existing files. Formatter failures propagate; the gate does not
format files. It records all failed stages and returns nonzero if any stage fails.

Passing means no regression detected by those checks. For release-sensitive
behavior, keep the additional acceptance evidence and its limits in the ledger.
