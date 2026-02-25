package cmd

import (
	"fmt"
	"strings"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
)

// handleSaveCommand saves a query: save <name> <command...>
func handleSaveCommand(args []string) {
	if len(args) < 2 {
		fmt.Println("  Usage: save <name> <command...>")
		fmt.Println("  Example: save btc-funding perps funding BTC-PERPETUAL -r 1d -n 30")
		fmt.Println("  Variables: save funding perps funding {instrument} -r 1d -n 30")
		return
	}

	name := args[0]
	command := strings.Join(args[1:], " ")

	sq, err := config.LoadSaved()
	if err != nil {
		output.Errorf("Loading saved queries: %s", err)
		return
	}

	existing := sq.Get(name)
	sq.Add(name, command)

	if err := config.SaveQueries(sq); err != nil {
		output.Errorf("Saving query: %s", err)
		return
	}

	if existing != nil {
		output.Successf("Updated saved query %s%s%s", "\033[1m", name, "\033[0m")
	} else {
		output.Successf("Saved query %s%s%s", "\033[1m", name, "\033[0m")
	}

	dim := "\033[2m"
	reset := "\033[0m"
	fmt.Printf("  %s→ %s%s\n", dim, command, reset)

	placeholders := config.CountPlaceholders(command)
	if placeholders > 0 {
		fmt.Printf("  %s  %d variable(s) — run %s %s <args...>%s\n",
			dim, placeholders, name, name, reset)
	}
}

// handleRunCommand executes a saved query: run <name> [args...]
func handleRunCommand(args []string, client *api.Client) {
	if len(args) < 1 {
		fmt.Println("  Usage: run <name> [args...]")
		fmt.Println("  Example: run btc-funding")
		fmt.Println("  With vars: run funding BTC-PERPETUAL")
		return
	}

	name := args[0]
	runArgs := args[1:]

	sq, err := config.LoadSaved()
	if err != nil {
		output.Errorf("Loading saved queries: %s", err)
		return
	}

	query := sq.Get(name)
	if query == nil {
		output.Errorf("No saved query named %q. Use 'saves' to list all.", name)
		return
	}

	command := query.Command

	// Expand {variable} placeholders
	placeholders := config.CountPlaceholders(command)
	if placeholders > 0 {
		if len(runArgs) < placeholders {
			output.Errorf("Query %q expects %d argument(s) but got %d.", name, placeholders, len(runArgs))
			dim := "\033[2m"
			reset := "\033[0m"
			fmt.Printf("  %s→ %s%s\n", dim, command, reset)
			return
		}
		command = config.Expand(command, runArgs)
	}

	dim := "\033[2m"
	reset := "\033[0m"
	fmt.Printf("  %s→ %s%s\n\n", dim, command, reset)

	// Execute the expanded command through the REPL
	executeREPLCommand(command, client)
}

// handleSavesCommand lists all saved queries: saves
func handleSavesCommand() {
	sq, err := config.LoadSaved()
	if err != nil {
		output.Errorf("Loading saved queries: %s", err)
		return
	}

	if len(sq.Queries) == 0 {
		fmt.Println("  No saved queries yet.")
		fmt.Println("  Use: save <name> <command...>")
		return
	}

	bold := "\033[1m"
	dim := "\033[2m"
	cyan := "\033[36m"
	yellow := "\033[33m"
	reset := "\033[0m"

	fmt.Printf("\n  %s%sSaved Queries%s (%d)\n\n", bold, cyan, reset, len(sq.Queries))

	// Find max name width for alignment
	maxWidth := 0
	for _, q := range sq.Queries {
		if len(q.Name) > maxWidth {
			maxWidth = len(q.Name)
		}
	}

	for _, q := range sq.Queries {
		placeholders := config.CountPlaceholders(q.Command)
		varHint := ""
		if placeholders > 0 {
			varHint = fmt.Sprintf(" %s(%d var)%s", yellow, placeholders, reset)
		}
		fmt.Printf("  %s%-*s%s  %s→ %s%s%s\n",
			bold, maxWidth, q.Name, reset,
			dim, reset, q.Command, varHint)
	}
	fmt.Println()
}

// handleUnsaveCommand removes a saved query: unsave <name>
func handleUnsaveCommand(args []string) {
	if len(args) < 1 {
		fmt.Println("  Usage: unsave <name>")
		return
	}

	name := args[0]

	sq, err := config.LoadSaved()
	if err != nil {
		output.Errorf("Loading saved queries: %s", err)
		return
	}

	if !sq.Remove(name) {
		output.Errorf("No saved query named %q.", name)
		return
	}

	if err := config.SaveQueries(sq); err != nil {
		output.Errorf("Saving: %s", err)
		return
	}

	output.Successf("Removed saved query %q", name)
}
