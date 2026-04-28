package instruments

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:   "instruments",
	Short: "Cross-product instrument registry — contract specs across all exchanges",
	Long: `Browse and inspect contract specifications across spot, perpetuals, futures,
and options on every supported exchange — a single registry for instrument
metadata that complements the per-product catalog commands.`,
	Example: `  laevitas instruments list --market-type perpetual --base-currency BTC
  laevitas instruments list --exchange deribit --market-type option --expiry-from 2026-01-01T00:00:00Z
  laevitas instruments detail BTC-PERPETUAL --exchange deribit
  laevitas instruments list --status all --base-currency ETH -o json`,
}

// ─── list ───────────────────────────────────────────────────────────────────

var listFlags struct {
	cmdutil.CommonFlags
	MarketType    string
	BaseCurrency  string
	QuoteCurrency string
	Status        string
	Name          string
	MarginType    string
	OptionType    string
	ExpiryFrom    string
	ExpiryTo      string
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List instruments with filters (paginated)",
	Example: `  laevitas instruments list --market-type spot --exchange binance
  laevitas instruments list --market-type option --base-currency BTC --expiry-from 2026-06-01T00:00:00Z
  laevitas instruments list --status all --name BTC -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := listFlags.CommonFlags.ToParams()
		// Cross-product registry — no time-series, drop the time-window defaults.
		params.Start = ""
		params.End = ""
		params.Resolution = ""
		params.SortDir = ""

		params.Exchange = cmdutil.Exchange
		params.MarketType = listFlags.MarketType
		// CommonFlags.Currency carries base; the instruments endpoint uses base_currency
		// which buildURL already maps from .Currency. Use BaseCurrency override if set.
		if listFlags.BaseCurrency != "" {
			params.Currency = listFlags.BaseCurrency
		}
		params.QuoteCurrency = listFlags.QuoteCurrency
		params.Status = listFlags.Status
		params.MarginType = listFlags.MarginType
		params.OptionType = listFlags.OptionType
		params.ExpiryFrom = listFlags.ExpiryFrom
		params.ExpiryTo = listFlags.ExpiryTo
		if listFlags.Name != "" {
			params.InstrumentName = listFlags.Name
		}
		cmdutil.RunAndPrint(client, api.InstrumentsList, params)
	},
}

// ─── detail ─────────────────────────────────────────────────────────────────

var detailCmd = &cobra.Command{
	Use:     "detail <instrument>",
	Aliases: []string{"show"},
	Short:   "Full contract specification for a single instrument",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas instruments detail BTC-PERPETUAL --exchange deribit
  laevitas instruments detail BTCUSDT --exchange binance
  laevitas instruments detail {{OPT_P}} --exchange deribit`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.InstrumentsDetail, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(listCmd, &listFlags.CommonFlags)
	listCmd.Flags().StringVar(&listFlags.MarketType, "market-type", "", "Filter: spot, perpetual, future, option")
	listCmd.Flags().StringVar(&listFlags.BaseCurrency, "base-currency", "", "Filter by base currency (BTC, ETH, SOL)")
	listCmd.Flags().StringVar(&listFlags.QuoteCurrency, "quote-currency", "", "Filter by quote currency (USD, USDT, USDC)")
	listCmd.Flags().StringVar(&listFlags.Status, "status", "", "Filter: active (default), expired, delisted, suspended, all")
	listCmd.Flags().StringVar(&listFlags.Name, "name", "", "Partial match on instrument name (case-insensitive)")
	listCmd.Flags().StringVar(&listFlags.MarginType, "margin-type", "", "Filter: linear or inverse")
	listCmd.Flags().StringVar(&listFlags.OptionType, "option-type", "", "Filter: call or put")
	listCmd.Flags().StringVar(&listFlags.ExpiryFrom, "expiry-from", "", "Min expiry datetime (ISO 8601)")
	listCmd.Flags().StringVar(&listFlags.ExpiryTo, "expiry-to", "", "Max expiry datetime (ISO 8601)")

	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(detailCmd)
}
