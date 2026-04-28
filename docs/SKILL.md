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

### Spot Markets
```bash
laevitas spot catalog [--exchange binance|coinbase|bybit|okx|kraken] [--currency BTC|ETH] [--quote-currency USDT|USDC|USD] [-n LIMIT]
laevitas spot snapshot --exchange binance [--currency BTC] [--quote-currency USDT] [--date ISO]
laevitas spot metadata <instrument> --exchange binance
laevitas spot ohlcvt <instrument> [-p PERIOD] [-r RESOLUTION] [-n LIMIT]
laevitas spot ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas spot volume <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas spot level1 <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas spot orderbook <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas spot orderbook-raw <instrument> [-p PERIOD]
laevitas spot trades [<instrument>] [--currency BTC] [--quote-currency USDT] [--direction buy|sell] [--min-quote-amount N] [--top-n N]
```
Instrument format: `BTCUSDT`, `BTC-USD`, `ETHUSDC` (exchange-specific). Default exchange is `binance` (deribit does not trade spot).
Use spot as a reference-price layer for derivatives basis: `(perps_mark - spot_close)` gives perp basis; `(future_mark - spot_close)` gives the cash-and-carry premium.

### Cross-Product Instruments Registry
```bash
laevitas instruments list [--market-type spot|perpetual|future|option] [--exchange EX] [--base-currency BTC] [--quote-currency USD] [--status active|expired|all] [--name PATTERN] [--margin-type linear|inverse] [--option-type call|put] [--expiry-from ISO] [--expiry-to ISO] [-n LIMIT]
laevitas instruments detail <instrument> --exchange EX
```
Use `instruments list` for cross-product browsing (e.g. "every active BTC option"). Use `instruments detail` for a single instrument's full contract specification including raw exchange API data. The product-specific `catalog` commands are still the right call for product-scoped browsing — `instruments list` is for cross-product queries.

### Prediction Markets (Polymarket)
```bash
laevitas predictions catalog [--keyword TERM] [--category CATEGORY] [-n LIMIT]
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

### Global flags (every command)

| Flag | Values | Description |
|------|--------|-------------|
| `-o` | `json`, `table`, `csv` | Output format (always use `json` for parsing) |
| `--exchange` | `deribit`, `binance` | Exchange |

### Time-series flags

Apply to `ohlcvt`, `oi`, `carry`, `trades`, `volume`, `level1`, `orderbook`, `ticker`, `ref-price`, `volatility`, `liquidations`, and similar commands.

| Flag | Values | Description |
|------|--------|-------------|
| `-p` | `1h`, `6h`, `24h`, `3d`, `7d`, `30d` | Lookback period (default 7d) |
| `-r` | `1m`, `5m`, `15m`, `1h`, `4h`, `1d` | Candle resolution |
| `-n` | 1-1000 | Record limit |
| `--start` | ISO 8601 datetime | Exact start (overrides `-p`) |
| `--end` | ISO 8601 datetime | Exact end (overrides `-p`) |
| `--currency` | `BTC`, `ETH` | Base currency filter |
| `--cursor` | string | Pagination cursor from previous response |
| `--sort-dir` | `ASC`, `DESC` | Default `DESC` (newest first). Pass `ASC` for chronological. Skipped automatically when `--cursor` is set. |

**Default sort direction is DESC.** Row 0 of any time-series JSON response is the most recent record in the window — `jq '.[0]'` gives "now," not the oldest record. Pass `--sort-dir ASC` to opt into oldest-first when you actually want chronological iteration. Charts always render left-to-right in time regardless of sort direction.

### Catalog flags (paginated)

`catalog` commands list all available instruments and DO support pagination plus per-product filters. Use them to discover instrument names before fetching data.

| Flag | Applies to | Description |
|------|------------|-------------|
| `-n`, `--limit` | all catalogs | Max records (1-1000) |
| `--cursor` | all catalogs | Continue a paginated scan |
| `--exchange` | all catalogs | Filter by exchange |
| `--currency` | all catalogs | Filter by base currency |
| `--maturity` | futures, perps, options | Filter by expiry (e.g. `26JUN26`) |
| `--option-type` | options | `C` (call) or `P` (put) |
| `--strike-min` / `--strike-max` | options | Strike range |
| `--quote-currency` | spot | Filter by quote currency (USDT, USDC, USD) |
| `--category` / `--event` / `--keyword` | predictions | Polymarket-specific filters |

### Snapshot flags

**`snapshot` commands return a complete point-in-time view — they do NOT accept `-n`/`--limit`, `-p`/`--period`, `--start`/`--end`, `-r`/`--resolution`, or `--cursor`.** Passing any of these will fail with `unknown flag`. To trim a snapshot response, filter downstream with `jq` (e.g. `jq '.[:3]'`).

| Flag | Values | Description |
|------|--------|-------------|
| `--currency` | `BTC`, `ETH` | Filter by base currency |
| `--quote-currency` | `USDT`, `USDC`, `USD` | Filter by quote currency (spot only) |
| `--date` | ISO 8601 datetime | Snapshot timestamp (defaults to now) |

```bash
# Wrong — snapshot does not paginate
laevitas futures snapshot --currency BTC -n 3      # ✗ unknown flag

# Right — fetch the full snapshot, trim downstream
laevitas futures snapshot --currency BTC -o json | jq '.[:3]'
```

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

# Spot reference price (for derivatives basis calculations)
laevitas spot ohlcvt BTCUSDT -p 1h -o json -n 1 | jq '.[0].close'

# Compute perp basis vs spot
PERP=$(laevitas perps carry BTC-PERPETUAL -o json -n 1 | jq '.[0].mark_price')
SPOT=$(laevitas spot ohlcvt BTCUSDT -o json -n 1 | jq '.[0].close')
echo "basis: $(echo "$PERP - $SPOT" | bc)"

# Browse all active BTC perps across exchanges
laevitas instruments list --market-type perpetual --base-currency BTC -o json

# Full contract specification for one instrument
laevitas instruments detail BTC-PERPETUAL --exchange deribit -o json
```

## Help text examples

Instrument names like `BTC-26JUN26` or `BTC-26JUN26-100000-C` shown in `--help` output are computed from today's date — the CLI substitutes a plausible near-term quarterly expiry at runtime. Examples never look stale, but they may not match a real exchange listing exactly. Use `laevitas {group} catalog` to discover real active instruments before fetching data.

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
