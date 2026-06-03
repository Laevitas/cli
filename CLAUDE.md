# CLAUDE.md

## Project Overview

Laevitas CLI is a Go command-line tool for accessing crypto market data. It wraps the Laevitas V2 REST API (`https://apiv2.laevitas.ch`) and the WebSocket gateway, presenting futures, perpetuals, options, spot, volatility surfaces, prediction markets, cross-product instruments, analytics, and live streams in human-friendly and agent-friendly formats.

**This is a read-only data client.** It fetches and formats data — it does not trade, place orders, or modify any state.

## Architecture

```
main.go → cmd/root.go → cmd/{futures,perps,options,spot,predictions,instruments,analytics,wallet,config,update}/
                       → internal/cmdutil → internal/api → internal/output
                       → cmd/ws + internal/wsclient/internal/wsrender (live streaming)
                       → cmd/interactive.go (REPL)
                       → cmd/watch.go (REST polling)
                       → cmd/saved.go (saved queries)
```

### Command Flow (every data command)

1. `cmdutil.MustClient()` — load config, create API client (or reuse shared client in REPL)
2. `flags.ToParams()` — convert CLI flags to `api.RequestParams`
3. `cmdutil.RunAndPrint(client, endpoint, params)` — fetch data, print output, render chart

### Key Packages

| Package | What it does |
|---------|-------------|
| `cmd/root.go` | Root command, global flags (`-o`, `--exchange`, `--verbose`, `--no-chart`), help template, version command |
| `cmd/interactive.go` | REPL shell — readline, spinner, search, save/run/saves/unsave |
| `cmd/watch.go` | Live-updating mode — raw terminal, color-diff, status bar |
| `cmd/saved.go` | Saved query handlers with `{variable}` placeholder expansion |
| `cmd/futures/` | Dated futures subcommands (catalog, snapshot, ohlcvt, carry, oi, trades, etc.) |
| `cmd/perps/` | Perpetual swap subcommands (same structure as futures) |
| `cmd/options/` | Options subcommands + vol-surface sub-group |
| `cmd/spot/` | Spot market subcommands (catalog, snapshot, ohlcvt, ticker, volume, l2-orderbook, trades) |
| `cmd/predictions/` | Polymarket prediction market subcommands |
| `cmd/instruments/` | Cross-product instrument registry — list + detail across all exchanges/market types |
| `cmd/analytics/` | Computed analytics, currently realized volatility |
| `cmd/wallet/` | x402 wallet UX — show/init/set-key/unset/address/credits |
| `cmd/ws/` | WebSocket streaming — trades, ticker/vt, liquidations, book |
| `cmd/update/` | Self-update from GitHub Releases |
| `cmd/config/` | Config init/show/set/unset/path |
| `internal/api/client.go` | HTTP client — auth (`apiKey` header), retry on 429, network error wrapping |
| `internal/api/endpoints.go` | API endpoint path constants |
| `internal/cmdutil/cmdutil.go` | Shared CLI helpers — `MustClient()`, `RunAndPrint()`, `CommonFlags`, global state |
| `internal/cmdutil/examples.go` | Dynamic instrument-name helpers for help text (`{{FUT}}` / `{{OPT_C}}` / `{{OPT_P}}` / `{{MAT}}` token substitution) |
| `internal/config/config.go` | Config loading/saving, env var overrides, defaults |
| `internal/config/saved.go` | Saved queries file I/O, placeholder expansion |
| `internal/output/printer.go` | Table/JSON/CSV formatting, lipgloss styles, number formatting |
| `internal/output/chart.go` | ASCII line charts via asciigraph |
| `internal/output/colors.go` | ANSI color constants, `Errorf`/`Successf`/`Warnf` helpers |
| `internal/completer/` | Readline autocompleter with lazy catalog caching |
| `internal/wsclient/` | WebSocket client, JSON-RPC subscription, reconnect handling |
| `internal/wsrender/` | TUI renderers for rolling tape, book scan, and book ladder |
| `internal/x402/` | EVM signing client for x402 payments |
| `internal/version/` | Version auto-detection from git tags at runtime |

## Key File Locations

| What | Where |
|------|-------|
| Entry point | `main.go` |
| Root command + global flags | `cmd/root.go` |
| REPL implementation | `cmd/interactive.go` |
| Watch mode | `cmd/watch.go` |
| Saved query handlers | `cmd/saved.go` |
| API client + retry logic | `internal/api/client.go` |
| API endpoint constants | `internal/api/endpoints.go` |
| CLI helpers + RunAndPrint | `internal/cmdutil/cmdutil.go` |
| Config struct + load/save | `internal/config/config.go` |
| Saved queries struct | `internal/config/saved.go` |
| Table/JSON/CSV printer | `internal/output/printer.go` |
| Inline ASCII charts | `internal/output/chart.go` |
| ANSI color helpers | `internal/output/colors.go` |
| Tab-completion + catalog cache | `internal/completer/completer.go` |
| Version auto-detection | `internal/version/version.go` |
| Build config | `Makefile` |
| Agent skill (installable via `npx skills add laevitas/cli`) | `skills/laevitas-cli/SKILL.md` + `skills/laevitas-cli/reference/` |
| Agent skill (legacy pointer) | `docs/SKILL.md` (redirects to the above) |
| Go dependencies | `go.mod` |
| Config file location | `~/.config/laevitas/config.json` |
| Saved queries location | `~/.config/laevitas/saved.json` |
| REPL history location | `~/.config/laevitas/history` |

## Build & Run

```bash
go build -o laevitas .       # Dev build (version auto-detected from git)
make build                   # Production build with ldflags → bin/laevitas
make install                 # Install to $GOPATH/bin
make release                 # Cross-compile linux/darwin/windows (amd64/arm64)
make test                    # go test ./... -v
make lint                    # golangci-lint
make fmt                     # gofmt -s -w .
```

## Versioning

Version is auto-detected at runtime from `git describe --tags --always --dirty`. The leading `v` prefix is stripped so the code always stores `0.1.0` (display adds the `v`).

```bash
# Tag a release
git tag -a v0.2.0 -m "v0.2.0 — description"

# Version priority: ldflags > git tag > commit hash > "dev"
```

When built via `make build`, ldflags inject version/commit/date and take priority over runtime detection.

## Code Conventions

### Go style
- Go 1.25. Keep implementation straightforward and avoid clever abstractions.
- Cobra for CLI framework. Every subcommand follows the same pattern (see below).
- `fmt.Fprintf(os.Stderr, ...)` for user-facing messages. `output.Errorf`/`Successf`/`Warnf` for styled messages.
- Errors bubble up through cobra. `SilenceErrors: true` on rootCmd — errors printed in `Execute()`.

### Command pattern (all data commands follow this)
```go
var flags cmdutil.CommonFlags

var myCmd = &cobra.Command{
    Use:  "subcmd <instrument>",
    Args: cobra.ExactArgs(1),
    Run: func(cmd *cobra.Command, args []string) {
        client, _ := cmdutil.MustClient()
        params := flags.ToParams()
        params.InstrumentName = args[0]
        params.Exchange = cmdutil.Exchange
        cmdutil.RunAndPrint(client, api.EndpointConst, params)
    },
}

func init() {
    cmdutil.AddCommonFlags(myCmd, &flags)
    ParentCmd.AddCommand(myCmd)
}
```

### Global state in cmdutil
`cmdutil` holds shared mutable state set by root persistent flags:
- `OutputFormat`, `Exchange`, `Verbose`, `NoChart` — set in `PersistentPreRun`
- `SharedClient` — persistent API client in REPL mode
- `InteractiveMode` — true during REPL command execution (prevents `os.Exit`)
- `SpinnerInstance` — active spinner during REPL API calls

### Config
- All defaults in `internal/config/config.go`: `DefaultBaseURL`, `DefaultExchange`, `DefaultOutput`, `DefaultLimit`
- Config loaded from `~/.config/laevitas/config.json`
- Env vars override file: `LAEVITAS_API_KEY`, `LAEVITAS_WALLET_KEY`, `LAEVITAS_AUTH`, `LAEVITAS_BASE_URL`, `LAEVITAS_EXCHANGE`, `LAEVITAS_OUTPUT`
- API auth via `apiKey` header (not `Authorization`)

### Output formatting
- `output.Resolve(format)` — auto-detects: table if TTY, JSON if piped
- Table uses lipgloss for styled headers/separators
- Numbers auto-formatted: thousand separators, smart decimal places
- Signed values (funding rates, carry, basis) color-coded green/red
- Timestamps shown as relative durations (5s, 30m, 2d)
- Terminal width detection — columns truncated to fit

### REPL specifics
- Readline with persistent history file
- Autocompleter lazily fetches instrument catalogs from API, caches with `sync.RWMutex`
- Strips leading `laevitas` from input (users copy examples from help)
- Flags reset between commands via `resetFlags()` to prevent state leakage
- Spinner runs during API calls, stopped before output

### Watch mode
- Raw terminal mode to detect `q` keypress without blocking
- Compares current vs previous data — highlights changes (green up, red down)
- Hardcoded endpoint map resolves command strings to API paths
- Supports 3-level command keys (e.g., `options vol-surface snapshot`)

## API Contract

- Base URL: `https://apiv2.laevitas.ch`
- Auth: `apiKey` header
- User-Agent: `laevitas-cli/{version} (+https://github.com/laevitas/cli)`
- REST JSON envelope: success is `{ "success": true, "data": ..., "meta": {...} }`; failure is `{ "success": false, "error": {"code": "...", "message": "..."} }`
- Table and CSV output format `.data` directly; they are not enveloped.
- WebSocket output is NDJSON (`{"channel": "...", "data": {...}}` per line), not the REST envelope.
- Rate limit: 429 → auto-retry with exponential backoff (2s, 4s, 8s), max 3 retries
- Auth errors (401/403): no retry, show helpful message

## Markets vocabulary (canonical tokens)

There are three different name-spaces for market types in this codebase, all carrying the same concept. Internal code MUST use the canonical (plural) form; the boundaries to other layers translate at the edge.

| Layer | Token form | Example |
|---|---|---|
| **CLI input (what the user types)** | any alias | `perp`, `perpetual`, `perpetuals`, `swap`, `fut`, `futures`, `opt`, `options`, `spot`, `predictions`, `poly` |
| **Internal canonical** | plural | `perpetuals`, `futures`, `options`, `spot`, `predictions` |
| **REST API filter `?market_type=`** | singular | `perpetual`, `future`, `option`, `spot`, `prediction` |
| **WebSocket channel segment** | plural (= canonical) | `book.perpetuals.<venue>.<instrument>` |

**Rule**: every CLI entry point (cobra `Run`/`RunE`) calls `api.NormalizeMarket(input)` immediately. Internal code (resolvers, panels, channel builders) sees the canonical plural form only. When a REST request needs the singular form, call `api.MarketRESTToken(canonical)` at the request-build site. WS channels need no translation — canonical = WS form.

Same pattern for **margin types**:

| Layer | Token form | Example |
|---|---|---|
| **CLI input** | any alias | `linear`, `lin`, `usdt`, `usdc`, `stable`, `inverse`, `inv`, `coin`, `crypto` |
| **Internal canonical / REST filter** | one word | `linear`, `inverse` |

`api.NormalizeMargin(input)` returns the canonical form; pass it directly to `params.MarginType`.

**Where the helpers live**: `internal/api/markets.go`. Adding a new market or margin type means editing the alias map there in ONE place; every CLI entry point picks up the new alias automatically.

**Why plural for canonical**: matches WS channels and what the user already types in `laevitas ws perpetuals book ...` and `laevitas dash book perpetuals BTCUSDT`. The REST API is the odd layer; we wrap it.

**Never invent a fourth token form.** If you find yourself reaching for a new shorthand (e.g. `perp-linear` as a single CLI value), stop and add it to the alias table or use existing flags.

## REST / WS feature parity

**Rule**: any flag added to one transport for a given data shape lands on every other transport surfacing the same shape, with byte-identical flag names, defaults, and semantics. Agents shouldn't have to learn two flag dialects — REST and WS speak the same language for the same data.

**The book-snapshot shape** (asks/bids arrays + tier liquidity stats + microprice) is surfaced by:

| Surface | Endpoint / channel |
|---|---|
| `laevitas perps orderbook-raw <instrument>` | `/api/v1/perpetuals/orderbook-raw` |
| `laevitas futures orderbook-raw <instrument>` | `/api/v1/futures/orderbook-raw` |
| `laevitas spot orderbook-raw <instrument>` | `/api/v1/spot/l2-orderbook-raw` |
| `laevitas predictions orderbook <instrument>` | `/api/v1/predictions/orderbook-raw` |
| `laevitas ws <market> book <pair>` | `book.{market}.{exchange}.{instrument}` |

All five wire `output.AddBookFilterFlags` (`--depth`, `--compact`) and route through `output.ApplyBookFilter` (REST) or the WS emit path's call to the same helper. **The same flag value produces the same shape on the wire regardless of transport.**

**The orderbook-stats shape** (time-series of liquidity metrics: `bid_liq_10/20/50/100_open/close/high/low/avg`, no asks/bids array) is surfaced by:

| Surface | Endpoint |
|---|---|
| `laevitas perps orderbook <instrument>` | `/api/v1/perpetuals/orderbook` |
| `laevitas futures orderbook <instrument>` | `/api/v1/futures/orderbook` |
| `laevitas spot orderbook <instrument>` | `/api/v1/spot/l2-orderbook` |

`--depth N` on these picks which tier's columns surface in the compact table view (10/20/50/100 — the four tiers the API computes). `--compact` is reserved for future "drop OHLC fan-out, keep close" semantics — same flag bundle so the registration stays uniform.

**Defaults that adapt to audience, not transport**:

| Output mode | Audience | `--depth` default |
|---|---|---|
| `-o table` (TTY, human) | human | top-20 each side (display cap; full data still on wire) |
| `-o json` | agent | full wire payload (typically 100 each side) |
| `-o csv` | agent | full wire payload |
| WS NDJSON to stdout | agent | full wire payload |

The display cap is purely a render-time concern in `internal/output/book_table.go`; the wire payload is never silently trimmed for agents. An agent piping `... -o json` always gets what's on the wire.

**Where the shared helpers live**:

- `internal/output/book_filter.go` — `BookFilterFlags`, `AddBookFilterFlags`, `ApplyBookFilter`, `IsAllowedDepthTier`. Single registration point.
- `internal/cmdutil/cmdutil.go` — `RunAndPrintFiltered` threads the filter through every REST command. Mirrors the WS emit path's call to `ApplyBookFilter`, so REST and WS produce identical post-filter payloads.
- `internal/output/book_table.go` — inline ladder renderer for the human-facing table. Reads `Printer.StatsTier` to decide whether to apply the display-time cap.
- `internal/ladder/ladder.go` — `NextDepthTier` / `PrevDepthTier` cycle for the TUI keybindings (10 → 20 → 50 → 100). Same tier set the API exposes; same set `--depth N` accepts on the stats shape.

**Adding a new flag for an existing shape**: add it to the appropriate flag bundle in `internal/output`, wire it into `ApplyBookFilter` (snapshot) or the printer's table renderer (stats). Every command using that shape picks it up automatically. Don't add the flag to one surface only — that's how parity drifts.

**Adding a new shape**: build a new flag bundle in `internal/output` (e.g. `OptionsFilterFlags` if a future options-snapshot endpoint emerges) and a new `RunAndPrintWith…Filter` variant in `cmdutil` if needed. Don't extend `BookFilterFlags` to mean different things on different shapes.

## Dependencies

| Dependency | Purpose |
|-----------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/chzyer/readline` | REPL readline with history |
| `github.com/charmbracelet/lipgloss` | Terminal styling (table formatting) |
| `github.com/charmbracelet/bubbletea` | Live WebSocket TUI rendering |
| `github.com/coder/websocket` | WebSocket client |
| `github.com/coinbase/x402/go` | x402 payment support |
| `github.com/guptarohit/asciigraph` | ASCII line charts |
| `github.com/briandowns/spinner` | Loading spinner animation |
| `golang.org/x/term` | Terminal detection, raw mode, size |
| `golang.org/x/text` | Number formatting |

## Common Mistakes to Avoid

1. **Snapshot endpoints don't paginate. Catalog endpoints do.** Snapshots return a complete point-in-time view in a single response — never add `CommonFlags` to a `snapshot` command. Catalogs are paginated (`limit`, `cursor`) and accept per-product filters (`maturity`, `strike-min`/`strike-max`, `option-type`, `quote-currency`, etc.); they DO use `CommonFlags`. When adding a catalog command, also strip the time-window fields after `ToParams()` because catalog has no time-series:

   ```go
   params := flags.CommonFlags.ToParams()
   params.Start = ""
   params.End = ""
   params.Resolution = ""
   params.SortDir = ""
   ```

2. **Time-series default sort is `DESC` (newest first).** `CommonFlags.ToParams()` sets `SortDir = "DESC"` when the user passed neither `--sort-dir` nor `--cursor`. Don't override this in command Run funcs unless you have a reason. When `--cursor` is present, the CLI deliberately doesn't inject a default direction so paginated scans keep their starting direction.

3. **Charts must be passed chronologically.** `RunAndPrint` reverses the JSON array before passing it to `RenderChart` whenever `params.SortDir == "DESC"`. If you write a new chart-rendering path, mirror this — charts are visually meaningless if drawn in reverse-time.

4. **Instrument names in `--help` text use tokens, not literal dates.** Use `{{FUT}}` (e.g. `BTC-26JUN26`), `{{OPT_C}}` / `{{OPT_P}}` (e.g. `BTC-26JUN26-100000-C`), and `{{MAT}}` (e.g. `26JUN26`) in cobra `Long` and `Example` fields. `cmdutil.SubstituteExamplesRecursive(rootCmd)` is called once from `cmd/root.go init()` and replaces all tokens at startup. For flag descriptions (which aren't covered by the recursive walk), use `cmdutil.ExampleMaturity()` directly: `"Filter by maturity (e.g. "+cmdutil.ExampleMaturity()+")"`.

5. **Never use `time.Now()` for version detection in tests.** `version.go` calls git at init time. If building in a non-git context, it falls back to `"dev"`.

6. **Never forget to update all five places when adding a command:**
   - The command definition in `cmd/{group}/{group}.go`
   - The endpoint constant in `internal/api/endpoints.go`
   - The watch endpoint map in `cmd/watch.go`
   - The completer command tree in `internal/completer/completer.go`
   - The catalog endpoint map in `internal/completer/completer.go` (if the new group has a catalog used for instrument autocompletion)

7. **Never add a command to two cobra parents.** Cobra doesn't support it. If you need an alias, use `Aliases` on the command itself.

8. **Never skip `resetFlags()` in the REPL.** Persistent flags leak between commands. The `resetFlags()` call in `executeREPLCommand` clears them after each invocation.

9. **Never print to stdout for diagnostic/progress messages.** Use `os.Stderr`. Stdout is reserved for data output — agents and pipes depend on clean JSON/table output.

10. **Vol-surface is under options, not a top-level command.** The API paths are `/api/v1/options/vol-surface/...` and the CLI mirrors this: `laevitas options vol-surface snapshot`.

11. **Spot defaults to `binance`, not `deribit`.** Deribit doesn't trade spot. `cmd/spot/spot.go` has a `spotExchange()` helper that falls back to `binance` when the global `cmdutil.Exchange` is `deribit`. Use it on every spot command.

12. **WebSocket docs and errors use `laevitas`, not `lvt`.** Do not introduce `lvt` examples unless the binary/alias is actually shipped.

13. **The API `instrument_name` field is the canonical identifier.** Don't invent your own naming — use exactly what the catalog endpoint returns.

14. **Don't add error handling for flags Cobra already validates.** `cobra.ExactArgs(1)` handles missing arguments. `MarkFlagRequired` handles missing required flags.

15. **Version strings never include the `v` prefix internally.** Git tags use `v0.1.0`, but `version.Version` stores `0.1.0`. Display code adds the `v`.

16. **`--sort-dir` is registered on every time-series command via `AddCommonFlags`.** Don't re-declare it on individual commands or Cobra panics with "flag redefined: sort-dir". Trades/liquidations used to declare their own; they no longer do.

17. **Never store unnormalised market or margin tokens internally.** Every CLI `Run` func that accepts a market or margin type MUST call `api.NormalizeMarket` / `api.NormalizeMargin` on user input before storing it. Internal code assumes canonical form (plural for markets, lowercase one-word for margins). See "Markets vocabulary" above. Three different forms across CLI / WS / REST is a real pre-existing inconsistency — the normaliser is the chokepoint that hides it from the rest of the codebase.

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `LAEVITAS_API_KEY` | (empty) | API key (overrides config file) |
| `LAEVITAS_WALLET_KEY` | (empty) | Hex EVM private key for x402 REST payments |
| `LAEVITAS_AUTH` | `auto` | Auth mode: `auto`, `api-key`, `x402` |
| `LAEVITAS_BASE_URL` | `https://apiv2.laevitas.ch` | API base URL |
| `LAEVITAS_EXCHANGE` | `deribit` | Default exchange |
| `LAEVITAS_OUTPUT` | `auto` | Default output format |

## Workflow Guidance

### Adding a new data command
1. Add endpoint constant to `internal/api/endpoints.go`
2. Add cobra command in the appropriate `cmd/{group}/{group}.go` with the standard pattern. Use `{{FUT}}` / `{{OPT_C}}` / `{{OPT_P}}` / `{{MAT}}` tokens in `Example` and `Long` fields instead of literal expiry dates.
3. Register in `init()` with `Cmd.AddCommand(newCmd)`
4. Add to watch endpoint map in `cmd/watch.go`
5. Add to completer command tree in `internal/completer/completer.go`
6. If the new command requires a query field that's not on `RequestParams`, add it AND wire it through `buildURL` in `internal/api/client.go`.
7. Update the agent skill (`skills/laevitas-cli/SKILL.md` and the relevant file under `skills/laevitas-cli/reference/`) and `README.md` with the new command
8. Build and test: `go build -o laevitas . && ./laevitas {group} {cmd} --help`

### Adding a new product group (like spot or instruments)
1. Steps 1-7 above for each subcommand.
2. Create `cmd/{group}/{group}.go` with a top-level `Cmd` variable and per-subcommand `init()` registration.
3. Register the group in `cmd/root.go`: import + `rootCmd.AddCommand({group}.Cmd)`.
4. Add the group to `topLevelCommands` and `commandTree` in `internal/completer/completer.go`.
5. If the group has a `catalog` endpoint used for instrument autocompletion, add it to `catalogEndpoints` in `internal/completer/completer.go`.

### Testing changes
```bash
go build -o laevitas .                              # Build
./laevitas version                                  # Verify version
./laevitas futures catalog                          # Test a command
./laevitas futures snapshot --currency BTC -o json  # Test JSON output
./laevitas --help                                   # Verify help template
```

No test suite exists yet — verify manually against the live API.

### After corrections
- Update this file so the same mistake doesn't happen twice.
- Write rules that prevent the pattern, not just document the incident.

### Shipping a release

The canonical public procedure is `RELEASING.md`; the executable agent checklist is `.claude/skills/release/SKILL.md`. Keep this section, `RELEASING.md`, and the release skill aligned when the release flow changes.

The canonical release flow. **Every release follows these steps in this order**, no shortcuts. The `/release` skill in `.claude/skills/release/` enforces it.

**Pre-flight (in any order):**
1. CHANGELOG.md has a `## [X.Y.Z] — YYYY-MM-DD` entry at the top.
2. README.md and the agent skill (`skills/laevitas-cli/SKILL.md` + `skills/laevitas-cli/reference/`) mention every new command/flag/field.
3. `go build -o laevitas-test.exe .` is clean (no errors, no `dirty` other than the pending changes).
4. Smoke tests pass — at minimum: top-level `--help` lists new groups, JSON envelope on a representative command parses cleanly, error envelope on a forced auth failure carries the expected error code.

**Branch + commit + push:**
5. `git checkout -b release/vX.Y.Z` from latest `main`. Resolve any CHANGELOG conflicts with main → keep both blocks ordered newest-first.
6. `git add` all the changed files explicitly (never `git add -A` — gitignored secrets like `.env` must not slip in).
7. **Single commit** with a descriptive message. Multi-paragraph body summarising BREAKING / ADDED / CHANGED / FIXED / DEFERRED sections. Include `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`.
8. `git push -u origin release/vX.Y.Z`.

**PR + merge (USER does this in GitHub):**
9. Open PR at `https://github.com/Laevitas/cli/pull/new/release/vX.Y.Z`. Title matches the release theme. Body points to CHANGELOG entry.
10. CI runs (test + vet on the branch). Wait for green.
11. Merge to `main`. Squash optional; merge commit fine since the release branch had a single commit anyway.

**Tag + ship (Claude does this once main is updated):**
12. `git checkout main && git pull` — verify HEAD is the merge commit and `go.mod`/CHANGELOG reflect the release.
13. `git tag -a vX.Y.Z -m "vX.Y.Z"`.
14. `git push origin vX.Y.Z` — fires the GoReleaser pipeline at `.github/workflows/release.yml`.

**Pipeline auto-runs (~3-5 min):**
- Cross-compile 6 binaries (linux/darwin/windows × amd64/arm64).
- Generate `checksums.txt` (SHA-256).
- Create the GitHub Release with archives + checksums attached.
- Push a Cask formula to `Laevitas/homebrew-cli`.
- Push a Scoop manifest to `Laevitas/scoop-bucket`.

**Post-deploy verification:**
15. `https://github.com/Laevitas/cli/actions` — Release workflow green.
16. `https://github.com/Laevitas/cli/releases/tag/vX.Y.Z` — 6 archives present.
17. `https://github.com/Laevitas/homebrew-cli/blob/main/Casks/laevitas.rb` — `version "X.Y.Z"`.
18. End-to-end: `brew update && brew upgrade laevitas/cli/laevitas && laevitas version` — prints `vX.Y.Z`.

**One-time setup (already done, here for reference):**
- `Laevitas/homebrew-cli` and `Laevitas/scoop-bucket` repos exist (empty when created).
- `HOMEBREW_TAP_TOKEN` secret in `Laevitas/cli` repo settings — fine-grained PAT with Contents:write on both tap repos.
- See [RELEASING.md](RELEASING.md) for full one-time setup.

**Common failure modes:**
- **Pipeline fails at Homebrew step**: `HOMEBREW_TAP_TOKEN` expired or scope changed. Regenerate, update secret, re-run workflow.
- **Pipeline succeeds but `brew upgrade` doesn't see the new version**: Homebrew tap repo didn't get the formula push. Check `https://github.com/Laevitas/homebrew-cli/commits/main` for a commit with the version. If absent, secret is the issue.
- **`laevitas update` 404s on hosts ≤v0.4.0**: those binaries had a broken self-updater. One-time manual install needed: `curl -fsSL https://cli.laevitas.ch/install.sh | sh`. v0.5.2+ self-updater works for all future releases.

**Never do:**
- Tag from a feature branch. Always tag the merge commit on `main`.
- Tag without merging first. Pipeline runs against the tagged commit, not your local branch.
- Push a tag matching an existing one. If you need to redo, bump the patch version (e.g. v0.7.0 → v0.7.1) — never force-delete a published tag.
- Skip the CHANGELOG conflict resolution. Letting both branches land independently produces a broken main.

### Memory and skill enforcement

This shipping flow is also recorded in:
- `~/.claude/projects/c--Users-hnaas-OneDrive-Documents-GitHub-laevitas-cli/memory/release_shipping_flow.md` — Claude's persistent memory across sessions.
- `.claude/skills/release/SKILL.md` — invokable as `/release` to walk through the flow with confirmation gates.

Update all three (this section, the memory file, the skill) when the flow changes.
