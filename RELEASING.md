# Releasing the Laevitas CLI

Tagged releases are published automatically by [`.github/workflows/release.yml`](.github/workflows/release.yml) using [GoReleaser](https://goreleaser.com/). Pushing a `vX.Y.Z` tag triggers cross-compilation, archive packaging, checksum generation, GitHub Release creation, Homebrew tap update, and Scoop bucket update.

This document covers:

1. [The release procedure](#release-procedure) — what to do for every release.
2. [One-time setup](#one-time-setup) — what needs to exist before the first tag goes out.
3. [Versioning rules](#versioning) — semver in 0.x and beyond.
4. [Local dry-runs](#local-dry-runs) — how to validate the config without cutting a real version.
5. [Troubleshooting](#troubleshooting) — common failure modes.

---

## Release procedure

Pre-flight checklist before tagging:

- [ ] All intended changes are merged to `main` and CI is green.
- [ ] [`CHANGELOG.md`](CHANGELOG.md) has a section for the new version with today's date.
- [ ] `git status` is clean; you're on `main` at the head commit.

Cut the release:

```sh
# 1. Confirm the version you're tagging matches what CHANGELOG.md says.
grep -E "^## \[" CHANGELOG.md | head -3

# 2. Tag and push.
git tag -a v0.5.0 -m "v0.5.0"
git push origin v0.5.0
```

That's it. The `release.yml` workflow will:

1. Run on the new tag.
2. Invoke `goreleaser release --clean`.
3. Build binaries for linux/darwin/windows × amd64/arm64.
4. Package archives (`.tar.gz` for Unix, `.zip` for Windows).
5. Generate `checksums.txt` (SHA-256).
6. Create a GitHub Release at `https://github.com/laevitas/cli/releases/tag/vX.Y.Z` with all archives + the checksum file attached.
7. Push an updated formula to [`laevitas/homebrew-cli`](https://github.com/laevitas/homebrew-cli) and an updated manifest to [`laevitas/scoop-bucket`](https://github.com/laevitas/scoop-bucket) (only if `HOMEBREW_TAP_TOKEN` is set — see below).

Verify after the workflow finishes:

```sh
# Confirm the release page is live
gh release view v0.5.0

# Confirm Homebrew picked it up (after the tap commit lands)
brew update && brew info laevitas/cli/laevitas
```

If something goes wrong mid-run, see [Troubleshooting](#troubleshooting).

---

## One-time setup

These steps need to happen **once**, before the first tagged release. They have not been done as part of the v0.5.0 PR — the release workflow will skip the corresponding steps if the prerequisites are absent and log a warning.

### 1. Create the Homebrew tap repo

GoReleaser pushes the generated formula to a separate repo. Create it:

```sh
# On GitHub (or via gh CLI):
gh repo create laevitas/homebrew-cli --public --description "Homebrew tap for the Laevitas CLI"
git clone https://github.com/laevitas/homebrew-cli.git
cd homebrew-cli
mkdir Formula
git commit --allow-empty -m "init"
git push
```

Once it exists, users can install via `brew install laevitas/cli/laevitas`.

### 2. Create the Scoop bucket repo (Windows users)

Same idea:

```sh
gh repo create laevitas/scoop-bucket --public --description "Scoop bucket for the Laevitas CLI"
```

### 3. Generate a tap-write Personal Access Token

GoReleaser needs write access to both tap repos. The default `GITHUB_TOKEN` available in Actions only has access to the *current* repo, so we need a separate PAT.

1. Go to https://github.com/settings/tokens?type=beta (fine-grained PAT).
2. **Repository access**: pick *Only select repositories* → `laevitas/homebrew-cli` and `laevitas/scoop-bucket`.
3. **Permissions** → *Repository permissions*:
   - `Contents`: Read and write
   - `Metadata`: Read
4. Generate the token, copy the value.
5. In `laevitas/cli` go to **Settings → Secrets and variables → Actions → New repository secret**:
   - Name: `HOMEBREW_TAP_TOKEN`
   - Value: paste the PAT.

Once this secret is set, future tagged releases will automatically update the tap and bucket.

> The secret name is `HOMEBREW_TAP_TOKEN` for historical reasons even though it covers Scoop too — keeps the `.goreleaser.yaml` config readable.

### 4. Cloudflare Worker secrets for `cli.laevitas.ch`

`cli.laevitas.ch` is served by a **Cloudflare Worker** named `cli` using the [Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/) feature — i.e. an assets-only Worker pointing at `./site`. It is deployed by [`.github/workflows/deploy-site.yml`](.github/workflows/deploy-site.yml) on every push to `main` that touches `site/**`, the root `install.{ps1,sh}`, [`wrangler.jsonc`](wrangler.jsonc), or the workflow itself. Before the first push the workflow needs two repo secrets:

1. **Create a Cloudflare API token.** Cloudflare dashboard → My Profile → API Tokens → **Create Token** → use the **"Edit Cloudflare Workers"** template, or a custom token with `Account → Workers Scripts → Edit`. Scope to your account; no IP filter.
2. **Grab your Account ID** from the right sidebar of any Workers dashboard page.
3. **In `laevitas/cli` → Settings → Secrets and variables → Actions**, add:
   - `CF_API_TOKEN` = the token from step 1
   - `CF_ACCOUNT_ID` = the ID from step 2

The Worker name (`cli`) and the assets directory (`./site`) are both declared in [`wrangler.jsonc`](wrangler.jsonc). The custom domain (`cli.laevitas.ch`) is intentionally **not** declared in `wrangler.jsonc` — leaving it out means wrangler preserves whatever is already attached in the dashboard, so deploys can't accidentally detach the domain.

If the dashboard shows a "disconnected from Git" banner on the Worker, **leave it disconnected** — CI drives deploys now, and reconnecting Cloudflare's own builder would fight this workflow.

The root `install.{ps1,sh}` are the source of truth. The workflow copies them into `site/` immediately before `wrangler deploy`, so there is no second copy to keep in sync. `site/install.{ps1,sh}` is `.gitignore`d to enforce this.

---

## Versioning

This project follows [Semantic Versioning](https://semver.org/) starting from v0.5.0. While we're in `0.x`:

- **Patch** (`0.5.0 → 0.5.1`): bug fixes, doc fixes, no behavior changes that would surprise an existing scripted user.
- **Minor** (`0.5.0 → 0.6.0`): new commands, new flags, additive features. Default-behavior changes (like the v0.5.0 sort_dir flip) also go here while we're sub-1.0.
- **Major** (`0.x → 1.0.0`): commit to a stable surface. Once we cut 1.0, breaking changes require a major bump.

Pre-releases use the standard suffix: `v0.5.0-rc1`, `v0.5.0-beta.1`. GoReleaser auto-detects these and marks the GitHub Release as a pre-release; Homebrew/Scoop publishing is skipped automatically (`skip_upload: auto`).

The CLI's runtime version comes from `git describe --tags`. There is no source-side version constant to bump — **the tag is the version**.

---

## Local dry-runs

Before pushing a real tag, validate the config locally:

```sh
# Validate .goreleaser.yaml syntax without running anything.
goreleaser check

# Full snapshot build — produces dist/ but does NOT publish or tag.
make release-snapshot
# or directly:
goreleaser release --snapshot --clean
```

Snapshot builds use a synthesized version like `0.5.1-dev-abc1234` so they can't accidentally collide with a real tag. Inspect `dist/` for the resulting archives, checksums, and metadata.

If you don't have `goreleaser` installed, run it via `go run`:

```sh
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```

---

## Troubleshooting

### The workflow ran but no GitHub Release was created

Check the workflow logs at `https://github.com/laevitas/cli/actions`. The most common cause is a `.goreleaser.yaml` validation error — the run will fail at the `Run GoReleaser` step before any artifact is produced.

### The Release was created but Homebrew/Scoop weren't updated

Either `HOMEBREW_TAP_TOKEN` isn't set, or the PAT lacks write access to the tap repos. Goreleaser logs `skipping publish: HOMEBREW_TAP_TOKEN not set` (or similar) in this case. Fix the secret per [One-time setup](#one-time-setup) and re-run by deleting the tag, fixing, and re-tagging — see below.

### I tagged the wrong commit / need to redo a release

Once a tag is pushed publicly, the cleanest fix is to bump the version (e.g. `v0.5.0 → v0.5.1`) and tag again. Force-deleting a published tag is possible but breaks anyone who already cloned at that tag.

For a tag that hasn't been pushed yet:

```sh
git tag -d v0.5.0       # local delete only
```

For one that's already pushed but you're confident no one has consumed it:

```sh
git push --delete origin v0.5.0
git tag -d v0.5.0
gh release delete v0.5.0 --yes
# fix things, then re-tag
```

### A pre-release got marked as the latest

GoReleaser's `prerelease: auto` setting infers pre-release from the tag suffix (anything after `-`, like `-rc1`, `-beta.1`). If a pre-release is incorrectly marked as latest, edit the GitHub Release and toggle the "Set as latest release" checkbox.

### CI is green but the release workflow fails

`ci.yml` and `release.yml` are independent. CI runs on every push to `main` and every PR; it does not run on tag pushes. The release workflow runs only on `v*` tag pushes. If `release.yml` fails, the `ci.yml` status is irrelevant — look at the release-workflow logs directly.
