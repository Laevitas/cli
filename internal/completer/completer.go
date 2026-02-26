package completer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/laevitas/cli/internal/api"
)

// ─── Command tree definition ─────────────────────────────────────────────────

// subCmd describes a subcommand: its name and whether it expects a positional
// instrument argument (e.g. "ohlcv <instrument>").
type subCmd struct {
	Name           string
	NeedsInstrument bool
}

// commandTree defines the full command hierarchy with instrument expectations.
// The key is the top-level command name (e.g. "futures").
var commandTree = map[string][]subCmd{
	"futures": {
		{Name: "catalog"},
		{Name: "snapshot"},
		{Name: "ohlcvt", NeedsInstrument: true},
		{Name: "oi", NeedsInstrument: true},
		{Name: "carry", NeedsInstrument: true},
		{Name: "trades", NeedsInstrument: true},
		{Name: "volume", NeedsInstrument: true},
		{Name: "level1", NeedsInstrument: true},
		{Name: "orderbook", NeedsInstrument: true},
		{Name: "ticker", NeedsInstrument: true},
		{Name: "ref-price", NeedsInstrument: true},
		{Name: "metadata", NeedsInstrument: true},
	},
	"perps": {
		{Name: "catalog"},
		{Name: "snapshot"},
		{Name: "carry", NeedsInstrument: true},
		{Name: "ohlcvt", NeedsInstrument: true},
		{Name: "oi", NeedsInstrument: true},
		{Name: "trades", NeedsInstrument: true},
		{Name: "volume", NeedsInstrument: true},
		{Name: "level1", NeedsInstrument: true},
		{Name: "orderbook", NeedsInstrument: true},
		{Name: "ticker", NeedsInstrument: true},
		{Name: "ref-price", NeedsInstrument: true},
		{Name: "metadata", NeedsInstrument: true},
	},
	"options": {
		{Name: "catalog"},
		{Name: "snapshot"},
		{Name: "flow"},
		{Name: "trades"},
		{Name: "ohlcvt", NeedsInstrument: true},
		{Name: "oi", NeedsInstrument: true},
		{Name: "volatility", NeedsInstrument: true},
		{Name: "level1", NeedsInstrument: true},
		{Name: "ticker", NeedsInstrument: true},
		{Name: "volume", NeedsInstrument: true},
		{Name: "ref-price", NeedsInstrument: true},
		{Name: "metadata", NeedsInstrument: true},
		{Name: "vol-surface"},
	},
	"predictions": {
		{Name: "catalog"},
		{Name: "categories"},
		{Name: "snapshot"},
		{Name: "ohlcvt", NeedsInstrument: true},
		{Name: "trades", NeedsInstrument: true},
		{Name: "ticker", NeedsInstrument: true},
		{Name: "orderbook", NeedsInstrument: true},
		{Name: "metadata", NeedsInstrument: true},
	},
	"config": {
		{Name: "init"},
		{Name: "show"},
		{Name: "set"},
		{Name: "unset"},
		{Name: "path"},
	},
	"watch": {},
}

// topLevelCommands includes both tree commands and REPL built-ins.
var topLevelCommands = []string{
	"futures", "perps", "options", "predictions",
	"config", "watch", "version", "search",
	"save", "run", "saves", "unsave",
	"help", "quit", "exit", "clear",
}

// savedNameCommands are commands whose first argument should complete
// with saved query names.
var savedNameCommands = map[string]bool{
	"run":    true,
	"unsave": true,
}

// configSetKeys are valid keys for "config set <key>".
var configSetKeys = []string{
	"api_key", "exchange", "output", "base_url", "wallet_key", "auth",
}

// configUnsetKeys are valid keys for "config unset <key>".
var configUnsetKeys = []string{
	"api_key", "wallet_key",
}

// configValueOptions maps config keys to their valid values for "config set <key> <value>".
var configValueOptions = map[string][]string{
	"auth":     {"auto", "api-key", "x402"},
	"output":   {"auto", "json", "table", "csv"},
	"exchange": {"deribit", "binance", "bybit", "okx"},
}

// catalogEndpoints maps top-level command to the API endpoint for its catalog.
var catalogEndpoints = map[string]string{
	"futures":     api.FuturesCatalog,
	"perps":       api.PerpsCatalog,
	"options":     api.OptionsCatalog,
	"predictions": api.PredictionsCatalog,
}

// ─── Completer ───────────────────────────────────────────────────────────────

// Completer implements readline.AutoCompleter with dynamic instrument lookup.
type Completer struct {
	client *api.Client

	mu       sync.RWMutex
	catalogs map[string][]string // "futures" → ["BTC-28MAR25", ...]

	// SavedNamesFunc returns the names of saved queries for tab-completion.
	// Set by the caller (interactive.go) after loading saved queries.
	SavedNamesFunc func() []string
}

// New creates a new Completer backed by the given API client.
func New(client *api.Client) *Completer {
	return &Completer{
		client:   client,
		catalogs: make(map[string][]string),
	}
}

// Do implements readline.AutoCompleter.
// It receives the entire line as runes and the cursor position,
// and returns completion suffixes and the shared prefix length.
func (c *Completer) Do(line []rune, pos int) ([][]rune, int) {
	// Only complete up to the cursor position
	lineStr := string(line[:pos])

	// Split into segments
	segments := splitLine(lineStr)
	trailing := len(lineStr) > 0 && lineStr[len(lineStr)-1] == ' '

	// Determine what to complete based on segment count
	switch {
	case len(segments) == 0 || (len(segments) == 1 && !trailing):
		// Completing the top-level command
		prefix := ""
		if len(segments) == 1 {
			prefix = segments[0]
		}
		return filterCompletions(topLevelCommands, prefix)

	case len(segments) == 1 && trailing:
		// Top-level command typed, now complete subcommand or saved name
		cmd := strings.ToLower(segments[0])
		if savedNameCommands[cmd] {
			return c.completeSavedNames("")
		}
		return c.completeSubcommand(segments[0], "")

	case len(segments) == 2 && !trailing:
		// Partially typed subcommand or saved name
		cmd := strings.ToLower(segments[0])
		if savedNameCommands[cmd] {
			return c.completeSavedNames(segments[1])
		}
		return c.completeSubcommand(segments[0], segments[1])

	case len(segments) == 2 && trailing:
		// Subcommand complete — complete config keys or instrument
		if strings.ToLower(segments[0]) == "config" {
			return c.completeConfigKey(segments[1], "")
		}
		return c.completeInstrument(segments[0], segments[1], "")

	case len(segments) == 3 && !trailing:
		// Partially typed 3rd arg — config key or instrument
		if strings.ToLower(segments[0]) == "config" {
			return c.completeConfigKey(segments[1], segments[2])
		}
		last := segments[2]
		if !strings.HasPrefix(last, "-") {
			return c.completeInstrument(segments[0], segments[1], last)
		}
		return nil, 0

	case len(segments) == 3 && trailing:
		// 3rd arg complete — config value completion (e.g. "config set auth ")
		if strings.ToLower(segments[0]) == "config" {
			return c.completeConfigValue(segments[2], "")
		}
		return nil, 0

	case len(segments) == 4 && !trailing:
		// Partially typed 4th arg — config value (e.g. "config set auth au")
		if strings.ToLower(segments[0]) == "config" {
			return c.completeConfigValue(segments[2], segments[3])
		}
		return nil, 0

	case len(segments) >= 3 && !trailing:
		// Could be partially typed instrument
		last := segments[len(segments)-1]
		if !strings.HasPrefix(last, "-") {
			return c.completeInstrument(segments[0], segments[1], last)
		}
		return nil, 0

	default:
		return nil, 0
	}
}

// completeSubcommand returns completions for subcommands of the given parent.
func (c *Completer) completeSubcommand(parent, prefix string) ([][]rune, int) {
	parent = strings.ToLower(parent)
	subs, ok := commandTree[parent]
	if !ok {
		return nil, 0
	}

	names := make([]string, 0, len(subs))
	for _, s := range subs {
		names = append(names, s.Name)
	}
	return filterCompletions(names, prefix)
}

// completeInstrument returns completions for instrument names.
// It fetches and caches the catalog for the given parent command.
func (c *Completer) completeInstrument(parent, sub, prefix string) ([][]rune, int) {
	parent = strings.ToLower(parent)
	sub = strings.ToLower(sub)

	// Check if this subcommand needs an instrument
	subs, ok := commandTree[parent]
	if !ok {
		return nil, 0
	}
	needsInstrument := false
	for _, s := range subs {
		if s.Name == sub {
			needsInstrument = s.NeedsInstrument
			break
		}
	}
	if !needsInstrument {
		return nil, 0
	}

	instruments := c.getCatalog(parent)
	if len(instruments) == 0 {
		return nil, 0
	}

	return filterCompletions(instruments, strings.ToUpper(prefix))
}

// completeConfigKey returns completions for config key names.
func (c *Completer) completeConfigKey(subCmd, prefix string) ([][]rune, int) {
	switch strings.ToLower(subCmd) {
	case "set":
		return filterCompletions(configSetKeys, prefix)
	case "unset":
		return filterCompletions(configUnsetKeys, prefix)
	}
	return nil, 0
}

// completeConfigValue returns completions for config values given a key.
func (c *Completer) completeConfigValue(key, prefix string) ([][]rune, int) {
	opts, ok := configValueOptions[strings.ToLower(key)]
	if !ok {
		return nil, 0
	}
	return filterCompletions(opts, prefix)
}

// completeSavedNames returns completions for saved query names.
func (c *Completer) completeSavedNames(prefix string) ([][]rune, int) {
	if c.SavedNamesFunc == nil {
		return nil, 0
	}
	names := c.SavedNamesFunc()
	return filterCompletions(names, prefix)
}

// getCatalog returns cached instruments for the given category,
// fetching from the API on first access.
func (c *Completer) getCatalog(category string) []string {
	c.mu.RLock()
	cached, ok := c.catalogs[category]
	c.mu.RUnlock()
	if ok {
		return cached
	}

	// Fetch from API
	endpoint, ok := catalogEndpoints[category]
	if !ok {
		return nil
	}

	instruments := fetchInstrumentNames(c.client, endpoint)
	if instruments == nil {
		return nil
	}

	c.mu.Lock()
	c.catalogs[category] = instruments
	c.mu.Unlock()

	return instruments
}

// GetAllInstruments returns all cached instruments across all categories.
// It triggers fetching for any category not yet cached.
func (c *Completer) GetAllInstruments() map[string][]string {
	result := make(map[string][]string)
	for cat := range catalogEndpoints {
		instruments := c.getCatalog(cat)
		if len(instruments) > 0 {
			result[cat] = instruments
		}
	}
	return result
}

// Search performs a fuzzy search across all instrument catalogs.
// Each keyword must be a case-insensitive substring of the instrument name.
// Returns results as "category: instrument" strings, sorted.
func (c *Completer) Search(keywords []string) []SearchResult {
	all := c.GetAllInstruments()

	var results []SearchResult
	for cat, instruments := range all {
		for _, inst := range instruments {
			if matchesAll(inst, keywords) {
				results = append(results, SearchResult{
					Category:   cat,
					Instrument: inst,
				})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Category != results[j].Category {
			return results[i].Category < results[j].Category
		}
		return results[i].Instrument < results[j].Instrument
	})
	return results
}

// SearchResult holds a single search match.
type SearchResult struct {
	Category   string
	Instrument string
}

// PreloadCatalogs fetches all catalogs in the background.
func (c *Completer) PreloadCatalogs() {
	for cat := range catalogEndpoints {
		go c.getCatalog(cat)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// fetchInstrumentNames calls the catalog endpoint and extracts instrument_name
// from each record in the response.
func fetchInstrumentNames(client *api.Client, endpoint string) []string {
	data, err := client.Get(endpoint, nil)
	if err != nil {
		return nil
	}

	// Try as array of objects
	var records []map[string]interface{}
	if json.Unmarshal(data, &records) != nil {
		// Try { "data": [...] } wrapper
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &wrapper) != nil || wrapper.Data == nil {
			return nil
		}
		if json.Unmarshal(wrapper.Data, &records) != nil {
			return nil
		}
	}

	seen := make(map[string]bool)
	var names []string
	for _, rec := range records {
		name, ok := rec["instrument_name"]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", name)
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}

	sort.Strings(names)
	return names
}

// filterCompletions returns completions matching the given prefix.
// It returns the suffixes (what to append) and the shared prefix length.
func filterCompletions(candidates []string, prefix string) ([][]rune, int) {
	prefixLower := strings.ToLower(prefix)
	var matches [][]rune

	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), prefixLower) {
			// Return the suffix (part after the prefix) plus a trailing space
			suffix := c[len(prefix):] + " "
			matches = append(matches, []rune(suffix))
		}
	}
	return matches, len(prefix)
}

// matchesAll returns true if all keywords are found as case-insensitive
// substrings in the given string.
func matchesAll(s string, keywords []string) bool {
	upper := strings.ToUpper(s)
	for _, kw := range keywords {
		if !strings.Contains(upper, strings.ToUpper(kw)) {
			return false
		}
	}
	return true
}

// splitLine splits a line into space-separated tokens.
func splitLine(s string) []string {
	fields := strings.Fields(s)
	return fields
}
