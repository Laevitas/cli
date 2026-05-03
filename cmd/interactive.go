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

// printBanner renders the REPL header. Replaces the giant ASCII block letters
// with a single branded line: "▲ LAEVITAS  v0.5.0" in brand green + bold white.
// Tagline and exit hint follow on dim grey lines.
func printBanner() {
	green := output.BrandGreen
	grey := output.BrandGreyMid
	bold := output.Bold
	reset := output.Reset

	fmt.Fprintf(os.Stdout, "\n  %s%s▲%s  %s%sLAEVITAS%s  %sv%s%s\n",
		bold, green, reset,
		bold, output.BrandGreyLight, reset,
		grey, version.Version, reset,
	)
	fmt.Fprintf(os.Stdout, "  %sDerivatives data without the spread.%s\n", grey, reset)
	fmt.Fprintf(os.Stdout, "  %sType %shelp%s%s for commands, %sexit%s%s to quit.%s\n\n",
		grey, bold, reset, grey, bold, reset, grey, reset,
	)
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

	// If no API key and no wallet key, run inline onboarding before entering the REPL
	if cfg.APIKey == "" && cfg.WalletKey == "" {
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
		fmt.Printf("  %sDocs:    https://cli.laevitas.ch  ·  API: https://apiv2.laevitas.ch%s\n", dim, reset)
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
	replCompleter = completer.New(client, rootCmd)
	replCompleter.PreloadCatalogs()

	// Wire saved query name completion
	replCompleter.SavedNamesFunc = func() []string {
		sq, err := config.LoadSaved()
		if err != nil {
			return nil
		}
		return sq.Names()
	}

	// Prompt: brand-glyph + chevron. Uses 8-color ANSI (\033[32m / \033[2m)
	// rather than truecolor because chzyer/readline's Windows ANSI shim
	// doesn't parse 24-bit SGR sequences and panics on the prompt redraw.
	// Banner above can use truecolor freely — it's printed before readline
	// starts and bypasses readline's parser.
	prompt := "\033[32m▲\033[0m \033[2m›\033[0m "

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

		// Slash-command syntax: /help, /save, /run, /quit, etc.
		// Strips a single leading "/" before dispatching so REPL meta-
		// commands match the convention humans expect from modern AI/dev
		// CLIs (Claude Code, Codex, Copilot CLI). The bare-keyword form
		// (`save foo ...`, `quit`, etc.) keeps working unchanged so
		// existing muscle memory and scripted REPL input don't break.
		// Only one leading slash is stripped — `//literal` would survive
		// for a hypothetical future case where a real command name
		// starts with "/", though none do today.
		if strings.HasPrefix(line, "/") {
			line = strings.TrimPrefix(line, "/")
			line = strings.TrimSpace(line)
			if line == "" {
				// Bare "/" — show the slash-command reference.
				printREPLHelp()
				continue
			}
		}

		switch strings.ToLower(line) {
		case "quit", "exit":
			fmt.Println("Bye!")
			return nil
		case "clear":
			fmt.Print("\033[H\033[2J")
			continue
		case "help":
			printREPLHelp()
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
			case "help":
				printREPLHelp()
				continue
			case "commands":
				// Bare `commands` in the REPL is a human-context shortcut
				// for the table view. Any explicit args/flags
				// (`commands -o json`, `commands --filter ws`) flow
				// through to the real cobra command unchanged.
				if len(args) == 1 {
					line = "commands -o table"
				}
				// Fall through to cobra dispatch.
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

// printREPLHelp lists the REPL meta-commands. Both slash form (the
// preferred form, matching modern AI/dev CLIs) and the bare-keyword
// alias are shown so users can pick whichever they prefer. Triggered
// by /help, help, or a bare /.
func printREPLHelp() {
	bold := output.Bold
	dim := output.BrandGreyMid
	reset := output.Reset

	fmt.Println()
	fmt.Printf("  %sREPL meta-commands%s — slash form preferred; bare keyword works too.\n", bold, reset)
	fmt.Println()
	fmt.Printf("    %s/help%s              Show this reference.\n", bold, reset)
	fmt.Printf("    %s/clear%s             Clear the screen.\n", bold, reset)
	fmt.Printf("    %s/quit%s, %s/exit%s       Leave the REPL.\n", bold, reset, bold, reset)
	fmt.Println()
	fmt.Printf("    %s/save <name> <cmd>%s    Save a command under a name.\n", bold, reset)
	fmt.Printf("    %s/run <name>%s          Run a saved command.\n", bold, reset)
	fmt.Printf("    %s/saves%s              List saved commands.\n", bold, reset)
	fmt.Printf("    %s/unsave <name>%s      Remove a saved command.\n", bold, reset)
	fmt.Println()
	fmt.Printf("    %s/search <keywords>%s  Search instruments by keyword.\n", bold, reset)
	fmt.Printf("    %s/commands%s          Browse command inventory (table). Use %scommands -o json%s for manifest.\n", bold, reset, bold, reset)
	fmt.Println()
	fmt.Printf("  %sFor data queries, type the command directly:%s\n", dim, reset)
	fmt.Printf("    perps snapshot --currency BTC\n")
	fmt.Printf("    options vol-surface snapshot --currency BTC -o json\n")
	fmt.Println()
	fmt.Printf("  %sAt the system shell, run %slaevitas <subcommand>%s%s — the REPL is\n", dim, bold, reset, dim)
	fmt.Printf("  %sa human-only convenience surface; agents and scripts pipe directly.%s\n", dim, reset)
	fmt.Println()
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
	wide = false
	widthOverride = 0
	rootCmd.PersistentFlags().Set("output", "auto")
	rootCmd.PersistentFlags().Set("exchange", "")
	rootCmd.PersistentFlags().Set("verbose", "false")
	rootCmd.PersistentFlags().Set("no-chart", "false")
	rootCmd.PersistentFlags().Set("wide", "false")
	rootCmd.PersistentFlags().Set("width", "0")
	output.WidthOverride = -1
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
	_ = os.MkdirAll(histDir, 0o700)
	return histDir + "/history"
}
