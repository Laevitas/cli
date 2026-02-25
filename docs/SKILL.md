# Laevitas CLI — Agent Skill

You have access to the `laevitas` CLI which provides real-time cryptocurrency derivatives data. Always use `-o json` for structured output.

## Authentication

The CLI must be configured with an API key:
```bash
export LAEVITAS_API_KEY="<key>"
# or: laevitas config set api_key <key>
```

## Available Data

### Futures (dated contracts)
```bash
laevitas futures catalog [--exchange deribit|binance]
laevitas futures snapshot --currency BTC|ETH
laevitas futures ohlcvt <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas futures oi <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas futures carry <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas futures trades <instrument> [-p PERIOD] [-n LIMIT]
laevitas futures volume <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures level1 <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures orderbook <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures ref-price <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures metadata <instrument>
```
Instrument format: `BTC-27MAR26`, `ETH-26JUN26`

### Perpetual Swaps
```bash
laevitas perps catalog [--exchange deribit|binance]
laevitas perps snapshot [--currency BTC|ETH]
laevitas perps carry <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas perps ohlcvt <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas perps oi <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps trades <instrument> [-p PERIOD] [-n LIMIT]
laevitas perps volume <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps level1 <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps orderbook <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps ref-price <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps metadata <instrument>
```
Deribit instruments: `BTC-PERPETUAL`, `ETH-PERPETUAL`
Binance instruments: `BTCUSDT`, `ETHUSDT`, `SOLUSDT` (use `--exchange binance`)

### Options
```bash
laevitas options catalog
laevitas options snapshot --currency BTC|ETH
laevitas options flow --currency BTC|ETH [--min-premium N] [--top-n N]
laevitas options trades --currency BTC|ETH [--direction buy|sell] [--type C|P] [--maturity 28MAR25] [--block-only] [--sort premium_usd] [--sort-dir DESC]
laevitas options trades --instrument <instrument>
laevitas options ohlcvt <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas options oi <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas options volatility <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas options level1 <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas options ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas options volume <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas options ref-price <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas options metadata <instrument>
```
Instrument format: `BTC-27MAR26-70000-C` (currency-maturity-strike-type, C=call P=put)

### Volatility Surface (under options)
```bash
laevitas options vol-surface snapshot --currency BTC|ETH [--date ISO] [-r RESOLUTION]
laevitas options vol-surface term-structure --currency BTC|ETH [--date ISO] [-r RESOLUTION]
laevitas options vol-surface history --currency BTC|ETH --maturity 28MAR25 [-p PERIOD] [-r RESOLUTION]
```
Returns: ATM IV, 25-delta call/put IV, skew, butterfly for each maturity/tenor.

### Prediction Markets (Polymarket)
```bash
laevitas predictions catalog [--keyword TERM] [--category CATEGORY]
laevitas predictions categories
laevitas predictions snapshot [--category CATEGORY] [--event EVENT_SLUG]
laevitas predictions ohlcvt <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas predictions trades <instrument> [-p PERIOD] [-n LIMIT]
laevitas predictions ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas predictions orderbook <instrument>
laevitas predictions metadata <instrument>
```
Instrument format: `{market-slug}-YES` or `{market-slug}-NO`

## Key Parameters

| Flag | Values | Description |
|------|--------|-------------|
| `-o` | `json`, `table`, `csv` | Output format (always use `json` for parsing) |
| `-p` | `1h`, `6h`, `24h`, `3d`, `7d`, `30d` | Lookback period (default 7d) |
| `-r` | `1m`, `5m`, `15m`, `1h`, `4h`, `1d` | Time resolution |
| `-n` | 1-1000 | Record limit |
| `--start` | ISO 8601 datetime | Exact start (overrides -p) |
| `--end` | ISO 8601 datetime | Exact end (overrides -p) |
| `--exchange` | `deribit`, `binance` | Exchange |
| `--currency` | `BTC`, `ETH` | Base currency |
| `--cursor` | string | Pagination cursor from previous response |

## Common Patterns

```bash
# Get latest BTC funding rate (last hour)
laevitas perps carry BTC-PERPETUAL -p 1h -o json -n 1

# BTC futures OHLCV last 24 hours, hourly candles
laevitas futures ohlcvt BTC-27MAR26 -p 24h -r 1h -o json

# Compare futures carry across expirations
laevitas futures snapshot --currency BTC -o json

# Find large options trades
laevitas options trades --currency BTC --sort premium_usd --sort-dir DESC -n 10 -o json

# BTC implied vol over last 3 days
laevitas options volatility BTC-27MAR26-70000-C -p 3d -r 1h -o json

# Get ATM implied volatility across the term structure
laevitas options vol-surface term-structure --currency BTC -o json

# Check prediction market probability
laevitas predictions ohlcvt <instrument>-YES -p 7d -o json -n 1
```

## Error Handling

- Exit code 0 = success, non-zero = error
- JSON errors: `{"error": "message"}`
- Common: 401 (bad API key), 429 (rate limited), 400 (bad params)

## Versioning & Release

Version is auto-detected from git tags at runtime. No ldflags needed for dev builds.

```bash
# Check current version
laevitas version

# Tag a release (strips leading v internally — always use v prefix on tags)
git tag -a v0.2.0 -m "v0.2.0 — description"

# Build (version auto-detected from tag)
go build -o laevitas .

# Production build with ldflags (Makefile does this)
make build          # → bin/laevitas
make install        # → $GOPATH/bin/laevitas
make release        # → dist/ (linux/darwin/windows, amd64/arm64)

# Push tag to remote
git push origin main --tags
```

Version priority: ldflags > git tag > commit hash > "dev"
