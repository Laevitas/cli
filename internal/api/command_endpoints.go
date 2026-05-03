package api

// CommandEndpoints returns the canonical mapping from a command's
// space-separated CLI path (e.g. "perps orderbook-raw") to the REST
// endpoint URL it hits. Used by:
//   - watch mode (cmd/watch.go) to resolve "watch 5s perps snapshot ..."
//     into the underlying endpoint to poll.
//   - the introspection command (cmd/commands.go) to surface
//     `endpoint_hint` per command in the JSON manifest.
//
// Single source of truth so the two paths can't drift. Any new REST
// command goes here AND gets a cobra subcommand AND a completer entry
// (per CLAUDE.md mistake #6).
//
// Naming note: the key is the subcommand path AS THE USER TYPES IT,
// not the variable name in code. For example the options vol-surface
// subcommand whose Use is "snapshot" is keyed "options vol-surface
// snapshot" even though its endpoint constant is VolSurfaceByExpiry —
// the legacy endpoint name doesn't match the user-facing command name.
//
// Streaming commands (ws book/trades/etc.) and dashboard commands
// don't appear here — they don't have a single REST endpoint. The
// commands manifest reports `endpoint_hint: ""` and `streaming: true`
// for those.
func CommandEndpoints() map[string]string {
	return map[string]string{
		// Futures
		"futures catalog":        FuturesCatalog,
		"futures snapshot":       FuturesSnapshot,
		"futures ohlcvt":         FuturesOHLCVT,
		"futures oi":             FuturesOpenInterest,
		"futures carry":          FuturesCarry,
		"futures trades":         FuturesTrades,
		"futures volume":         FuturesVolume,
		"futures level1":         FuturesLevel1,
		"futures orderbook":      FuturesOrderbook,
		"futures orderbook-raw":  FuturesOrderbookRaw,
		"futures ticker":         FuturesTickerHistory,
		"futures ref-price":      FuturesReferencePrice,
		"futures metadata":       FuturesMetadata,
		"futures liquidations":   FuturesLiquidations,
		"futures trades-summary": FuturesTradesSummary,
		"futures flow":           FuturesFlow,

		// Perps
		"perps catalog":        PerpsCatalog,
		"perps snapshot":       PerpsSnapshot,
		"perps carry":          PerpsCarry,
		"perps ohlcvt":         PerpsOHLCVT,
		"perps oi":             PerpsOpenInterest,
		"perps trades":         PerpsTrades,
		"perps volume":         PerpsVolume,
		"perps level1":         PerpsLevel1,
		"perps orderbook":      PerpsOrderbook,
		"perps orderbook-raw":  PerpsOrderbookRaw,
		"perps ticker":         PerpsTickerHistory,
		"perps ref-price":      PerpsReferencePrice,
		"perps metadata":       PerpsMetadata,
		"perps liquidations":   PerpsLiquidations,
		"perps trades-summary": PerpsTradesSummary,
		"perps flow":           PerpsFlow,

		// Options
		"options catalog":        OptionsCatalog,
		"options snapshot":       OptionsSnapshot,
		"options ohlcvt":         OptionsOHLCVT,
		"options trades":         OptionsTrades,
		"options oi":             OptionsOpenInterest,
		"options volume":         OptionsVolume,
		"options level1":         OptionsLevel1,
		"options ref-price":      OptionsReferencePrice,
		"options flow":           OptionsFlow,
		"options ticker":         OptionsTickerHistory,
		"options volatility":     OptionsVolatility,
		"options metadata":       OptionsMetadata,
		"options trades-summary": OptionsTradesSummary,

		// Vol surface (under options) — keys reflect the user-facing
		// subcommand names (snapshot/term-structure/history), not the
		// legacy endpoint names (by-expiry/by-tenor/by-time).
		"options vol-surface snapshot":       VolSurfaceByExpiry,
		"options vol-surface term-structure": VolSurfaceByTenor,
		"options vol-surface history":        VolSurfaceByTime,

		// Predictions
		"predictions catalog":    PredictionsCatalog,
		"predictions categories": PredictionsCategories,
		"predictions snapshot":   PredictionsSnapshot,
		"predictions ohlcvt":     PredictionsOHLCVT,
		"predictions trades":     PredictionsTrades,
		"predictions orderbook":  PredictionsOrderbookRaw,
		"predictions ticker":     PredictionsTickerHistory,
		"predictions metadata":   PredictionsMetadata,

		// Spot
		"spot catalog":       SpotCatalog,
		"spot snapshot":      SpotSnapshot,
		"spot metadata":      SpotMetadata,
		"spot ohlcvt":        SpotOHLCVT,
		"spot ticker":        SpotTicker,
		"spot volume":        SpotVolume,
		"spot level1":        SpotLevel1,
		"spot orderbook":     SpotL2Orderbook,
		"spot orderbook-raw": SpotL2OrderbookRaw,
		"spot trades":        SpotTrades,

		// Cross-product instruments registry
		"instruments list":   InstrumentsList,
		"instruments detail": InstrumentsDetail,

		// Analytics
		"analytics realized-volatility": AnalyticsRealizedVolatility,
	}
}
