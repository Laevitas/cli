package panels

// FlowChartPanel — live ASCII candlestick chart for the flow
// detail dashboard.
//
// Subscribes to the same trades channel as FlowTapePanel. REST
// seeding fetches the selected OHLCVT resolution, then each live
// trade is folded into the candles.Aggregator using the active
// timeframe; View draws the rendered candles via candles.Render.
//
// The panel reuses the trades feed FlowTapePanel already subscribes
// to. Both panels declare the same channel via Subscriptions; the
// kernel's FeedRouter dedupes so only one server-side subscription
// exists.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/candles"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// flowChartCapacity is the number of active-timeframe candles
// retained. REST seeding asks for this many candles at the selected
// resolution, so 1h gets the same pane density as 1m instead of only
// a few downsampled bars.
const flowChartCapacity = 240

const flowChartDefaultTimeframe = time.Minute

const flowChartCandleStride = 2

var flowChartTimeframes = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
}

// FlowChartPanel implements dashboard.Panel.
type FlowChartPanel struct {
	selection   dashboard.Selection
	aggregator  *candles.Aggregator
	client      *api.Client
	timeframe   time.Duration
	seedKey     string
	seedLoading bool
	seedErr     string
}

type flowChartSeedMsg struct {
	key     string
	candles []candles.Candle
	err     error
}

// NewFlowChartPanel constructs the panel with an initial selection.
func NewFlowChartPanel(initial dashboard.Selection, client ...*api.Client) *FlowChartPanel {
	var c *api.Client
	if len(client) > 0 {
		c = client[0]
	}
	p := &FlowChartPanel{
		selection:  initial,
		aggregator: candles.New(flowChartCapacity),
		client:     c,
		timeframe:  flowChartDefaultTimeframe,
	}
	p.aggregator.SetTimeframe(p.timeframe)
	p.seedKey = flowChartSeedKey(initial, flowChartResolution(p.timeframe))
	return p
}

// currentChannel returns the trades channel for the current
// selection. The chart and the tape panel both read from this
// channel; the FeedRouter dedupes server-side.
func (p *FlowChartPanel) currentChannel() string {
	return tradesChannelForSelection(p.selection)
}

func flowChartSeedKey(sel dashboard.Selection, resolution string) string {
	if sel.Market == "" || sel.Venue == "" || sel.Symbol == "" {
		return ""
	}
	return sel.Market + "|" + sel.Venue + "|" + sel.Symbol + "|" + resolution
}

func (p *FlowChartPanel) fetchSeedCmd(sel dashboard.Selection, key string, timeframe time.Duration) tea.Cmd {
	if p.client == nil || key == "" {
		return nil
	}
	end := time.Now().UTC()
	start := end.Add(-time.Duration(flowChartCapacity) * timeframe)
	resolution := flowChartResolution(timeframe)
	endpoint, ok := flowChartOHLCVTEndpoint(sel.Market)
	if !ok {
		return func() tea.Msg {
			return flowChartSeedMsg{key: key, err: fmt.Errorf("ohlcvt unavailable for market %q", sel.Market)}
		}
	}
	client := p.client
	return func() tea.Msg {
		params := &api.RequestParams{
			Exchange:       sel.Venue,
			InstrumentName: sel.Symbol,
			Start:          start.Format(time.RFC3339),
			End:            end.Format(time.RFC3339),
			Resolution:     resolution,
			Limit:          flowChartCapacity,
			SortDir:        "ASC",
		}
		body, err := client.Get(endpoint, params)
		if err != nil {
			return flowChartSeedMsg{key: key, err: err}
		}
		seed, err := parseFlowChartOHLCVT(body, timeframe)
		if err != nil {
			return flowChartSeedMsg{key: key, err: err}
		}
		return flowChartSeedMsg{key: key, candles: seed}
	}
}

func flowChartOHLCVTEndpoint(market string) (string, bool) {
	switch strings.ToLower(market) {
	case "perpetuals", "perps", "perp":
		return api.PerpsOHLCVT, true
	case "futures", "future":
		return api.FuturesOHLCVT, true
	case "spot":
		return api.SpotOHLCVT, true
	case "options", "option":
		return api.OptionsOHLCVT, true
	case "predictions", "prediction":
		return api.PredictionsOHLCVT, true
	default:
		return "", false
	}
}

// CardSubtitle returns the venue:instrument identity for the
// CardPanel decorator's top-border label. Empty when no
// selection is installed.
func (p *FlowChartPanel) CardSubtitle() string {
	if p.selection.Venue == "" || p.selection.Symbol == "" {
		return ""
	}
	return p.selection.Venue + ":" + p.selection.Symbol
}

// Init seeds the chart if constructed with an already-drillable
// selection. In the flow dashboard this is normally a no-op because
// detail panes are constructed empty and receive SelectionChangedMsg
// on drill.
func (p *FlowChartPanel) Init() tea.Cmd {
	if p.client == nil || p.seedKey == "" {
		return nil
	}
	p.seedLoading = true
	return p.fetchSeedCmd(p.selection, p.seedKey, p.chartTimeframe())
}

func (p *FlowChartPanel) chartTimeframe() time.Duration {
	if p.timeframe <= 0 {
		return flowChartDefaultTimeframe
	}
	return p.timeframe
}

func (p *FlowChartPanel) chartCandles() []candles.Candle {
	return p.aggregator.Candles()
}

func (p *FlowChartPanel) cycleTimeframe() tea.Cmd {
	current := p.chartTimeframe()
	idx := 0
	for i, tf := range flowChartTimeframes {
		if tf == current {
			idx = i
			break
		}
	}
	p.timeframe = flowChartTimeframes[(idx+1)%len(flowChartTimeframes)]
	p.aggregator.SetTimeframe(p.timeframe)
	p.aggregator.Reset()
	p.seedErr = ""
	p.seedLoading = false
	p.seedKey = flowChartSeedKey(p.selection, flowChartResolution(p.timeframe))
	if p.client != nil && p.seedKey != "" {
		p.seedLoading = true
		return p.fetchSeedCmd(p.selection, p.seedKey, p.timeframe)
	}
	return nil
}

// Update folds matching trades into the aggregator. SelectionChangedMsg
// resets the aggregator (chart of one instrument's history is not
// meaningful for another).
func (p *FlowChartPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if keymap.ClassifyKey(m.String()) == keymap.ActTimeframeCycle {
			return p, p.cycleTimeframe()
		}
	case dashboard.SelectionChangedMsg:
		p.selection = m.New
		p.aggregator.SetTimeframe(p.chartTimeframe())
		p.aggregator.Reset()
		p.seedKey = flowChartSeedKey(m.New, flowChartResolution(p.chartTimeframe()))
		p.seedErr = ""
		p.seedLoading = false
		if p.client != nil && p.seedKey != "" {
			p.seedLoading = true
			return p, p.fetchSeedCmd(m.New, p.seedKey, p.chartTimeframe())
		}
	case flowChartSeedMsg:
		if m.key != p.seedKey {
			return p, nil
		}
		p.seedLoading = false
		if m.err != nil {
			p.seedErr = m.err.Error()
			return p, nil
		}
		p.seedErr = ""
		p.aggregator.SetTimeframe(p.chartTimeframe())
		p.aggregator.Seed(m.candles)
	case dashboard.FeedTickMsg:
		want := p.currentChannel()
		if want == "" || m.Event.Channel != want {
			return p, nil
		}
		var t struct {
			Date      string  `json:"date"`
			Timestamp int64   `json:"timestamp"`
			Price     float64 `json:"price"`
			Size      float64 `json:"size"`
			CoinAmt   float64 `json:"coin_amount"`
			Amount    float64 `json:"amount"`
		}
		if err := json.Unmarshal(m.Event.Data, &t); err != nil {
			return p, nil
		}
		size := t.CoinAmt
		if size == 0 {
			size = t.Size
		}
		if size == 0 {
			size = t.Amount
		}
		// Validate before feeding the aggregator: a zero-price
		// trade would corrupt the candle's High/Low range and
		// flatten the price scale. Drop incomplete payloads.
		if t.Price <= 0 || size <= 0 {
			return p, nil
		}
		p.aggregator.Add(candles.Trade{
			Timestamp: parseTradeTime(t.Date, t.Timestamp),
			Price:     t.Price,
			Size:      size,
		})
	}
	return p, nil
}

// Subscriptions returns the trades channel — same as FlowTapePanel.
// FeedRouter dedupes; a single server-side subscription serves
// both panels.
func (p *FlowChartPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	ch := tradesChannelForSelection(sel)
	if ch != p.currentChannel() {
		p.selection = sel
		p.aggregator.SetTimeframe(p.chartTimeframe())
		p.aggregator.Reset()
		p.seedKey = flowChartSeedKey(sel, flowChartResolution(p.chartTimeframe()))
		p.seedErr = ""
		p.seedLoading = false
	}
	if ch == "" {
		return dashboard.FeedSpec{}
	}
	return dashboard.FeedSpec{Channels: []string{ch}}
}

func (p *FlowChartPanel) Title() string { return "" }
func (p *FlowChartPanel) Capabilities() keymap.Capabilities {
	return keymap.Capabilities{ChartTimeframe: true}
}

// View renders a stats line on top, the coloured candle grid in
// the middle, a compact volume strip when height allows, and a
// HH:MM time axis at the bottom.
//
// Layout (top → bottom):
//
//	row 0:        stats line — timeframe + OHLC + volume
//	rows 1..N:     candle grid via candles.Render
//	next rows:     volume bars
//	row H-1:       time axis (HH:MM at start / middle / end)
//
// The stats line is panel-side, not baked into candles.Render,
// because the data shape (OHLC of latest, recent volume) is a
// presentation concern; the candles package stays a pure
// renderer.
//
// Empty-state semantics:
//   - No selection → blank centred placeholder.
//   - Selection set, no candles → "waiting" status.
//   - 1+ candles → stats + chart + volume + axis.
func (p *FlowChartPanel) View(width, height int, ctx dashboard.PanelContext) string {
	tf := p.chartTimeframe()
	cs := p.chartCandles()
	if p.currentChannel() == "" {
		return waitingView(width, height, "", ctx.SpinnerFrame)
	}
	if len(cs) == 0 {
		if p.seedLoading {
			return waitingView(width, height, "loading chart history…", ctx.SpinnerFrame)
		}
		if p.seedErr != "" {
			return waitingView(width, height, "history unavailable; waiting for trades…", ctx.SpinnerFrame)
		}
		// Truly empty — no trades since the panel was reset. Show
		// a thin "waiting" status. The moment the first trade
		// lands, the chart renders a real right-edge candle; no
		// warmup gate above 0 candles, so the user sees real data
		// from the first tick.
		return waitingView(width, height, "waiting for first trade…", ctx.SpinnerFrame)
	}
	if width < 20 || height < 3 {
		return buildCompactChartView(cs, width, height, tf)
	}

	// Stats line takes 1 row, time axis takes 1 row. When height
	// allows, reserve a small volume band under the price chart
	// with a blank row between price and volume. Tight panes shed
	// axis, then the spacer, then volume, then stats so price stays
	// visible.
	statsRows := 1
	axisRows := 1
	volumeRows := 2
	if height < 9 {
		volumeRows = 1
	}
	spacerRows := 0
	if volumeRows > 0 && height >= 10 {
		spacerRows = 1
	}
	chartH := height - statsRows - axisRows - spacerRows - volumeRows
	if chartH < 3 {
		// Tight pane — drop the axis to reclaim a row.
		axisRows = 0
		chartH = height - statsRows - spacerRows - volumeRows
	}
	if chartH < 3 {
		// Reclaim the visual gap before dropping data.
		spacerRows = 0
		chartH = height - statsRows - volumeRows
	}
	if chartH < 3 {
		// Keep candles over volume when only one visual band fits.
		volumeRows = 0
		chartH = height - statsRows
	}
	if chartH < 3 {
		// Even tighter — drop the stats line too. Price-only
		// fallback for absurdly short panes.
		statsRows = 0
		chartH = height
	}

	stats := ""
	if statsRows > 0 {
		stats = buildChartStats(cs, width, tf)
	}
	rows := candles.Render(cs, width, chartH, candles.RenderOptions{
		Timeframe:    tf,
		CandleStride: flowChartCandleStride,
		UpColor:      output.BrandGreen,
		DownColor:    output.Red,
		FlatColor:    output.BrandGreyMid,
		Reset:        output.Reset,
		SolidBodies:  true,
	})
	rows = overlayLatestPriceFlag(rows, cs, width, tf, flowChartCandleStride)
	if statsRows > 0 {
		rows = append([]string{stats}, rows...)
	}
	if spacerRows > 0 {
		rows = append(rows, strings.Repeat(" ", width))
	}
	if volumeRows > 0 {
		rows = append(rows, buildVolumeRows(cs, width, volumeRows, tf, flowChartCandleStride)...)
	}
	if axisRows > 0 {
		axis := buildTimeAxis(cs, width, tf)
		rows = append(rows, axis)
	}
	return strings.Join(rows, "\n")
}

func buildCompactChartView(cs []candles.Candle, width, height int, timeframe time.Duration) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(cs) == 0 {
		return strings.Repeat(" ", width)
	}
	latest := cs[len(cs)-1]
	color := output.BrandGreen
	sign := "+"
	changePct := 0.0
	if latest.Open > 0 {
		changePct = ((latest.Close - latest.Open) / latest.Open) * 100
	}
	if changePct < 0 {
		color = output.Red
		sign = "-"
		changePct = -changePct
	}
	line := fmt.Sprintf("%s C %s %s%.2f%%", flowChartTimeframeLabel(timeframe), output.FormatBookPrice(latest.Close), sign, changePct)
	line = output.PadRightAnsi(color+line+output.Reset, width)
	rows := []string{line}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

func parseFlowChartOHLCVT(body []byte, timeframe time.Duration) ([]candles.Candle, error) {
	var wrapped api.APIResponse
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return parseFlowChartOHLCVTData(wrapped.Data, timeframe)
	}
	return parseFlowChartOHLCVTData(body, timeframe)
}

func parseFlowChartOHLCVTData(data []byte, timeframe time.Duration) ([]candles.Candle, error) {
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		var obj map[string]json.RawMessage
		if objErr := json.Unmarshal(data, &obj); objErr != nil {
			return nil, err
		}
		for _, key := range []string{"data", "rows", "items", "candles", "ohlcvt"} {
			if raw, ok := obj[key]; ok {
				if err := json.Unmarshal(raw, &rows); err != nil {
					return nil, err
				}
				break
			}
		}
		if rows == nil {
			return nil, fmt.Errorf("ohlcvt response does not contain candle rows")
		}
	}

	out := make([]candles.Candle, 0, len(rows))
	for _, row := range rows {
		c, ok := flowChartCandleFromRow(row, timeframe)
		if ok {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].BucketStart.Before(out[j].BucketStart)
	})
	if len(out) > flowChartCapacity {
		out = out[len(out)-flowChartCapacity:]
	}
	return out, nil
}

func flowChartCandleFromRow(row map[string]any, timeframe time.Duration) (candles.Candle, bool) {
	if timeframe <= 0 {
		timeframe = flowChartDefaultTimeframe
	}
	ts, ok := flowChartRowTime(row)
	if !ok {
		return candles.Candle{}, false
	}
	open, okO := flowChartRowFloat(row, "open", "o")
	high, okH := flowChartRowFloat(row, "high", "h")
	low, okL := flowChartRowFloat(row, "low", "l")
	closePx, okC := flowChartRowFloat(row, "close", "c")
	if !okO || !okH || !okL || !okC || open <= 0 || high <= 0 || low <= 0 || closePx <= 0 {
		return candles.Candle{}, false
	}
	vol, _ := flowChartRowFloat(row, "volume", "vol", "v")
	trades, _ := flowChartRowInt(row, "trades", "trade_count", "count")
	return candles.Candle{
		BucketStart: ts.UTC().Truncate(timeframe),
		Open:        open,
		High:        high,
		Low:         low,
		Close:       closePx,
		Volume:      vol,
		TradeCount:  trades,
	}, true
}

func flowChartRowTime(row map[string]any) (time.Time, bool) {
	for _, key := range []string{"date", "time", "timestamp", "bucket_start", "start"} {
		v, ok := row[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x == "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339, x); err == nil {
				return t, true
			}
			if t, err := time.Parse("2006-01-02T15:04:05Z", x); err == nil {
				return t, true
			}
			if t, err := time.Parse("2006-01-02 15:04:05", x); err == nil {
				return t, true
			}
		case float64:
			t := unixLikeTime(x)
			if !t.IsZero() {
				return t, true
			}
		case json.Number:
			if n, err := x.Float64(); err == nil {
				t := unixLikeTime(n)
				if !t.IsZero() {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func unixLikeTime(v float64) time.Time {
	if v > 1e12 {
		return time.UnixMilli(int64(v)).UTC()
	}
	if v > 1e9 {
		return time.Unix(int64(v), 0).UTC()
	}
	return time.Time{}
}

func flowChartRowFloat(row map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		v, ok := row[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			return x, true
		case json.Number:
			n, err := x.Float64()
			return n, err == nil
		case string:
			var n float64
			if _, err := fmt.Sscanf(x, "%f", &n); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func flowChartRowInt(row map[string]any, keys ...string) (int, bool) {
	n, ok := flowChartRowFloat(row, keys...)
	return int(n), ok
}

// buildChartStats emits a single-line stats summary at the top of
// the chart pane: timeframe label + OHLC of the most recent
// candle + volume + total candle count.
//
// Width-adaptive: full bar at ≥80 cells; OHLC-only at 60–80;
// just close at <60. The renderer drops fields right-to-left as
// width shrinks so the most useful (close) is the last to go.
//
// VWAP is omitted for v0.10.0 — the aggregator doesn't track
// per-candle VWAP (would need sum(price × size) per bucket which
// is a state addition with ripple). Volume + close together
// convey enough liquidity-weighted-price signal for the screener
// scan.
func buildChartStats(cs []candles.Candle, width int, timeframe time.Duration) string {
	if len(cs) == 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}
	latest := cs[len(cs)-1]
	grey := output.BrandGreyMid
	reset := output.Reset

	// Color the close in green/red based on direction relative to
	// open — gives the user instant visual signal on whether the
	// most recent bar is up or down without reading the numbers.
	closeColor := output.BrandGreen
	if latest.Close < latest.Open {
		closeColor = output.Red
	}

	tf := flowChartTimeframeLabel(timeframe)
	o := output.FormatBookPrice(latest.Open)
	h := output.FormatBookPrice(latest.High)
	l := output.FormatBookPrice(latest.Low)
	c := output.FormatBookPrice(latest.Close)
	vol := output.FormatBookSize(latest.Volume)
	count := fmt.Sprintf("%d candles", len(cs))

	// Build candidate strings for each width tier; pick the
	// widest that fits.
	full := fmt.Sprintf("%s%s · O %s · H %s · L %s · C %s%s%s · VOL %s · %s%s",
		grey, tf, o, h, l,
		closeColor, c, grey,
		vol, count, reset)
	medium := fmt.Sprintf("%s%s · O %s · H %s · L %s · C %s%s%s",
		grey, tf, o, h, l,
		closeColor, c, reset)
	minimal := fmt.Sprintf("%s%s · C %s%s%s",
		grey, tf, closeColor, c, reset)

	for _, candidate := range []string{full, medium, minimal} {
		if output.VisibleWidth(candidate) <= width {
			pad := width - output.VisibleWidth(candidate)
			if pad > 0 {
				candidate += strings.Repeat(" ", pad)
			}
			return candidate
		}
	}
	// Even the minimal didn't fit — truncate.
	return output.TruncateAnsi(minimal, width)
}

func flowChartTimeframeLabel(tf time.Duration) string {
	switch tf {
	case 5 * time.Minute:
		return "5m"
	case 15 * time.Minute:
		return "15m"
	case time.Hour:
		return "1h"
	default:
		return "1m"
	}
}

func flowChartResolution(tf time.Duration) string {
	return flowChartTimeframeLabel(tf)
}

func buildVolumeRows(cs []candles.Candle, width, height int, timeframe time.Duration, candleStride int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	rows := make([]string, height)
	for i := range rows {
		rows[i] = strings.Repeat(" ", width)
	}
	if len(cs) == 0 {
		return rows
	}

	const priceGutter = 8
	chartW := width - priceGutter
	labelW := priceGutter
	if chartW < 8 {
		chartW = width
		labelW = 0
	}
	if chartW <= 0 {
		return rows
	}

	slots := visibleChartSlots(cs, chartW, timeframe, candleStride)
	maxVol := 0.0
	for _, c := range slots {
		if c != nil && c.Volume > maxVol {
			maxVol = c.Volume
		}
	}
	if maxVol <= 0 {
		return rows
	}

	grid := make([][]string, height)
	for r := range grid {
		grid[r] = make([]string, chartW)
		for col := range grid[r] {
			grid[r][col] = " "
		}
	}
	for col, c := range slots {
		if c == nil {
			continue
		}
		barH, fracGlyph := volumeBarShape(c.Volume, maxVol, height)
		color := output.BrandGreen
		if c.Close < c.Open {
			color = output.Red
		}
		for r := height - 1; r >= height-barH; r-- {
			if r < 0 {
				break
			}
			glyph := "█"
			if r == height-barH {
				glyph = fracGlyph
			}
			grid[r][col] = color + glyph + output.Reset
		}
	}

	latest := cs[len(cs)-1]
	latestColor := output.BrandGreen
	if latest.Close < latest.Open {
		latestColor = output.Red
	}
	volLabel := latestColor + output.FormatBookSize(latest.Volume) + output.Reset

	for r := range grid {
		var b strings.Builder
		for _, cell := range grid[r] {
			b.WriteString(cell)
		}
		if labelW > 0 {
			if r == 0 {
				b.WriteString(rightAlignAnsi(volLabel, labelW))
			} else {
				b.WriteString(strings.Repeat(" ", labelW))
			}
		}
		rows[r] = output.PadRightAnsi(b.String(), width)
	}
	return rows
}

func volumeBarShape(volume, maxVol float64, height int) (int, string) {
	if volume <= 0 || maxVol <= 0 || height <= 0 {
		return 0, " "
	}
	units := int(math.Ceil((volume / maxVol) * float64(height) * 8))
	if units < 1 {
		units = 1
	}
	maxUnits := height * 8
	if units > maxUnits {
		units = maxUnits
	}
	fullRows := units / 8
	remainder := units % 8
	if remainder == 0 {
		if fullRows < 1 {
			fullRows = 1
		}
		return fullRows, "█"
	}
	return fullRows + 1, volumeBlockGlyph(remainder)
}

func volumeBlockGlyph(level int) string {
	switch {
	case level <= 1:
		return "▁"
	case level == 2:
		return "▂"
	case level == 3:
		return "▃"
	case level == 4:
		return "▄"
	case level == 5:
		return "▅"
	case level == 6:
		return "▆"
	case level == 7:
		return "▇"
	default:
		return "█"
	}
}

func overlayLatestPriceFlag(rows []string, cs []candles.Candle, width int, timeframe time.Duration, candleStride int) []string {
	if len(rows) == 0 || len(cs) == 0 || width <= 8 {
		return rows
	}
	const priceGutter = 8
	chartW := width - priceGutter
	if chartW < 4 {
		return rows
	}
	slots := visibleChartSlots(cs, chartW, timeframe, candleStride)
	lo, hi := visiblePriceRange(slots)
	latest := cs[len(cs)-1]
	row := len(rows) / 2
	if hi > lo {
		frac := (hi - latest.Close) / (hi - lo)
		row = int(math.Round(frac * float64(len(rows)-1)))
		if row < 0 {
			row = 0
		}
		if row >= len(rows) {
			row = len(rows) - 1
		}
	}
	color := output.BrandGreen
	if latest.Close < latest.Open {
		color = output.Red
	}
	label := color + output.FormatBookPrice(latest.Close) + output.Reset
	left := output.PadRightAnsi(output.TruncateAnsi(rows[row], chartW), chartW)
	rows[row] = left + rightAlignAnsi(label, priceGutter)
	return rows
}

func visiblePriceRange(slots []*candles.Candle) (float64, float64) {
	lo := math.Inf(1)
	hi := math.Inf(-1)
	for _, c := range slots {
		if c == nil {
			continue
		}
		if c.Low < lo {
			lo = c.Low
		}
		if c.High > hi {
			hi = c.High
		}
	}
	if math.IsInf(lo, 1) {
		return 0, 0
	}
	return lo, hi
}

func rightAlignAnsi(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if output.VisibleWidth(s) > width {
		return output.TruncateAnsi(s, width)
	}
	pad := width - output.VisibleWidth(s)
	return strings.Repeat(" ", pad) + s
}

// buildTimeAxis emits a single-line HH:MM axis aligned with actual
// candle body columns. Sparse charts stay sparse: 1-2 visible
// candles show latest only, 3-8 show first/latest, 9+ show
// first/middle/latest. Labels are attempted in priority order
// latest → first → middle, so collision keeps the most actionable
// timestamp.
//
// The axis returns exactly `width` cells so the panel's joined
// View doesn't shift.
func buildTimeAxis(cs []candles.Candle, width int, timeframe time.Duration) string {
	if len(cs) == 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}
	const priceGutter = 8
	chartW := width - priceGutter
	if chartW < 8 {
		chartW = width
	}
	slots := visibleChartSlots(cs, chartW, timeframe, flowChartCandleStride)
	row := make([]byte, width)
	for i := range row {
		row[i] = ' '
	}
	real := make([]struct {
		col int
		t   time.Time
	}, 0, len(slots))
	for col, c := range slots {
		if c != nil {
			real = append(real, struct {
				col int
				t   time.Time
			}{col: col, t: c.BucketStart})
		}
	}
	if len(real) == 0 {
		return string(row)
	}
	candidates := []struct {
		col int
		t   time.Time
	}{}
	switch {
	case len(real) <= 2:
		candidates = append(candidates, real[len(real)-1])
	case len(real) <= 8:
		candidates = append(candidates, real[len(real)-1], real[0])
	default:
		candidates = append(candidates, real[len(real)-1], real[0], real[len(real)/2])
	}
	for _, candidate := range candidates {
		label := candidate.t.Format("15:04")
		start := candidate.col - len(label)/2
		if start < 0 {
			start = 0
		}
		if start+len(label) > chartW {
			start = chartW - len(label)
		}
		placeAxisLabel(row, start, chartW, label)
	}
	return string(row)
}

func placeAxisLabel(row []byte, start, limit int, label string) {
	if start < 0 || start+len(label) > limit {
		return
	}
	for i := 0; i < len(label); i++ {
		if row[start+i] != ' ' {
			return
		}
	}
	copy(row[start:], label)
}

func visibleChartSlots(cs []candles.Candle, width int, timeframe time.Duration, candleStride int) []*candles.Candle {
	if len(cs) == 0 || width <= 0 {
		return nil
	}
	if timeframe <= 0 {
		timeframe = time.Minute
	}
	if candleStride <= 0 {
		candleStride = 2
	}
	rev := make([]*candles.Candle, 0, width)
	for i := len(cs) - 1; i >= 0 && len(rev) < width; i-- {
		for s := 1; s < candleStride && len(rev) < width; s++ {
			rev = append(rev, nil)
		}
		if len(rev) >= width {
			break
		}
		rev = append(rev, &cs[i])
		if i == 0 {
			break
		}
		delta := cs[i].BucketStart.Sub(cs[i-1].BucketStart)
		gaps := int(delta/timeframe) - 1
		if gaps < 0 {
			gaps = 0
		}
		gaps *= candleStride
		room := width - len(rev)
		if gaps > room {
			gaps = room
		}
		for g := 0; g < gaps; g++ {
			rev = append(rev, nil)
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if len(rev) < width {
		padded := make([]*candles.Candle, width)
		copy(padded[width-len(rev):], rev)
		return padded
	}
	return rev
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowChartPanel)(nil)
