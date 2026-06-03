# Order books — two shapes, REST/WS parity

There are **two distinct order-book shapes**. Picking the wrong one is the most common order-book mistake.

## Snapshot shape — `asks` + `bids` arrays + microprice + tier liquidity

"What does the book look like right now (or at a past timestamp)." Five surfaces, identical wire shape, identical flags:

```bash
laevitas perps       orderbook-raw <instrument>     # /api/v1/perpetuals/orderbook-raw
laevitas futures     orderbook-raw <instrument>     # /api/v1/futures/orderbook-raw
laevitas spot        orderbook-raw <instrument>     # /api/v1/spot/l2-orderbook-raw
laevitas predictions orderbook     <instrument>     # /api/v1/predictions/orderbook-raw
laevitas ws <market> book <exchange>:<instrument>   # WS stream of the same shape
```

Flags (identical across all five — the same value produces the same wire shape on every transport):

- `--depth N` — trim asks/bids to top-N levels.
- `--compact` — drop tier-aggregate fields (`ask_liquidity_*`, `bid_liquidity_*`, `imbalance_*`); keep raw asks/bids/microprice/metadata.

REST snapshot default is `-n 1` (current state). Pass `-n 50 -p 1h` for history, or `--start <ISO> --end <ISO>` for an exact past window.

## Stats shape — time-series of liquidity metrics (no asks/bids array)

"What was depth-N liquidity over time." Three surfaces:

```bash
laevitas perps   orderbook <instrument>   # /api/v1/perpetuals/orderbook
laevitas futures orderbook <instrument>   # /api/v1/futures/orderbook
laevitas spot    orderbook <instrument>   # /api/v1/spot/l2-orderbook
```

Each row is one bar (`-r 1m|5m|1h`) of OHLC liquidity stats across four depth tiers (10/20/50/100). `--depth N` picks which tier's columns surface in the compact table; full payload via `-o json`.

## Output defaults adapt to audience, not transport

| Output mode | Default depth | Why |
|---|---|---|
| `-o table` (TTY, human) | top-20 each side | fits one screen |
| `-o json` | full wire payload (~100 levels) | agents need full data |
| `-o csv` | full wire payload | agents need full data |
| WS NDJSON to stdout | full payload per event | agents need full data |

The display cap is render-time only; the wire payload is never silently trimmed for programmatic consumers. An agent on `-o json` or NDJSON always gets the full payload unless `--depth` is passed explicitly.

## Picking the right command

- "Current book, one call" → `<group> orderbook-raw <instr>` (REST snapshot)
- "Stream live book updates for processing" → `ws <market> book <exch>:<instr>` (NDJSON; `--depth N --compact` recommended)
- "How did depth-50 liquidity move over the last hour?" → `<group> orderbook <instr> -p 1h -r 1m --depth 50` (stats time-series)
- "Book at an exact past timestamp" → `<group> orderbook-raw <instr> --start <ISO> --end <ISO>`
- "Live multi-venue TUI" → `dash book <market> <symbol>` (human only — see dashboards.md)
