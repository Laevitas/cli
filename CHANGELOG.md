# Changelog

All notable changes to the Laevitas CLI are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versions ≤ 0.4.0 are recorded in git tag annotations only; this file starts at 0.5.0.

## [0.6.0] — 2026-04-28

Major release. Two headline changes: a stable JSON envelope (breaking for any
consumer that parses `-o json` output), and a refreshed terminal UI tuned to
the Laevitas brand palette. Also bundles an `analytics` command group, the
`base_currency` fix on `instruments list`, and a slate of table-rendering fixes
that make every command read better.

### Changed (BREAKING — JSON consumers)

- **JSON output is now wrapped in a stable envelope.** Every `-o json` response
  — and the JSON path of `-o auto` when stdout is not a TTY — emits one of two
  shapes:
  - **Success:** `{"success": true, "data": [...], "meta": {...}}`
  - **Failure:** `{"success": false, "error": {"message": "...", "code": "...", "status": 401, "endpoint": "..."}}`
- Existing scripts that read fields from the bare data array must update to
  read from `.data` instead. E.g. `jq '.[0].mark_price'` becomes
  `jq '.data[0].mark_price'`.
- Errors now write to **stdout as JSON** (instead of stderr free-text) when
  output is JSON. A single shape works for both success and failure parsing —
  no more switching between stdout and stderr. Exit code remains non-zero for
  any error.

### Added

- **Stable error codes** in the JSON error envelope. Branch on `.error.code`,
  not `.error.message`:
  - `AUTH_INVALID` (401)
  - `AUTH_FORBIDDEN` (403)
  - `RATE_LIMITED` (429)
  - `PAYMENT_REQUIRED` (402)
  - `BAD_REQUEST` (4xx other than the above)
  - `NOT_FOUND` (404)
  - `SERVER_ERROR` (5xx)
  - `NETWORK_ERROR` (DNS / TCP / timeout)
  - `UNKNOWN_ERROR` (fallback)
- **`analytics` command group** with `realized-volatility` (alias `rv`). Wraps
  the `/api/v1/analytics/realized-volatility` endpoint. Supports both modes:
  - **Snapshot mode** (no `-p`/`--start`/`--end`/`-n`/`--cursor`): latest reading
    per `(estimator, frequency, window_days)` combination.
  - **Historical mode** (any time-window flag set): paginated time-series.
  - Flags: `--instrument` (required), `--window-days {7,30,60,90,180,365}`,
    `--estimator {close_to_close,parkinson,garman_klass}`,
    `--frequency {daily,hourly}`, `--currency`, `--date` (snapshot mode only).
- New `RequestParams` fields: `Frequency`, `WindowDays`, `Estimator`,
  `BaseCurrency`. Wired through `buildURL`.

### Changed (UI — terminal output)

- **Branded REPL banner.** Replaced the six-line ASCII block-letter banner with
  a single line: `▲ LAEVITAS  v0.6.0` in brand green, followed by a dim tagline.
  Truecolor uses the exact brand palette (`#46be52` primary, `#475057` mid-grey).
- **Branded REPL prompt.** Old `LAEVITAS > ` becomes `▲ ›` — short, branded,
  ergonomic. Uses 8-color ANSI in the prompt itself (truecolor would crash
  `chzyer/readline`'s Windows ANSI shim); banner above it uses full truecolor.
- **Removed cosmetic dot-separator rows** that interrupted table output every
  five rows. They were purely decorative and broke visual flow.
- **Column order now matches the API response.** Replaced the static
  `columnPriorities` map (130 lines, hand-tuned, drift-prone) with a JSON-byte
  walk that recovers the API's source order. Columns now arrive grouped by
  base name (e.g. `funding_rate_*` together, `basis_*` together) instead of
  scattered by suffix priority.

### Fixed

- **`instruments list --base-currency` actually filters now.** The CLI was
  sending `?currency=BTC` to the registry endpoint, which silently ignored it
  (the registry expects `?base_currency`). Result: filtering by BTC was
  returning rows for ADA, AVAX, etc. — every contract alphabetically. New
  `BaseCurrency` field on `RequestParams` is wired separately from `Currency`
  so product catalogs and the cross-product registry coexist.
- **`options trades --top-n` now works.** The flag was registered on
  `options flow` but missing from `options trades`, even though the API
  documents `top_n` for options trades and every other product's `trades`
  command (futures/perps/spot) has it. Now consistent across all four.
- **`predictions snapshot --keyword` and `--instrument` now work.** The API
  accepts both for filtering snapshot responses, but the CLI only registered
  `--category`, `--event`, `--date`, `--resolution`. Calling with `--keyword`
  failed with `unknown flag` even though the same flag was accepted on
  `predictions catalog`. Both flags now registered and forwarded.
- **`predictions` commands now default `exchange=polymarket`.** The global
  `--exchange` flag defaults to `deribit` for the rest of the CLI, and that
  value was leaking into every predictions request as `?exchange=deribit` —
  meaningless on a Polymarket-only endpoint. New `predictionsExchange()`
  helper in `cmd/predictions/predictions.go` mirrors the pattern from
  `cmd/spot/spot.go` (where deribit isn't valid either) and pins the value
  to `polymarket` unless the user explicitly chose a known prediction-market
  exchange.

### Coordinated with API v1.16.0

`/api/v1/instruments` and `/api/v1/instruments/detail` were returning every
historical snapshot of each contract because the underlying ClickHouse table
is append-only and the service did no dedup. Reported during this release
cycle, fixed server-side in the same window. Post-API-deploy:

- `instruments list` returns one row per `(exchange, instrument_name)` —
  `--limit 20` now means "20 distinct contracts," matching the contract the
  flag implies.
- `meta.count` reports distinct contracts (`uniqExact` over the dedup tuple)
  rather than raw rows, so pagination decisions are trustworthy.
- `instruments detail` deterministically returns the latest snapshot rather
  than whichever row ClickHouse happened to surface first.

No CLI changes needed for the dedup fix — the client just consumes whatever
shape the API returns. The combination of the v0.6.0 CLI `base_currency`
wiring fix above and the v1.16.0 API dedup fix is what makes
`instruments list --base-currency BTC` actually do what it says.

### Removed

- `columnPriorities` map and `columnWeight` function in
  `internal/output/printer.go`. 130 lines of dead code; no callers remain
  after the API-order migration.

### Unchanged

- **Table output (`-o table`)** is not enveloped — table is row-oriented and
  the envelope is JSON-only. Stderr metadata footer, inline charts, and the
  pagination hint are all untouched.
- **CSV output (`-o csv`)** is not enveloped for the same reason.
- The `-o` flag itself: still `auto | json | table | csv`. No new aliases like
  `--table` / `--pretty` / `--json`.

### Migration notes

JSON parsers need a one-line update — read fields under `.data` instead of
from the top-level array:

```sh
# before v0.6.0
laevitas perps carry BTC-PERPETUAL -o json | jq '.[0].funding_rate_close'

# v0.6.0 onward
laevitas perps carry BTC-PERPETUAL -o json | jq '.data[0].funding_rate_close'
```

Error handling becomes simpler:

```sh
RESP=$(laevitas futures snapshot --currency BTC -o json)
if [ "$(echo "$RESP" | jq -r '.success')" = "true" ]; then
  echo "$RESP" | jq '.data'
else
  echo "$RESP" | jq -r '.error.code, .error.message'
fi
```

### Internal

- New file `internal/cmdutil/examples.go` (added in 0.5.0; mentioned here
  because token substitution still drives help text).
- New `cmd/analytics/analytics.go`.
- New endpoint constant `AnalyticsRealizedVolatility` in
  `internal/api/endpoints.go`.
- `cmd/root.go`, `cmd/watch.go` endpoint map, and
  `internal/completer/completer.go` command tree extended for the analytics
  group.
- New helpers `wrapSuccessEnvelope`, `buildMeta`, and `printErrorEnvelope` in
  `internal/cmdutil/cmdutil.go`. New `APIError.Code()` method and
  `NetworkError.Code()` method in `internal/api/client.go`.
- `internal/output/printer.go` now extracts column order from raw JSON via a
  one-pass token walk (`extractFirstObjectKeyOrder`, `readObjectKeys`,
  `skipJSONValue`, `collectKeys`).

## [0.5.2] — 2026-04-28

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
</content>
</invoke>