---
name: release
description: Walk through the canonical 18-step release procedure for laevitas-cli. Invoke when the user says "ship", "release", "tag", "v0.X.Y", "deploy", or any equivalent. Refuses shortcuts; confirms before each irreversible step. Don't improvise releases — always invoke this skill.
---

# `/release` — laevitas-cli release flow

You are about to ship a new version of `laevitas/cli`. **Follow this skill end-to-end. Do not improvise. Do not skip steps.**

The full reference lives in [CLAUDE.md "Shipping a release" section](../../../CLAUDE.md). This skill is the executable form: confirm before each irreversible step, surface failures clearly, and never let the user skip ahead.

## Before you start

Confirm three things by asking the user (one message, three questions):

1. **What version are we shipping?** Format `vX.Y.Z` (semver). If the user says a number without `v`, prepend it.
2. **What's the release theme?** One sentence. Used in PR title and commit message.
3. **Is the work already on a branch, or do we need to create one?** If on a branch, get the branch name.

If the user can't answer any of these, **stop**. Don't guess.

## Phase 1 — Pre-flight checks

Run all four. Report each as PASS / FAIL with the actual evidence.

1. **CHANGELOG.md has a top entry for this version.**
   ```
   grep -E "^## \[" CHANGELOG.md | head -1
   ```
   FAIL if the first match isn't `## [X.Y.Z] — YYYY-MM-DD` matching today and the version we're shipping.

2. **README.md and docs/SKILL.md mention the new commands/flags.** Open both. Look for the headline feature names from the CHANGELOG entry. If a v0.X.0 added a new command group or flag, the docs must reference it. FAIL if not.

3. **Build is clean.**
   ```sh
   go build -o laevitas-test.exe .
   ```
   FAIL on any output (warnings, errors). Clean up the binary after: `rm -f laevitas-test.exe`.

4. **Smoke tests pass.** At minimum:
   - `./laevitas-test.exe --help` lists every new top-level command from this release.
   - `./laevitas-test.exe perps carry BTC-PERPETUAL -p 1h -n 1 -o json` returns a valid envelope (`success: true`, `data` array, `meta` object).
   - If the release includes auth/wallet changes, also: `LAEVITAS_API_KEY=invalid ./laevitas-test.exe perps carry BTC-PERPETUAL -p 1h -o json` returns `success: false` with the right error code.

If any of the four FAIL: **stop**. Tell the user what failed. Don't proceed.

## Phase 2 — Branch, commit, push

5. Create the release branch (or check out the existing one):
   ```sh
   git checkout -b release/vX.Y.Z
   # or if it exists: git checkout release/vX.Y.Z
   ```

6. **Stage explicitly. Never `git add -A`.** List the changed files from `git status --short`, exclude anything in `.gitignore` (especially `.env`), and `git add` them by name.

7. **One commit, descriptive body.** Use a HEREDOC. The message structure:
   ```
   vX.Y.Z — <release theme>

   <multi-paragraph body summarising>:
   BREAKING — <if any>
   ADDED — <new commands, flags, fields>
   CHANGED — <behaviour changes>
   FIXED — <bug fixes>
   DEFERRED — <work scoped but not shipped>

   Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
   ```

8. Push:
   ```sh
   git push -u origin release/vX.Y.Z
   ```

## Phase 3 — PR + merge (the user does this)

Tell the user verbatim:
> Branch pushed. Open the PR at `https://github.com/Laevitas/cli/pull/new/release/vX.Y.Z`, wait for CI green, then merge to main. Tell me when it's merged and I'll handle the tag.

**Stop and wait.** Do not proceed to Phase 4 until the user confirms the merge.

## Phase 4 — Tag and ship

9. Switch to main and pull:
   ```sh
   git checkout main
   git pull
   ```

10. **Verify HEAD is the merge commit:**
    ```sh
    git log --oneline -3
    ```
    Confirm the top commit is the PR merge. If it's not (e.g. user did a different action), **stop** and ask them what happened.

11. **Verify the version bump is live on main:**
    - `grep -E "^## \[" CHANGELOG.md | head -1` shows the new version section.
    - If the release touched `go.mod` (e.g. a Go toolchain bump), `grep "^toolchain" go.mod` shows the bumped value.

12. Tag and push:
    ```sh
    git tag -a vX.Y.Z -m "vX.Y.Z"
    git push origin vX.Y.Z
    ```

## Phase 5 — Watch the pipeline

The Release workflow at `.github/workflows/release.yml` fires on the tag push. It runs GoReleaser which:

- Cross-compiles 6 binaries (linux/darwin/windows × amd64/arm64).
- Generates `checksums.txt`.
- Creates the GitHub Release with archives attached.
- Pushes a Cask formula to `Laevitas/homebrew-cli`.
- Pushes a Scoop manifest to `Laevitas/scoop-bucket`.

Tell the user:
> Tag pushed. Watch the workflow at `https://github.com/Laevitas/cli/actions`. Should finish green in ~3-5 min. Tell me when it's done and I'll verify the install paths.

**Stop and wait.** Don't proceed to Phase 6 until the user reports back.

## Phase 6 — Post-deploy verification

Once the workflow is green:

13. GitHub Release page: `https://github.com/Laevitas/cli/releases/tag/vX.Y.Z` should show 6 archives + `checksums.txt`.

14. Homebrew tap: `https://github.com/Laevitas/homebrew-cli/blob/main/Casks/laevitas.rb` should contain `version "X.Y.Z"`.

15. End-to-end install test (the user runs this on a real host):
    ```sh
    brew update && brew upgrade laevitas/cli/laevitas
    laevitas version
    ```
    Expected: `laevitas vX.Y.Z (build: <sha>, ...)` with a **single** `v`.

If any of those FAIL, surface the specific failure mode from CLAUDE.md's "Common failure modes" table.

## Hard rules — never break these

- **NEVER tag from a feature branch.** Always `main` after merge.
- **NEVER tag before merging.** Pipeline runs against tagged commit.
- **NEVER reuse a tag.** Bump patch (v0.7.0 → v0.7.1) instead of force-deleting.
- **NEVER `git add -A`** during a release commit. `.env` is gitignored but verify with `git status` before staging.
- **NEVER mention internal agent/bot names** in CHANGELOG, commit messages, PR descriptions, or any artifact that lands in the public repo. Use "consumers" / "scripts" / "JSON parsers" instead.
- **NEVER run `gh release create` or `git push --force`** to bypass a failed pipeline. Investigate the root cause; the pipeline is the source of truth.

## What's already set up (don't re-do)

- `Laevitas/homebrew-cli` and `Laevitas/scoop-bucket` repos exist.
- `HOMEBREW_TAP_TOKEN` secret configured in `Laevitas/cli` settings.
- `.github/workflows/release.yml` triggers on `v*` tag pushes.
- `.goreleaser.yaml` configured for cross-compile, Cask, Scoop.
- `cli.laevitas.ch/install.sh` redirects via Cloudflare to GitHub raw.

If any of these break (e.g. PAT expired), see `RELEASING.md` for re-setup instructions.
