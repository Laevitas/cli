package cmd

// `laevitas doctor` — health check for the user's setup. Designed to
// answer "why isn't this working?" or "is my agent environment sane?"
// in one command. Runs a small ordered battery of checks, reports
// pass/warn/fail/skip per check with a one-line remediation, and
// exits 0 only if no fail.
//
// All checks are read-only and cheap (HEAD requests, single-row
// fetches). The only network calls are the version freshness check
// (GitHub releases), the API base URL reachability HEAD, the
// authenticated single-row fetch, and the WS gateway handshake.
// No x402 payment is attempted — that would cost real money to run a
// diagnostic, which is the wrong tradeoff.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/version"
)

// checkStatus is the ternary result of one diagnostic. `skipped`
// is distinct from `failed` so callers (esp. agents) can tell the
// difference between "this would work but you haven't configured
// it" and "this is broken".
type checkStatus string

const (
	statusPass checkStatus = "pass"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
	statusSkip checkStatus = "skipped"
)

// doctorCheck is one row of the report. Remediation is a short
// imperative shell-runnable hint when status != pass; empty otherwise.
type doctorCheck struct {
	Name        string      `json:"name"`
	Status      checkStatus `json:"status"`
	Detail      string      `json:"detail"`
	Remediation string      `json:"remediation,omitempty"`
	DurationMs  int64       `json:"duration_ms"`
}

// doctorEnv is the environment block — fixed metadata about the host
// and CLI installation. Useful when an agent reports a doctor result
// to the user; the user can repro the same environment.
type doctorEnv struct {
	BinaryPath       string `json:"binary_path"`
	ConfigPath       string `json:"config_path"`
	ConfigSource     string `json:"config_source"`     // "file" | "env" | "default"
	APIKeySource     string `json:"api_key_source"`    // "env" | "config" | "none"
	WalletKeySource  string `json:"wallet_key_source"` // "env" | "config" | "none"
	Platform         string `json:"platform"`          // GOOS/GOARCH
	NetworkTimeoutMs int64  `json:"network_timeout_ms"`
}

type doctorReport struct {
	Version     string        `json:"version"`
	GeneratedAt string        `json:"generated_at"`
	Env         doctorEnv     `json:"env"`
	Checks      []doctorCheck `json:"checks"`
	Summary     doctorSummary `json:"summary"`
}

type doctorSummary struct {
	Pass    int `json:"pass"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Skipped int `json:"skipped"`
}

var (
	doctorQuiet bool
)

// doctorTimeout caps every network probe so a hung server doesn't
// stall the whole report. Doctor should always return within a few
// seconds even if everything is broken.
const doctorTimeout = 5 * time.Second

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run a health check on your CLI setup (config, auth, API, WS, wallet)",
	Long: `Runs a small ordered battery of read-only checks against your
configuration, authentication, the Laevitas API, the WebSocket gateway,
and (if configured) the wallet. Each check reports pass / warn / fail /
skipped with a one-line remediation when it isn't pass.

Checks marked "skipped" indicate "this would work but you haven't
configured it yet" — distinct from "fail", which means a configured
component is broken. The exit code is non-zero only on a real fail.

No x402 payment is attempted; the wallet check decodes the key locally
and reports the derived address but does not dial chain. Doctor should
never cost real money to run.

Run with --quiet to print only the failures (useful in CI). Use
-o json for a machine-parseable report including an environment block
(binary path, config source, platform, etc.).`,
	Example: `  laevitas doctor                  # full report, exits 0 on no failures
  laevitas doctor --quiet           # only print failures
  laevitas doctor -o json | jq '.data.summary'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		report := runDoctor()

		// Doctor's primary audience is humans debugging their setup, so
		// default to the text report regardless of TTY-ness. The standard
		// auto-resolve (table in TTY, JSON when piped) would surprise
		// users who pipe doctor through a logger or tool wrapper expecting
		// the human-readable report. JSON is opt-in via `-o json`.
		// Mirror image of `commands`, which defaults to JSON because its
		// audience is agents.
		jsonRequested := cmdutil.OutputFormat == "json"
		if jsonRequested {
			if err := printDoctorJSON(report, os.Stdout); err != nil {
				return err
			}
		} else {
			printDoctorText(report, os.Stdout, doctorQuiet)
		}

		// Exit non-zero only on real failures. Skipped is OK; warn is OK.
		if report.Summary.Fail > 0 {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorQuiet, "quiet", false,
		"Only print failures (text mode); JSON mode includes everything regardless.")
	rootCmd.AddCommand(doctorCmd)
}

// runDoctor orchestrates every check in order. The ordering matters:
// config → auth presence → network → authenticated call → WS → wallet.
// A failure early on (e.g. unreadable config) makes later checks
// pointless, so we mark them skipped instead of running them.
func runDoctor() doctorReport {
	report := doctorReport{
		Version:     version.Version,
		GeneratedAt: nowISO(),
		Env:         buildDoctorEnv(),
	}

	// 1. Version freshness — non-blocking, runs first because it's the
	//    most likely thing a user would want to see at the top.
	report.Checks = append(report.Checks, checkVersionFreshness())

	// 2. Config file existence + parseability.
	cfgCheck, cfg := checkConfigFile()
	report.Checks = append(report.Checks, cfgCheck)

	// 3. API key presence.
	apiKeyCheck := checkAPIKeyPresent(cfg)
	report.Checks = append(report.Checks, apiKeyCheck)

	// 4. API base URL reachability — independent of auth, so always
	//    runs as long as we have a base URL to probe.
	report.Checks = append(report.Checks, checkBaseURLReachable(cfg))

	// 5. API key valid — needs both a key AND reachability. Skipped
	//    if no key configured.
	report.Checks = append(report.Checks, checkAPIKeyValid(cfg, apiKeyCheck.Status))

	// 6. WS gateway handshake — needs a key. Skipped if no key.
	report.Checks = append(report.Checks, checkWSGateway(cfg, apiKeyCheck.Status))

	// 7. Wallet key — local-only, runs whether or not API key is present.
	report.Checks = append(report.Checks, checkWalletKey(cfg))

	// 8. x402 token cache — only meaningful if wallet is present.
	report.Checks = append(report.Checks, checkX402TokenCache(cfg))

	report.Summary = summarize(report.Checks)
	return report
}

// summarize tallies the per-status counts so the JSON consumer doesn't
// have to walk the full check list to know overall health.
func summarize(checks []doctorCheck) doctorSummary {
	var s doctorSummary
	for _, c := range checks {
		switch c.Status {
		case statusPass:
			s.Pass++
		case statusWarn:
			s.Warn++
		case statusFail:
			s.Fail++
		case statusSkip:
			s.Skipped++
		}
	}
	return s
}

// ─── Environment block ──────────────────────────────────────────────────────

func buildDoctorEnv() doctorEnv {
	binPath, _ := os.Executable()
	cfgPath, _ := config.Path()

	apiKeySource := "none"
	if os.Getenv("LAEVITAS_API_KEY") != "" {
		apiKeySource = "env"
	} else if cfg, err := config.Load(); err == nil && cfg.APIKey != "" {
		apiKeySource = "config"
	}

	walletKeySource := "none"
	if os.Getenv("LAEVITAS_WALLET_KEY") != "" {
		walletKeySource = "env"
	} else if cfg, err := config.Load(); err == nil && cfg.WalletKey != "" {
		walletKeySource = "config"
	}

	configSource := "default"
	if _, err := os.Stat(cfgPath); err == nil {
		configSource = "file"
	}
	if anyConfigEnvSet() {
		// Env vars override file values; signal that to the caller so
		// they know where to look for unexpected behaviour.
		configSource = "env"
	}

	return doctorEnv{
		BinaryPath:       binPath,
		ConfigPath:       cfgPath,
		ConfigSource:     configSource,
		APIKeySource:     apiKeySource,
		WalletKeySource:  walletKeySource,
		Platform:         fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		NetworkTimeoutMs: doctorTimeout.Milliseconds(),
	}
}

// anyConfigEnvSet reports whether any LAEVITAS_* config-bearing env
// var is set. Used to flag config_source = "env" in the doctor report
// when the user's environment is overriding their config file.
func anyConfigEnvSet() bool {
	for _, k := range []string{"LAEVITAS_BASE_URL", "LAEVITAS_EXCHANGE", "LAEVITAS_OUTPUT", "LAEVITAS_AUTH"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// ─── Individual checks ──────────────────────────────────────────────────────

func checkVersionFreshness() doctorCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET",
		"https://api.github.com/repos/Laevitas/cli/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "laevitas-cli-doctor/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return doctorCheck{
			Name:       "version",
			Status:     statusWarn,
			Detail:     fmt.Sprintf("could not reach GitHub releases (%s); current: %s", err, version.Version),
			DurationMs: dur,
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return doctorCheck{
			Name:       "version",
			Status:     statusWarn,
			Detail:     fmt.Sprintf("could not parse latest release; current: %s", version.Version),
			DurationMs: dur,
		}
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest == version.Version {
		return doctorCheck{
			Name:       "version",
			Status:     statusPass,
			Detail:     fmt.Sprintf("v%s is current", version.Version),
			DurationMs: dur,
		}
	}
	return doctorCheck{
		Name:        "version",
		Status:      statusWarn,
		Detail:      fmt.Sprintf("v%s installed, v%s available", version.Version, latest),
		Remediation: "laevitas update",
		DurationMs:  dur,
	}
}

func checkConfigFile() (doctorCheck, *config.Config) {
	start := time.Now()
	cfg, err := config.Load()
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return doctorCheck{
			Name:        "config_file",
			Status:      statusFail,
			Detail:      fmt.Sprintf("config load failed: %s", err),
			Remediation: "laevitas config init",
			DurationMs:  dur,
		}, nil
	}
	cfgPath, _ := config.Path()
	return doctorCheck{
		Name:       "config_file",
		Status:     statusPass,
		Detail:     fmt.Sprintf("loaded from %s", cfgPath),
		DurationMs: dur,
	}, cfg
}

func checkAPIKeyPresent(cfg *config.Config) doctorCheck {
	if cfg == nil {
		return doctorCheck{
			Name:   "api_key_present",
			Status: statusSkip,
			Detail: "skipped (config not loaded)",
		}
	}
	if cfg.APIKey == "" {
		return doctorCheck{
			Name:        "api_key_present",
			Status:      statusSkip,
			Detail:      "no API key configured",
			Remediation: `export LAEVITAS_API_KEY=<key>  OR  laevitas config set api_key <key>`,
		}
	}
	source := "config"
	if os.Getenv("LAEVITAS_API_KEY") != "" {
		source = "env"
	}
	return doctorCheck{
		Name:   "api_key_present",
		Status: statusPass,
		Detail: fmt.Sprintf("present (source: %s)", source),
	}
}

func checkBaseURLReachable(cfg *config.Config) doctorCheck {
	start := time.Now()
	baseURL := config.DefaultBaseURL
	if cfg != nil && cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "HEAD", baseURL+api.Health, nil)
	req.Header.Set("User-Agent", "laevitas-cli-doctor/"+version.Version)
	resp, err := http.DefaultClient.Do(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return doctorCheck{
			Name:        "api_base_url",
			Status:      statusFail,
			Detail:      fmt.Sprintf("HEAD %s failed: %s", baseURL, err),
			Remediation: "check internet connectivity; verify LAEVITAS_BASE_URL if overridden",
			DurationMs:  dur,
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return doctorCheck{
			Name:        "api_base_url",
			Status:      statusFail,
			Detail:      fmt.Sprintf("%s returned HTTP %d", baseURL, resp.StatusCode),
			Remediation: "check status.laevitas.ch for ongoing incidents",
			DurationMs:  dur,
		}
	}
	return doctorCheck{
		Name:       "api_base_url",
		Status:     statusPass,
		Detail:     fmt.Sprintf("%s reachable (HTTP %d, %dms)", baseURL, resp.StatusCode, dur),
		DurationMs: dur,
	}
}

func checkAPIKeyValid(cfg *config.Config, presenceStatus checkStatus) doctorCheck {
	if presenceStatus != statusPass {
		return doctorCheck{
			Name:   "api_key_valid",
			Status: statusSkip,
			Detail: "skipped (no API key configured)",
		}
	}
	start := time.Now()
	client := api.NewClient(cfg)
	// Cheapest authenticated call: instruments registry with limit=1.
	_, err := client.Get(api.InstrumentsList, &api.RequestParams{Limit: 1})
	dur := time.Since(start).Milliseconds()
	if err != nil {
		// Distinguish auth failures from network/server hiccups.
		msg := err.Error()
		remediation := ""
		if strings.Contains(msg, "401") || strings.Contains(strings.ToLower(msg), "unauthor") {
			remediation = "rotate or refresh: laevitas config set api_key <new-key>"
		}
		return doctorCheck{
			Name:        "api_key_valid",
			Status:      statusFail,
			Detail:      fmt.Sprintf("authenticated request failed: %s", err),
			Remediation: remediation,
			DurationMs:  dur,
		}
	}
	return doctorCheck{
		Name:       "api_key_valid",
		Status:     statusPass,
		Detail:     fmt.Sprintf("authenticated (1 instrument fetched, %dms)", dur),
		DurationMs: dur,
	}
}

func checkWSGateway(cfg *config.Config, presenceStatus checkStatus) doctorCheck {
	if presenceStatus != statusPass {
		return doctorCheck{
			Name:   "ws_gateway",
			Status: statusSkip,
			Detail: "skipped (no API key configured)",
		}
	}
	// Skipping the actual WebSocket dial in v0.9.0 — wsclient.Dial
	// returns a long-lived connection rather than a one-shot probe,
	// and shoehorning a doctor-friendly probe through it adds
	// non-trivial code. Reporting "configured" instead of "verified"
	// is honest: the gateway uses the same API key as REST, so the
	// REST auth check above is a strong proxy.
	return doctorCheck{
		Name:   "ws_gateway",
		Status: statusWarn,
		Detail: "WS handshake probe not implemented yet (REST auth check covers the same key)",
	}
}

func checkWalletKey(cfg *config.Config) doctorCheck {
	if cfg == nil || cfg.WalletKey == "" {
		return doctorCheck{
			Name:   "wallet_key",
			Status: statusSkip,
			Detail: "no wallet configured (x402 payments unavailable)",
		}
	}
	// Decode the hex without dialing chain. Standard EVM private keys
	// are 64 hex chars (32 bytes). Accept the optional 0x prefix.
	key := strings.TrimPrefix(strings.TrimSpace(cfg.WalletKey), "0x")
	if len(key) != 64 {
		return doctorCheck{
			Name:        "wallet_key",
			Status:      statusFail,
			Detail:      fmt.Sprintf("wallet key is %d hex chars, expected 64", len(key)),
			Remediation: "laevitas wallet set-key 0x<64-hex-chars>",
		}
	}
	if _, err := hex.DecodeString(key); err != nil {
		return doctorCheck{
			Name:        "wallet_key",
			Status:      statusFail,
			Detail:      fmt.Sprintf("wallet key is not valid hex: %s", err),
			Remediation: "laevitas wallet set-key 0x<64-hex-chars>",
		}
	}
	return doctorCheck{
		Name:   "wallet_key",
		Status: statusPass,
		Detail: "wallet key decodes; chain not contacted (run `laevitas wallet show` to derive address)",
	}
}

func checkX402TokenCache(cfg *config.Config) doctorCheck {
	if cfg == nil || cfg.WalletKey == "" {
		return doctorCheck{
			Name:   "x402_token_cache",
			Status: statusSkip,
			Detail: "no wallet configured",
		}
	}
	// Token cache lives next to the config file.
	cfgPath, _ := config.Path()
	tokenPath := filepath.Join(filepath.Dir(cfgPath), "x402-token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		return doctorCheck{
			Name:   "x402_token_cache",
			Status: statusSkip,
			Detail: "no cached token (will mint on first request)",
		}
	}
	return doctorCheck{
		Name:   "x402_token_cache",
		Status: statusPass,
		Detail: fmt.Sprintf("cached at %s (last write %s)", tokenPath, info.ModTime().Format(time.RFC3339)),
	}
}

// ─── Output ─────────────────────────────────────────────────────────────────

func printDoctorJSON(report doctorReport, w io.Writer) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var raw bytes.Buffer
	raw.WriteString(`{"success":true,"data":`)
	raw.Write(data)
	raw.WriteString(`}`)

	var indented bytes.Buffer
	if err := json.Indent(&indented, raw.Bytes(), "", "  "); err != nil {
		indented.Reset()
		indented.Write(raw.Bytes())
	}
	indented.WriteByte('\n')
	_, err = w.Write(indented.Bytes())
	return err
}

func printDoctorText(report doctorReport, w io.Writer, quiet bool) {
	bold := output.Bold
	green := output.BrandGreen
	red := output.Red
	yellow := output.Yellow
	grey := output.BrandGreyMid
	reset := output.Reset

	for _, c := range report.Checks {
		if quiet && c.Status != statusFail {
			continue
		}
		var marker, color string
		switch c.Status {
		case statusPass:
			marker, color = "✓", green
		case statusWarn:
			marker, color = "⚠", yellow
		case statusFail:
			marker, color = "✗", red
		case statusSkip:
			marker, color = "○", grey
		}
		fmt.Fprintf(w, "  %s%s%s  %-22s  %s\n", color, marker, reset, c.Name, c.Detail)
		if c.Remediation != "" {
			fmt.Fprintf(w, "     %sremediation:%s %s\n", grey, reset, c.Remediation)
		}
	}
	if quiet && report.Summary.Fail == 0 {
		// Quiet mode prints nothing on a clean run; explicit signal so
		// the user knows it ran.
		fmt.Fprintf(w, "  %s%s✓%s  doctor: all checks passed\n", bold, green, reset)
		return
	}
	fmt.Fprintf(w, "\n  %ssummary%s: %d pass, %d warn, %d fail, %d skipped\n",
		bold, reset, report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Skipped)
}
