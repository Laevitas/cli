# Changelog

All notable changes to the Laevitas CLI are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versions ≤ 0.4.0 are recorded in git tag annotations only; this file starts at 0.5.0.

## [0.5.1] — 2026-04-28

### Fixed

- **`laevitas update` now works.** The self-update command in 0.5.0 still asked for the pre-GoReleaser asset name (`laevitas-<os>-<arch>`) and 404'd on every platform. It now resolves GoReleaser archives (`laevitas_<ver>_<OS>_<ARCH>.{tar.gz,zip}`), verifies the SHA-256 against `checksums.txt`, and extracts the binary before the existing atomic-replace step. Linux/arm64 hosts (e.g. Hetzner CAX) are unblocked.
- **`install.ps1` detects arm64 Windows.** Previously hard-coded `x86_64` on any 64-bit Windows; now branches on `RuntimeInformation.OSArchitecture` and picks `arm64` when appropriate.

### Added

- **`windows/arm64` build target.** Removed the `ignore:` block in `.goreleaser.yaml`; releases now ship six archives instead of five.

### Release engineering

- **`cli.laevitas.ch` now has CI/CD.** New `.github/workflows/deploy-site.yml` copies the canonical root `install.{ps1,sh}` into `site/` and deploys the `cli` Cloudflare Pages project on every push to `main` that touches `site/**` or the install scripts. Requires repo secrets `CF_API_TOKEN` and `CF_ACCOUNT_ID`.
- **Removed duplicate install scripts.** `site/install.ps1` and `site/install.sh` are deleted from git and `.gitignore`d; they are now generated at deploy time from the root copies, so the asset-naming convention has a single source of truth.

## [0.5.0] — 2026-04-27

### Added

- **`spot` command group** (10 subcommands): `catalog`, `snapshot`, `metadata`, `ohlcvt`, `ticker`, `volume`, `level1`, `orderbook`, `orderbook-raw`, `trades`. Default exchange is `binance` (deribit doesn't trade spot); also supports coinbase, bybit, okx, kraken, bullish. Use spot as a reference-price layer for derivatives basis calculations.
- **`instruments` command group** (`list`, `detail`): cross-product instrument registry. `list` browses contract specs across spot/perpetual/future/option on every supported exchange with filters for market type, currency, status, expiry range, margin type, option type, and instrument-name partial match. `detail` returns the full contract specification including raw exchange API data.
- **`--sort-dir` flag** registered globally on every time-series command via `AddCommonFlags`. Accepts `ASC` or `DESC`. Previously was only declared (and accepted) on `trades`/`liquidations`/`trades-summary`; now uniform across all time-series commands.
- **Pagination + filters on every catalog command.** `futures catalog`, `perps catalog`, `options catalog`, `predictions catalog`, and the new `spot catalog` now accept `-n`/`--limit` and `--cursor`. Per-product filters added: `--maturity` (futures, perps, options), `--option-type`/`--strike-min`/`--strike-max` (options), `--quote-currency` (spot).
- **Dynamic instrument-name examples in `--help` output.** Example expiries like `BTC-26JUN26` are now computed from `time.Now()` at startup. New helpers in `internal/cmdutil/examples.go`: `ExampleFuturesInstrument`, `ExampleNearTermFuturesInstrument`, `ExampleOptionInstrument`, `ExampleMaturity`, plus a `SubstituteExamplesRecursive` walker called once from `cmd/root.go` that replaces `{{FUT}}` / `{{OPT_C}}` / `{{OPT_P}}` / `{{MAT}}` tokens in cobra `Long` / `Example` / `Short` strings.
- New `RequestParams` fields wired through `buildURL`: `QuoteCurrency`, `MinQuoteAmount`, `StrikeMin`, `StrikeMax`, `MarketType`, `Status`, `MarginType`, `ExpiryFrom`, `ExpiryTo`.

### Changed

- **Time-series default sort is now `DESC` (newest first).** `CommonFlags.ToParams()` defaults `SortDir = "DESC"` when the caller did not pass `--sort-dir` and did not pass `--cursor`. Row 0 of any time-series JSON response is now the most recent record in the window. To opt back into chronological iteration, pass `--sort-dir ASC`. When `--cursor` is set, the CLI does NOT inject a default direction so paginated scans keep the direction they started with.
  - This depends on a coordinated API change that adds `sort_dir` support to time-series endpoints (`carry`, `ohlcvt`, `oi`, `volume`, `ticker`, `level1`, `l2-orderbook`, `volatility`, `vol-surface/history`, `vol-surface/by-expiry`). Endpoints that already supported `sort_dir` (`trades`, `liquidations`, `trades-summary`) continue to behave as before.
- **Inline charts always render chronologically.** When a response is requested DESC, `RunAndPrint` reverses the array client-side before passing it to `RenderChart`, so charts still draw oldest→newest left-to-right regardless of how the JSON/table is sorted.
- **Pagination hint text** reads "→ Older results available" (instead of "→ More results available") when in DESC mode, since `--cursor` walks backward in time when sorting DESC.
- **Documentation overhaul.** README and `docs/SKILL.md` parameter tables are now split into Time-Series, Catalog, and Snapshot sections to remove the false implication that snapshot endpoints accept `-n`/`--start`/etc. (Snapshots are point-in-time and never paginate.) `CLAUDE.md` updated with the corrected catalog rule and new mistakes-to-avoid entries for sort_dir defaults, chart reversal, dynamic example tokens, and the `--sort-dir` flag-redefinition pitfall.

### Fixed

- Catalog commands previously silently ignored their pagination and filter parameters because the CLI only sent `--exchange`. They now forward every filter the API documents.
- Removed duplicate `SortDir` field declarations on local `tradesFlags` / `liquidationsFlags` structs that previously shadowed `CommonFlags.SortDir`.

### Internal

- New file `internal/cmdutil/examples.go`.
- New endpoint constants in `internal/api/endpoints.go`: `Spot*` (10 entries) and `Instruments{List,Detail}`.
- `cmd/spot/spot.go` and `cmd/instruments/instruments.go` added.
- `cmd/watch.go` endpoint map and `internal/completer/completer.go` command tree extended for both new groups.
- `internal/completer/completer.go` `catalogEndpoints` map now includes `spot` for instrument autocompletion.

### Release engineering

- **GoReleaser-driven release pipeline.** `.goreleaser.yaml` now drives cross-compilation (linux/darwin/windows × amd64/arm64), archive packaging (tar.gz / zip), checksum generation, GitHub Release publishing, Homebrew tap formula updates, and Scoop bucket manifest updates. Triggered by `git push <vX.Y.Z tag>` via `.github/workflows/release.yml`.
- **`install.sh` and `install.ps1`** at the repo root. Detect OS/arch, fetch the latest (or pinned) release tarball/zip, verify checksum, install to `$HOME/.local/bin` (POSIX) or `$env:USERPROFILE\bin` (Windows), and warn / auto-update PATH.
- **CI split.** `.github/workflows/ci.yml` is now test-only. Release lives in `.github/workflows/release.yml` and runs only on `v*` tag pushes.
- **Makefile**: `make release` now defers to `goreleaser release --skip=publish` for local dry-runs; `make release-snapshot` produces an unpublished `dist/` tree to validate the config without tagging.
- New `RELEASING.md` documents the full tag → publish procedure, required GitHub secrets (`HOMEBREW_TAP_TOKEN`), and the one-time tap-repo setup.
