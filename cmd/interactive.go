package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/chzyer/readline"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/completer"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/version"
)

const banner = `
  ██╗      █████╗ ███████╗██╗   ██╗██╗████████╗ █████╗ ███████╗
  ██║     ██╔══██╗██╔════╝██║   ██║██║╚══██╔══╝██╔══██╗██╔════╝
  ██║     ███████║█████╗  ██║   ██║██║   ██║   ███████║███████╗
  ██║     ██╔══██║██╔══╝  ╚██╗ ██╔╝██║   ██║   ██╔══██║╚════██║
  ███████╗██║  ██║███████╗ ╚████╔╝ ██║   ██║   ██║  ██║███████║
  ╚══════╝╚═╝  ╚═╝╚══════╝  ╚═══╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚══════╝
`

func printBanner() {
	cyan := "\033[36m"
	bold := "\033[1m"
	dim := "\033[2m"
	reset := "\033[0m"

	fmt.Fprintf(os.Stdout, "%s%s%s%s", bold, cyan, banner, reset)
	fmt.Fprintf(os.Stdout, "  %sDerivatives Data Without The Spread%s            %sv%s%s\n", dim, reset, dim, version.Version, reset)
	fmt.Fprintf(os.Stdout, "  Type %s'help'%s for commands, %s'quit'%s to exit\n\n", bold, reset, bold, reset)
}

// replCompleter is the session-scoped completer with catalog caching.
var replCompleter *completer.Completer

func runInteractive() error {
	printBanner()

	// Load config and create a persistent API client
	cfg, err := config.Load()
	if err != nil {
		output.Errorf("Loading config: %s", err)
		return err
	}

	// If no API key, run inline onboarding before entering the REPL
	if cfg.APIKey == "" {
		bold := "\033[1m"
		dim := "\033[2m"
		reset := "\033[0m"
		fmt.Println("  Welcome to Laevitas CLI!")
		fmt.Println()
		fmt.Println("  Derivatives data for your terminal -- futures, perps, options,")
		fmt.Println("  vol surfaces, and prediction markets across 15+ exchanges.")
		fmt.Println()
		fmt.Printf("  Quick start:\n")
		fmt.Printf("    %slaevitas config init%s           Set up your API key\n", bold, reset)
		fmt.Printf("    %slaevitas futures catalog%s       Browse available instruments\n", bold, reset)
		fmt.Printf("    %slaevitas perps carry BTC-PERPETUAL%s  Check funding rates\n", bold, reset)
		fmt.Println()
		fmt.Printf("  Get an API key: %shttps://app.laevitas.ch%s (Enterprise plan)\n", bold, reset)
		fmt.Printf("  %sDocs:    https://apiv2.laevitas.ch/redoc%s\n", dim, reset)
		fmt.Printf("  %sDiscord: https://discord.com/invite/yaXc4EFFay%s\n", dim, reset)
		fmt.Println()

		reader := bufio.NewReader(os.Stdin)
		fmt.Print("  Paste your API key: ")
		key, readErr := reader.ReadString('\n')
		if readErr != nil {
			output.Errorf("Reading input: %s", readErr)
			return readErr
		}
		key = strings.TrimSpace(key)
		if key == "" {
			output.Errorf("No API key provided.")
			return fmt.Errorf("no API key configured")
		}
		cfg.APIKey = key
		if saveErr := config.Save(cfg); saveErr != nil {
			output.Errorf("Saving config: %s", saveErr)
			return saveErr
		}
		output.Successf("API key saved to ~/.config/laevitas/config.json")
		fmt.Println()
	}

	client := api.NewClient(cfg)

	// Create the dynamic completer and preload catalogs in background
	replCompleter = completer.New(client)
	replCompleter.PreloadCatalogs()

	// Wire saved query name completion
	replCompleter.SavedNamesFunc = func() []string {
		sq, err := config.LoadSaved()
		if err != nil {
			return nil
		}
		return sq.Names()
	}

	prompt := "\033[36mLAEVITAS\033[0m > "

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     historyFilePath(),
		AutoComplete:    replCompleter,
		InterruptPrompt: "^C",
		EOFPrompt:       "quit",
	})
	if err != nil {
		return fmt.Errorf("initializing readline: %w", err)
	}
	defer rl.Close()

	// Store the shared client so commands can pick it up in REPL mode
	cmdutil.SharedClient = client

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			// Ctrl+C — if line is empty, quit; otherwise just clear it
			if len(line) == 0 {
				fmt.Println("Bye!")
				return nil
			}
			continue
		}
		if err == io.EOF {
			fmt.Println("Bye!")
			return nil
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch strings.ToLower(line) {
		case "quit", "exit":
			fmt.Println("Bye!")
			return nil
		case "clear":
			fmt.Print("\033[H\033[2J")
			continue
		}

		// Handle REPL-only commands before passing to cobra
		args := splitArgs(line)

		// Strip leading "laevitas" — users often copy examples from help text
		if len(args) > 1 && strings.ToLower(args[0]) == "laevitas" {
			args = args[1:]
			line = strings.Join(args, " ")
		}

		if len(args) >= 1 {
			switch strings.ToLower(args[0]) {
			case "search":
				runSearch(args[1:])
				continue
			case "save":
				handleSaveCommand(args[1:])
				continue
			case "run":
				handleRunCommand(args[1:], client)
				continue
			case "saves":
				handleSavesCommand()
				continue
			case "unsave":
				handleUnsaveCommand(args[1:])
				continue
			}
		}

		executeREPLCommand(line, client)
	}
}

func executeREPLCommand(line string, client *api.Client) {
	args := splitArgs(line)
	if len(args) == 0 {
		return
	}

	// Strip leading "laevitas" — users often copy examples from help text
	if strings.ToLower(args[0]) == "laevitas" {
		args = args[1:]
		if len(args) == 0 {
			return
		}
	}

	// Handle bare "help" → show root help
	if args[0] == "help" {
		if len(args) == 1 {
			rootCmd.SetArgs([]string{"--help"})
			rootCmd.Execute()
			return
		}
		// "help futures" → "futures --help"
		args = append(args[1:], "--help")
	}

	// Start spinner
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Loading..."
	s.Color("cyan")
	cmdutil.SpinnerInstance = s

	// Reset the root command's flag state for re-entrant execution.
	// Cobra caches parsed flags; we need to reset them between invocations.
	rootCmd.SetArgs(args)

	// Temporarily override os.Exit behavior — Cobra commands call os.Exit(1)
	// on errors, but we don't want to kill the REPL.
	cmdutil.InteractiveMode = true
	defer func() {
		cmdutil.InteractiveMode = false
		cmdutil.SpinnerInstance = nil
	}()

	if err := rootCmd.Execute(); err != nil {
		output.Errorf("%s", err)
	}

	// Reset flags for next command
	resetFlags()
}

// resetFlags clears persistent flag values back to defaults so they
// don't leak between REPL commands.
func resetFlags() {
	outputFormat = "auto"
	exchange = ""
	verbose = false
	noChart = false
	rootCmd.PersistentFlags().Set("output", "auto")
	rootCmd.PersistentFlags().Set("exchange", "")
	rootCmd.PersistentFlags().Set("verbose", "false")
	rootCmd.PersistentFlags().Set("no-chart", "false")
}

// runSearch performs a fuzzy search across all instrument catalogs.
func runSearch(keywords []string) {
	if len(keywords) == 0 {
		fmt.Println("  Usage: search <keywords...>")
		fmt.Println("  Example: search btc mar")
		return
	}

	if replCompleter == nil {
		output.Errorf("Completer not initialized.")
		return
	}

	results := replCompleter.Search(keywords)
	if len(results) == 0 {
		fmt.Printf("  No instruments matching %s\n", strings.Join(keywords, " "))
		return
	}

	bold := "\033[1m"
	dim := "\033[2m"
	cyan := "\033[36m"
	reset := "\033[0m"

	fmt.Printf("\n  %s%d match(es)%s for %s%s%s:\n\n",
		dim, len(results), reset,
		bold, strings.Join(keywords, " "), reset)

	// Group by category
	grouped := make(map[string][]string)
	var cats []string
	for _, r := range results {
		if _, ok := grouped[r.Category]; !ok {
			cats = append(cats, r.Category)
		}
		grouped[r.Category] = append(grouped[r.Category], r.Instrument)
	}

	for _, cat := range cats {
		instruments := grouped[cat]
		fmt.Printf("  %s%s%s%s\n", bold, cyan, strings.ToUpper(cat), reset)
		for _, inst := range instruments {
			// Highlight matching parts
			display := inst
			for _, kw := range keywords {
				display = highlightSubstring(display, kw)
			}
			fmt.Printf("    %s\n", display)
		}
		fmt.Println()
	}
}

// highlightSubstring highlights the first case-insensitive occurrence of substr
// in s using bold yellow ANSI codes.
func highlightSubstring(s, substr string) string {
	upper := strings.ToUpper(s)
	kwUpper := strings.ToUpper(substr)
	idx := strings.Index(upper, kwUpper)
	if idx < 0 {
		return s
	}
	yellow := "\033[1;33m"
	reset := "\033[0m"
	return s[:idx] + yellow + s[idx:idx+len(substr)] + reset + s[idx+len(substr):]
}

// splitArgs splits a command line into arguments, respecting quoted strings.
func splitArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		} else {
			if ch == '"' || ch == '\'' {
				inQuote = true
				quoteChar = ch
			} else if ch == ' ' || ch == '\t' {
				if current.Len() > 0 {
					args = append(args, current.String())
					current.Reset()
				}
			} else {
				current.WriteByte(ch)
			}
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// historyFilePath returns the path to the REPL history file.
func historyFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	histDir := dir + "/laevitas"
	os.MkdirAll(histDir, 0o755)
	return histDir + "/history"
}
