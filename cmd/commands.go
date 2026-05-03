package cmd

// `laevitas commands` — machine-readable inventory of every command,
// its arguments, flags, and execution constraints. Designed for agents
// doing first-run discovery: one JSON document beats crawling
// `--help` for 80+ commands.
//
// Output defaults to JSON regardless of TTY. The whole point of this
// command is machine-readability; humans wanting a browseable list
// can pass `-o table`. This is the only command that intentionally
// breaks the global "auto = table in TTY, JSON when piped" default.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/version"
)

// commandManifest is the top-level shape returned by `commands`.
// Wrapped in the standard envelope by the printer; consumers read
// `.data.commands` for the array.
type commandManifest struct {
	Version     string             `json:"version"`
	GeneratedAt string             `json:"generated_at"`
	Commands    []commandManifestE `json:"commands"`
}

// commandManifestE is one row of the manifest. Field names are
// snake_case to match the rest of the agent-facing JSON envelope.
type commandManifestE struct {
	Path         string                `json:"path"`
	Short        string                `json:"short"`
	Long         string                `json:"long,omitempty"`
	Args         []commandManifestArg  `json:"args"`
	Flags        []commandManifestFlag `json:"flags"`
	Examples     []string              `json:"examples,omitempty"`
	Aliases      []string              `json:"aliases,omitempty"`
	RequiresAuth bool                  `json:"requires_auth"`
	// RequiresWallet is true for commands that need a configured x402
	// wallet key, distinct from the API key. Most commands accept
	// either auth path; only wallet-management subcommands strictly
	// require the wallet.
	RequiresWallet bool     `json:"requires_wallet"`
	Streaming      bool     `json:"streaming"`
	OutputModes    []string `json:"output_modes"`
	// EndpointHint is the REST URL the command typically hits.
	// Treated as metadata, not a stability contract — the URL may
	// change between server versions. Empty for streaming commands
	// (ws/dash) and local commands (config/wallet/version/update).
	EndpointHint string `json:"endpoint_hint,omitempty"`
}

type commandManifestArg struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type commandManifestFlag struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

var commandsFilter string

var commandsCmd = &cobra.Command{
	Use:   "commands",
	Short: "Print a machine-readable inventory of every command, flag, and arg",
	Long: `Walks the entire CLI command tree and emits one JSON document
describing every command — path, short/long help, args, flags, examples,
aliases, and execution constraints (requires_auth, requires_wallet,
streaming, output_modes). Designed for agents doing first-run discovery
so they can ingest one document instead of crawling --help for 80+
commands.

Output defaults to JSON regardless of TTY (the whole point is
machine-readability). Humans wanting a browseable list can pass
-o table.

Use --filter to narrow the output to commands whose path contains
the given substring. Cheap convenience for both humans and agents
who only need a slice.`,
	Example: `  laevitas commands                 # full JSON manifest
  laevitas commands -o table         # human-readable summary
  laevitas commands --filter ws      # only WebSocket commands
  laevitas commands --filter book    # everything orderbook-related
  laevitas commands -o json | jq '.data.commands[] | select(.streaming)'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest := buildCommandManifest(rootCmd, commandsFilter)

		// Force JSON unless the user explicitly asked for a different
		// format. The default `auto` resolves to table in a TTY, but
		// this command's audience is agents — table view is opt-in.
		format := output.Resolve(cmdutil.OutputFormat)
		if cmdutil.OutputFormat == "auto" || cmdutil.OutputFormat == "" {
			format = output.FormatJSON
		}

		switch format {
		case output.FormatTable:
			return printCommandsTable(manifest, os.Stdout)
		default:
			return printCommandsJSON(manifest, os.Stdout)
		}
	},
}

func init() {
	commandsCmd.Flags().StringVar(&commandsFilter, "filter", "",
		"Only include commands whose path contains this substring (case-insensitive).")
	rootCmd.AddCommand(commandsCmd)
}

// buildCommandManifest walks the cobra tree from root and produces
// one entry per leaf-or-group command. Excludes the implicit `help`
// subcommand cobra adds to every parent.
func buildCommandManifest(root *cobra.Command, filter string) commandManifest {
	endpoints := api.CommandEndpoints()
	var entries []commandManifestE

	var walk func(c *cobra.Command, prefix []string)
	walk = func(c *cobra.Command, prefix []string) {
		// Skip cobra's auto-generated help command — not user-typeable
		// in any meaningful way and would clutter the manifest.
		if c.Name() == "help" {
			return
		}
		path := append(prefix, c.Name())
		// Skip the root command itself; we want only its children.
		if len(path) > 1 {
			entries = append(entries, buildEntry(c, path, endpoints))
		}
		for _, sub := range c.Commands() {
			walk(sub, path)
		}
	}
	walk(root, nil)

	// Apply --filter (case-insensitive substring match on path).
	if filter != "" {
		needle := strings.ToLower(filter)
		filtered := make([]commandManifestE, 0, len(entries))
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Path), needle) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	// Stable order: alphabetical by path. Predictable diffs across
	// runs and easy for agents to bisect.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	return commandManifest{
		Version:     version.Version,
		GeneratedAt: nowISO(),
		Commands:    entries,
	}
}

func buildEntry(c *cobra.Command, path []string, endpoints map[string]string) commandManifestE {
	pathStr := strings.Join(path[1:], " ") // drop the "laevitas" root
	return commandManifestE{
		Path:           pathStr,
		Short:          c.Short,
		Long:           strings.TrimSpace(c.Long),
		Args:           parseArgsFromUse(c.Use),
		Flags:          collectFlags(c),
		Examples:       parseExamples(c.Example),
		Aliases:        c.Aliases,
		RequiresAuth:   commandRequiresAuth(pathStr),
		RequiresWallet: commandRequiresWallet(pathStr),
		Streaming:      commandIsStreaming(pathStr),
		OutputModes:    commandOutputModes(pathStr),
		EndpointHint:   endpoints[pathStr],
	}
}

// parseArgsFromUse extracts positional args from cobra's Use field.
// Convention: "<arg>" is required, "[arg]" is optional. The first
// space-separated token is the command name itself and is dropped.
//
// Cobra doesn't expose a structured arg list; the Use field is the
// only signal. Most commands follow this convention; ones that don't
// just produce an empty args list, which is acceptable graceful
// degradation.
func parseArgsFromUse(use string) []commandManifestArg {
	tokens := strings.Fields(use)
	if len(tokens) <= 1 {
		return nil
	}
	var args []commandManifestArg
	for _, t := range tokens[1:] {
		if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
			args = append(args, commandManifestArg{
				Name:     strings.Trim(t, "<>"),
				Required: true,
			})
		} else if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			args = append(args, commandManifestArg{
				Name:     strings.Trim(t, "[]"),
				Required: false,
			})
		}
	}
	return args
}

// collectFlags enumerates the command's own flags PLUS any inherited
// flags that aren't on the root persistent flag set. Persistent flags
// from rootCmd (--exchange, -o, --verbose, etc.) are excluded — those
// are documented once at the manifest level rather than repeating on
// every entry.
func collectFlags(c *cobra.Command) []commandManifestFlag {
	rootPersistent := map[string]struct{}{}
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		rootPersistent[f.Name] = struct{}{}
	})

	var flags []commandManifestFlag
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if _, isRoot := rootPersistent[f.Name]; isRoot {
			return
		}
		// Skip cobra's auto-generated --help; agents already know it exists.
		if f.Name == "help" {
			return
		}
		// Skip flags marked hidden — those are intentionally invisible
		// to users (typically deprecated aliases kept for compat).
		if f.Hidden {
			return
		}
		_, required := requiredFlagSet(c)[f.Name]
		flags = append(flags, commandManifestFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
			Required:    required,
			Description: f.Usage,
		})
	})
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Name < flags[j].Name
	})
	return flags
}

// requiredFlagSet returns the set of flag names cobra has been told
// are required. Cobra stores this as an annotation on the flag itself
// rather than a queryable list, so we walk the same VisitAll loop and
// inspect annotations.
func requiredFlagSet(c *cobra.Command) map[string]struct{} {
	required := map[string]struct{}{}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Annotations == nil {
			return
		}
		if _, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok {
			required[f.Name] = struct{}{}
		}
	})
	return required
}

// parseExamples splits cobra's Example field (a multi-line string)
// into one entry per non-empty line. Leading whitespace is preserved
// inside each example so multi-line shell pipelines stay readable.
func parseExamples(example string) []string {
	var out []string
	for _, line := range strings.Split(example, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// commandRequiresAuth reports whether the command needs an API key
// or wallet to function. Defined as an explicit deny-list rather than
// a positive list — the vast majority of commands hit the API and
// adding new auth-free commands is rare. The deny-list lives here
// rather than spread across each command file.
func commandRequiresAuth(path string) bool {
	authFree := map[string]bool{
		"version":         true,
		"commands":        true,
		"doctor":          true,
		"update":          true,
		"config init":     true,
		"config show":     true,
		"config set":      true,
		"config unset":    true,
		"config path":     true,
		"wallet show":     true,
		"wallet init":     true,
		"wallet set-key":  true,
		"wallet unset":    true,
		"wallet address":  true,
		// `wallet credits` does NOT appear here — it reads cached
		// state from disk but the canonical path is to refresh from
		// API responses, so we treat it as auth-touching.
	}
	return !authFree[path]
}

// commandRequiresWallet reports whether the command strictly needs a
// configured x402 wallet key (vs. just any auth path). Distinct from
// requires_auth because most commands accept either an API key or
// the x402 wallet — only the wallet-management subcommands force
// the wallet path.
func commandRequiresWallet(path string) bool {
	walletOnly := map[string]bool{
		"wallet init":    true,
		"wallet set-key": true,
		"wallet unset":   true,
	}
	return walletOnly[path]
}

// commandIsStreaming reports whether the command opens a long-lived
// connection (WebSocket subscribe or live TUI dashboard) instead of
// returning a single REST response. Streaming commands emit NDJSON
// rather than the canonical envelope; agents that try to parse
// streaming output as one JSON document will hang.
func commandIsStreaming(path string) bool {
	return strings.HasPrefix(path, "ws") || strings.HasPrefix(path, "dash")
}

// commandOutputModes enumerates the -o values the command accepts.
// REST commands accept the full set; streaming commands skip table
// and csv (the output is line-by-line and non-tabular). Watch is
// table-only (its raison d'être is the live grid). Doctor accepts
// table and json.
func commandOutputModes(path string) []string {
	if strings.HasPrefix(path, "ws") || strings.HasPrefix(path, "dash") {
		return []string{"auto", "json"}
	}
	if path == "watch" {
		return []string{"table"}
	}
	return []string{"auto", "json", "table", "csv"}
}

// printCommandsJSON wraps the manifest in the standard success
// envelope and writes it to w. Marshals the envelope as raw bytes so
// key order stays canonical (success, data, meta) — the same trick
// wrapSuccessEnvelope uses in cmdutil.
func printCommandsJSON(m commandManifest, w io.Writer) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var raw bytes.Buffer
	raw.WriteString(`{"success":true,"data":`)
	raw.Write(data)
	fmt.Fprintf(&raw, `,"meta":{"count":%d}}`, len(m.Commands))

	var indented bytes.Buffer
	if err := json.Indent(&indented, raw.Bytes(), "", "  "); err != nil {
		// Fallback: emit unindented bytes so caller still gets valid JSON.
		indented.Reset()
		indented.Write(raw.Bytes())
	}
	indented.WriteByte('\n')
	_, err = w.Write(indented.Bytes())
	return err
}

// printCommandsTable renders the manifest as a compact human-readable
// summary: one line per command with path, streaming/auth flags, and
// short description. Designed for `laevitas commands -o table | less`
// — agents should use the JSON form.
func printCommandsTable(m commandManifest, w io.Writer) error {
	fmt.Fprintf(w, "%d commands (%s, generated %s)\n\n", len(m.Commands), m.Version, m.GeneratedAt)
	fmt.Fprintf(w, "%-40s  %-7s  %-5s  %s\n", "PATH", "STREAM", "AUTH", "DESCRIPTION")
	fmt.Fprintf(w, "%s\n", strings.Repeat("─", 100))
	for _, e := range m.Commands {
		stream := ""
		if e.Streaming {
			stream = "yes"
		}
		auth := ""
		if e.RequiresAuth {
			auth = "yes"
		}
		fmt.Fprintf(w, "%-40s  %-7s  %-5s  %s\n", e.Path, stream, auth, e.Short)
	}
	return nil
}

// nowISO returns the current UTC time formatted as RFC3339 with a
// trailing Z. Matches the format the API uses for `meta.date` and
// the format we suggest for --start/--end flags.
func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
