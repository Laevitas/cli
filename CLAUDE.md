# CLAUDE.md

## Project Overview

Laevitas CLI is a Go command-line tool for accessing crypto derivatives market data. It wraps the Laevitas V2 REST API (`https://apiv2.laevitas.ch`) and presents futures, perpetuals, options, volatility surfaces, and prediction market data in table, JSON, or CSV format.

**This is a read-only data client.** It fetches and formats data — it does not trade, place orders, or modify any state.

## Architecture

```
main.go → cmd/root.go → cmd/{futures,perps,options,predictions,config}/ → internal/cmdutil → internal/api → internal/output
                       → cmd/interactive.go (REPL)
                       → cmd/watch.go (live-updating)
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
| `cmd/predictions/` | Polymarket prediction market subcommands |
| `cmd/config/` | Config init/show/set/path |
| `internal/api/client.go` | HTTP client — auth (`apiKey` header), retry on 429, network error wrapping |
| `internal/api/endpoints.go` | API endpoint path constants |
| `internal/cmdutil/cmdutil.go` | Shared CLI helpers — `MustClient()`, `RunAndPrint()`, `CommonFlags`, global state |
| `internal/config/config.go` | Config loading/saving, env var overrides, defaults |
| `internal/config/saved.go` | Saved queries file I/O, placeholder expansion |
| `internal/output/printer.go` | Table/JSON/CSV formatting, lipgloss styles, number formatting |
| `internal/output/chart.go` | ASCII line charts via asciigraph |
| `internal/output/colors.go` | ANSI color constants, `Errorf`/`Successf`/`Warnf` helpers |
| `internal/completer/` | Readline autocompleter with lazy catalog caching |
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
| Agent skill doc | `docs/SKILL.md` |
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
- Go 1.22. No generics used — keep it simple.
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
- Env vars override file: `LAEVITAS_API_KEY`, `LAEVITAS_BASE_URL`, `LAEVITAS_EXCHANGE`, `LAEVITAS_OUTPUT`
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
- Response envelope: `{ "data": [...], "meta": { "next_cursor": "..." } }` — auto-unwrapped by printer
- Rate limit: 429 → auto-retry with exponential backoff (2s, 4s, 8s), max 3 retries
- Auth errors (401/403): no retry, show helpful message

## Dependencies

| Dependency | Purpose |
|-----------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/chzyer/readline` | REPL readline with history |
| `github.com/charmbracelet/lipgloss` | Terminal styling (table formatting) |
| `github.com/guptarohit/asciigraph` | ASCII line charts |
| `github.com/briandowns/spinner` | Loading spinner animation |
| `golang.org/x/term` | Terminal detection, raw mode, size |
| `golang.org/x/text` | Number formatting |

## Common Mistakes to Avoid

1. **Never add `CommonFlags` to snapshot/catalog commands.** These endpoints return all records in a single response — no pagination, no `-n` limit. Only time-series endpoints (ohlcvt, carry, oi, trades, volume, ticker, etc.) use `CommonFlags`.

2. **Never use `time.Now()` for version detection in tests.** `version.go` calls git at init time. If building in a non-git context, it falls back to `"dev"`.

3. **Never forget to update all four places when adding a command:**
   - The command definition in `cmd/{group}/{group}.go`
   - The endpoint constant in `internal/api/endpoints.go`
   - The watch endpoint map in `cmd/watch.go`
   - The completer command tree in `internal/completer/completer.go`

4. **Never add a command to two cobra parents.** Cobra doesn't support it. If you need an alias, use `Aliases` on the command itself.

5. **Never skip `resetFlags()` in the REPL.** Persistent flags leak between commands. The `resetFlags()` call in `executeREPLCommand` clears them after each invocation.

6. **Never print to stdout for diagnostic/progress messages.** Use `os.Stderr`. Stdout is reserved for data output — agents and pipes depend on clean JSON/table output.

7. **Vol-surface is under options, not a top-level command.** The API paths are `/api/v1/options/vol-surface/...` and the CLI mirrors this: `laevitas options vol-surface snapshot`.

8. **The API `instrument_name` field is the canonical identifier.** Don't invent your own naming — use exactly what the catalog endpoint returns.

9. **Don't add error handling for flags Cobra already validates.** `cobra.ExactArgs(1)` handles missing arguments. `MarkFlagRequired` handles missing required flags.

10. **Version strings never include the `v` prefix internally.** Git tags use `v0.1.0`, but `version.Version` stores `0.1.0`. Display code adds the `v`.

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `LAEVITAS_API_KEY` | (empty) | API key (overrides config file) |
| `LAEVITAS_BASE_URL` | `https://apiv2.laevitas.ch` | API base URL |
| `LAEVITAS_EXCHANGE` | `deribit` | Default exchange |
| `LAEVITAS_OUTPUT` | `auto` | Default output format |

## Workflow Guidance

### Adding a new data command
1. Add endpoint constant to `internal/api/endpoints.go`
2. Add cobra command in the appropriate `cmd/{group}/{group}.go` with the standard pattern
3. Register in `init()` with `Cmd.AddCommand(newCmd)`
4. Add to watch endpoint map in `cmd/watch.go`
5. Add to completer command tree in `internal/completer/completer.go`
6. Update `docs/SKILL.md` with the new command
7. Build and test: `go build -o laevitas . && ./laevitas {group} {cmd} --help`

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
