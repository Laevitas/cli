# Commands & instrument formats

Every command group, its subcommands, and the instrument-name format each expects. For the authoritative live list use `laevitas commands -o json` — it emits one entry per command with `path`, `args`, `flags`, `examples`, `requires_auth`, `streaming`, and `output_modes`.

## Futures (dated contracts)

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
laevitas futures orderbook-raw <instrument> [-p PERIOD]
laevitas futures ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures ref-price <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas futures metadata <instrument>
```

Instrument format: `BTC-27MAR26`, `ETH-26JUN26` (currency-DDMMMYY expiry).

## Perpetual swaps

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
laevitas perps orderbook-raw <instrument> [-p PERIOD]
laevitas perps ticker <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps ref-price <instrument> [-p PERIOD] [-r RESOLUTION]
laevitas perps metadata <instrument>
```

Deribit instruments: `BTC-PERPETUAL`, `ETH-PERPETUAL`. Binance: `BTCUSDT`, `ETHUSDT`, `SOLUSDT` (pass `--exchange binance`).

`carry` is the funding/basis command — `funding_rate_close`, `mark_price`, `index_price` per row.

## Options

```bash
laevitas options catalog
laevitas options snapshot --currency BTC|ETH
laevitas options flow --currency BTC|ETH [--min-premium N] [--top-n N]
laevitas options trades --currency BTC|ETH [--direction buy|sell] [--type C|P] [--maturity 28MAR26] [--block-only] [--sort premium_usd] [--sort-dir DESC]
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

Instrument format: `BTC-27MAR26-70000-C` (currency-maturity-strike-type; `C` = call, `P` = put).

### Volatility surface (under options)

```bash
laevitas options vol-surface snapshot --currency BTC|ETH [--date ISO] [-r RESOLUTION]
laevitas options vol-surface term-structure --currency BTC|ETH [--date ISO] [-r RESOLUTION]
laevitas options vol-surface history --currency BTC|ETH --maturity 28MAR26 [-p PERIOD] [-r RESOLUTION]
```

Returns ATM IV, 25-delta call/put IV, skew, and butterfly per maturity/tenor. Note the path mirrors the API: `options vol-surface …`, not a top-level command.

## Spot markets

```bash
laevitas spot catalog [--exchange binance|coinbase|bybit|okx|kraken] [--currency BTC] [--quote-currency USDT|USDC|USD] [-n LIMIT]
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

Instrument format: `BTCUSDT`, `BTC-USD`, `ETHUSDC` (exchange-specific). **Default exchange is `binance`** — Deribit does not trade spot, so spot commands fall back to Binance even when the global default exchange is Deribit. Use spot as the reference-price layer for basis: `(perp_mark − spot_close)` is perp basis; `(future_mark − spot_close)` is the cash-and-carry premium.

## Cross-product instruments registry

```bash
laevitas instruments list [--market-type spot|perpetual|future|option] [--exchange EX] [--base-currency BTC] [--quote-currency USD] [--status active|expired|all] [--name PATTERN] [--margin-type linear|inverse] [--option-type call|put] [--expiry-from ISO] [--expiry-to ISO] [-n LIMIT]
laevitas instruments detail <instrument> --exchange EX
```

Use `instruments list` for cross-product queries ("every active BTC option across all venues"); use the product-specific `catalog` for product-scoped browsing. `instruments detail` returns one instrument's full contract spec including the raw exchange payload.

## Analytics

```bash
# Snapshot mode (latest readings — no time flags)
laevitas analytics realized-volatility --instrument <instrument> [--window-days 7|30|60|90|180|365] [--estimator close_to_close|parkinson|garman_klass] [--frequency daily|hourly] [--currency BTC]

# Historical mode (time-series — pass any of -p / --start / --end / -n)
laevitas analytics rv --instrument BTC-PERPETUAL --window-days 30 -p 30d -r 1d
```

`analytics rv` ≡ `analytics realized-volatility`. Values are annualised percentages (`38.76` = 38.76%). Snapshot mode returns one row per `(estimator, frequency, window_days)` matching the filters; pass `--estimator` and `--frequency` to narrow to one row. Passing any time flag switches to historical mode.

## Prediction markets (Polymarket)

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

Instrument format: `{market-slug}-YES` or `{market-slug}-NO`. The `close` of a `-YES` market is the implied probability (0–1). Discover slugs with `predictions catalog --keyword <term>`.

## Local / setup commands (no auth)

```bash
laevitas version              # current version (no leading v internally)
laevitas commands -o json     # machine-readable command manifest
laevitas doctor               # health check: config, auth, API, WS, wallet
laevitas config init|show|set|unset|path
laevitas wallet show|init|set-key|unset|address|credits
laevitas update               # self-update from GitHub Releases
```

## Common multi-step patterns

```bash
# Latest BTC funding rate
laevitas perps carry BTC-PERPETUAL -p 1h -n 1 -o json | jq '.data[0].funding_rate_close'

# Highest-carry future, then its order book
BEST=$(laevitas futures snapshot --currency BTC -o json | jq -r '.data | sort_by(.annualized_carry) | last | .instrument_name')
laevitas futures orderbook-raw "$BEST" -o json

# Perp basis vs spot
PERP=$(laevitas perps carry BTC-PERPETUAL -n 1 -o json | jq '.data[0].mark_price')
SPOT=$(laevitas spot ohlcvt BTCUSDT -n 1 -o json | jq '.data[0].close')
echo "basis: $(echo "$PERP - $SPOT" | bc)"

# Prediction-market probability
laevitas predictions ohlcvt <slug>-YES -n 1 -o json | jq '.data[0].close'
```
