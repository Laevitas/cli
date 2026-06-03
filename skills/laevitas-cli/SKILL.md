---
name: laevitas-cli
description: Fetch cryptocurrency market data through the laevitas CLI — futures, perpetuals, options, spot, prediction markets, volatility surfaces, cross-product instruments, analytics, and live WebSocket streams. Use when the user wants crypto derivatives or spot data, funding/carry/basis, open interest, options flow or implied-vol surfaces, order books, realized volatility, Polymarket odds, or live trade/liquidation/book streams — and the laevitas binary is on PATH. Read-only: it fetches and formats data, never trades.
---

# laevitas CLI

`laevitas` is a read-only crypto market-data client wrapping the Laevitas REST and WebSocket APIs. It is built to be driven by agents: stable JSON envelopes on REST, NDJSON on streams, a machine-readable command manifest, and a self-diagnosing `doctor`.

This skill front-loads the few things that actually trip agents up. The detail lives in `reference/` — load a file only when the task needs it.

## The five rules that prevent most failures

1. **Always pass `-o json` for REST**, then read `.success` *before* `.data` or `.error`. The payload is enveloped: success is `{success:true, data, meta}`, failure is `{success:false, error}`. Reading `.data[0]` without checking `.success` silently yields `null` on an auth error. **Errors print to stdout** under `-o json`, so one jq pipeline handles both paths.
   *Why:* the envelope is the contract. Branching on `.error.code` (stable) rather than `.error.message` (prose, may change) is what makes automation survive server updates.

2. **Never run the bare `laevitas` command.** With no subcommand it opens an interactive REPL (readline + history) that hangs forever waiting on stdin in a non-TTY context. Always invoke a subcommand directly: `laevitas <group> <subcommand> … -o json`.

3. **Discover instrument names before fetching time-series.** Instrument identifiers are exchange-specific and the canonical form is whatever the catalog returns (`BTC-PERPETUAL` on Deribit, `BTCUSDT` on Binance, `BTC-27MAR26-70000-C` for an option). Don't invent them — list first: `laevitas <group> catalog …` or `laevitas instruments list …`.
   *Why:* a guessed name returns `NOT_FOUND`; the catalog is the source of truth and also tells you which exchange lists what.

4. **Snapshots are point-in-time; don't hand them time-series flags.** `snapshot` commands reject `-n`, `-p/--period`, `--start`, `--end`, `-r/--resolution`, `--cursor` with `unknown flag`. To trim a snapshot, filter downstream (`jq '.data[:3]'`). Time-series commands (`ohlcvt`, `oi`, `carry`, `trades`, `volume`, `volatility`, `orderbook`, …) take those flags and default to **newest-first** (`--sort-dir DESC`): `.data[0]` is "now", not the oldest row.

5. **REST and WS are different output contracts.** REST groups (`futures perps options spot predictions instruments analytics wallet`) emit the JSON envelope. `ws` and `dash` stream **NDJSON** — one `{channel, data}` object per line, no `.success` wrapper; parse line-by-line. `dash` is a TTY-only human dashboard — agents should never invoke it for data, use `ws` or REST instead.

## Quick start

```bash
laevitas doctor                                         # verify auth + API + WS health before anything
laevitas futures snapshot --currency BTC -o json        # full BTC futures term structure (point-in-time)
laevitas perps carry BTC-PERPETUAL -p 1h -n 1 -o json   # latest funding/carry reading
laevitas options flow --currency BTC -o json            # large options trades
laevitas options vol-surface term-structure --currency BTC -o json   # ATM IV / skew per maturity
laevitas instruments list --market-type perpetual --base-currency BTC -o json   # cross-venue discovery
laevitas ws perpetuals trades binance:BTCUSDT           # live NDJSON trade stream (Ctrl-C to stop)
```

Error-aware extraction — works on both success and failure because errors go to stdout:

```bash
RESP=$(laevitas perps carry BTC-PERPETUAL -p 1h -o json)
if [ "$(echo "$RESP" | jq -r '.success')" = "true" ]; then
  echo "$RESP" | jq '.data[0].funding_rate_close'
else
  echo "$RESP" | jq -r '.error.code'   # branch on the stable code, not the message
fi
```

## Self-discovery (prefer over crawling `--help`)

```bash
laevitas commands -o json        # every command: path, args, flags, examples, requires_auth, streaming, output_modes
laevitas commands --filter ws    # narrow the manifest by substring
laevitas doctor -o json          # pass/warn/fail/skipped per check + environment block; never costs money
```

`laevitas commands` defaults to JSON regardless of TTY — it exists to be parsed. Reach for it whenever you're unsure a command, flag, or instrument-format exists; it is cheaper and more reliable than guessing.

## Reference (load on demand)

| Need | File |
|---|---|
| Every command group, subcommand, and instrument format | [reference/commands.md](reference/commands.md) |
| REST envelope, all stable error codes, reading `.data`/`.meta` | [reference/response-shape.md](reference/response-shape.md) |
| Auth: API key vs x402 wallet, budget-aware loops, `.meta.auth` | [reference/auth.md](reference/auth.md) |
| Time-series / catalog / snapshot flag tables, market & margin tokens | [reference/parameters.md](reference/parameters.md) |
| Live `ws` streaming: channels, wildcards, discriminators, reconnect | [reference/streaming.md](reference/streaming.md) |
| Order-book commands — snapshot vs stats shape, REST/WS parity | [reference/orderbooks.md](reference/orderbooks.md) |
| `dash` TUI dashboards (human-only) and their REST/WS equivalents | [reference/dashboards.md](reference/dashboards.md) |

When in doubt about a flag or shape, confirm with `laevitas <cmd> --help` or `laevitas commands -o json` rather than assuming — instrument names in `--help` are generated from today's date and may not match a real listing.
