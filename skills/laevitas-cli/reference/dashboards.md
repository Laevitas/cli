# `dash` TUI dashboards (human-only)

The `laevitas dash` group hosts multi-pane live dashboards. They are **TTY-only** and meant for humans — screenshots, monitoring, exploration.

**Agents should not invoke `dash` for data extraction.** It renders a terminal UI, not parseable output. For programmatic flow/book/tape data, use `ws` (NDJSON) or REST (JSON envelope). Each dashboard's data-equivalent commands are listed below.

## `dash book` — multi-venue order book

```bash
laevitas dash book perpetuals BTC --margin linear      # currency mode: one contract per venue
laevitas dash book perpetuals BTC --margin linear --quote USDC   # strict quote
laevitas dash book spot BTC
laevitas dash book perpetuals BTCUSDT                   # literal mode: exact instrument name
laevitas dash book futures BTC-26JUN26
```

Markets: `perpetuals`, `futures`, `spot`, `predictions` (options rejected — no L2 on the gateway). Renders an aggregated centre-price ladder with per-venue-coloured liquidity bars and a microprice sparkline, a venue strip of best bid/ask cards with a CONSOLIDATED cross-venue summary and ARB detection, and a split-ladder toggle (`m`).

In currency mode the CLI resolves one contract per venue via `GET /api/v1/instruments` (server-side filter on base currency / market type / margin / active status; drop sub-exchange forks; per-venue quote cascade USDT > USDC > USD; tie-break toward separator-bearing names). Venues that don't list the product simply don't appear. A "waiting on …" footer annotates venues that haven't sent a snapshot yet (plain 0–5s, `(stale Ns)` 5–30s, dropped after 30s).

**Agent equivalent:** `laevitas perps orderbook-raw <instr> -o json` per venue, or `laevitas ws perpetuals book "*:BTCUSDT"` to aggregate across venues.

## `dash flow` — flow screener + drill-down

```bash
laevitas dash flow perpetuals BTC                       # currency mode: every venue carrying BTC
laevitas dash flow futures BTC --sort basis
laevitas dash flow spot --exchange binance --sort quote-volume   # exchange mode
laevitas dash flow spot BTC --exchange binance --sort liquidity  # narrow mode
```

Markets: `perpetuals`, `futures`, `spot` (options rejected). Two modes: a **screener** (REST snapshot, one row per venue/instrument, market-specific columns, `--sort <key>` / `--asc`, `/` to filter) and a **detail** drill-down (`Enter`) with chart / book / tape / liquidations-or-large-prints panes.

**Agent equivalents:**
- screener → `laevitas perps snapshot --currency BTC -o json` (or `futures` / `spot snapshot --exchange <venue>`)
- chart → `laevitas perps ohlcvt <instr> --exchange <venue> -r 1m -o json`
- tape → `laevitas ws perpetuals trades <venue>:<instr>`
- liquidations → `laevitas ws perpetuals liquidations <venue>:<instr>`
- book → `laevitas ws perpetuals book <venue>:<instr>`

## Shared keybindings

Every dashboard surface shares one vocabulary: `+`/`-` price grouping, `d` stats depth tier, `c` recenter, `m` aggregated↔split (book), `v` venue picker, `t` chart timeframe (flow detail), `tab`/`shift+tab` cycle pane, `1`–`4` jump-and-expand, `j/k ↑/↓ PgUp/PgDn g/G` scroll, `/` filter, `p` pause, `?`/`h` help, `q`/`Esc` quit.
