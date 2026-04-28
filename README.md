# Laevitas CLI

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)]()
[![Twitter](https://img.shields.io/twitter/follow/laevitas1?style=social)](https://twitter.com/laevitas1)
[![Discord](https://img.shields.io/discord/laevitas?color=5865F2&logo=discord&logoColor=white)](https://discord.com/invite/yaXc4EFFay)

> **Derivatives Data Without The Spread** — in your terminal.

Crypto derivatives analytics CLI for futures, perpetuals, options, vol surfaces, and prediction markets. Agent-native, pipe-friendly, human-readable.

Real-time data from **Deribit**, **Binance**, and **Polymarket**.

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
# 1. Configure your API key
laevitas config init

# 2. Explore available instruments
laevitas futures catalog
laevitas perps catalog --exchange binance
laevitas options catalog
laevitas spot catalog --exchange binance
laevitas instruments list --market-type perpetual --base-currency BTC

# 3. Fetch data
laevitas futures snapshot --currency BTC
laevitas perps carry BTC-PERPETUAL -r 1d -n 30
laevitas options flow --currency BTC --min-premium 5000
laevitas options vol-surface snapshot --currency BTC
laevitas spot ohlcvt BTCUSDT -p 24h -r 1h
laevitas predictions catalog --keyword bitcoin
```

## Commands

| Command | Description |
|---------|-------------|
| `futures` | Dated futures — catalog, snapshot, OHLCVT, OI, carry, trades, volume, L1/L2, ticker |
| `perps` | Perpetual swaps — catalog, snapshot, OHLCVT, OI, **carry**, trades, volume, L1/L2, ticker |
| `options` | Options — catalog, snapshot, OHLCVT, OI, **flow**, **trades**, **volatility**, L1, ticker, **vol-surface** |
| `spot` | Spot markets — catalog, snapshot, OHLCVT, ticker, volume, L1/L2 orderbook, trades |
| `predictions` | Prediction markets — catalog, categories, snapshot, OHLCVT, trades, ticker, orderbook |
| `instruments` | Cross-product instrument registry — `list` + `detail` across all exchanges |
| `config` | Configuration — init, show, set |
| `version` | Print version and build information |

### Global Flags

```
-o, --output    Output format: auto, json, table, csv (default: auto)
    --exchange  Override default exchange (deribit, binance)
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
    --exchange         Filter by exchange (deribit, binance, okx, bybit, hyperliquid, kraken)
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
    --exchange        Override default exchange (deribit, binance)
```

```bash
# Wrong — snapshot does not paginate
laevitas futures snapshot --currency BTC -n 3      # ✗ unknown flag

# Right — fetch the full snapshot, trim downstream
laevitas futures snapshot --currency BTC -o json | jq '.[:3]'
```

## Output Modes

The CLI auto-detects your environment:

- **Interactive terminal** → colored table format
- **Piped/redirected** → JSON (machine-readable)

Override with `-o json`, `-o table`, or `-o csv`.

```bash
# Human-readable
laevitas perps carry BTC-PERPETUAL

# Machine-readable (piped)
laevitas perps carry BTC-PERPETUAL | jq '.[0].funding_rate_close'

# Explicit JSON
laevitas perps carry BTC-PERPETUAL -o json

# CSV for spreadsheets
laevitas perps carry BTC-PERPETUAL -o csv > funding.csv
```

## Agent Integration

The CLI is designed to be used by AI agents (Claude, GPT, Codex, etc.) as a native tool.

### Why agents love CLIs

- **No SDK needed** — any agent with terminal access can use it
- **Structured output** — `-o json` returns parseable data
- **Composable** — pipe, filter, combine with `jq`, `awk`, other CLIs
- **Discoverable** — `--help` on every command
- **Deterministic** — same input → same output

### Agent examples

```bash
# "What's the current BTC futures term structure?"
laevitas futures snapshot --currency BTC -o json | jq '[.[] | {instrument: .instrument_name, basis: .mark_price - .index_price, days: .days_to_expiry, carry: .annualized_carry}]'

# "Is funding positive or negative right now?"
laevitas perps carry BTC-PERPETUAL -o json -n 1 | jq '.[0].funding_rate_close'

# "Show me the biggest BTC options trades today"
laevitas options trades --currency BTC --sort premium_usd --sort-dir DESC -n 10 -o json

# "What does the vol surface look like?"
laevitas options vol-surface snapshot --currency BTC -o json | jq '[.[] | {maturity, atm_iv, skew_25d}]'

# "What's the probability of Bitcoin reaching 250k?"
laevitas predictions ohlcvt will-bitcoin-reach-250000-YES -o json -n 1 | jq '.[0].close'

# Combined pipeline: find the highest-carry future and get its order book
BEST=$(laevitas futures snapshot --currency BTC -o json | jq -r 'sort_by(.annualized_carry) | last | .instrument_name')
laevitas futures orderbook "$BEST" -o json

# Spot reference price for derivatives basis calculation
laevitas spot ohlcvt BTCUSDT -p 24h -o json -n 1 | jq '.[0].close'

# Browse all active BTC perpetuals across exchanges
laevitas instruments list --market-type perpetual --base-currency BTC -o json

# Full contract specification for a single instrument
laevitas instruments detail BTC-PERPETUAL --exchange deribit -o json
```

> **Note on examples:** instrument names like `BTC-26JUN26` in `--help` output are computed dynamically from today's date — the CLI substitutes a plausible near-term quarterly expiry at runtime. Examples in `--help` will always look current; they may not always match a *real* exchange listing exactly. Use `laevitas futures catalog --currency BTC` (or the equivalent `perps`/`options`/`spot` catalog) to discover real active instruments.

### Claude / Codex Skill

To teach an AI agent about this CLI, point it at the `--help` output or include this in your system prompt:

```
The laevitas CLI provides crypto derivatives data. Key commands:
- laevitas futures snapshot --currency BTC -o json  (all BTC futures)
- laevitas perps carry <instrument> -o json          (funding/carry)
- laevitas options flow --currency BTC -o json      (options flow)
- laevitas options vol-surface snapshot --currency BTC      (vol surface)
- laevitas predictions catalog --keyword <term>     (prediction markets)
Always use -o json for structured output. Use --help on any command for details.
```

## Configuration

Config is stored at `~/.config/laevitas/config.json`:

```json
{
  "api_key": "your-api-key",
  "base_url": "https://apiv2.laevitas.ch",
  "exchange": "deribit",
  "output": "auto"
}
```

Environment variables override config file values:

| Variable | Description |
|----------|-------------|
| `LAEVITAS_API_KEY` | API key |
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
