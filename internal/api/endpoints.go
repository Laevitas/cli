package api

// ─── General ────────────────────────────────────────────────────────────

const (
	Health = "/api/v1/health"
)

// ─── Futures ────────────────────────────────────────────────────────────

const (
	FuturesCatalog        = "/api/v1/futures/catalog"
	FuturesSnapshot       = "/api/v1/futures/snapshot"
	FuturesOHLCVT         = "/api/v1/futures/ohlcvt"
	FuturesOpenInterest   = "/api/v1/futures/open-interest"
	FuturesCarry          = "/api/v1/futures/carry"
	FuturesTrades         = "/api/v1/futures/trades"
	FuturesVolume         = "/api/v1/futures/volume"
	FuturesLevel1         = "/api/v1/futures/level1"
	FuturesOrderbook      = "/api/v1/futures/orderbook"
	FuturesOrderbookRaw   = "/api/v1/futures/orderbook-raw"
	FuturesReferencePrice = "/api/v1/futures/reference-price"
	FuturesTickerHistory  = "/api/v1/futures/ticker-history"
	FuturesMetadata       = "/api/v1/futures/metadata"
)

// ─── Perpetuals ─────────────────────────────────────────────────────────

const (
	PerpsCatalog        = "/api/v1/perpetuals/catalog"
	PerpsSnapshot       = "/api/v1/perpetuals/snapshot"
	PerpsOHLCVT         = "/api/v1/perpetuals/ohlcvt"
	PerpsOpenInterest   = "/api/v1/perpetuals/open-interest"
	PerpsCarry          = "/api/v1/perpetuals/carry"
	PerpsTrades         = "/api/v1/perpetuals/trades"
	PerpsVolume         = "/api/v1/perpetuals/volume"
	PerpsLevel1         = "/api/v1/perpetuals/level1"
	PerpsOrderbook      = "/api/v1/perpetuals/orderbook"
	PerpsOrderbookRaw   = "/api/v1/perpetuals/orderbook-raw"
	PerpsReferencePrice = "/api/v1/perpetuals/reference-price"
	PerpsTickerHistory  = "/api/v1/perpetuals/ticker-history"
	PerpsMetadata       = "/api/v1/perpetuals/metadata"
)

// ─── Options ────────────────────────────────────────────────────────────

const (
	OptionsCatalog        = "/api/v1/options/catalog"
	OptionsSnapshot       = "/api/v1/options/snapshot"
	OptionsOHLCVT         = "/api/v1/options/ohlcvt"
	OptionsOpenInterest   = "/api/v1/options/open-interest"
	OptionsTrades         = "/api/v1/options/trades"
	OptionsFlow           = "/api/v1/options/flow"
	OptionsVolume         = "/api/v1/options/volume"
	OptionsLevel1         = "/api/v1/options/level1"
	OptionsReferencePrice = "/api/v1/options/reference-price"
	OptionsTickerHistory  = "/api/v1/options/ticker-history"
	OptionsVolatility     = "/api/v1/options/volatility"
	OptionsMetadata       = "/api/v1/options/metadata"
)

// ─── Volatility Surface (under /options/) ───────────────────────────────

const (
	VolSurfaceByExpiry = "/api/v1/options/vol-surface/by-expiry"
	VolSurfaceByTenor  = "/api/v1/options/vol-surface/by-tenor"
	VolSurfaceByTime   = "/api/v1/options/vol-surface/by-time"
)

// ─── Predictions ────────────────────────────────────────────────────────

const (
	PredictionsCatalog       = "/api/v1/predictions/catalog"
	PredictionsCategories    = "/api/v1/predictions/categories"
	PredictionsSnapshot      = "/api/v1/predictions/snapshot"
	PredictionsOHLCVT        = "/api/v1/predictions/ohlcvt"
	PredictionsTrades        = "/api/v1/predictions/trades"
	PredictionsTickerHistory = "/api/v1/predictions/ticker-history"
	PredictionsOrderbookRaw  = "/api/v1/predictions/orderbook-raw"
	PredictionsMetadata      = "/api/v1/predictions/metadata"
)
