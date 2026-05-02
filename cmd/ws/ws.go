// Package ws is the `laevitas ws` subscribe command — opens a WebSocket
// connection to the Laevitas streaming gateway, validates the channel set
// client-side, and emits NDJSON to stdout (one JSON object per line).
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
	"github.com/laevitas/cli/internal/wsrender"
)

// Markets and exchanges per market — source of truth is the v1.17.0 channel
// matrix. Hardcoded here so we can reject typos before opening the socket.
// New exchanges/markets should land here in lockstep with the API team's
// matrix updates.
var marketExchanges = map[string][]string{
	"perpetuals":  {"deribit", "binance", "okx", "bybit", "hyperliquid", "kraken", "nado", "bullish"},
	"futures":     {"deribit", "binance", "okx", "bybit", "kraken"},
	"options":     {"deribit", "binance", "okx", "bybit", "bullish", "derive"},
	"spot":        {"binance", "coinbase", "bybit", "okx", "kraken"},
	"predictions": {"polymarket"},
}

// validStreams maps the user-facing stream name to its channel-string prefix.
// `trades` is single-token; `ticker` and `vt` are nested under `ohlc.`;
// `liquidations` and `book` are single-token and constrained to a subset
// of markets (see streamsByMarket).
var validStreams = map[string]string{
	"trades":       "trades",
	"ticker":       "ohlc.ticker",
	"vt":           "ohlc.vt",
	"liquidations": "liquidations",
	"book":         "book",
}

// streamsByMarket gates streams that don't apply to every market. A stream
// not listed here is implicitly available to every market in marketExchanges.
//   - liquidations: forced-close events exist on derivatives only.
//   - book: L2 order book is published for spot / perps / futures /
//     predictions. Options excluded — venues don't expose L2 for options.
var streamsByMarket = map[string]map[string]struct{}{
	"liquidations": {
		"perpetuals": {},
		"futures":    {},
	},
	"book": {
		"spot":        {},
		"perpetuals":  {},
		"futures":     {},
		"predictions": {},
	},
}

// validTimeframes is the documented timeframe set. Only ticker/vt accept --tf.
var validTimeframes = map[string]struct{}{
	"1m": {}, "5m": {}, "15m": {}, "30m": {},
	"1h": {}, "4h": {}, "12h": {}, "1d": {},
}

var flags struct {
	Timeframe string
	// Layout overrides the auto-detected book view. Empty / "auto" keeps
	// auto-detect (single pair → ladder, multi → scan); "scan" forces the
	// summary grid; "ladder" forces the depth ladder (only valid with one
	// channel). Ignored for non-book streams.
	Layout string

	// BookFilter holds --depth / --compact for the book stream.
	// Same flag bundle every REST orderbook-raw command uses, via
	// output.AddBookFilterFlags — so an agent that learned the flags
	// on `spot orderbook-raw` finds them here too with identical
	// semantics. Ignored for non-book streams.
	BookFilter output.BookFilterFlags
}

// Cmd is the top-level `ws` command.
var Cmd = &cobra.Command{
	Use:   "ws <market> <stream> <exchange:instrument>[,<exchange:instrument>...]",
	Short: "Subscribe to live streaming data via WebSocket",
	Long: `Open a WebSocket subscription to one or more channels and emit NDJSON
to stdout — one JSON object per line.

Channel grammar:
  trades.{market}.{exchange}.{instrument}
  ohlc.ticker.{market}.{exchange}.{instrument}.{tf}
  ohlc.vt.{market}.{exchange}.{instrument}.{tf}
  liquidations.{market}.{exchange}.{instrument}
  book.{market}.{exchange}.{instrument}

  market    perpetuals | futures | options | spot | predictions
  stream    trades | ticker | vt | liquidations | book
  tf        1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d  (only for ticker / vt)

  liquidations is only available for perpetuals and futures.
  book is available for spot, perpetuals, futures, and predictions
       (options is not supported — venues don't expose L2 for options).

Multiple <exchange:instrument> pairs can be passed comma-separated; they
share a single connection.

Wildcards: '*' is accepted in the market, exchange, or instrument
position to subscribe to every value at once. One wildcard pattern counts
as one subscription against the server's 200/connection cap. Examples:
  *                  any market           (e.g. trades.*.binance.BTCUSDT)
  binance:*          all binance instruments for the chosen market
  *:BTCUSDT          BTCUSDT across every exchange that lists it
  *:*                everything (firehose — see warning below)

Wildcards are NOT accepted in the stream position or the --tf timeframe;
the server rejects those because the payload shape differs per stream
type and per timeframe. PowerShell users: quote '*' so the shell doesn't
expand it to filenames before laevitas sees it.

In a TTY the book stream renders a live trading view: a multi-pair scan
table when several books are subscribed, a centre-price ladder when one
is. Press Enter on a scan row to drill into the ladder, Esc to return.
Use --layout=scan or --layout=ladder to override the auto-detect.

In any TUI surface, press '?' or 'h' for the keybinding overlay (q to
quit, p to pause, ↑↓/jk to navigate, PgUp/PgDn to page, g/G for top/end,
+/- to change ladder depth tier).

Output is NDJSON — every event is a single line of {"channel", "data"}.
Pipe through jq for filtering, or redirect to a file for replay.`,
	Example: `  # Live BTC perp trades on Binance
  laevitas ws perpetuals trades binance:BTCUSDT

  # OHLC ticker for two options at once
  laevitas ws options ticker deribit:BTC-30JAN26-100000-C,deribit:BTC-30JAN26-110000-C --tf 5m

  # Spot trades, append to a file
  laevitas ws spot trades binance:BTCUSDT > btc-spot.ndjson

  # Live forced-close events (liquidations) on the most active perps
  laevitas ws perpetuals liquidations binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP

  # Single-pair order book — opens straight into the centre-price ladder
  laevitas ws perpetuals book binance:BTCUSDT

  # Multi-pair order book scan — list view; press Enter to drill into a ladder
  laevitas ws perpetuals book binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP

  # Wildcard — every BTCUSDT perp book across every supported exchange
  laevitas ws perpetuals book "*:BTCUSDT"

  # Wildcard — every perpetual liquidation across every exchange
  laevitas ws perpetuals liquidations "*:*"

  # Polymarket prediction market trades
  laevitas ws predictions trades polymarket:will-bitcoin-reach-250000-by-december-31-2026-YES`,
	Args: validateArgs,
	RunE: run,
}

// validateArgs runs before run() and catches the most common PowerShell
// gotcha: an unquoted `*` in argv gets expanded by the shell into the
// list of files in cwd. Cobra's ExactArgs(3) check would normally fire
// with "accepts 3 arg(s), received 12" — true but unhelpful. We catch
// that case here and surface the actual fix.
func validateArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 3 && looksLikeShellGlob(args) {
		return fmt.Errorf(
			"the shell expanded `*` into %d files before laevitas saw it. Quote the wildcard:\n  laevitas ws \"*\" trades \"*:*\"",
			len(args),
		)
	}
	if hint := wsArgHint(args); hint != "" {
		return fmt.Errorf("%s", hint)
	}
	if err := cobra.ExactArgs(3)(cmd, args); err != nil {
		return err
	}
	return nil
}

func wsArgHint(args []string) string {
	// Hints surface the *canonical* market form (perpetuals, futures,
	// options, …) even when the user typed an alias. Keeps the help
	// path consistent with what agents should emit and matches what
	// shows up in --help.
	if len(args) == 3 && isStream(args[0]) && isMarket(args[1]) {
		canonical, _ := api.NormalizeMarket(args[1])
		return fmt.Sprintf(
			"ws expects: laevitas ws <market> <stream> <exchange:instrument>\nTry: laevitas ws %s %s %s",
			canonical,
			args[0],
			args[2],
		)
	}
	if len(args) == 4 && isStream(args[0]) && isMarket(args[1]) {
		canonical, _ := api.NormalizeMarket(args[1])
		return fmt.Sprintf(
			"ws expects: laevitas ws <market> <stream> <exchange:instrument>\nTry: laevitas ws %s %s %s:%s",
			canonical,
			args[0],
			args[2],
			args[3],
		)
	}
	if len(args) == 4 && isMarket(args[0]) && isStream(args[1]) {
		canonical, _ := api.NormalizeMarket(args[0])
		return fmt.Sprintf(
			"ws exchange and instrument must be one colon-separated argument.\nTry: laevitas ws %s %s %s:%s",
			canonical,
			args[1],
			args[2],
			args[3],
		)
	}
	return ""
}

func isMarket(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "*" {
		return true
	}
	// Accept any alias (perp / perpetual / perpetuals / swap / fut /
	// futures / opt / options) so the auto-reorder logic in arg
	// validation does the right thing regardless of which form the
	// user typed.
	if _, ok := api.NormalizeMarket(s); ok {
		return true
	}
	return false
}

func isStream(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "*" {
		return true
	}
	_, ok := validStreams[s]
	return ok
}

// looksLikeShellGlob heuristically detects the PowerShell glob-expansion
// case: argv contains entries that match real files in cwd. We only need
// to be confident that the user meant `*`, not 100% accurate, because
// cobra's "accepts 3 arg(s)" is the fallback.
func looksLikeShellGlob(args []string) bool {
	matches := 0
	for _, a := range args {
		// Skip the few tokens that are valid args even though they
		// happen to live as filenames (rare but plausible: "main.go"
		// is not a market name; "trades" is).
		if _, err := os.Stat(a); err == nil {
			matches++
		}
	}
	// More than half of argv pointing at real files = glob expansion.
	return matches >= len(args)/2 && matches >= 2
}

// looksFirehose returns a warning string when any constructed channel has
// `*` in two or more segments — those patterns can deliver thousands of
// events/sec, which exceeds what most terminals (and the 20 msg/s
// inbound cap before close code 4008) can sustain on a slow consumer.
//
// The threshold is conservative: a single `*` in the instrument slot
// (e.g. `book.perpetuals.binance.*`) is fine — that's "every binance
// perp", which is bounded and useful. Two or more `*` (`book.*.*.*`,
// `trades.*.binance.*`) is when the volume genuinely opens up.
//
// Returns empty string if no warning is warranted.
func looksFirehose(channels []string) string {
	for _, ch := range channels {
		stars := strings.Count(ch, ".*") + strings.Count(ch, "*.")
		// `.*` and `*.` overlap on `*.*`, so dedupe by counting segments.
		segs := strings.Split(ch, ".")
		wildSegs := 0
		for _, s := range segs {
			if s == "*" {
				wildSegs++
			}
		}
		_ = stars
		if wildSegs >= 2 {
			return fmt.Sprintf("pattern %q has %d wildcard segments — this can deliver thousands of events/sec. If your terminal can't drain fast enough, the server will close the connection with 4003 (slow consumer).", ch, wildSegs)
		}
	}
	return ""
}

// detectGlobExpansion is the second-line check, invoked from run() once
// cobra has accepted exactly three args. Catches the case where the shell
// expanded `*` to a single match (cwd had exactly one file), so cobra's
// arg-count check passed but args[0] is a filename like "go.mod" instead
// of a market or `*`. Validation downstream would otherwise produce
// `unknown market "go.mod"`, which is true but unhelpful.
func detectGlobExpansion(args []string) error {
	if len(args) < 2 {
		return nil
	}
	for i, a := range args[:2] {
		if a == "" || a == "*" {
			continue
		}
		// Only consider it a glob match if the arg points at a real file
		// AND looks file-y (contains a dot or path separator). "trades"
		// happens to be a valid stream name; we don't want to false-flag
		// it just because someone has a "trades" file in cwd.
		if !strings.ContainsAny(a, "./\\") {
			continue
		}
		if _, err := os.Stat(a); err == nil {
			slot := []string{"market", "stream"}[i]
			return fmt.Errorf(
				"%s argument %q looks like a file, not a wildcard. The shell may have expanded `*` — quote it:\n  laevitas ws \"*\" trades \"*:*\"",
				slot, a,
			)
		}
	}
	return nil
}

func init() {
	Cmd.Flags().StringVar(&flags.Timeframe, "tf", "1m", "Timeframe for ticker / vt streams (1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d)")
	Cmd.Flags().StringVar(&flags.Layout, "layout", "auto", "Book view layout: auto (default), scan (multi-pair grid), or ladder (centre-price depth ladder, single pair only)")
	// --depth / --compact registered via the shared bundle so the
	// flag names, defaults, and help strings stay byte-identical
	// with the REST orderbook-raw commands (perps, futures, spot,
	// predictions). Same data shape, same flag dialect.
	output.AddBookFilterFlags(Cmd, &flags.BookFilter)
}

// run is the cobra entry point. Validates everything client-side, builds the
// channel list, opens the connection, and pumps events to stdout until the
// user hits Ctrl-C or the connection becomes unrecoverable.
func run(cmd *cobra.Command, args []string) error {
	// PowerShell expands an unquoted `*` in an argv slot to whatever files
	// happen to live in cwd. Detect that as "user typed * but the shell
	// ate it" rather than letting a confusing error like
	//   unknown market "go.mod"
	// come back. We only flag market and stream — pairs are quoted by the
	// `:` so PowerShell rarely mangles them.
	if err := detectGlobExpansion(args); err != nil {
		return err
	}

	rawMarket := strings.TrimSpace(args[0])
	streamName := strings.ToLower(strings.TrimSpace(args[1]))
	pairs := strings.Split(args[2], ",")

	// Single-pair convenience: if the user typed a bare instrument and
	// passed --exchange (a global flag REST/dash already accept),
	// synthesize the exchange:instrument colon form so WS matches the
	// rest of the CLI. Only kicks in when every pair is bare — mixing
	// bare and colon forms keeps the original error so users notice the
	// inconsistency. Multi-pair fan-out still requires explicit colons
	// because --exchange is one value, not a per-pair list.
	if cmd.Flags().Changed("exchange") && cmdutil.Exchange != "" {
		anyColon := false
		for _, p := range pairs {
			if strings.Contains(p, ":") {
				anyColon = true
				break
			}
		}
		if !anyColon && len(pairs) == 1 {
			pairs[0] = cmdutil.Exchange + ":" + strings.TrimSpace(pairs[0])
		}
	}

	// Normalise the market token via api.NormalizeMarket so users
	// can type any common alias (perp, perpetual, perpetuals, swap,
	// fut, futures, opt, options, etc.) and we always end up with
	// the canonical plural form internally. `*` is a gateway-side
	// wildcard and skips normalisation — it stays as "*".
	var market string
	var allowedExchanges []string
	if rawMarket == "*" {
		market = "*"
	} else {
		canonical, ok := api.NormalizeMarket(rawMarket)
		if !ok {
			return fmt.Errorf("unknown market %q. Valid: %s, or * for any market", rawMarket, strings.Join(sortedKeys(marketExchanges), ", "))
		}
		market = canonical
		allowedExchanges, ok = marketExchanges[market]
		if !ok {
			return fmt.Errorf("market %q is recognised but no streams are wired for it yet", market)
		}
	}

	// Validate the stream. Wildcards are NOT allowed in the stream / channel
	// position — the server explicitly rejects this because each stream type
	// has a different payload shape. Reject client-side for fast feedback.
	if streamName == "*" {
		return fmt.Errorf("wildcards not allowed in the stream position. Pick one of: trades, ticker, vt, liquidations, book")
	}
	streamPrefix, ok := validStreams[streamName]
	if !ok {
		return fmt.Errorf("unknown stream %q. Valid: trades, ticker, vt, liquidations, book", streamName)
	}

	// Reject obviously-invalid book-filter values early. Same validation
	// as the REST path (RunAndPrintFiltered) so agents see consistent
	// errors regardless of transport.
	if err := flags.BookFilter.Validate(); err != nil {
		return err
	}

	// Some streams only apply to a subset of markets (e.g. liquidations is
	// derivatives-only). Reject the combination here so the user gets a
	// targeted error instead of a generic "invalid channel" from the server.
	// Skipped for the wildcard market — the server filters per-event.
	if market != "*" {
		if allowedMarkets, restricted := streamsByMarket[streamName]; restricted {
			if _, ok := allowedMarkets[market]; !ok {
				markets := sortedKeysSet(allowedMarkets)
				return fmt.Errorf("stream %q is only available for: %s", streamName, strings.Join(markets, ", "))
			}
		}
	}

	// Validate --tf only for OHLC streams; reject explicit --tf with anything
	// else (none of the other streams bucket by timeframe). Wildcards are
	// not allowed in the timeframe position — server explicitly rejects.
	tf := flags.Timeframe
	usesTimeframe := streamName == "ticker" || streamName == "vt"
	if usesTimeframe {
		if tf == "*" {
			return fmt.Errorf("wildcards not allowed in the --tf position. Subscribe per timeframe (1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d)")
		}
		if _, ok := validTimeframes[tf]; !ok {
			return fmt.Errorf("invalid --tf %q. Valid: 1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d", tf)
		}
	} else if cmd.Flags().Changed("tf") {
		return fmt.Errorf("--tf only applies to ticker and vt streams, not %s", streamName)
	}

	// --layout is book-only. Reject the flag on other streams so users don't
	// silently set it and wonder why nothing changed.
	if cmd.Flags().Changed("layout") && streamName != "book" {
		return fmt.Errorf("--layout only applies to the book stream, not %s", streamName)
	}
	switch strings.ToLower(flags.Layout) {
	case "", "auto", "scan", "ladder":
		// ok
	default:
		return fmt.Errorf("invalid --layout %q. Valid: auto, scan, ladder", flags.Layout)
	}
	if strings.ToLower(flags.Layout) == "ladder" && len(pairs) > 1 {
		return fmt.Errorf("--layout=ladder requires a single exchange:instrument; got %d", len(pairs))
	}

	// Build the channel list. For concrete markets and exchanges, reject
	// combinations the server would 400 anyway. Wildcard market or
	// wildcard exchange skips the whitelist (server resolves the pattern).
	channels := make([]string, 0, len(pairs))
	deprecationNudges := make([]string, 0)
	for _, p := range pairs {
		exchange, instrument, err := splitPair(p)
		if err != nil {
			return err
		}
		// Whitelist exchange only when both market and exchange are
		// concrete. With market == "*" we don't know which whitelist to
		// apply; with exchange == "*" we're explicitly asking for all of
		// them.
		if market != "*" && exchange != "*" {
			if !contains(allowedExchanges, exchange) {
				return fmt.Errorf(
					"exchange %q not available for market %q. Valid for %s: %s, or * for any exchange",
					exchange, market, market, strings.Join(allowedExchanges, ", "),
				)
			}
		}

		// Deprecation hint: API supports a legacy alias where perpetual
		// instruments fire on the `futures` channel for one minor. If the
		// user types `futures trades binance:BTCUSDT`, we still subscribe
		// (server forwards) but warn so the next minor doesn't break them.
		// Doesn't apply when either side is a wildcard.
		if market == "futures" && exchange != "*" && instrument != "*" && looksLikePerp(instrument) {
			deprecationNudges = append(deprecationNudges,
				fmt.Sprintf("%s:%s looks like a perpetual; the legacy futures alias will be removed next minor — use `laevitas ws perpetuals %s %s:%s`", exchange, instrument, streamName, exchange, instrument))
		}

		ch := buildChannel(streamPrefix, market, exchange, instrument, tf, usesTimeframe)
		channels = append(channels, ch)
	}

	// Firehose warning: patterns with `*` in two or more positions can
	// emit thousands of events/sec on a popular channel type. The server
	// will cut us with close code 4003 if we can't drain fast enough.
	// Print the heads-up on stderr before dialing — non-blocking, just
	// makes the "why am I getting 4003?" question answer itself.
	//
	// Warning fires regardless of whether stdout is a TTY: agents
	// pipe stdout to jq/files, but stderr is usually still attached to
	// the terminal, and the warning is exactly the kind of signal an
	// agent's parent process needs to log. Earlier versions gated this
	// on output.IsTTY() (stdout-TTY check) which silently dropped the
	// warning for the most common agent invocation pattern — the case
	// it was most likely to matter for. v0.8.4 drops the gate.
	if firehoseWarning := looksFirehose(channels); firehoseWarning != "" {
		fmt.Fprintf(os.Stderr, "%s⚠ %s%s\n", output.Yellow, firehoseWarning, output.Reset)
	}

	// Resolve auth — same path as REST commands. We respect LAEVITAS_AUTH so a
	// user explicitly on x402 mode gets a clear error rather than a silent
	// auth failure later.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.APIKey == "" {
		// Wallet-only mode: WS doesn't accept x402 yet. Surface that loudly.
		if cfg.WalletKey != "" {
			return fmt.Errorf("WebSocket streaming requires an API key today; x402 wallet auth is not yet supported on the streaming gateway. Set LAEVITAS_API_KEY or run `laevitas config init`")
		}
		return fmt.Errorf("no API key configured. Set LAEVITAS_API_KEY or run `laevitas config init`")
	}

	// Print deprecation nudges (stderr) before we open the connection so they
	// can't get lost in the data stream.
	for _, msg := range deprecationNudges {
		fmt.Fprintf(os.Stderr, "%s⚠ %s%s\n", output.Yellow, msg, output.Reset)
	}

	// Decide output mode. Three relevant settings of the global -o flag:
	//   "json" or "csv" → always NDJSON (csv has no streaming form yet, so
	//                     we treat it as JSON to keep the pipeline working).
	//   "table"         → always live table, even when stdout is piped.
	//                     Forces TTY-only behaviour; user explicitly opted in.
	//   "auto" (default) or anything else
	//                   → live table when stdout is a TTY, NDJSON otherwise.
	mode := strings.ToLower(cmdutil.OutputFormat)
	useLiveTable := false
	switch mode {
	case "table":
		useLiveTable = true
	case "json", "csv":
		useLiveTable = false
	default:
		useLiveTable = output.IsTTY()
	}

	// Set up Ctrl-C handler — clean unsubscribe, close socket, exit 0.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// NDJSON mode prints a one-line "subscribed" header on stderr if running
	// in a TTY so the user sees something before data flows. The live-table
	// mode owns the whole screen and renders its own header — skip it there.
	if !useLiveTable && output.IsTTY() {
		fmt.Fprintf(os.Stderr, "%s%s▲%s  subscribed: %d channel%s, Ctrl-C to exit\n",
			output.Bold, output.BrandGreen, output.Reset,
			len(channels), pluralS(len(channels)),
		)
		for _, ch := range channels {
			fmt.Fprintf(os.Stderr, "%s   %s%s\n", output.BrandGreyMid, ch, output.Reset)
		}
	}

	// Connect.
	cli, err := wsclient.Dial(ctx, wsclient.Config{
		APIKey:   cfg.APIKey,
		Channels: channels,
	})
	if err != nil {
		return fmt.Errorf("dialing %s: %w", wsclient.NativeURL, err)
	}
	defer cli.Close()

	if useLiveTable {
		// Book stream gets its own scan/ladder TUI; trades, ticker, vt,
		// liquidations stay on the rolling-table renderer that's served
		// them since v0.8.0.
		if streamName == "book" {
			layout := strings.ToLower(flags.Layout)
			if layout == "" || layout == "auto" {
				// Single concrete subscription → ladder (deep-dive on
				// one book). Multi-pair OR any wildcard → scan, because
				// a wildcard expands to an unknown-but-likely-many set
				// of concrete pairs and the user explicitly asked for
				// breadth.
				hasWildcard := false
				for _, ch := range channels {
					if strings.Contains(ch, "*") {
						hasWildcard = true
						break
					}
				}
				if len(channels) == 1 && !hasWildcard {
					layout = "ladder"
				} else {
					layout = "scan"
				}
			}
			return runBookTable(ctx, cli, channels, layout)
		}
		return runLiveTable(ctx, cli, channels)
	}
	return runNDJSON(ctx, cli)
}

// runNDJSON pumps events to stdout as one JSON object per line. Used in
// every non-TTY context (pipes, redirects, agents) and when the user
// explicitly asked for -o json.
func runNDJSON(ctx context.Context, cli *wsclient.Client) error {
	emitErr := func(e error) {
		if output.IsTTY() {
			fmt.Fprintf(os.Stderr, "%s⚠ %s%s\n", output.Yellow, e, output.Reset)
		} else {
			warning := map[string]interface{}{
				"warning":   e.Error(),
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			}
			_ = json.NewEncoder(os.Stderr).Encode(warning)
		}
	}

	// Drain soft errors on stderr while events flow. Track the most recent
	// one — wsclient closes events first, then errs, so we'll synchronously
	// drain any final fatal warning after the events loop exits.
	var lastErr atomic.Value // string
	errsDone := make(chan struct{})
	go func() {
		defer close(errsDone)
		for e := range cli.Errs() {
			lastErr.Store(e.Error())
			emitErr(e)
		}
	}()

	enc := json.NewEncoder(os.Stdout)
	for ev := range cli.Events() {
		// Book events optionally pass through the shared book-filter
		// helper (output.ApplyBookFilter), which applies --depth /
		// --compact before emit. Non-book events and book events
		// without flags pass through verbatim. Same trim function
		// the REST orderbook-raw commands call, so REST and WS
		// produce identically-shaped payloads under the same flags.
		if flags.BookFilter.Active() && strings.HasPrefix(ev.Channel, "book.") {
			ev.Data = output.ApplyBookFilter(ev.Data, flags.BookFilter)
		}
		if err := enc.Encode(ev); err != nil {
			return fmt.Errorf("writing event to stdout: %w", err)
		}
	}

	// Events closed → wait for the errs goroutine to finish so the fatal
	// warning (if any) is reflected in lastErr before we report.
	<-errsDone

	if ctx.Err() != nil {
		return nil
	}
	if v := lastErr.Load(); v != nil {
		return fmt.Errorf("%s", v.(string))
	}
	return fmt.Errorf("stream ended unexpectedly")
}

// runLiveTable enters raw terminal mode and hands the wsclient stream to the
// wsrender package for live-updating display. Used when stdout is a TTY and
// the user hasn't explicitly asked for JSON. Soft errors from the wsclient
// surface in the table's footer rather than scrolling on stderr — anything
// printed to stderr in raw mode would corrupt the rendered frame.
func runLiveTable(ctx context.Context, cli *wsclient.Client, channels []string) error {
	table := wsrender.NewLiveTable(channels)

	// Drain soft errors into the table's footer instead of stderr.
	go func() {
		for e := range cli.Errs() {
			table.SetLastError(e.Error())
		}
	}()

	// Pump events into the table on a dedicated goroutine so the renderer's
	// blocking Run() can own stdin (raw-mode key reads).
	go func() {
		for ev := range cli.Events() {
			table.Push(ev)
		}
	}()

	// Run blocks until the user presses 'q' / Ctrl-C or the stream closes.
	if err := table.Run(); err != nil {
		return fmt.Errorf("live table: %w", err)
	}
	return nil
}

// runBookTable is the book-stream variant of runLiveTable. Same plumbing —
// drain errs, pump events, block on Run — but the renderer is the
// snapshot-replacing BookTable instead of the rolling-tape LiveTable.
//
// Layout is "scan" or "ladder" by the time we get here; auto-detect happens
// in the caller.
func runBookTable(ctx context.Context, cli *wsclient.Client, channels []string, layout string) error {
	table := wsrender.NewBookTable(channels, layout)

	go func() {
		for e := range cli.Errs() {
			table.SetLastError(e.Error())
		}
	}()
	go func() {
		for ev := range cli.Events() {
			table.Push(ev)
		}
	}()

	if err := table.Run(); err != nil {
		return fmt.Errorf("book view: %w", err)
	}
	return nil
}

// splitPair splits "exchange:instrument" with no whitespace tolerance —
// the user is typing a positional arg, not building a sentence. Returns
// helpful errors for obvious typos.
func splitPair(p string) (string, string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", "", fmt.Errorf("empty exchange:instrument pair")
	}
	idx := strings.Index(p, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("expected exchange:instrument, got %q", p)
	}
	exchange := strings.ToLower(strings.TrimSpace(p[:idx]))
	instrument := strings.TrimSpace(p[idx+1:])
	if exchange == "" || instrument == "" {
		return "", "", fmt.Errorf("expected exchange:instrument, got %q", p)
	}
	return exchange, instrument, nil
}

// buildChannel formats a channel string per the v1.17.0 grammar.
func buildChannel(prefix, market, exchange, instrument, tf string, withTimeframe bool) string {
	base := fmt.Sprintf("%s.%s.%s.%s", prefix, market, exchange, instrument)
	if withTimeframe {
		return base + "." + tf
	}
	return base
}

// looksLikePerp is a heuristic for the futures-alias deprecation nudge.
// Distinguishes BTCUSDT-style perp tickers and BTC-PERPETUAL from dated
// futures (BTC-27MAR26). Not perfect — server-side is the source of truth —
// but good enough to avoid false-negative warnings for the common cases.
func looksLikePerp(instrument string) bool {
	upper := strings.ToUpper(instrument)
	if strings.Contains(upper, "PERPETUAL") || strings.Contains(upper, "PERP") {
		return true
	}
	// Symbols like BTCUSDT, ETHUSDT, SOLUSDC are perp on Binance/OKX/Bybit.
	if !strings.Contains(upper, "-") {
		for _, q := range []string{"USDT", "USDC", "USD", "BUSD"} {
			if strings.HasSuffix(upper, q) {
				return true
			}
		}
	}
	return false
}

// contains reports whether s appears in slice. Tiny helper, avoids slices.Contains
// requiring Go 1.21+ semantics in a code path that's repeatedly called.
func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic order for the error message.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func sortedKeysSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// _ ensures cmdutil stays referenced even if we don't use it directly later.
// cmdutil is the natural import for any cobra-tree command in this CLI.
var _ = cmdutil.Exchange
