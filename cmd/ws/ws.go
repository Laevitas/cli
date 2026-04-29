// Package ws is the `lvt ws` subscribe command — opens a WebSocket
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
// `liquidations` is single-token and only valid for perpetuals/futures
// (see streamsByMarket).
var validStreams = map[string]string{
	"trades":       "trades",
	"ticker":       "ohlc.ticker",
	"vt":           "ohlc.vt",
	"liquidations": "liquidations",
}

// streamsByMarket gates streams that don't apply to every market. A stream
// not listed here is implicitly available to every market in marketExchanges.
// Liquidations only exist on linear/inverse derivatives — spot / options /
// predictions don't have a forced-close concept.
var streamsByMarket = map[string]map[string]struct{}{
	"liquidations": {
		"perpetuals": {},
		"futures":    {},
	},
}

// validTimeframes is the documented timeframe set. Only ticker/vt accept --tf.
var validTimeframes = map[string]struct{}{
	"1m": {}, "5m": {}, "15m": {}, "30m": {},
	"1h": {}, "4h": {}, "12h": {}, "1d": {},
}

var flags struct {
	Timeframe string
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

  market    perpetuals | futures | options | spot | predictions
  stream    trades | ticker | vt | liquidations
  tf        1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d  (only for ticker / vt)

  liquidations is only available for perpetuals and futures.

Multiple <exchange:instrument> pairs can be passed comma-separated; they
share a single connection.

Output is NDJSON — every event is a single line of {"channel", "data"}.
Pipe through jq for filtering, or redirect to a file for replay.`,
	Example: `  # Live BTC perp trades on Binance
  lvt ws perpetuals trades binance:BTCUSDT

  # OHLC ticker for two options at once
  lvt ws options ticker deribit:BTC-30JAN26-100000-C,deribit:BTC-30JAN26-110000-C --tf 5m

  # Spot trades, append to a file
  lvt ws spot trades binance:BTCUSDT > btc-spot.ndjson

  # Live forced-close events (liquidations) on the most active perps
  lvt ws perpetuals liquidations binance:BTCUSDT,bybit:BTCUSDT,okx:BTC-USDT-SWAP

  # Polymarket prediction market trades
  lvt ws predictions trades polymarket:will-bitcoin-reach-250000-by-december-31-2026-YES`,
	Args: cobra.ExactArgs(3),
	RunE: run,
}

func init() {
	Cmd.Flags().StringVar(&flags.Timeframe, "tf", "1m", "Timeframe for ticker / vt streams (1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d)")
}

// run is the cobra entry point. Validates everything client-side, builds the
// channel list, opens the connection, and pumps events to stdout until the
// user hits Ctrl-C or the connection becomes unrecoverable.
func run(cmd *cobra.Command, args []string) error {
	market := strings.ToLower(strings.TrimSpace(args[0]))
	streamName := strings.ToLower(strings.TrimSpace(args[1]))
	pairs := strings.Split(args[2], ",")

	// Validate the market.
	allowedExchanges, ok := marketExchanges[market]
	if !ok {
		return fmt.Errorf("unknown market %q. Valid: %s", market, strings.Join(sortedKeys(marketExchanges), ", "))
	}

	// Validate the stream.
	streamPrefix, ok := validStreams[streamName]
	if !ok {
		return fmt.Errorf("unknown stream %q. Valid: trades, ticker, vt, liquidations", streamName)
	}

	// Some streams only apply to a subset of markets (e.g. liquidations is
	// derivatives-only). Reject the combination here so the user gets a
	// targeted error instead of a generic "invalid channel" from the server.
	if allowedMarkets, restricted := streamsByMarket[streamName]; restricted {
		if _, ok := allowedMarkets[market]; !ok {
			markets := sortedKeysSet(allowedMarkets)
			return fmt.Errorf("stream %q is only available for: %s", streamName, strings.Join(markets, ", "))
		}
	}

	// Validate --tf only for OHLC streams; reject explicit --tf with `trades`
	// or `liquidations` (neither buckets by timeframe).
	tf := flags.Timeframe
	usesTimeframe := streamName == "ticker" || streamName == "vt"
	if usesTimeframe {
		if _, ok := validTimeframes[tf]; !ok {
			return fmt.Errorf("invalid --tf %q. Valid: 1m, 5m, 15m, 30m, 1h, 4h, 12h, 1d", tf)
		}
	} else if cmd.Flags().Changed("tf") {
		return fmt.Errorf("--tf only applies to ticker and vt streams, not %s", streamName)
	}

	// Build the channel list. Reject any exchange that's not in the matrix
	// for this market — server would 400 us anyway, but a clean client-side
	// error tells the user exactly what's wrong.
	channels := make([]string, 0, len(pairs))
	deprecationNudges := make([]string, 0)
	for _, p := range pairs {
		exchange, instrument, err := splitPair(p)
		if err != nil {
			return err
		}
		if !contains(allowedExchanges, exchange) {
			return fmt.Errorf(
				"exchange %q not available for market %q. Valid for %s: %s",
				exchange, market, market, strings.Join(allowedExchanges, ", "),
			)
		}

		// Deprecation hint: API supports a legacy alias where perpetual
		// instruments fire on the `futures` channel for one minor. If the
		// user types `futures trades binance:BTCUSDT`, we still subscribe
		// (server forwards) but warn so the next minor doesn't break them.
		if market == "futures" && looksLikePerp(instrument) {
			deprecationNudges = append(deprecationNudges,
				fmt.Sprintf("%s:%s looks like a perpetual; the legacy futures alias will be removed next minor — use `lvt ws perpetuals %s %s:%s`", exchange, instrument, streamName, exchange, instrument))
		}

		ch := buildChannel(streamPrefix, market, exchange, instrument, tf, usesTimeframe)
		channels = append(channels, ch)
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
