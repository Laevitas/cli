# Parameters & vocabulary

Three flag families — time-series, catalog, snapshot — plus the market/margin token vocabulary. Mixing families is the most common flag error (see rule 4 in SKILL.md).

## Global flags (every command)

| Flag | Values | Description |
|------|--------|-------------|
| `-o` | `auto`, `json`, `table`, `csv` | Output format (use `json` for parsing) |
| `--exchange` | market-dependent | Exchange filter or override |
| `--no-chart` | boolean | Disable inline charts in table output |
| `--wide` | boolean | Disable table column truncation |
| `--width` | integer | Override table width |
| `--verbose` | boolean | Redacted HTTP diagnostics on stderr |

## Time-series flags

Apply to `ohlcvt`, `oi`, `carry`, `trades`, `volume`, `level1`, `orderbook`, `ticker`, `ref-price`, `volatility`, and similar history commands.

| Flag | Values | Description |
|------|--------|-------------|
| `-p` | `1h`, `6h`, `24h`, `3d`, `7d`, `30d` | Lookback period (default 7d) |
| `-r` | `1m`, `5m`, `15m`, `1h`, `4h`, `1d` | Candle resolution |
| `-n` | 1–1000 | Record limit |
| `--start` | ISO 8601 | Exact start (overrides `-p`) |
| `--end` | ISO 8601 | Exact end (overrides `-p`) |
| `--currency` | `BTC`, `ETH` | Base currency filter |
| `--cursor` | string | Pagination cursor from a previous `.meta.next_cursor` |
| `--sort-dir` | `ASC`, `DESC` | Default `DESC` (newest-first). `--sort-dir` is auto-skipped when `--cursor` is set so a scan keeps its starting direction. |

**Default sort is DESC.** `.data[0]` is the most recent record in the window — `jq '.data[0]'` gives "now", not the oldest row. Pass `--sort-dir ASC` for chronological iteration. Charts always render left-to-right in time regardless of sort.

## Catalog flags (paginated)

`catalog` lists available instruments and supports pagination plus per-product filters. Use it (or `instruments list`) to discover real instrument names before fetching data.

| Flag | Applies to | Description |
|------|------------|-------------|
| `-n`, `--limit` | all catalogs | Max records (1–1000) |
| `--cursor` | all catalogs | Continue a paginated scan |
| `--exchange` | all catalogs | Filter by exchange |
| `--currency` | all catalogs | Filter by base currency |
| `--maturity` | futures, perps, options | Filter by expiry (e.g. `26JUN26`) |
| `--option-type` | options | `C` (call) or `P` (put) |
| `--strike-min` / `--strike-max` | options | Strike range |
| `--quote-currency` | spot | Quote currency (USDT, USDC, USD) |
| `--category` / `--event` / `--keyword` | predictions | Polymarket-specific filters |

## Snapshot flags

`snapshot` returns a complete point-in-time view and **rejects** `-n`/`--limit`, `-p`/`--period`, `--start`/`--end`, `-r`/`--resolution`, `--cursor` with `unknown flag`. Trim downstream with `jq`.

| Flag | Values | Description |
|------|--------|-------------|
| `--currency` | `BTC`, `ETH` | Base currency filter |
| `--quote-currency` | `USDT`, `USDC`, `USD` | Quote currency (spot only) |
| `--date` | ISO 8601 | Snapshot timestamp (defaults to now) |

```bash
laevitas futures snapshot --currency BTC -n 3            # ✗ unknown flag
laevitas futures snapshot --currency BTC -o json | jq '.data[:3]'   # ✓ trim downstream
```

## Market-type tokens

Wherever a market type is taken (`--market-type`, `ws <market>`, `dash book <market>`), the CLI accepts any alias and normalises internally. **Emit the canonical (plural) form** when generating commands.

| Canonical | Also accepted |
|---|---|
| `perpetuals` | `perp`, `perps`, `perpetual`, `swap`, `swaps` |
| `futures` | `fut`, `future`, `dated` |
| `options` | `opt`, `opts`, `option` |
| `spot` | `spot` |
| `predictions` | `prediction`, `predict`, `poly`, `polymarket` |

## Margin-type tokens

| Canonical | Aliases |
|---|---|
| `linear` | `lin`, `usdt`, `usdc`, `stable` |
| `inverse` | `inv`, `coin`, `coins`, `crypto` |

The REST API filter `?market_type=` uses the **singular** form (`perpetual`, `future`, `option`, `prediction`, `spot`); WS channels use the **plural** form (`book.perpetuals.<venue>.<instrument>`). The CLI translates at the boundary — you supply canonical and it handles the rest.
