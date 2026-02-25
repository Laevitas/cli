# Laevitas CLI

Crypto derivatives analytics from your terminal. Built for humans and AI agents.

Real-time data from **Deribit**, **Binance**, and **Polymarket** — futures, perpetuals, options, volatility surfaces, and prediction markets.

```
$ laevitas futures snapshot --currency BTC -o table

instrument_name  mark_price  index_price  open_interest  volume_usd_24h  days_to_expiry  annualized_carry
─────────────────────────────────────────────────────────────────────────────────────────────────────────
BTC-28MAR25      97,450.20   96,800.00    12,450.5       245,000,000     31              8.12%
BTC-27JUN25      99,100.00   96,800.00    8,230.1        89,000,000      122             6.94%
BTC-26SEP25      101,200.00  96,800.00    3,100.8        32,000,000      213             7.81%
```

## Install

### macOS / Linux

```bash
curl -sSL https://cli.laevitas.ch/install.sh | sh
```

### Windows (PowerShell)

```powershell
iwr https://cli.laevitas.ch/install.ps1 -UseBasicParsing | iex
```

### Homebrew (macOS / Linux)

```bash
brew tap laevitas/cli
brew install laevitas
```

### Go

```bash
go install github.com/laevitas/cli@latest
```

### Docker

```bash
docker run --rm -e LAEVITAS_API_KEY=your-key laevitas/cli futures snapshot --currency BTC -o json
```

## Quick Start

```bash
# 1. Configure your API key
laevitas config init

# 2. Explore available instruments
laevitas futures catalog
laevitas perps catalog --exchange binance
laevitas options catalog

# 3. Fetch data
laevitas futures snapshot --currency BTC
laevitas perps funding BTC-PERPETUAL -r 1d -n 30
laevitas options flow --currency BTC --min-premium 5000
laevitas vol-surface snapshot --currency BTC
laevitas predictions catalog --keyword bitcoin
```

## Commands

| Command | Description |
|---------|-------------|
| `futures` | Dated futures — catalog, snapshot, OHLCV, OI, basis, trades, volume, L1/L2, ticker |
| `perps` | Perpetual swaps — catalog, snapshot, OHLCV, OI, **funding**, trades, volume, L1/L2, ticker |
| `options` | Options — catalog, snapshot, OHLCV, OI, **flow**, **trades**, **volatility**, L1, ticker |
| `vol-surface` | Volatility surface — **snapshot**, **term-structure**, **history** |
| `predictions` | Prediction markets — catalog, categories, snapshot, OHLCVT, trades, ticker, orderbook |
| `config` | Configuration — init, show, set, path |

### Global Flags

```
-o, --output    Output format: auto, json, table, csv (default: auto)
    --exchange  Override default exchange (deribit, binance)
    --version   Print version
    --help      Print help
```

### Common Data Flags

```
    --start       Start datetime (ISO 8601)
    --end         End datetime (ISO 8601)
-r, --resolution  Candle resolution: 1m, 5m, 15m, 1h, 4h, 1d
-n, --limit       Number of records (1-1000)
    --cursor      Pagination cursor
    --currency    Base currency filter (BTC, ETH)
```

## Output Modes

The CLI auto-detects your environment:

- **Interactive terminal** → colored table format
- **Piped/redirected** → JSON (machine-readable)

Override with `-o json`, `-o table`, or `-o csv`.

```bash
# Human-readable
laevitas perps funding BTC-PERPETUAL

# Machine-readable (piped)
laevitas perps funding BTC-PERPETUAL | jq '.[0].funding_rate_close'

# Explicit JSON
laevitas perps funding BTC-PERPETUAL -o json

# CSV for spreadsheets
laevitas perps funding BTC-PERPETUAL -o csv > funding.csv
```

## Agent Integration

The CLI is designed to be used by AI agents as a native tool.

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
laevitas perps funding BTC-PERPETUAL -o json -n 1 | jq '.[0].funding_rate_close'

# "Show me the biggest BTC options trades today"
laevitas options trades --currency BTC --sort premium_usd --sort-dir DESC -n 10 -o json

# "What does the vol surface look like?"
laevitas vol-surface snapshot --currency BTC -o json | jq '[.[] | {maturity, atm_iv, skew_25d}]'

# "What's the probability of Bitcoin reaching 250k?"
laevitas predictions ohlcvt will-bitcoin-reach-250000-YES -o json -n 1 | jq '.[0].close'

# Combined pipeline: find the highest-carry future and get its order book
BEST=$(laevitas futures snapshot --currency BTC -o json | jq -r 'sort_by(.annualized_carry) | last | .instrument_name')
laevitas futures orderbook "$BEST" -o json
```

### AI Agent Skill

To teach an AI agent about this CLI, point it at the `--help` output or include this in your system prompt:

```
The laevitas CLI provides crypto derivatives data. Key commands:
- laevitas futures snapshot --currency BTC -o json  (all BTC futures)
- laevitas perps funding <instrument> -o json       (funding rates)
- laevitas options flow --currency BTC -o json      (options flow)
- laevitas vol-surface snapshot --currency BTC      (vol surface)
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
