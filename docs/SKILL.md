# Laevitas CLI — Agent Skill

You have access to the `laevitas` CLI for cryptocurrency market data. It has two output contracts:

- **REST commands** (`futures`, `perps`, `options`, `spot`, `predictions`, `instruments`, `analytics`, `wallet`) use `-o json` and return a stable JSON envelope.
- **WebSocket commands** (`ws`) stream newline-delimited JSON (NDJSON), one event per line, when piped or forced with `-o json`.

For REST automation, always use `-o json`, parse `.success` first, then read `.data` or `.error`.

## Operating Rules for LLMs

1. Use `laevitas`, not `lvt`, unless the user explicitly says they created a local alias.
2. For REST commands, never read top-level array paths like `.[0]`; read `.data[0]`.
3. For REST errors, branch on `.error.code`, not `.error.message`.
4. For WebSocket commands, parse one JSON object per line; there is no REST-style `.success` envelope.
5. Use catalog or `instruments list` to discover real instrument names before fetching time-series data.
6. Do not pass time-series flags to snapshots. Snapshot commands do not accept `-n`, `--period`, `--start`, `--end`, `--resolution`, or `--cursor`.

## Authentication

Two REST auth options are available:

- **API key** — `LAEVITAS_API_KEY=<key>` env var or `laevitas config set api_key <key>`. Required for WebSocket streaming today.
- **x402 wallet** — `LAEVITAS_WALLET_KEY=0x<hex>` env var or `laevitas wallet set-key 0x<hex>`. Pays per REST request in USDC on Base. After the first on-chain payment, the API issues a JWT credit token cached at `~/.config/laevitas/x402-token` and used for subsequent requests until it expires. Check state any time with `laevitas wallet show -o json`.

Set `LAEVITAS_AUTH=auto|api-key|x402` to control which path the CLI prefers when both are configured. Default is `auto` (API key first, wallet fallback on 401/402).

Read `.meta.auth` on every REST response to confirm which path served the request: `api-key`, `credit` (cached x402 token), or `on-chain` (fresh x402 payment).

WebSocket auth is API-key only for now. If `LAEVITAS_AUTH=x402` is set, use REST commands or switch to API-key auth before calling `laevitas ws`.

## Response shape (v0.6.0+, extended in v0.7.0)

Every REST JSON response uses a stable envelope. Always parse `.success` first, then either `.data` (on success) or `.error` (on failure).

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
# first futures mark price
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

Orderbook note: `futures orderbook` and `perps orderbook` are historical metrics endpoints. Table output is compacted to latest-close liquidity, imbalance, microprice, and snapshot count; use `-o json` or `-o csv` for the full wide metrics payload. For a live human-readable book ladder, use `laevitas ws perpetuals book binance:BTCUSDT` or `laevitas ws futures book deribit:<instrument>`.

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

**Auth:** API key only on the streaming gateway today. x402 wallet auth is REST-only; calling `laevitas ws` in wallet-only mode returns a clear error.

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

## TUI dashboards (v0.8.3+)

The `laevitas dash` command group hosts multi-pane live dashboards. These
are **TTY-only** — pipe-friendly NDJSON consumers should keep using
`laevitas ws` (which is unchanged and emits the same shape it always has).

**Agents should not invoke `dash` for data extraction.** Use it for
reference, screenshots, or as documentation; for parsing, use `ws` (NDJSON
streams) or REST endpoints (JSON envelope).

### `dash book` — multi-venue order book

```bash
# Currency mode — resolves per-venue contracts via the instruments
# registry. Per-venue quote-currency cascade USDT → USDC → USD.
laevitas dash book perpetuals BTC --margin linear
laevitas dash book perpetuals ETH --margin inverse
laevitas dash book perpetuals BTC --margin linear --quote USDC   # strict quote
laevitas dash book spot BTC

# Literal mode — exact instrument name; only venues that name the
# contract that way contribute.
laevitas dash book perpetuals BTCUSDT
laevitas dash book futures BTC-26JUN26
```

Markets supported: `perpetuals`, `futures`, `spot`, `predictions`. Options
is rejected (no L2 data on the streaming gateway).

The dashboard renders:

- An **aggregated centre-price ladder** with cumulative liquidity columns
  on each side, segmented bars coloured by per-venue contribution, and a
  microprice sparkline next to MID.
- A **venue strip** of bordered cards (one per venue) with each venue's
  best bid/ask, spread, imbalance, and instrument name; plus a CONSOLIDATED
  cross-venue summary card with ARB detection on crossed books.
- Toggle to **split-ladder mode** (`m`) — one narrow per-venue column
  side-by-side instead of the merged ladder.

#### Keys (every dashboard surface uses the same vocabulary)

| Key | Action |
|---|---|
| `+` / `-` | Cycle price grouping |
| `d` | Cycle stats depth tier (10 → 20 → 50) |
| `c` | Recenter viewport on the spread |
| `m` | Toggle aggregated ↔ split ladder |
| `v` | Venue picker |
| `j/k`, `↑/↓`, `PgUp/PgDn`, `g/G` | Scroll / page / top / bottom |
| `p` | Pause |
| `?` / `h` | Help overlay |
| `q` / `Esc` | Quit |

#### Resolver behaviour (currency mode)

When the user passes a bare currency (`BTC`, `ETH`) or supplies
`--margin`/`--quote`, the CLI calls `GET /api/v1/instruments` with the
canonical filters and picks **one contract per venue**:

1. Server-side filter: `base_currency=BTC&market_type=perpetual&margin_type=linear&status=active`.
2. Drop sub-exchange forks (rows with `sub_exchange != ""`).
3. Per-venue cascade: prefer `USDT` > `USDC` > `USD` > first available.
4. Tie-break: prefer instrument names containing a separator (`-`, `_`,
   `:`) so e.g. hyperliquid's `ETH-USD` is picked over the bare `ETH` alias.
5. Build per-venue WS channels: `book.<market>.<exchange>.<instrument_name>`.

Venues that don't list the requested product (coinbase has no USDT perp;
deribit has only USDC linear; hyperliquid has no inverse perp) are absent
from the resolver output and won't appear in "waiting on …".

#### Stale-venue annotation

The "waiting on …" footer compares received snapshots against the
expected-venue set (resolver output in currency mode; exact-name registry
lookup in literal mode). Three stages:

- 0–5s after first venue arrives: plain `waiting on: derive, hyperliquid`.
- 5–30s: stale annotation `waiting on: derive (stale 12s), hyperliquid (stale 12s)`.
- 30s+: dropped from the list entirely (proven not coming on this run).

Stage gate fires only after the connection is proven healthy (≥1 snapshot
received), so connection latency doesn't false-alarm as per-venue silence.

## Order book commands — REST/WS parity

There are **two distinct order-book shapes** in the API. Pick the right
endpoint for what you actually need:

### Snapshot shape (`asks` + `bids` arrays + microprice + tier liquidity)

This is "what does the book look like right now (or at a past timestamp)".
Five surfaces; identical wire shape; identical flags:

```bash
laevitas perps     orderbook-raw <instrument>     # /api/v1/perpetuals/orderbook-raw
laevitas futures   orderbook-raw <instrument>     # /api/v1/futures/orderbook-raw
laevitas spot      orderbook-raw <instrument>     # /api/v1/spot/l2-orderbook-raw
laevitas predictions orderbook   <instrument>     # /api/v1/predictions/orderbook-raw
laevitas ws <market> book <exchange>:<instrument> # WS stream of the same shape
```

Each carries `--depth N` (trim asks/bids to top-N levels) and `--compact`
(drop tier-aggregate fields like `ask_liquidity_*` / `bid_liquidity_*` /
`imbalance_*`; preserve raw asks/bids/microprice/metadata).

Default for REST snapshot endpoints: `-n 1` (one snapshot — current
state). Pass `-n 50 -p 1h` for historical.

### Stats shape (time-series of liquidity metrics — no asks/bids array)

This is "what was depth-N liquidity over time". Three surfaces:

```bash
laevitas perps   orderbook <instrument>   # /api/v1/perpetuals/orderbook
laevitas futures orderbook <instrument>   # /api/v1/futures/orderbook
laevitas spot    orderbook <instrument>   # /api/v1/spot/l2-orderbook
```

Each row is one bar (1m/5m/1h depending on `-r`) of OHLC liquidity stats
across four depth tiers (10/20/50/100). Use `--depth N` to pick which
tier's columns surface in the compact table view; full payload via JSON.

### Output defaults (adapt to audience)

| Output mode | Default depth | Why |
|---|---|---|
| `-o table` (TTY, human) | top-20 levels each side | fits one screen |
| `-o json` | full wire payload (~100 levels) | agents need full data |
| `-o csv` | full wire payload | agents need full data |
| WS NDJSON to stdout | full wire payload per event | agents need full data |

**Agents emitting JSON or NDJSON always get the full wire payload unless
`--depth` is explicitly passed.** The display cap is human-only; it never
affects programmatic consumers.

### Picking the right command for the task

- "Show me the current book" → `<group> orderbook-raw <instr>` (REST snapshot, one call)
- "Stream live book updates for processing" → `ws <market> book <exch>:<instr>` (NDJSON, --depth N --compact recommended)
- "Live multi-venue TUI" → `dash book <market> <symbol>` (TTY only)
- "How did depth-50 liquidity change over the last hour?" → `<group> orderbook <instr> -p 1h -r 1m --depth 50` (stats time-series)
- "What's the book at this exact past timestamp?" → `<group> orderbook-raw <instr> --start <ISO> --end <ISO>` (snapshot history)

## Key Parameters

### Market type tokens (canonical + aliases)

Wherever the CLI takes a market type (`--market-type`, `ws <market>`, `dash book <market>`), the CLI accepts any common alias and normalises internally. **Always check `--help` for what an individual flag accepts** — the alias table is wide:

| Canonical (use this when generating commands) | Also accepted |
|---|---|
| `perpetuals` | `perp`, `perps`, `perpetual`, `swap`, `swaps` |
| `futures` | `fut`, `future`, `dated` |
| `options` | `opt`, `opts`, `option` |
| `spot` | `spot` |
| `predictions` | `prediction`, `predict`, `poly`, `polymarket` |

For agents: emit the **canonical** form. The aliases are for human convenience.

Margin types follow the same rule:

| Canonical | Aliases |
|---|---|
| `linear` | `lin`, `usdt`, `usdc`, `stable` |
| `inverse` | `inv`, `coin`, `coins`, `crypto` |

REST API filter `?market_type=` uses the **singular** form (`perpetual`, `future`, `option`, `prediction`, `spot`); WS channels use the **plural** form (`book.perpetuals.<venue>.<instrument>`). The CLI handles the translation — you don't need to.

### Global flags (every command)

| Flag | Values | Description |
|------|--------|-------------|
| `-o` | `auto`, `json`, `table`, `csv` | Output format (always use `json` for REST parsing) |
| `--exchange` | market-dependent | Exchange filter or override |
| `--no-chart` | boolean | Disable inline charts in table output |
| `--wide` | boolean | Disable table column truncation |
| `--width` | integer | Override table width |
| `--verbose` | boolean | Redacted HTTP diagnostics |

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
- REST JSON errors: `{"success": false, "error": {"code": "...", "message": "..."}}`
- Common codes: `AUTH_INVALID`, `AUTH_FORBIDDEN`, `RATE_LIMITED`, `PAYMENT_REQUIRED`, `WALLET_NOT_CONFIGURED`, `INSUFFICIENT_BALANCE`, `PAYMENT_REJECTED`, `BAD_REQUEST`, `NOT_FOUND`, `SERVER_ERROR`, `NETWORK_ERROR`, `UNKNOWN_ERROR`
- WebSocket commands stream NDJSON; runtime warnings are diagnostic messages, not REST error envelopes.

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
