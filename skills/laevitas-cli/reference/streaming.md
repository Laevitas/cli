# Live streaming (`ws`)

`laevitas ws <market> <stream> <exchange:instrument>[,...]` opens a WebSocket subscription and emits **NDJSON** to stdout — one `{"channel": "...", "data": {...}}` per line. Built to be piped through `jq` or redirected to a file. `Ctrl-C` closes the connection cleanly and exits 0.

There is no `.success` envelope on streams — parse one JSON object per line.

```bash
laevitas ws perpetuals trades binance:BTCUSDT
laevitas ws options ticker deribit:BTC-30JAN26-100000-C --tf 5m
laevitas ws spot trades binance:BTCUSDT,coinbase:BTC-USD
laevitas ws predictions trades polymarket:<slug>-YES
laevitas ws perpetuals liquidations binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP
laevitas ws perpetuals book binance:BTCUSDT
laevitas ws perpetuals book "*:BTCUSDT"     # wildcard — every venue
```

## Markets and streams

- **Markets:** `perpetuals`, `futures`, `options`, `spot`, `predictions`.
- **Streams:** `trades`, `ticker`, `vt` (volume + trade OHLC), `liquidations` (perpetuals/futures only — forced-close events), `book` (L2 — perpetuals/futures/spot/predictions; **not** options).
- **Timeframes** (`ticker`/`vt` only): `1m 5m 15m 30m 1h 4h 12h 1d`, default `1m`. `trades`, `liquidations`, `book` don't bucket by timeframe — passing `--tf` errors.

## Exchanges per market

| Market | Exchanges |
|---|---|
| perpetuals | deribit, binance, okx, bybit, hyperliquid, kraken, nado, bullish |
| futures (dated) | deribit, binance, okx, bybit, kraken |
| options | deribit, binance, okx, bybit, bullish, derive |
| spot | binance, coinbase, bybit, okx, kraken |
| predictions | polymarket |

## Wildcards (`*`)

Accepted per-segment in the market, exchange, and instrument positions; rejected in the stream, OHLC dataType, and `--tf` positions (server-enforced). One wildcard pattern counts as one subscription. The wire `channel` field is always the **resolved concrete path**, so dispatch logic keyed on `msg.channel` keeps working.

```bash
laevitas ws perpetuals book "*:BTCUSDT"        # BTCUSDT perp on every exchange
laevitas ws "*" trades "binance:BTCUSDT"        # binance BTCUSDT across all markets
laevitas ws perpetuals liquidations "*:*"       # every perp liquidation everywhere
```

PowerShell users must quote `*` so the shell doesn't expand it to filenames. Patterns with two-or-more `*` segments can deliver thousands of events/sec; if your consumer can't drain fast enough the server closes with code **4003 (slow consumer)**.

## Parsing without inspecting the channel string

The `data` shape varies by market/stream. Dispatch on these discriminators:

- `condition_id` present → predictions
- `option_type` and `strike` present → options
- `maturity == "PERPETUAL"` → perpetual
- any other `maturity` value → dated future
- `quote_currency` present, no `mark_price` → spot
- `position_side` and `category == "forced"` → liquidation (`position_side` ∈ {`long`,`short`} is the side liquidated; `direction` is the inverse forced-order side)

```bash
# only large BTC perp trades
laevitas ws perpetuals trades binance:BTCUSDT \
  | jq -c 'select(.data.amount > 100000) | {t: .data.timestamp, p: .data.price, side: .data.direction}'

# capture 60s of options trades for offline analysis
timeout 60 laevitas ws options trades deribit:BTC-30JAN26-100000-C > options.ndjson
```

## Reconnect

Automatic with exponential backoff. Lost messages during downtime are **not** replayed — assume gaps are possible. In non-TTY mode, reconnect events surface as `{"warning": "...", "timestamp": "..."}` lines on **stderr** (so they don't pollute the stdout data stream).

## Agent vs human rendering

For agent piping (`-o json` or non-TTY) the TUI is bypassed entirely — events stream as NDJSON to stdout. When a real terminal is attached, `ws` renders a live TUI (rolling tape, book scan, book ladder) sharing one keymap; press `?` for a context overlay. Keybindings apply only to the attached-terminal case.
