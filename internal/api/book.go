// Book payload types for the WebSocket book.* channels. Lives in
// internal/api because the wire format is a domain concern, not a
// rendering concern — both the rolling-tape book renderer
// (internal/wsrender) and the new dashboard book panel
// (internal/dashboard/panels) consume the same struct.
//
// We deliberately avoid putting these in a renderer package: that
// would make the dashboard kernel depend on a leaf renderer, which
// is the wrong direction architecturally. Domain types live with
// the API client; renderers and panels import them.
package api

import (
	"encoding/json"
	"time"
)

// BookSnapshot is the parsed wire payload from the book.* channel.
// Every field on the wire is captured even if the renderer doesn't
// always show it — extra fields cost nothing on a single payload
// per message and we'd rather not re-decode if a panel decides it
// needs `quote_currency` later.
//
// Channel and ReceivedAt are renderer-local — populated by the
// caller when the snapshot is enqueued, not part of the wire shape.
type BookSnapshot struct {
	Channel        string      `json:"-"`
	ReceivedAt     time.Time   `json:"-"`
	Timestamp      int64       `json:"timestamp"`
	Exchange       string      `json:"exchange"`
	InstrumentName string      `json:"instrument_name"`
	Currency       string      `json:"currency"`
	InstrumentType string      `json:"instrument_type"`
	QuoteCurrency  string      `json:"quote_currency,omitempty"`
	Depth          int         `json:"depth"`
	Asks           []BookLevel `json:"asks"`
	Bids           []BookLevel `json:"bids"`
	AskLiq10       float64     `json:"ask_liquidity_10"`
	AskLiq20       float64     `json:"ask_liquidity_20"`
	AskLiq50       float64     `json:"ask_liquidity_50"`
	AskLiq100      float64     `json:"ask_liquidity_100"`
	BidLiq10       float64     `json:"bid_liquidity_10"`
	BidLiq20       float64     `json:"bid_liquidity_20"`
	BidLiq50       float64     `json:"bid_liquidity_50"`
	BidLiq100      float64     `json:"bid_liquidity_100"`
	Imbalance10    float64     `json:"imbalance_10"`
	Imbalance20    float64     `json:"imbalance_20"`
	Imbalance50    float64     `json:"imbalance_50"`
	Imbalance100   float64     `json:"imbalance_100"`
	Microprice     float64     `json:"microprice"`
}

// BookLevel is one (price, size) pair. The wire format is normally
// a JSON tuple [price, size], but predictions currently emit objects
// {price, size} (producer fix in flight per the API team). Both
// shapes decode through the custom UnmarshalJSON so callers don't
// have to special-case predictions.
type BookLevel struct {
	Price float64
	Size  float64
}

// UnmarshalJSON accepts either the tuple form ([price, size]) or
// the object form ({"price": ..., "size": ...}). Tuple is the
// canonical wire format; object is the predictions-only legacy
// shape. Once the producer's tuple-fix lands across all markets
// we can simplify this back to a plain [2]float64 unmarshal.
func (b *BookLevel) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		var tuple [2]float64
		if err := json.Unmarshal(data, &tuple); err != nil {
			return err
		}
		b.Price, b.Size = tuple[0], tuple[1]
		return nil
	}
	var obj struct {
		Price float64 `json:"price"`
		Size  float64 `json:"size"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	b.Price, b.Size = obj.Price, obj.Size
	return nil
}

// LiquidityForTier returns (bidLiq, askLiq, imbalance) for a given
// tier (10, 20, 50, or 100). Centralised here so every renderer
// reads the same field without re-implementing the switch. Falls
// back to tier-10 when given an unknown tier.
func (s *BookSnapshot) LiquidityForTier(tier int) (bidLiq, askLiq, imbalance float64) {
	switch tier {
	case 10:
		return s.BidLiq10, s.AskLiq10, s.Imbalance10
	case 20:
		return s.BidLiq20, s.AskLiq20, s.Imbalance20
	case 50:
		return s.BidLiq50, s.AskLiq50, s.Imbalance50
	case 100:
		return s.BidLiq100, s.AskLiq100, s.Imbalance100
	default:
		return s.BidLiq10, s.AskLiq10, s.Imbalance10
	}
}

// BestLevels returns top-of-book on each side. Empty struct (Price
// == 0, Size == 0) is returned for an empty side rather than a nil
// pointer — saves callers a length check before display.
func (s *BookSnapshot) BestLevels() (bid, ask BookLevel) {
	if len(s.Bids) > 0 {
		bid = s.Bids[0]
	}
	if len(s.Asks) > 0 {
		ask = s.Asks[0]
	}
	return bid, ask
}
