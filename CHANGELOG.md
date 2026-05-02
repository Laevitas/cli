# Changelog

All notable changes to the Laevitas CLI are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versions ≤ 0.4.0 are recorded in git tag annotations only; this file starts at 0.5.0.

## [0.8.6] — 2026-05-02

Two follow-up fixes from the v0.8.5 smoke test.

### Fixed

- **Watch-mode table renderer no longer dumps Go map literals for
  book arrays**. The v0.8.5 CSV/table fix (`internal/output/printer.go`'s
  `formatValue`) had a counterpart in `cmd/watch.go`'s `watchFmtValue`
  that still used the bare `fmt.Sprintf("%v")` fallback. Both paths now
  JSON-encode slices and maps so `laevitas watch 5s perps orderbook-raw …`
  renders parseable cells like `[{"price":78499,"size":31340},…]`
  instead of `[map[price:78499 size:31340] …]`.
- **`ws book BTCUSDT` (bare instrument, no `--exchange`) now surfaces
  a `Try:` hint**. v0.8.5 added validation that erred clearly but
  stopped short of suggesting the colon form. The error now mirrors
  the existing `wsArgHint` style:

      expected exchange:instrument, got "BTCUSDT"
      Try: laevitas ws perpetuals book <exchange>:BTCUSDT

  Market alias normalisation runs before hint construction so
  `ws perp book BTCUSDT` produces a hint with `perpetuals`.

## [0.8.5] — 2026-05-02

Reliability + parity follow-up to v0.8.4 — addresses agent feedback on
order-book commands, fixes CSV book serialisation, makes the WebSocket
client resilient to flaky first-attempt subscribes.

### Added

- **WS `--exchange` synthesis for single-pair subscribes**. WebSocket
  positional argument required `<exchange>:<instrument>` (because
  multi-pair fan-out is comma-separated colon pairs), but REST and
  `dash` accept a global `--exchange` flag. Single-pair `ws book` now
  honours `--exchange` and synthesises the colon form, so the same
  invocation pattern works across REST/dash/WS for the common case.
  Multi-pair subscribes still require explicit colons.
- **`--depth` validation** on every order-book surface. Negative values
  are rejected with a clear error before any HTTP/WS work happens; same
  validation runs on REST (`RunAndPrintFiltered`) and WS (`RunE`) so
  agents see consistent errors regardless of transport.

### Changed

- **wsclient subscribe is now self-healing**. Per-channel retry budget
  (3 attempts, 2s spacing) on top of the canonical bundled subscribe
  RPC. The success reply's `channels[]` echo is consulted to detect
  partial bundle acks — channels we asked for but the gateway dropped
  silently are re-armed for retry instead of leaving the session
  half-subscribed. Retry budgets reset on every reconnect.
  Addresses the "flaky first-attempt subscribe" symptom where a brief
  server-side validation race would silently kill data flow for the
  whole session.
- **`--depth` / `--compact` help text covers both shapes**. Earlier
  text was snapshot-only ("trim asks/bids to top-N levels"), which
  read as a no-op on stats orderbook commands. Help now explains both
  surface semantics in one description.

### Fixed

- **CSV asks/bids no longer dump as Go map literals**.
  `formatValue()` JSON-encodes slices and maps instead of falling back
  to `fmt.Sprintf("%v")`. CSV consumers can now parse the cells:
  `[{"price":78390.5,"size":140120},...]` rather than
  `[map[price:78390.5 size:140120] ...]`.
- **`laevitas watch` now resolves `perps orderbook-raw` and
  `futures orderbook-raw`**. The watch endpoint map missed both new
  commands at v0.8.4 release time; both are wired now.
- **REPL tab completion exposes `orderbook-raw`** under `perps` and
  `futures` (not just `spot`). Adding the command to the completer
  command tree was missed at v0.8.4 release time.

## [0.8.4] — 2026-05-02

REST/WS feature parity for order books, agent-friendly NDJSON trimming,
new `orderbook-raw` commands for perps and futures, dashboard quality-of-
life fixes from external agent feedback.

### Added

- **`laevitas perps orderbook-raw <instrument>`** and **`laevitas futures orderbook-raw <instrument>`** —
  REST snapshot commands matching the existing `spot orderbook-raw` and
  `predictions orderbook`. Closes the parity gap with `laevitas ws <market> book`,
  which already streamed the same shape. Both default to `-n 1` (current
  state) so a one-shot call returns one snapshot, not 100 historical rows.
- **`--depth N` / `--compact` flags** registered via the shared
  `output.AddBookFilterFlags` bundle on every order-book surface. Same
  flag names, same semantics adapted to the data shape:
  - **Snapshot shape** (`*-raw`, ws book): `--depth N` trims asks/bids
    to top-N levels; `--compact` drops tier-aggregate fields
    (`ask_liquidity_*`, `bid_liquidity_*`, `imbalance_*`), preserving
    asks/bids/microprice/metadata.
  - **Stats shape** (`<group> orderbook`): `--depth N` selects which
    tier's columns surface in the compact table view (10/20/50/100 —
    matching the four tiers the API computes).

  Same wire payload regardless of transport — REST and WS both call
  `output.ApplyBookFilter` on the same `BookFilterFlags` struct.

- **Inline ladder table renderer** (`internal/output/book_table.go`).
  Snapshot payloads (asks/bids arrays + microprice) now render as a
  centre-price ladder with cumulative-liquidity columns and a spread
  separator, matching the `dash book` and `ws book` ladders. Earlier
  versions dumped the raw Go map literal into the table — unreadable.
  Human-facing default caps at top-20 levels each side; agents using
  `-o json`/`-o csv` always get the full wire payload.
- **Tier 100 in the depth-tier cycle** (`d` key in TUI surfaces). The
  API exposes pre-computed liquidity stats at 10/20/50/100; the cycle
  now visits all four. Earlier it stopped at 50 because rendering 100
  rows was unbounded — now bounded by the row-cap chrome budget.

### Changed

- **Stale-venue annotation thresholds tightened** to 3s/15s (was 5s/30s).
  Agent feedback indicated the 30-second drop felt too patient for
  typical interaction windows. Stale annotation appears at 3s past
  feed-health proof; the venue is dropped from the "waiting on" footer
  at 15s.
- **WS `orderbook-raw` payloads pass through identical post-filter**.
  Both REST and WS now share the trim function, so an agent piping
  either transport with `--depth 5 --compact` sees the same shape.
- **Default snapshot table view caps at top-20 each side** for human
  readability. JSON / CSV / WS NDJSON paths are unaffected — agents
  always get the full wire payload unless `--depth` is passed
  explicitly. The cap is purely a render-time concern.

### Fixed

- **Firehose warning fires regardless of TTY.** Previously gated on
  stdout being a TTY, which silently dropped the warning for the most
  common agent invocation pattern (stdout piped to jq / file). Now
  the warning always goes to stderr.
- **Non-TTY dashboard error message is friendlier.** `laevitas dash book`
  on a piped stdout no longer surfaces tea's terse `/dev/tty` device
  error; instead points the user at `laevitas ws ...` for scripting.
- **WS book ladder header no longer scrolls off the alt-screen.**
  `ladder.RowCap` chrome budget tightened from 5 to 8 lines (the
  earlier value undercounted by 3); `wsrender.View()` adds a hard
  body clip so even if a renderer drifts past its row budget, the
  HeaderLine + StatsLine stay frozen at the top.
- **`--depth` on stats commands now picks the correct tier**. The
  table previously hardcoded tier-10 columns regardless of flag,
  silently hiding the deeper tiers (20/50/100) the API returned.

### Documented

- **REST/WS feature parity principle** (CLAUDE.md, docs/SKILL.md).
  Any flag added to one transport for a given data shape lands on
  every other transport surfacing the same shape, with byte-identical
  flag names, defaults, and semantics. Output-mode defaults adapt to
  audience (humans → top-20 cap; agents → full wire) but never to
  transport.
- **Snapshot-vs-stats distinction** spelled out for agents: which
  command answers which question, what `--depth` means in each shape,
  default `-n` behaviour.

### Deferred

- v0.8.5: per-channel retroactive subscribe in `internal/wsclient` so
  flaky first-subscribe self-heals without a dashboard restart.
- v0.9.0: heatmap mode (Bookmap-style price × time × size grid) and
  `dash flow` dashboard.
- Upstream x402 SDK vulnerability (`GO-2026-4647`) — no patched version
  available yet; no exposure on the API-key auth path. Track upstream
  for fix and bump when published.

## [0.8.3] — 2026-04-30

Multi-venue aggregated book dashboard, currency-driven contract resolution,
shared ladder primitives, and unified market-type vocabulary across CLI /
WS / REST.

### Added

- **`laevitas dash book <market> <symbol-or-currency>`** — multi-venue order
  book dashboard.
  - Aggregated centre-price ladder, segmented bars coloured by per-venue
    contribution, cumulative liquidity columns on each side
    (CUM_BID … PRICE … CUM_ASK).
  - Per-venue strip cards bordered in venue brand colour; CONSOLIDATED
    cross-venue summary with ARB detection on crossed books.
  - Two presentations toggled by `m`:
    - **Aggregated**: one merged ladder, segmented bars by venue.
    - **Split**: one narrow per-venue ladder column side-by-side.
  - Header sparkline (~60-tick microprice ring) + smart "waiting on" footer
    that annotates stale venues (`venue (stale 12s)`) past 5s and drops them
    past 30s, gated on connection-health proof.
  - Keys: `+/-` group, `d` depth, `c` recenter, `m` mode, `v` venues,
    `p` pause, `j/k`/`PgUp`/`PgDn`/`g/G` scroll, `?` help, `q` quit.
- **Currency-driven contract resolution.** `laevitas dash book perpetuals BTC
  --margin linear` resolves to each venue's canonical contract (BTCUSDT on
  binance, BTC-USDT-SWAP on okx, BTC-USD on hyperliquid, …) via the
  instruments registry. Per-venue quote-currency cascade `USDT → USDC → USD`,
  override with `--quote`. Spot mode (`dash book spot BTC`) follows the same
  cascade. Legacy literal mode (`dash book perpetuals BTCUSDT`) still works.
- **Shared `internal/ladder` package.** Group-tick cycle, depth-tier cycle,
  bucketing, `Viewport` scroll/page/recenter, `MicroRing` sparkline buffer,
  `HeaderLine` and `StatsLine` renderers. Two surfaces (`laevitas ws book`
  and `laevitas dash book`), one implementation.
- **Shared `internal/output/layout.go`.** `JoinSideBySide`, `PadRightAnsi`,
  `TruncateAnsi`, `VisibleWidth` — ANSI- and unicode-cell-aware via lipgloss.
  Replaces three near-identical local copies in `wsrender` and `panels`.
- **Market-type aliases.** Every CLI flag and positional that accepts a
  market type now normalises any common alias (perp / perpetual /
  perpetuals / swap / fut / future / futures / opt / option / options /
  spot / predictions / poly / …) to the canonical plural form. Same for
  margin types (linear / lin / usdt / inverse / inv / coin / …). The
  REST/WS layer translation is hidden — internal code only sees one form.
  See README "Market type aliases" and `internal/api/markets.go`.
- `--margin` short alias for `--margin-type` on `instruments list`.

### Changed

- `laevitas ws perpetuals book <pair>` (legacy single-venue ladder) and
  `laevitas dash book` now share an identical two-line top header
  (`▲ <surface>  <pair>  N snapshots  X.X/s  PAUSED` + stats line) and
  identical footer hints / help overlay via `internal/keymap`. Moving
  between surfaces reads the same metrics in the same order.
- `+/-` is now price grouping on both book surfaces (was depth-tier on the
  legacy ladder); `d` cycles depth tier, `c` recenters viewport.
- Em-dash `—` zero-placeholder replaced with ASCII `-` everywhere — the
  em-dash glyph is East-Asian-Ambiguous Unicode and breaks bordered-card
  width math on Windows Terminal and similar.

### Fixed

- Bordered cards in the venue strip no longer escape their borders when
  wrapping wide content. Cause was a custom byte-counting `stripAnsiLen`
  helper that over-counted multi-byte UTF-8 glyphs (e.g. `▮` segmented bar);
  replaced with `lipgloss.Width` everywhere.
- Venue card content is composed via `lipgloss.JoinHorizontal` instead of
  `+`-concatenated styled strings, fixing nested-ANSI width measurement
  drift in the CONSOLIDATED block.
- "Waiting on …" footer no longer mentions venues that don't list the
  active contract (coinbase has no USDT perp; deribit has only USDC linear;
  hyperliquid has no inverse perp). Expected-venue list comes from the
  instruments registry, not a hardcoded curated palette.

### Deferred

- **v0.8.4**: Per-channel retroactive subscribe in `internal/wsclient` so
  flaky first-subscribe (occasional binance "didn't show up on first open")
  self-heals without a dashboard restart. Belongs in the WS client layer,
  not per-panel.
- **v0.9.0**: Heatmap mode (Bookmap-style price × time × size grid),
  `dash flow` dashboard.

## [0.8.2] — 2026-04-30

L2 order book streaming, channel wildcards, and a long-standing
default-exchange bug that was silently scoping every cross-product query
to a single venue.

### Added

- **`book` stream** for `laevitas ws perpetuals|futures|spot|predictions`.
  Channel grammar: `book.{market}.{exchange}.{instrument}`. Options is
  not supported — venues don't expose L2 for options. Two views:
  - **Ladder** (single-pair): centre-price Bloomberg DEPT layout — bids
    on the left growing toward the price column, asks on the right
    growing away from it, spread separator centred. Sizes log-scaled so
    a single 50 BTC level doesn't visually swallow every smaller quote.
    Header shows MID + 8-tick microprice sparkline (colored by net
    direction), spread bps, imbalance, and tier liquidity. Levels that
    grew or shrank by ≥10% since the last snapshot get a `↑` / `↓`
    glyph for ~250ms; levels holding ≥30% of their side's tier
    liquidity get a `▲` "whale" badge.
  - **Scan** (multi-pair): one summary row per discovered (exchange,
    instrument), sorted alphabetically. Columns: PAIR, BID SZ, BID,
    SPREAD, BPS, ASK, ASK SZ, MICRO, IMB10, UPD. Arrow keys / `j`/`k`
    to move the cursor; `Enter` to drill into the ladder for that row;
    `Esc` to return.

  Auto-layout picks ladder for a single concrete pair, scan for any
  multi-pair or wildcard subscription. Override with `--layout=scan` or
  `--layout=ladder`. NDJSON mode emits the wire envelope unchanged for
  scripting and replay.

- **Channel wildcards** (`*` per segment) on every stream.
  One wildcard pattern counts as one subscription against the server's
  200-per-connection cap; the resolved concrete channel arrives on each
  event so existing renderer dispatch keeps working unchanged.

  Examples:
  ```
  laevitas ws perpetuals book "*:BTCUSDT"             # BTCUSDT perp on every venue
  laevitas ws "*" trades "binance:BTCUSDT"            # binance BTCUSDT across markets
  laevitas ws perpetuals liquidations "*:*"           # every perp liquidation
  ```

  Wildcards are rejected client-side in the three positions the server
  explicitly disallows: stream name (`*` channel-type breaks payload
  shape), OHLC `dataType`, and OHLC `--tf`. PowerShell users: quote `*`
  so the shell doesn't expand it to filenames before laevitas sees it.
  When laevitas detects probable shell-glob expansion (e.g. cwd had a
  matching file), it surfaces a targeted error pointing to the quoting
  fix.

  Patterns with two or more wildcard segments (`trades.*.*.*`,
  `book.*.*.*`) print a stderr warning before dialing — those can
  deliver thousands of events per second and trip the server's
  slow-consumer disconnect (close code 4003).

- **EXCHANGE column** appears between TIME and INSTRUMENT in trades,
  liquidations, ticker, and vt views whenever any subscribed pattern
  has `*` in the exchange position. Decided once at subscribe time
  from the channel patterns — column structure is fixed for the
  session, never flickers in/out depending on which venue happened to
  fire most recently. Concrete single-venue subs render unchanged (no
  EXCHANGE column).

- **Predictions object-level normalizer.** The `book.predictions.*`
  channel currently emits levels as `{price, size}` objects on the
  producer side while every other market emits `[price, size]` tuples
  (producer fix in flight per the API team). The CLI accepts both
  forms transparently so prediction books render today.

### Fixed

- **Cross-product endpoints no longer silently scope to the default
  exchange.** `perps catalog --currency CL` would return zero results
  because the CLI was injecting `exchange=deribit` (the config
  default) into every call, including catalog endpoints that span
  every venue. Same bug on `futures catalog`, `options catalog`, and
  `instruments list`. The fix introduces a `cmdutil.ExchangeExplicit`
  flag that distinguishes "user explicitly passed `--exchange`" from
  "exchange came from config defaults"; `ToParams()` now only injects
  the exchange in the explicit case. Concrete-instrument commands
  (snapshot, ohlcvt, etc.) keep their existing default behaviour
  because they require an exchange.
- **`--exchange ""` now actually clears.** Previously the empty-string
  override was silently dropped before reaching the API client.
- **Footer no longer mislabels cross-product calls.** When the CLI
  isn't filtering by exchange, the footer's `· deribit` segment is
  suppressed. The tag only shows when the request actually carried
  one.
- **Book wildcard scan navigation works.** Cursor bounds were checked
  against the subscription list rather than the discovered pairs, so
  arrow-down was a no-op for any wildcard subscription. Now bounded
  against the live discovered set; rows populate as venues fire and
  the cursor extends to match.
- **Book wildcard auto-layout.** Any subscription containing `*`
  defaults to scan view regardless of pattern count — single
  concrete-pair → ladder, anything else → scan.

### Changed

- **`Push` on the book renderer** now diffs against the previous
  snapshot under the same lock to record per-level direction (`+1`
  built, `-1` eaten/pulled), used by the flash glyphs. The microprice
  ring buffer is updated in the same critical section.
- **Standardised TUI keybindings across every surface.** Same key
  vocabulary, same wording, same order — defined once in
  `internal/wsrender/keymap.go` and consumed by every renderer
  (rolling tape, book scan, book ladder). Keys mirror the k9s + less
  + vim conventions:

  | Action | Keys |
  |---|---|
  | quit | `q` `Q` `Ctrl+C` |
  | pause / resume | `p` `P` |
  | help overlay | `?` `h` `H` |
  | back / close help | `Esc` |
  | select previous | `↑` `k` |
  | select next | `↓` `j` |
  | page up | `PgUp` `b` |
  | page down | `PgDn` `f` |
  | top / bottom | `g` / `G` (or `Home` / `End`) |
  | drill into selected | `Enter` |
  | depth tier (ladder) | `+` / `-` |
  | wheel scroll | mouse wheel up / down |

  Press `?` in any TUI surface for a context-aware overlay listing
  the active keys. Mouse wheel adds natural scrolling on lists; click
  events are deliberately NOT consumed so the terminal keeps its
  native click-drag-to-select for copy-paste (hold `Shift` while
  dragging on most terminals, or `Alt` in VS Code, to bypass any
  remaining mouse capture).
- **Rolling-tape ring buffer bumped to 100 events** (was 18). On tall
  iTerm / Windows Terminal / VS Code windows the table now fills the
  screen instead of leaving the bottom 70% blank. Trimmed at render
  time per terminal height; ~50 KB resident memory cost.

## [0.8.1] — 2026-04-29

Streaming hot-fix + liquidations channel. The gateway shipped two changes
the same week: v1.18.0 retired query-string auth on the WS upgrade, and
v1.21.0 added a dedicated `liquidations` channel for forced-close events
on perpetuals and futures. v0.8.1 adapts to both — the auth path is the
priority because v0.8.0 hosts cannot subscribe to anything without it.

### Fixed

- **WS upgrade auth.** The streaming gateway removed `?apiKey=...` query
  string authentication in API v1.18.0. v0.8.0 used that path exclusively,
  so every subscribe started returning 401 the moment the gateway picked
  up the new build. v0.8.1 sends the API key as the `apikey` upgrade
  header instead, matching the v1.18.0 / v1.19.0 contract. **Upgrade
  required** for any host running v0.8.0 against the current gateway.
- **Auth failures now exit cleanly.** When the server rejects a key —
  either via close code 4001 or via a JSON-RPC `{code: 401}` error frame —
  the client no longer reconnects in a tight loop. It emits the warning,
  then exits non-zero with the same message so consumers can branch on it.
- **Server close codes are surfaced individually.** v0.8.0 collapsed every
  disconnect into "connection lost; reconnecting." v0.8.1 distinguishes
  4001 (auth — fatal), 4002 (idle), 4003 (slow consumer), 4004 (24h
  lifetime), 4005 (conn cap — fatal), 4008 (rate limit). Each one now
  prints a specific message so the cause is obvious.

### Added

- **`liquidations` stream** for `laevitas ws perpetuals` and `laevitas ws futures`.
  Channel grammar: `liquidations.{market}.{exchange}.{instrument}`. Emits
  the v1.21.0 forced-close event shape — `position_side`, `direction`,
  `price`, `amount`, `amount_usd`, `mark_price`, `index_price`, plus the
  usual instrument metadata. Live-table mode color-codes the
  `position_side` column (red `LONG`, green `SHORT`) so the directional
  bias of each flush reads at a glance; NDJSON mode emits the raw
  envelope unchanged.
- **Per-stream market allowlist.** Streams that don't apply to every
  market (today: just `liquidations`, which only exists on derivatives)
  are gated client-side. `laevitas ws spot liquidations binance:BTCUSDT` now
  errors with `stream "liquidations" is only available for: futures,
  perpetuals` instead of opening a doomed subscription.

### Changed

- The `--tf` rejection message now names the actual stream the user
  passed (`--tf only applies to ticker and vt streams, not liquidations`)
  instead of always saying "trades."

## [0.8.0] — 2026-04-29

WebSocket release. The streaming gateway hit v1.17.0 with a coverage matrix
finally broad enough to ship a generic `ws` command against — all five
markets (perpetuals, futures, options, spot, predictions), all three channel
types (trades, ohlc.ticker, ohlc.vt), all eight timeframes. v0.8.0 adds the
`laevitas ws` command that subscribes to those channels and emits NDJSON to
stdout — pipe-friendly, agent-friendly, raw-tape friendly.

### Added

- **`laevitas ws <market> <stream> <exchange:instrument>[,...]`** — subscribe
  to live channels via the native WebSocket gateway at
  `wss://apiv2.laevitas.ch/ws`. Two output modes, picked automatically:
  - **TTY (interactive shell)** — live-updating table renders in raw
    terminal mode with brand-styled header (channel + update count + rate),
    rolling window of the most recent events, per-channel-type column
    layouts (trades / ticker / vt / spot trades / options trades /
    predictions trades), green/red coloring on side and price direction,
    `q` to quit.
  - **Pipe / non-TTY** — NDJSON, one `{"channel": "...", "data": {...}}`
    per line. Pipe-friendly for agents and `jq`/`grep`/`tee`.

  Override with `-o json` (force NDJSON even in a TTY, e.g. when piping
  through `tee` to keep both a file and a live view) or `-o table` (force
  live mode even when piped — rarely useful, mostly for debugging).
  Multiple `<exchange:instrument>` pairs share a single connection.
- **Client-side validation** of every channel before opening the socket:
  market in `{perpetuals, futures, options, spot, predictions}`, stream in
  `{trades, ticker, vt}`, exchange in the per-market list from the API
  matrix, timeframe in `{1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d}`. Typos get
  rejected with explicit "valid options for X are: ..." errors instead of
  silent gateway timeouts.
- **`--tf <timeframe>`** flag — required for `ticker` / `vt` streams,
  rejected loudly when used with `trades`. Default `1m`.
- **Auto-reconnect** with exponential backoff (1s → 30s, then steady) and
  full re-subscription on each reconnect. Reconnect events surface on
  stderr in TTY mode, as JSON warnings on non-TTY for agent log parsing.
- **App-level ping** every 25s plus a 60s receive timeout, so a hung
  connection (TCP-stuck, no data, no PINGs) gets detected and reconnected
  rather than silently dropping events.
- **Deprecation nudge** when a perpetual instrument is subscribed via the
  legacy `futures` channel (`laevitas ws futures trades binance:BTCUSDT`) —
  prints a one-line stderr warning recommending `perpetuals` and continues
  to subscribe so the user still gets data while the legacy alias is alive
  one more minor.
- **`Ctrl-C` graceful shutdown** — closes the socket cleanly, exits 0.
- **TTY header** before the data stream starts: `▲ subscribed: N channels,
  Ctrl-C to exit` followed by the resolved channel list. Skipped when piped
  so NDJSON output stays clean.

### Internal

- New `internal/wsclient/` package — thin native-WS client wrapping
  `github.com/coder/websocket`. Owns the JSON-RPC subscribe/unsubscribe/ping
  protocol, reconnect loop, and channel-based event delivery.
- New `internal/wsrender/` package — live-table renderer used by the TTY
  path. Built on `github.com/charmbracelet/bubbletea` (alt-screen + frame
  diffing) so Windows Terminal in raw mode redraws cleanly instead of
  scrolling. Owns the per-channel column layouts (trades / ticker / vt /
  spot / options / predictions) and the rolling event buffer. Soft errors
  from the wsclient surface in the table footer rather than on stderr
  (which would corrupt the rendered frame).
- New transitive deps from Bubble Tea: `bubbletea`, `muesli/ansi`,
  `muesli/cancelreader`, `muesli/termenv` (upgraded), `xo/terminfo`,
  `mattn/go-runewidth` (upgraded).
- New `cmd/ws/ws.go` — cobra command + validation tables. Validation
  tables (`marketExchanges`, `validStreams`, `validTimeframes`) are
  hardcoded from the v1.17.0 matrix and need to be updated in lockstep when
  the matrix expands.
- `cmd/root.go` and `internal/completer/completer.go` — register `ws` as a
  top-level command. No `commandTree` entry because `ws` takes positional
  args, not subcommands.
- New transitive dependency: `github.com/coder/websocket v1.8.14`.

### Not yet supported

- **x402 wallet auth on WS.** The streaming gateway only accepts API-key
  auth today. Calling `laevitas ws` while in
  `LAEVITAS_AUTH=x402` mode returns a clear error pointing at the gap.
  Will land when the gateway accepts the same auth methods as REST.
- **Auto-route shorthand (`laevitas ws binance:BTCUSDT`).** Looking up the
  market via `/instruments/detail` to skip a positional arg is queued for
  v0.8.x; for now the user passes `<market> <stream> <exchange:instrument>`
  explicitly.

## [0.7.0] — 2026-04-29

x402 release. The pay-per-request authentication path was fully plumbed
internally as of 0.5.0 but never surfaced — users had to know the magic words
(`config set wallet_key`) to access it. v0.7.0 turns it into a first-class
command group, makes its state visible in every JSON response, and gives
agents stable error codes to branch on. WebSocket streaming was scoped for
this release but deferred — the underlying API channel matrix is being
reshaped on the API side.

### Added — `wallet` command group

- **`laevitas wallet`** (or `wallet show`) — display address, auth mode,
  cached credit token state (with expiration parsed from the JWT), API key
  presence, and most-recent credits-remaining count. Pretty terminal output
  for humans (`-o table`/`-o auto` in a TTY); JSON envelope for agents
  (`-o json`/`-o auto` when piped).
- **`laevitas wallet init`** — interactive setup. Prompts for an EVM
  private key, validates by deriving the address before saving, and clears
  any stale credit token from a previous wallet.
- **`laevitas wallet set-key <hex>`** — non-interactive equivalent for
  scripts. Validates and saves in one step.
- **`laevitas wallet unset`** — clear the wallet key and any cached credit
  token. Use before sharing a config dump or rotating wallets.
- **`laevitas wallet address`** — print just the address (pipe-friendly,
  exits non-zero with an empty line if no wallet is configured).
- **`laevitas wallet credits`** — print credits remaining from the most
  recent x402 response (prints `unknown` until the next data request).

### Added — agent surface

- **`meta.auth`, `meta.credits_remaining`, `meta.latency_ms`** in every
  success JSON envelope. Agents can now read auth method and credit balance
  from any response without parsing stderr or inspecting headers.
- **Three new stable error codes** in the JSON error envelope:
  - `WALLET_NOT_CONFIGURED` — wallet path requested but no key set
  - `INSUFFICIENT_BALANCE` — wallet exists but doesn't have USDC on Base
  - `PAYMENT_REJECTED` — server validated and rejected the signed payment
- New `PaymentError` type in `internal/api/client.go` with a `Code()` method
  that maps to the codes above; `printErrorEnvelope` recognises it and
  surfaces `wallet_address` in the error block when available.

### Changed — UI

- **First-run onboarding offers a choice.** Old flow assumed an API key.
  New flow presents three paths: (1) API key, (2) x402 wallet, (3) skip.
  The wallet path validates the private key by deriving the address before
  saving and prints the address so the user knows where to send USDC.
- **Improved request-meta footer.** Format is now
  `▲ <auth> · <latency> · <records> · <exchange> · <credits>` with the
  brand-green ▲ glyph at the start, auth method bolded, and credits
  rendered in yellow when below 50 remaining.

### Internal

- New file `cmd/wallet/wallet.go`.
- `cmd/root.go` and `internal/completer/completer.go` extended for the
  wallet group.
- `internal/cmdutil/cmdutil.go` — `wrapSuccessEnvelope` now takes a
  `*api.RequestMeta` and threads request-time fields into the envelope's
  meta block via `buildMeta`. `promptOnboarding` rewritten as a chooser
  that delegates to `onboardAPIKey` or `onboardWallet`.
- `internal/api/client.go` — `handlePaymentRequired` returns typed
  `PaymentError` instead of generic `APIError`; signing failures with
  "insufficient" in the error string get classified as
  `INSUFFICIENT_BALANCE`.

### Deferred

- **WebSocket streaming (`laevitas ws`).** Scoped for v0.7.0; deferred
  pending an API-side reshape of the channel matrix. The current WS surface
  only supports `futures` (umbrella) and `options` markets, and only
  `trades`/`ohlc.ticker`/`ohlc.vt` channel types — too narrow to ship a
  generic `stream` command against. Will land in a follow-on release once
  the underlying matrix expands.

## [0.6.0] — 2026-04-28

Major release. Two headline changes: a stable JSON envelope (breaking for any
consumer that parses `-o json` output), and a refreshed terminal UI tuned to
the Laevitas brand palette. Also bundles an `analytics` command group, the
`base_currency` fix on `instruments list`, and a slate of table-rendering fixes
that make every command read better.

### Changed (BREAKING — JSON consumers)

- **JSON output is now wrapped in a stable envelope.** Every `-o json` response
  — and the JSON path of `-o auto` when stdout is not a TTY — emits one of two
  shapes:
  - **Success:** `{"success": true, "data": [...], "meta": {...}}`
  - **Failure:** `{"success": false, "error": {"message": "...", "code": "...", "status": 401, "endpoint": "..."}}`
- Existing scripts that read fields from the old bare data array must update to
  read from `.data` instead. A top-level array path such as `.[0].mark_price`
  becomes `.data[0].mark_price`.
- Errors now write to **stdout as JSON** (instead of stderr free-text) when
  output is JSON. A single shape works for both success and failure parsing —
  no more switching between stdout and stderr. Exit code remains non-zero for
  any error.

### Added

- **Stable error codes** in the JSON error envelope. Branch on `.error.code`,
  not `.error.message`:
  - `AUTH_INVALID` (401)
  - `AUTH_FORBIDDEN` (403)
  - `RATE_LIMITED` (429)
  - `PAYMENT_REQUIRED` (402)
  - `BAD_REQUEST` (4xx other than the above)
  - `NOT_FOUND` (404)
  - `SERVER_ERROR` (5xx)
  - `NETWORK_ERROR` (DNS / TCP / timeout)
  - `UNKNOWN_ERROR` (fallback)
- **`analytics` command group** with `realized-volatility` (alias `rv`). Wraps
  the `/api/v1/analytics/realized-volatility` endpoint. Supports both modes:
  - **Snapshot mode** (no `-p`/`--start`/`--end`/`-n`/`--cursor`): latest reading
    per `(estimator, frequency, window_days)` combination.
  - **Historical mode** (any time-window flag set): paginated time-series.
  - Flags: `--instrument` (required), `--window-days {7,30,60,90,180,365}`,
    `--estimator {close_to_close,parkinson,garman_klass}`,
    `--frequency {daily,hourly}`, `--currency`, `--date` (snapshot mode only).
- New `RequestParams` fields: `Frequency`, `WindowDays`, `Estimator`,
  `BaseCurrency`. Wired through `buildURL`.

### Changed (UI — terminal output)

- **Branded REPL banner.** Replaced the six-line ASCII block-letter banner with
  a single line: `▲ LAEVITAS  v0.6.0` in brand green, followed by a dim tagline.
  Truecolor uses the exact brand palette (`#46be52` primary, `#475057` mid-grey).
- **Branded REPL prompt.** Old `LAEVITAS > ` becomes `▲ ›` — short, branded,
  ergonomic. Uses 8-color ANSI in the prompt itself (truecolor would crash
  `chzyer/readline`'s Windows ANSI shim); banner above it uses full truecolor.
- **Removed cosmetic dot-separator rows** that interrupted table output every
  five rows. They were purely decorative and broke visual flow.
- **Column order now matches the API response.** Replaced the static
  `columnPriorities` map (130 lines, hand-tuned, drift-prone) with a JSON-byte
  walk that recovers the API's source order. Columns now arrive grouped by
  base name (e.g. `funding_rate_*` together, `basis_*` together) instead of
  scattered by suffix priority.

### Fixed

- **`instruments list --base-currency` actually filters now.** The CLI was
  sending `?currency=BTC` to the registry endpoint, which silently ignored it
  (the registry expects `?base_currency`). Result: filtering by BTC was
  returning rows for ADA, AVAX, etc. — every contract alphabetically. New
  `BaseCurrency` field on `RequestParams` is wired separately from `Currency`
  so product catalogs and the cross-product registry coexist.
- **`options trades --top-n` now works.** The flag was registered on
  `options flow` but missing from `options trades`, even though the API
  documents `top_n` for options trades and every other product's `trades`
  command (futures/perps/spot) has it. Now consistent across all four.
- **`predictions snapshot --keyword` and `--instrument` now work.** The API
  accepts both for filtering snapshot responses, but the CLI only registered
  `--category`, `--event`, `--date`, `--resolution`. Calling with `--keyword`
  failed with `unknown flag` even though the same flag was accepted on
  `predictions catalog`. Both flags now registered and forwarded.
- **`predictions` commands now default `exchange=polymarket`.** The global
  `--exchange` flag defaults to `deribit` for the rest of the CLI, and that
  value was leaking into every predictions request as `?exchange=deribit` —
  meaningless on a Polymarket-only endpoint. New `predictionsExchange()`
  helper in `cmd/predictions/predictions.go` mirrors the pattern from
  `cmd/spot/spot.go` (where deribit isn't valid either) and pins the value
  to `polymarket` unless the user explicitly chose a known prediction-market
  exchange.

### Security

- **Bumped Go toolchain from 1.25.7 to 1.25.9.** Closes four stdlib
  vulnerabilities flagged by `govulncheck`:
  - `GO-2026-4870` / `CVE-2026-32283` — TLS 1.3 KeyUpdate DoS in `crypto/tls`
    (reachable from every HTTPS request the CLI makes).
  - `GO-2026-4947` / `GO-2026-4946` — chain-building and policy-validation
    issues in `crypto/x509` (reachable through TLS verification).
  - `GO-2026-4869` — unbounded allocation for old GNU sparse archives in
    `archive/tar` (reachable from `laevitas update`'s archive extraction).

  One advisory remains open (`GO-2026-4647` against
  `github.com/coinbase/x402/go`) — there is no patched upstream version yet,
  so it ships as-is. Will be addressed when Coinbase publishes a fix.

### Coordinated with API v1.16.0

`/api/v1/instruments` and `/api/v1/instruments/detail` were returning every
historical snapshot of each contract because the underlying ClickHouse table
is append-only and the service did no dedup. Reported during this release
cycle, fixed server-side in the same window. Post-API-deploy:

- `instruments list` returns one row per `(exchange, instrument_name)` —
  `--limit 20` now means "20 distinct contracts," matching the contract the
  flag implies.
- `meta.count` reports distinct contracts (`uniqExact` over the dedup tuple)
  rather than raw rows, so pagination decisions are trustworthy.
- `instruments detail` deterministically returns the latest snapshot rather
  than whichever row ClickHouse happened to surface first.

No CLI changes needed for the dedup fix — the client just consumes whatever
shape the API returns. The combination of the v0.6.0 CLI `base_currency`
wiring fix above and the v1.16.0 API dedup fix is what makes
`instruments list --base-currency BTC` actually do what it says.

### Removed

- `columnPriorities` map and `columnWeight` function in
  `internal/output/printer.go`. 130 lines of dead code; no callers remain
  after the API-order migration.

### Unchanged

- **Table output (`-o table`)** is not enveloped — table is row-oriented and
  the envelope is JSON-only. Stderr metadata footer, inline charts, and the
  pagination hint are all untouched.
- **CSV output (`-o csv`)** is not enveloped for the same reason.
- The `-o` flag itself: still `auto | json | table | csv`. No new aliases like
  `--table` / `--pretty` / `--json`.

### Migration notes

JSON parsers need a one-line update — read fields under `.data` instead of
from the top-level array:

```sh
# before v0.6.0, output was a bare array
laevitas perps carry BTC-PERPETUAL -o json | jq '<top-level array path>'

# v0.6.0 onward
laevitas perps carry BTC-PERPETUAL -o json | jq '.data[0].funding_rate_close'
```

Error handling becomes simpler:

```sh
RESP=$(laevitas futures snapshot --currency BTC -o json)
if [ "$(echo "$RESP" | jq -r '.success')" = "true" ]; then
  echo "$RESP" | jq '.data'
else
  echo "$RESP" | jq -r '.error.code, .error.message'
fi
```

### Internal

- New file `internal/cmdutil/examples.go` (added in 0.5.0; mentioned here
  because token substitution still drives help text).
- New `cmd/analytics/analytics.go`.
- New endpoint constant `AnalyticsRealizedVolatility` in
  `internal/api/endpoints.go`.
- `cmd/root.go`, `cmd/watch.go` endpoint map, and
  `internal/completer/completer.go` command tree extended for the analytics
  group.
- New helpers `wrapSuccessEnvelope`, `buildMeta`, and `printErrorEnvelope` in
  `internal/cmdutil/cmdutil.go`. New `APIError.Code()` method and
  `NetworkError.Code()` method in `internal/api/client.go`.
- `internal/output/printer.go` now extracts column order from raw JSON via a
  one-pass token walk (`extractFirstObjectKeyOrder`, `readObjectKeys`,
  `skipJSONValue`, `collectKeys`).

## [0.5.2] — 2026-04-28

### Fixed

- **`laevitas update` now works.** The self-update command in 0.5.0 still asked for the pre-GoReleaser asset name (`laevitas-<os>-<arch>`) and 404'd on every platform. It now resolves GoReleaser archives (`laevitas_<ver>_<OS>_<ARCH>.{tar.gz,zip}`), verifies the SHA-256 against `checksums.txt`, and extracts the binary before the existing atomic-replace step. Linux/arm64 hosts (e.g. Hetzner CAX) are unblocked.
- **`install.ps1` detects arm64 Windows.** Previously hard-coded `x86_64` on any 64-bit Windows; now branches on `RuntimeInformation.OSArchitecture` and picks `arm64` when appropriate.

### Added

- **`windows/arm64` build target.** Removed the `ignore:` block in `.goreleaser.yaml`; releases now ship six archives instead of five.

### Release engineering

- **`cli.laevitas.ch` now has CI/CD.** New `.github/workflows/deploy-site.yml` copies the canonical root `install.{ps1,sh}` into `site/` and deploys the `cli` Cloudflare Pages project on every push to `main` that touches `site/**` or the install scripts. Requires repo secrets `CF_API_TOKEN` and `CF_ACCOUNT_ID`.
- **Removed duplicate install scripts.** `site/install.ps1` and `site/install.sh` are deleted from git and `.gitignore`d; they are now generated at deploy time from the root copies, so the asset-naming convention has a single source of truth.

## [0.5.0] — 2026-04-27

### Added

- **`spot` command group** (10 subcommands): `catalog`, `snapshot`, `metadata`, `ohlcvt`, `ticker`, `volume`, `level1`, `orderbook`, `orderbook-raw`, `trades`. Default exchange is `binance` (deribit doesn't trade spot); also supports coinbase, bybit, okx, kraken, bullish. Use spot as a reference-price layer for derivatives basis calculations.
- **`instruments` command group** (`list`, `detail`): cross-product instrument registry. `list` browses contract specs across spot/perpetual/future/option on every supported exchange with filters for market type, currency, status, expiry range, margin type, option type, and instrument-name partial match. `detail` returns the full contract specification including raw exchange API data.
- **`--sort-dir` flag** registered globally on every time-series command via `AddCommonFlags`. Accepts `ASC` or `DESC`. Previously was only declared (and accepted) on `trades`/`liquidations`/`trades-summary`; now uniform across all time-series commands.
- **Pagination + filters on every catalog command.** `futures catalog`, `perps catalog`, `options catalog`, `predictions catalog`, and the new `spot catalog` now accept `-n`/`--limit` and `--cursor`. Per-product filters added: `--maturity` (futures, perps, options), `--option-type`/`--strike-min`/`--strike-max` (options), `--quote-currency` (spot).
- **Dynamic instrument-name examples in `--help` output.** Example expiries like `BTC-26JUN26` are now computed from `time.Now()` at startup. New helpers in `internal/cmdutil/examples.go`: `ExampleFuturesInstrument`, `ExampleNearTermFuturesInstrument`, `ExampleOptionInstrument`, `ExampleMaturity`, plus a `SubstituteExamplesRecursive` walker called once from `cmd/root.go` that replaces `{{FUT}}` / `{{OPT_C}}` / `{{OPT_P}}` / `{{MAT}}` tokens in cobra `Long` / `Example` / `Short` strings.
- New `RequestParams` fields wired through `buildURL`: `QuoteCurrency`, `MinQuoteAmount`, `StrikeMin`, `StrikeMax`, `MarketType`, `Status`, `MarginType`, `ExpiryFrom`, `ExpiryTo`.

### Changed

- **Time-series default sort is now `DESC` (newest first).** `CommonFlags.ToParams()` defaults `SortDir = "DESC"` when the caller did not pass `--sort-dir` and did not pass `--cursor`. Row 0 of any time-series JSON response is now the most recent record in the window. To opt back into chronological iteration, pass `--sort-dir ASC`. When `--cursor` is set, the CLI does NOT inject a default direction so paginated scans keep the direction they started with.
  - This depends on a coordinated API change that adds `sort_dir` support to time-series endpoints (`carry`, `ohlcvt`, `oi`, `volume`, `ticker`, `level1`, `l2-orderbook`, `volatility`, `vol-surface/history`, `vol-surface/by-expiry`). Endpoints that already supported `sort_dir` (`trades`, `liquidations`, `trades-summary`) continue to behave as before.
- **Inline charts always render chronologically.** When a response is requested DESC, `RunAndPrint` reverses the array client-side before passing it to `RenderChart`, so charts still draw oldest→newest left-to-right regardless of how the JSON/table is sorted.
- **Pagination hint text** reads "→ Older results available" (instead of "→ More results available") when in DESC mode, since `--cursor` walks backward in time when sorting DESC.
- **Documentation overhaul.** README and `docs/SKILL.md` parameter tables are now split into Time-Series, Catalog, and Snapshot sections to remove the false implication that snapshot endpoints accept `-n`/`--start`/etc. (Snapshots are point-in-time and never paginate.) `CLAUDE.md` updated with the corrected catalog rule and new mistakes-to-avoid entries for sort_dir defaults, chart reversal, dynamic example tokens, and the `--sort-dir` flag-redefinition pitfall.

### Fixed

- Catalog commands previously silently ignored their pagination and filter parameters because the CLI only sent `--exchange`. They now forward every filter the API documents.
- Removed duplicate `SortDir` field declarations on local `tradesFlags` / `liquidationsFlags` structs that previously shadowed `CommonFlags.SortDir`.

### Internal

- New file `internal/cmdutil/examples.go`.
- New endpoint constants in `internal/api/endpoints.go`: `Spot*` (10 entries) and `Instruments{List,Detail}`.
- `cmd/spot/spot.go` and `cmd/instruments/instruments.go` added.
- `cmd/watch.go` endpoint map and `internal/completer/completer.go` command tree extended for both new groups.
- `internal/completer/completer.go` `catalogEndpoints` map now includes `spot` for instrument autocompletion.

### Release engineering

- **GoReleaser-driven release pipeline.** `.goreleaser.yaml` now drives cross-compilation (linux/darwin/windows × amd64/arm64), archive packaging (tar.gz / zip), checksum generation, GitHub Release publishing, Homebrew tap formula updates, and Scoop bucket manifest updates. Triggered by `git push <vX.Y.Z tag>` via `.github/workflows/release.yml`.
- **`install.sh` and `install.ps1`** at the repo root. Detect OS/arch, fetch the latest (or pinned) release tarball/zip, verify checksum, install to `$HOME/.local/bin` (POSIX) or `$env:USERPROFILE\bin` (Windows), and warn / auto-update PATH.
- **CI split.** `.github/workflows/ci.yml` is now test-only. Release lives in `.github/workflows/release.yml` and runs only on `v*` tag pushes.
- **Makefile**: `make release` now defers to `goreleaser release --skip=publish` for local dry-runs; `make release-snapshot` produces an unpublished `dist/` tree to validate the config without tagging.
- New `RELEASING.md` documents the full tag → publish procedure, required GitHub secrets (`HOMEBREW_TAP_TOKEN`), and the one-time tap-repo setup.