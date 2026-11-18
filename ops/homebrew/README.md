# Homebrew installation and tap maintenance

`cli-proxy-api.rb` is the reviewed source for the separate
`hrygo/homebrew-cliproxyapi` tap's `Formula/cli-proxy-api.rb`. Changing this copy
does not publish the tap. Use `hrygo/cliproxyapi`, not `hrygo/tap`.

## Install and upgrade

```bash
brew tap hrygo/cliproxyapi
brew install hrygo/cliproxyapi/cli-proxy-api
brew update
brew outdated hrygo/cliproxyapi/cli-proxy-api
brew upgrade hrygo/cliproxyapi/cli-proxy-api
brew info hrygo/cliproxyapi/cli-proxy-api
cliproxyapi -h
```

The formula installs the executable as `cliproxyapi` and keeps
`config.example.yaml` under the formula's `pkgshare`. Copy and edit that example
to a user-selected configuration path for a new installation, then start with
`cliproxyapi -config /absolute/path/config.yaml`. Preserve existing config and
auth files during upgrades; the formula does not manage them or register a service.

Disk installation and service restart are separate operations. Restart through
the existing service manager only when authorized. For the previously configured
macOS LaunchAgent, this is `launchctl kickstart -k gui/$(id -u)/com.hrygo.cliproxyapi`.
Do not run `brew services` in parallel with an existing custom LaunchAgent.
Verify the banner using `-h` (there is no `-version` flag), then verify the actual
running service and representative client behavior separately. After the
restart, watch the service logs and client-visible errors for at least 30 minutes
of real use; on a regression, roll back with the prepared versioned formula and
restart again.

## Update the tap after a complete release

1. Confirm successful release CI and a published Release with all ten archives
   and `checksums.txt`. See [release procedure](../../docs/maintenance.md).
2. Download its manifest into a unique temporary directory and render a candidate:

   ```bash
   release_dir="$(mktemp -d)"
   gh release download v1.2.3-upstream8.0.6 --repo hrygo/CLIProxyAPI \
     --pattern checksums.txt --dir "$release_dir"
   python3 ops/homebrew/render-formula.py v1.2.3-upstream8.0.6 "$release_dir/checksums.txt" \
     > "$release_dir/cli-proxy-api.rb"
   diff -u ops/homebrew/cli-proxy-api.rb "$release_dir/cli-proxy-api.rb"
   ```

   Replace the tag with the reviewed version. `diff` exits 1 for expected changes.
   The renderer rejects malformed versions, duplicate/invalid hashes and missing
   platform entries, and does not overwrite the source or tap. Review the generated
   formula and, when authorized, apply that generated result to the tap checkout.
   Keep `ops/homebrew/cli-proxy-api.rb` as a placeholder template; edit it only
   for changes to the template itself, never to insert a specific release.
3. In the tap checkout, validate the candidate formula before publishing:

   ```bash
   brew style Formula/cli-proxy-api.rb
   brew audit --strict hrygo/cliproxyapi/cli-proxy-api
   brew install hrygo/cliproxyapi/cli-proxy-api
   brew test hrygo/cliproxyapi/cli-proxy-api
   ```

   Use an isolated validation machine/runner for install/test, or an explicitly
   authorized upgrade on a machine where this formula is already installed.
   `brew install` will not test a changed formula over an installed version.
   Exercise the corresponding artifacts on supported target platforms; one
   local test does not validate all four URLs. Only then publish the tap change.

All four URLs and hashes must refer to the same release. The formula declares
`version` explicitly and `render-formula.py` substitutes it from the tag; do not
rely on Homebrew deriving the version from the URL, because release tags carry an
`-upstreamX.Y.Z` suffix that its URL parsing does not handle reliably. SHA256
verifies bytes, not publisher identity; the release workflow also verifies
uploaded assets before publication.

## Prepare rollback before upgrading

Homebrew has no `brew rollback` command. Prepare a tested versioned formula
using the previously installed fork release and its verified checksums. Generate
the old release values from the template, then adapt the versioned formula's
filename and Ruby class to Homebrew's naming rules in the authorized tap
checkout. [`brew extract`](https://docs.brew.sh/rubydoc/Homebrew/DevCmd/Extract.html)
cannot extract a formula into its own source tap.
Review and test the rollback formula before relying on it.
Retain the matching configuration and any migration notes. On authorized rollback,
unlink the current formula, install/link the prepared versioned formula, and
restart via the existing manager. Verify disk version and running behavior.
Do not rely on old Cellar directories surviving `brew cleanup`.

For a legacy manually installed regular file, first verify its ownership and
preserve it at a unique backup path outside Homebrew's managed directories.
Resolve only that known path conflict when installing the tap formula. Never
force-link over unknown files or copy a binary over a Homebrew symlink.
