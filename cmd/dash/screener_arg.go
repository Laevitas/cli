package dash

// Argument parser for `laevitas dash flow`. Lives in its own file
// so the parser is unit-testable without spinning up cobra or the
// dashboard. The cobra command in flow.go is a thin wrapper that
// calls into ParseFlowArgs and converts errors to fmt.Errorf.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/laevitas/cli/internal/api"
)

var flowSupportedMarkets = map[string]struct{}{
	"perpetuals": {},
	"futures":    {},
	"spot":       {},
}

var flowSortKeys = map[string]map[string]struct{}{
	"perpetuals": {
		"instrument": {}, "last": {}, "spread": {}, "volume": {}, "oi": {}, "funding": {},
	},
	"futures": {
		"instrument": {}, "last": {}, "spread": {}, "volume": {}, "oi": {}, "basis": {}, "dte": {},
	},
	"spot": {
		"instrument": {}, "last": {}, "spread": {}, "volume": {}, "quote-volume": {}, "liquidity": {},
	},
}

// FlowArgs is the parsed result of `dash flow <market> [currency]`.
// All fields are canonical.
type FlowArgs struct {
	Currency string
	Exchange string
	Market   string
	Sort     string
	SortDesc bool
}

// ParseFlowArgs validates the positional args plus scope/sort flags
// for `dash flow`. args[0] is the market token; args[1], when
// present, is the currency filter. exchange is the optional venue
// filter from --exchange. Currency and exchange can be combined to
// produce a narrow one-venue, one-currency screener.
func ParseFlowArgs(args []string, exchange, sortKey string, sortAsc bool) (FlowArgs, error) {
	if len(args) < 1 {
		return FlowArgs{}, fmt.Errorf("market is required (e.g. perpetuals BTC or spot --exchange binance)")
	}

	rawMarket := strings.TrimSpace(args[0])
	market, ok := api.NormalizeMarket(rawMarket)
	if !ok {
		return FlowArgs{}, fmt.Errorf(
			"unknown market %q. Use perpetuals, futures, or spot",
			args[0],
		)
	}
	if _, supported := flowSupportedMarkets[market]; !supported {
		return FlowArgs{}, fmt.Errorf(
			"flow dashboard supports perpetuals, futures, and spot; got %q",
			market,
		)
	}

	var currency string
	if len(args) >= 2 {
		rawCurrency := strings.TrimSpace(args[1])
		if !looksLikeCurrency(rawCurrency) {
			return FlowArgs{}, fmt.Errorf(
				"invalid currency %q. Use a short ASCII code like BTC, ETH, SOL",
				args[1],
			)
		}
		currency = strings.ToUpper(rawCurrency)
	}

	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if currency == "" && exchange == "" {
		return FlowArgs{}, fmt.Errorf("currency or --exchange is required (e.g. %s BTC or %s --exchange binance)", market, market)
	}

	sortKey = strings.ToLower(strings.TrimSpace(sortKey))
	if sortKey == "" {
		sortKey = "volume"
	}
	if _, ok := flowSortKeys[market][sortKey]; !ok {
		return FlowArgs{}, fmt.Errorf("invalid sort %q for %s. Use one of: %s", sortKey, market, flowSortKeyList(market))
	}

	return FlowArgs{
		Currency: currency,
		Exchange: exchange,
		Market:   market,
		Sort:     sortKey,
		SortDesc: !sortAsc,
	}, nil
}

func flowSortKeyList(market string) string {
	keys := make([]string, 0, len(flowSortKeys[market]))
	for key := range flowSortKeys[market] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
