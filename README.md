# Laevitas CLI

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)]()
[![Twitter](https://img.shields.io/twitter/follow/laevitas1?style=social)](https://twitter.com/laevitas1)
[![Discord](https://img.shields.io/discord/laevitas?color=5865F2&logo=discord&logoColor=white)](https://discord.com/invite/yaXc4EFFay)

> **Derivatives Data Without The Spread** — in your terminal.

Human-friendly and agent-native market data for futures, perpetuals, options, spot, prediction markets, cross-product instruments, analytics, and live WebSocket streams.

REST commands return tables for humans and stable JSON envelopes for scripts and LLMs. Streaming commands emit live TUI views in a terminal and NDJSON when piped.

```
$ laevitas futures snapshot --currency BTC -o table

instrument_name  mark_price  index_price  open_interest  volume_usd_24h  days_to_expiry  annualized_carry
─────────────────────────────────────────────────────────────────────────────────────────────────────────
BTC-28MAR25      97,450.20   96,800.00    12,450.5       245,000,000     31              8.12%
BTC-27JUN25      99,100.00   96,800.00    8,230.1        89,000,000      122             6.94%
BTC-26SEP25      101,200.00  96,800.00    3,100.8        32,000,000      213             7.81%
```

## Install

```bash
# Homebrew (macOS / Linux)
brew install laevitas/cli/laevitas

# One-line installer (macOS / Linux)
curl -fsSL https://raw.githubusercontent.com/laevitas/cli/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/laevitas/cli/main/install.ps1 | iex

# Scoop (Windows)
scoop bucket add laevitas https://github.com/laevitas/scoop-bucket
scoop install laevitas

# Go (from source)
go install github.com/laevitas/cli@latest
```

Or grab a pre-built binary from the [latest release](https://github.com/laevitas/cli/releases/latest).

## Quick Start

```bash
# 1. Configure authentication
laevitas config init

# 2. Explore available instruments
laevitas futures catalog
laevitas perps catalog --exchange binance
laevitas options catalog
laevitas spot catalog --exchange binance
laevitas instruments list --market-type perpetual --base-currency BTC

# 3. Fetch REST data
laevitas futures snapshot --currency BTC
laevitas perps carry BTC-PERPETUAL -r 1d -n 30
laevitas options flow --currency BTC --min-premium 5000
laevitas options vol-surface snapshot --currency BTC
laevitas spot ohlcvt BTCUSDT -p 24h -r 1h
laevitas predictions catalog --keyword bitcoin

# 4. Stream live data
laevitas ws perpetuals trades binance:BTCUSDT
laevitas ws perpetuals book "*:BTCUSDT"
```

## Commands

| Command | Description |
|---------|-------------|
| `futures` | Dated futures — catalog, snapshot, OHLCVT, OI, carry, trades, volume, L1/L2, **orderbook-raw**, ticker |
| `perps` | Perpetual swaps — catalog, snapshot, OHLCVT, OI, **carry**, trades, volume, L1/L2, **orderbook-raw**, ticker |
| `options` | Options — catalog, snapshot, OHLCVT, OI, **flow**, **trades**, **volatility**, L1, ticker, **vol-surface** |
| `spot` | Spot markets — catalog, snapshot, OHLCVT, ticker, volume, L1/L2, **orderbook-raw**, trades |
| `predictions` | Prediction markets — catalog, categories, snapshot, OHLCVT, trades, ticker, **orderbook** (raw L2) |
| `instruments` | Cross-product instrument registry — `list` + `detail` across all exchanges |
| `analytics` | Computed cross-asset analytics — realized volatility |
| `ws` | Live WebSocket streams — trades, OHLC ticker, OHLC vt, liquidations, **L2 book**, with `*` wildcards |
| `dash` | Multi-pane TUI dashboards — **`dash book`** aggregated multi-venue order book |
| `wallet` | x402 wallet — show, init, set-key, address, credits |
| `config` | Configuration — init, show, set |
| `watch` | Re-run REST commands at an interval with live-updating table output |
| `update` | Self-update from the latest GitHub Release |
| `version` | Print version and build information |

### Market type aliases

Wherever the CLI takes a market type (`--market-type`, `laevitas ws <market> ...`, `laevitas dash book <market> ...`), any common alias works — the CLI normalises to the canonical form internally. Type whichever feels natural:

| Canonical | Aliases |
|-----------|---------|
| `perpetuals` | `perp`, `perps`, `perpetual`, `swap`, `swaps` |
| `futures` | `fut`, `future`, `dated` |
| `options` | `opt`, `opts`, `option` |
| `spot` | `spot` |
| `predictions` | `prediction`, `predict`, `poly`, `polymarket` |

Margin type the same way (`--margin-type`, `--margin`):

| Canonical | Aliases |
|-----------|---------|
| `linear` | `lin`, `usdt`, `usdc`, `stable` |
| `inverse` | `inv`, `coin`, `coins`, `crypto` |

Examples — all equivalent:

```bash
laevitas instruments list --market-type perpetuals --margin-type linear --base-currency BTC
laevitas instruments list --market-type perp       --margin-type usdt   --base-currency BTC
laevitas instruments list --market-type swap       --margin-type lin    --base-currency BTC
```

```bash
laevitas dash book perpetuals BTCUSDT
laevitas dash book perp        BTCUSDT
laevitas dash book swap        BTCUSDT
```

### `dash book` — Multi-venue order book dashboard

Aggregates L2 depth across every venue listing the symbol into one TUI:

- **Aggregated ladder** (default) — consolidated centre-price ladder, bars
  segmented by per-venue contribution, cumulative liquidity columns either
  side, sparkline of recent microprice next to MID.
- **Split ladder** (toggle with `m`) — one narrow per-venue column
  side-by-side, useful for "which venue has a wall here?" comparisons.
- **Venue strip** (right) — per-venue BBO/spread/imbalance cards bordered
  in venue brand colour, plus a CONSOLIDATED summary card with ARB
  detection on crossed cross-venue books.

Two ways to call it:

```bash
# Currency mode (recommended) — resolves per-venue contracts via the
# instruments registry: BTCUSDT on binance, BTC-USDT-SWAP on okx,
# BTC-USD on hyperliquid, etc. Default per-venue quote cascade
# USDT → USDC → USD.
laevitas dash book perpetuals BTC --margin linear
laevitas dash book perpetuals BTC --margin inverse
laevitas dash book perpetuals ETH --margin linear --quote USDC
laevitas dash book spot BTC

# Literal mode (legacy) — exact symbol, every venue that names it
# this way contributes.
laevitas dash book perpetuals BTCUSDT
```

Markets supported: `perpetuals`, `futures`, `spot`, `predictions`. Options
has no L2 data on the streaming gateway and is rejected.

#### Keys

| Key | Action |
|-----|--------|
| `+` / `-` | Cycle price grouping (`tick → 0.01 → 0.05 → 0.10 → … → 50`) |
| `d` | Cycle stats depth tier (10 → 20 → 50) |
| `c` | Recenter viewport on the spread |
| `m` | Toggle aggregated ↔ split ladder mode |
| `v` | Open venue picker (hide/show specific venues) |
| `j` / `k` / `↓` / `↑` | Scroll one row |
| `PgUp` / `PgDn` | Scroll one page |
| `g` / `G` | Jump to top / bottom |
| `p` | Pause (freeze the visible state) |
| `?` / `h` | Help overlay |
| `q` / `Esc` | Quit |

The ladder shares its layout primitives (`internal/ladder` and
`internal/output/layout.go`) with `laevitas ws book` — both surfaces emit
the same top-line header and stats line, so muscle memory carries.

### Global Flags

```
-o, --output    Output format: auto, json, table, csv (default: auto)
    --exchange  Exchange filter/override. Valid values depend on market.
    --no-chart  Disable inline charts for table time-series output
    --wide      Disable column truncation in tables
    --width     Override terminal width for table formatting
    --verbose   Show redacted HTTP request/response diagnostics
    --version   Print version
    --help      Print help
```

### Time-Series Flags

Apply to time-series commands (`ohlcvt`, `oi`, `carry`, `trades`, `volume`, `level1`, `orderbook`, `ticker`, `ref-price`, `volatility`, `liquidations`, etc.):

```
-p, --period      Lookback period: 1h, 6h, 24h, 3d, 7d, 30d (default 7d)
    --start       Start datetime (ISO 8601)
    --end         End datetime (ISO 8601)
-r, --resolution  Candle resolution: 1m, 5m, 15m, 1h, 4h, 1d
-n, --limit       Number of records (1-1000)
    --cursor      Pagination cursor
    --currency    Base currency filter (BTC, ETH)
    --sort-dir    ASC or DESC (default DESC — newest first)
```

**Default sort: newest-first.** Row 0 of any time-series response is the most recent record in the window. Pass `--sort-dir ASC` to flip to chronological order. When you pass `--cursor` to continue a paginated scan, the CLI does not inject a default direction — the scan keeps the direction it started with.

Inline charts always render chronologically (left-to-right in time) regardless of sort direction.

### Catalog Flags (paginated)

`catalog` commands list available instruments and accept pagination plus per-product filters:

```
    --exchange         Filter by exchange (market-dependent)
    --currency         Filter by base currency (BTC, ETH, SOL)
    --maturity         Filter by expiry (e.g. 26JUN26)            [futures, perps, options]
    --option-type      Filter: C (call) or P (put)                 [options]
    --strike-min       Min strike price                            [options]
    --strike-max       Max strike price                            [options]
    --quote-currency   Filter by quote currency (USDT, USDC, USD)  [spot]
    --category         Filter by market category                   [predictions]
    --event            Filter by event slug                        [predictions]
    --keyword          Keyword search                              [predictions]
-n, --limit            Max records (1-1000)
    --cursor           Pagination cursor from previous response
```

### Snapshot Flags

`snapshot` commands return a complete point-in-time view of all matching instruments — they do **not** accept `-n`/`--limit`, `--start`/`--end`, `-r`/`--resolution`, `-p`/`--period`, or `--cursor`. Filter the response with the flags below, or trim downstream with `jq` / `head`.

```
    --currency        Filter by currency: BTC, ETH
    --quote-currency  Filter by quote currency: USDT, USDC, USD     [spot only]
    --date            Snapshot datetime in ISO 8601 (defaults to now)
    --exchange        Exchange override (market-dependent)
```

```bash
# Wrong — snapshot does not paginate
laevitas futures snapshot --currency BTC -n 3      # ✗ unknown flag

# Right — fetch the full snapshot, trim downstream
laevitas futures snapshot --currency BTC -o json | jq '.data[:3]'
```

## Output Contract

The CLI auto-detects output:

- **Interactive terminal** → colored table format
- **Piped/redirected** → JSON (machine-readable)

Override with `-o json`, `-o table`, or `-o csv`.

```bash
# Human-readable
laevitas perps carry BTC-PERPETUAL

# Machine-readable (piped)  — note: data lives under .data
laevitas perps carry BTC-PERPETUAL | jq '.data[0].funding_rate_close'

# Explicit JSON
laevitas perps carry BTC-PERPETUAL -o json

# CSV for spreadsheets
laevitas perps carry BTC-PERPETUAL -o csv > funding.csv
```

### REST JSON Envelope

REST commands using `-o json` are wrapped in a stable envelope so agents can parse one shape for both success and failure:

```json
// success
{
  "success": true,
  "data": [ ... ],
  "meta": {
    "next_cursor": "...",
    "count": 100,
    "auth": "api-key",
    "credits_remaining": 950,
    "latency_ms": 247
  }
}

// failure (printed to stdout, exit code is non-zero)
{
  "success": false,
  "error": {
    "message": "API key invalid or expired...",
    "code": "AUTH_INVALID",
    "status": 401,
    "endpoint": "/api/v1/perpetuals/carry"
  }
}
```

**Stable error codes for branching:** `AUTH_INVALID`, `AUTH_FORBIDDEN`, `RATE_LIMITED`, `PAYMENT_REQUIRED`, `WALLET_NOT_CONFIGURED`, `INSUFFICIENT_BALANCE`, `PAYMENT_REJECTED`, `BAD_REQUEST`, `NOT_FOUND`, `SERVER_ERROR`, `NETWORK_ERROR`, `UNKNOWN_ERROR`. Use `.error.code` rather than `.error.message` (which is human-readable and may change).

`-o table` and `-o csv` are not enveloped — they format `.data` directly. The envelope is JSON-only.

### WebSocket NDJSON

`laevitas ws ...` is a streaming command, not a REST request. In a terminal it renders a live TUI. When piped or forced with `-o json`, it emits newline-delimited JSON:

```json
{"channel":"trades.perpetuals.binance.BTCUSDT","data":{...}}
```

Read one object per line. Reconnect warnings and slow-consumer warnings are diagnostic output, not market data.

## Agent Integration

The CLI is designed to be used by AI agents (Claude, GPT, Codex, etc.) as a native tool.

### Why agents love CLIs

- **No SDK needed** — any agent with terminal access can use it
- **Structured REST output** — `-o json` returns a stable `{success,data,meta}` envelope
- **Streaming output** — `ws` emits NDJSON when piped
- **Composable** — pipe, filter, combine with `jq`, `awk`, other CLIs
- **Discoverable** — `--help` on every command, plus `laevitas commands -o json` for a full machine-readable manifest
- **Deterministic** — same input → same output
- **Self-diagnosing** — `laevitas doctor` validates auth, config, API reachability, and wallet state in one shot

### Introspection (v0.9.0+)

```bash
# Full command manifest — every path, flag, arg, example
laevitas commands -o json

# Narrow by substring
laevitas commands --filter ws

# Find streaming-only commands
laevitas commands -o json | jq '.data.commands[] | select(.streaming) | .path'

# Health check — pass/warn/fail/skipped per check
laevitas doctor
laevitas doctor -o json | jq '.data.summary'
```

### Agent examples

```bash
# "What's the current BTC futures term structure?"
laevitas futures snapshot --currency BTC -o json | jq '[.data[] | {instrument: .instrument_name, basis: .mark_price - .index_price, days: .days_to_expiry, carry: .annualized_carry}]'

# "Is funding positive or negative right now?"
laevitas perps carry BTC-PERPETUAL -o json -n 1 | jq '.data[0].funding_rate_close'

# "Show me the biggest BTC options trades today"
laevitas options trades --currency BTC --sort premium_usd --sort-dir DESC -n 10 -o json

# "What does the vol surface look like?"
laevitas options vol-surface snapshot --currency BTC -o json | jq '[.data[] | {maturity, atm_iv, skew_25d}]'

# "What's the probability of Bitcoin reaching 250k?"
laevitas predictions ohlcvt will-bitcoin-reach-250000-YES -o json -n 1 | jq '.data[0].close'

# Combined pipeline: find the highest-carry future and get its order book
BEST=$(laevitas futures snapshot --currency BTC -o json | jq -r '.data | sort_by(.annualized_carry) | last | .instrument_name')
laevitas futures orderbook "$BEST" -o json

# Historical orderbook metrics: table is compact, JSON/CSV keep every metric column
laevitas perps orderbook BTCUSDT --exchange binance -p 1h -r 1m
laevitas perps orderbook BTCUSDT --exchange binance -p 1h -r 1m -o json

# Spot reference price for derivatives basis calculation
laevitas spot ohlcvt BTCUSDT -p 24h -o json -n 1 | jq '.data[0].close'

# Browse all active BTC perpetuals across exchanges
laevitas instruments list --market-type perpetual --base-currency BTC -o json

# Full contract specification for a single instrument
laevitas instruments detail BTC-PERPETUAL --exchange deribit -o json

# Realized volatility (snapshot — latest reading)
laevitas analytics rv --instrument BTC-PERPETUAL --window-days 30 -o json

# Realized volatility (historical time-series, 30d daily)
laevitas analytics rv --instrument BTC-PERPETUAL --window-days 30 -p 30d -r 1d -o json

# Live perpetuals trades on Binance — NDJSON stream, one event per line
laevitas ws perpetuals trades binance:BTCUSDT

# OHLC ticker stream for two options at once, 5m candles
laevitas ws options ticker deribit:BTC-30JAN26-100000-C,deribit:BTC-30JAN26-110000-C --tf 5m

# Append a live spot tape to a file for later replay
laevitas ws spot trades binance:BTCUSDT > btc-spot.ndjson

# Live forced-close events (liquidations) on the most active perps
laevitas ws perpetuals liquidations binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP

# Live L2 order book — single pair opens straight into the centre-price ladder
laevitas ws perpetuals book binance:BTCUSDT

# Multi-pair book scan — list view; press Enter to drill into a ladder
laevitas ws perpetuals book binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP

# Wildcards (`*`) — every BTCUSDT perp book across all venues at once
laevitas ws perpetuals book "*:BTCUSDT"

# Wildcards firehose — every perpetual liquidation across every exchange
laevitas ws perpetuals liquidations "*:*"

# Error-aware REST extraction — works for both success and failure
RESP=$(laevitas perps carry BTC-PERPETUAL -p 1h -o json)
if [ "$(echo "$RESP" | jq -r '.success')" = "true" ]; then
  echo "$RESP" | jq '.data[0].funding_rate_close'
else
  echo "$RESP" | jq -r '"Error: \(.error.code) — \(.error.message)"'
fi
```

> **Note on examples:** instrument names like `BTC-26JUN26` in `--help` output are computed dynamically from today's date — the CLI substitutes a plausible near-term quarterly expiry at runtime. Examples in `--help` will always look current; they may not always match a *real* exchange listing exactly. Use `laevitas futures catalog --currency BTC` (or the equivalent `perps`/`options`/`spot` catalog) to discover real active instruments.

### Claude / Codex Skill

To teach an AI agent about this CLI, point it at the `--help` output or include this in your system prompt:

```
The laevitas CLI provides REST and WebSocket crypto market data. For REST commands, always use -o json and parse .success before reading .data or .error.
- laevitas futures snapshot --currency BTC -o json  (all BTC futures)
- laevitas perps carry <instrument> -o json          (funding/carry)
- laevitas options flow --currency BTC -o json      (options flow)
- laevitas options vol-surface snapshot --currency BTC      (vol surface)
- laevitas spot ohlcvt BTCUSDT -o json              (spot reference price)
- laevitas instruments list --market-type perpetual -o json (instrument discovery)
- laevitas ws perpetuals trades binance:BTCUSDT     (NDJSON live stream)
- laevitas predictions catalog --keyword <term>     (prediction markets)
Use .data for REST result rows. WebSocket events are one JSON object per line.
```

## Authentication

Two paths, configurable via the same `wallet` and `config` commands.

### API Key

```sh
laevitas config init                       # interactive, picks API key path
laevitas config set api_key <key>          # non-interactive
LAEVITAS_API_KEY=<key> laevitas …          # env override
```

### x402 Wallet (Pay Per Request, USDC on Base)

Pay per request with an EVM wallet — no signup, no API key. Each request triggers an on-chain payment if no credit token is cached; subsequent requests use the cached token until it expires.

```sh
laevitas wallet                            # show address, credits, auth mode
laevitas wallet init                       # interactive: paste private key
laevitas wallet set-key 0x<hex>            # non-interactive
laevitas wallet address                    # pipe-friendly address
laevitas wallet credits                    # pipe-friendly credit count

LAEVITAS_WALLET_KEY=0x<hex> laevitas …     # env override for REST wallet agents
LAEVITAS_AUTH=x402 laevitas …              # force wallet path even if API key is configured
```

WebSocket streaming currently requires API-key authentication. x402 is REST-only until the streaming gateway supports wallet auth.

When both are set, the `auth` config field decides:

- `auto` (default) — API key first, wallet as fallback on 401/402.
- `api-key` — always use API key, ignore wallet.
- `x402` — always use wallet, ignore API key.

Set via `laevitas config set auth <mode>` or `LAEVITAS_AUTH`.

## Configuration

Config is stored at `~/.config/laevitas/config.json`:

```json
{
  "api_key": "your-api-key",
  "wallet_key": "0x...",
  "auth": "auto",
  "base_url": "https://apiv2.laevitas.ch",
  "exchange": "deribit",
  "output": "auto"
}
```

The cached x402 credit token (when present) lives separately at `~/.config/laevitas/x402-token`.

Environment variables override config file values:

| Variable | Description |
|----------|-------------|
| `LAEVITAS_API_KEY` | API key |
| `LAEVITAS_WALLET_KEY` | Hex-encoded EVM private key for x402 |
| `LAEVITAS_AUTH` | `auto` / `api-key` / `x402` |
| `LAEVITAS_BASE_URL` | API base URL |
| `LAEVITAS_EXCHANGE` | Default exchange |
| `LAEVITAS_OUTPUT` | Default output format |

## Build from Source

```bash
git clone https://github.com/laevitas/cli.git
cd cli
make build       # → bin/laevitas
make install     # → $GOPATH/bin/laevitas
make release     # → dist/ (all platforms)
```

## License

MIT
