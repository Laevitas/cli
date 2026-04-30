# Laevitas CLI — Agent Skill

You have access to the `laevitas` CLI which provides real-time cryptocurrency derivatives data. Always use `-o json` for structured output.

## Authentication

Two options, equivalent for data access:

- **API key** — `LAEVITAS_API_KEY=<key>` env var (preferred for agents) or `laevitas config set api_key <key>`.
- **x402 wallet** — `LAEVITAS_WALLET_KEY=0x<hex>` env var or `laevitas wallet set-key 0x<hex>`. Pays per request in USDC on Base. After the first on-chain payment, the API issues a JWT credit token cached at `~/.config/laevitas/x402-token` and used for subsequent requests until it expires. Check state any time with `laevitas wallet show -o json`.

Set `LAEVITAS_AUTH=auto|api-key|x402` to control which path the CLI prefers when both are configured. Default is `auto` (API key first, wallet fallback on 401/402).

Read `meta.auth` on every response to confirm which path served the request: `api-key`, `credit` (cached x402 token), or `on-chain` (fresh x402 payment).

## Response shape (v0.6.0+, extended in v0.7.0)

Every JSON response uses a stable envelope. Always parse `.success` first, then either `.data` (on success) or `.error` (on failure).

**Success:**

```json
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
```

**Failure:**

```json
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

Errors are emitted to **stdout** (not stderr) when `-o json`, so a single jq pipeline works for both paths. Exit code is non-zero for any error.

**Stable error codes** (use these for branching, not `.error.message`):

| Code | When |
|------|------|
| `AUTH_INVALID` | 401 — API key missing/invalid |
| `AUTH_FORBIDDEN` | 403 — wrong tier or revoked access |
| `RATE_LIMITED` | 429 — back off, retry with delay |
| `PAYMENT_REQUIRED` | 402 — x402 payment needed and no wallet path was attempted |
| `WALLET_NOT_CONFIGURED` | 402 — payment needed, wallet path requested, but no key set. Run `laevitas wallet set-key 0x<hex>` or set `LAEVITAS_WALLET_KEY`. |
| `INSUFFICIENT_BALANCE` | 402 — wallet exists but lacks USDC on Base. The error envelope includes `wallet_address`; fund that address. |
| `PAYMENT_REJECTED` | 4xx after signing — server validated the signed payment and bounced it. Verify wallet config; not retryable without intervention. |
| `BAD_REQUEST` | 4xx — fix params and retry |
| `NOT_FOUND` | 404 — instrument or path doesn't exist |
| `SERVER_ERROR` | 5xx — transient, retry with backoff |
| `NETWORK_ERROR` | DNS/TCP/timeout — retry with backoff |
| `UNKNOWN_ERROR` | fallback |

**Reading data:** field paths now go through `.data`. Examples:

```bash
# bad row 0 mark price
laevitas futures snapshot --currency BTC -o json | jq '.data[0].mark_price'

# total record count from meta
laevitas perps carry BTC-PERPETUAL -p 7d -o json | jq '.meta.count'

# safe error-aware extraction
RESP=$(laevitas perps carry BTC-PERPETUAL -p 1h -o json)
if [ "$(echo "$RESP" | jq -r '.success')" = "true" ]; then
  echo "$RESP" | jq '.data[0].funding_rate_close'
else
  echo "$RESP" | jq -r '.error.code'
fi
```

Table (`-o table`) and CSV (`-o csv`) outputs are NOT enveloped — they format `.data` directly.

## Budget-aware loop with x402

Agents on the wallet path can self-throttle by reading `.meta.credits_remaining` after every request:

```bash
export LAEVITAS_WALLET_KEY=0x...
BUDGET=100

while ...; do
  RESP=$(laevitas perps carry BTC-PERPETUAL -p 1h -o json)
  if [ "$(echo "$RESP" | jq -r '.success')" = "false" ]; then
    code=$(echo "$RESP" | jq -r '.error.code')
    case "$code" in
      INSUFFICIENT_BALANCE) echo "fund $(echo "$RESP" | jq -r '.error.wallet_address')"; exit 2 ;;
      WALLET_NOT_CONFIGURED) echo "set LAEVITAS_WALLET_KEY"; exit 2 ;;
      RATE_LIMITED) sleep 2; continue ;;
      *) echo "$RESP" | jq -r '.error.message'; exit 1 ;;
    esac
  fi

  # Process the data, then check the budget threshold
  echo "$RESP" | jq '.data[0]'
  remaining=$(echo "$RESP" | jq -r '.meta.credits_remaining // empty')
  if [ -n "$remaining" ] && [ "$remaining" -lt "$BUDGET" ]; then
    echo "credits below threshold ($remaining); stopping"
    break
  fi
done
```

`laevitas wallet show -o json` returns the same envelope shape and can be called pre-flight to confirm wallet state without spending credits.

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

### Analytics
```bash
# Snapshot mode (latest readings — no time flags)
laevitas analytics realized-volatility --instrument <instrument> [--window-days 7|30|60|90|180|365] [--estimator close_to_close|parkinson|garman_klass] [--frequency daily|hourly] [--currency BTC]

# Historical mode (time-series — pass any of -p / --start / --end / -n)
laevitas analytics rv --instrument BTC-PERPETUAL --window-days 30 -p 30d -r 1d
```
Aliases: `analytics rv` ≡ `analytics realized-volatility`. Values are annualised percentages (e.g. `38.76` = 38.76%). Snapshot mode returns one row per `(estimator, frequency, window_days)` combination matching your filters; pass `--estimator` and `--frequency` to narrow to a single row. Historical mode paginates and accepts the standard time-series flags.

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

## Live streaming (v0.8.0+)

`laevitas ws <market> <stream> <exchange:instrument>[,...]` opens a WebSocket subscription and emits NDJSON to stdout — one `{"channel": "...", "data": {...}}` per line. Designed to be piped through `jq` or redirected to a file.

```bash
laevitas ws perpetuals trades binance:BTCUSDT
laevitas ws options ticker deribit:BTC-30JAN26-100000-C --tf 5m
laevitas ws spot trades binance:BTCUSDT,coinbase:BTC-USD
laevitas ws predictions trades polymarket:will-bitcoin-reach-250000-by-december-31-2026-YES
laevitas ws perpetuals liquidations binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP
laevitas ws perpetuals book binance:BTCUSDT
laevitas ws perpetuals book "*:BTCUSDT"     # wildcard — all venues
```

**Markets:** `perpetuals`, `futures`, `options`, `spot`, `predictions`.
**Streams:** `trades`, `ticker`, `vt` (volume + trade OHLC), `liquidations` (perpetuals / futures only — forced-close events), `book` (L2 order book — perpetuals / futures / spot / predictions; options not supported).
**Timeframes (ticker / vt only):** `1m`, `5m`, `15m`, `30m`, `1h`, `4h`, `12h`, `1d`. Default `1m`. Trades, liquidations, and book don't bucket by timeframe — passing `--tf` errors.

**Wildcards (`*`):** accepted per-segment in market, exchange, and instrument positions; rejected in stream, OHLC dataType, and `--tf` (server-enforced rules). One wildcard pattern counts as one subscription. The wire `channel` field on each event is always the resolved concrete path, so dispatch logic that keys off `msg.channel` keeps working unchanged.

```bash
laevitas ws perpetuals book "*:BTCUSDT"        # BTCUSDT perp on every exchange
laevitas ws "*" trades "binance:BTCUSDT"        # binance BTCUSDT across markets
laevitas ws perpetuals liquidations "*:*"       # every perp liquidation everywhere
```

PowerShell users must quote `*` so the shell doesn't expand it to filenames. Patterns with two or more `*` segments can deliver thousands of events/sec; if your consumer can't drain fast enough the server closes with code 4003 (slow consumer).

**TUI keybindings (live-table mode):** every renderer (rolling tape, book scan, book ladder) shares one keymap. Press `?` in any view for a context-aware overlay.

| Action | Keys |
|---|---|
| quit | `q` `Q` `Ctrl+C` |
| pause / resume | `p` `P` |
| help overlay | `?` `h` `H` |
| back / close help | `Esc` |
| select up / down (lists) | `↑` `k` / `↓` `j` |
| page up / down (lists) | `PgUp` `b` / `PgDn` `f` |
| top / bottom (lists) | `g` / `G` (or `Home` / `End`) |
| drill into selected | `Enter` |
| ladder depth tier | `+` / `-` (cycles 10 → 20 → 50) |
| scroll | mouse wheel up / down |

Click events are not consumed — the terminal keeps native click-drag-to-select for copy-paste (hold `Shift` while dragging on most terminals, or `Alt` in VS Code, to bypass any TUI mouse capture).

For agent piping (`-o json` or non-TTY), the TUI is bypassed entirely — events stream as NDJSON to stdout, one event per line. Keybindings only apply when a real terminal is attached.

**Exchanges per market:**

| Market | Exchanges |
|---|---|
| perpetuals | deribit, binance, okx, bybit, hyperliquid, kraken, nado, bullish |
| futures (dated) | deribit, binance, okx, bybit, kraken |
| options | deribit, binance, okx, bybit, bullish, derive |
| spot | binance, coinbase, bybit, okx, kraken |
| predictions | polymarket |

**Auth:** API key only on the streaming gateway today. x402 wallet auth is REST-only; calling `lvt ws` in wallet-only mode returns a clear error.

**Reconnect:** automatic with exponential backoff. Lost messages during downtime are not replayed — assume gaps are possible. Reconnect events surface as `{"warning": "...", "timestamp": "..."}` lines on stderr in non-TTY mode (agent-parseable).

**Discriminators for parsing:** the `data` shape varies by market and stream. To dispatch on incoming events without inspecting the channel string:

- `condition_id` present → predictions
- `option_type` and `strike` present → options
- `maturity == "PERPETUAL"` → perpetual
- any other `maturity` value → dated future
- `quote_currency` present (no `mark_price`) → spot
- `position_side` and `category == "forced"` → liquidation (`position_side` ∈ {`long`, `short`} = the side that was liquidated; `direction` is the inverse forced-order side)

```bash
# stream + filter only large BTC perp trades
laevitas ws perpetuals trades binance:BTCUSDT \
  | jq -c 'select(.data.amount > 100000) | {time: .data.timestamp, price: .data.price, side: .data.direction}'

# capture 60s of options trades into a file for offline analysis
timeout 60 laevitas ws options trades deribit:BTC-30JAN26-100000-C > options.ndjson
```

`Ctrl-C` cleanly closes the connection and exits 0.

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

**Default sort direction is DESC.** Row 0 of any time-series JSON response is the most recent record in the window — `jq '.data[0]'` gives "now," not the oldest record. Pass `--sort-dir ASC` to opt into oldest-first when you actually want chronological iteration. Charts always render left-to-right in time regardless of sort direction.

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

**`snapshot` commands return a complete point-in-time view — they do NOT accept `-n`/`--limit`, `-p`/`--period`, `--start`/`--end`, `-r`/`--resolution`, or `--cursor`.** Passing any of these will fail with `unknown flag`. To trim a snapshot response, filter downstream with `jq` (e.g. `jq '.data[:3]'`).

| Flag | Values | Description |
|------|--------|-------------|
| `--currency` | `BTC`, `ETH` | Filter by base currency |
| `--quote-currency` | `USDT`, `USDC`, `USD` | Filter by quote currency (spot only) |
| `--date` | ISO 8601 datetime | Snapshot timestamp (defaults to now) |

```bash
# Wrong — snapshot does not paginate
laevitas futures snapshot --currency BTC -n 3      # ✗ unknown flag

# Right — fetch the full snapshot, trim downstream
laevitas futures snapshot --currency BTC -o json | jq '.data[:3]'
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
laevitas spot ohlcvt BTCUSDT -p 1h -o json -n 1 | jq '.data[0].close'

# Compute perp basis vs spot
PERP=$(laevitas perps carry BTC-PERPETUAL -o json -n 1 | jq '.data[0].mark_price')
SPOT=$(laevitas spot ohlcvt BTCUSDT -o json -n 1 | jq '.data[0].close')
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
