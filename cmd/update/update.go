package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/version"
)

const (
	repo         = "laevitas/cli"
	binaryName   = "laevitas"
	githubAPIURL = "https://api.github.com/repos/" + repo + "/releases/latest"
)

// githubRelease is the subset of fields we need from the GitHub API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Cmd is the top-level "update" command.
var Cmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"upgrade", "self-update"},
	Short:   "Update the CLI to the latest version",
	Long:    "Check for and install the latest release from GitHub.",
	RunE:    runUpdate,
}

var checkOnly bool

func init() {
	Cmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for updates, don't install")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	current := version.Version

	fmt.Printf("Current version: %s\n", current)
	fmt.Print("Checking for updates... ")

	latest, err := fetchLatestVersion()
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("checking for updates: %w", err)
	}

	fmt.Printf("%s\n", latest.TagName)

	latestClean := strings.TrimPrefix(latest.TagName, "v")
	currentClean := strings.TrimPrefix(current, "v")

	if currentClean == latestClean || current == "dev" && !checkOnly {
		if current == "dev" {
			output.Warnf("Running dev build — cannot compare versions. Use --check or reinstall.")
			return nil
		}
		output.Successf("Already up to date.")
		return nil
	}

	if checkOnly {
		if currentClean != latestClean {
			fmt.Printf("\nUpdate available: %s → %s\n", current, latest.TagName)
			fmt.Printf("Run `laevitas update` to install.\n")
		} else {
			output.Successf("Already up to date.")
		}
		return nil
	}

	fmt.Printf("\nUpdating %s → %s\n", current, latest.TagName)

	// Determine binary asset name for this platform
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	assetName := fmt.Sprintf("%s-%s-%s%s", binaryName, runtime.GOOS, runtime.GOARCH, suffix)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest.TagName, assetName)

	fmt.Printf("Downloading %s... ", assetName)

	// Download to temp file
	tmpFile, err := downloadBinary(downloadURL)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("downloading update: %w", err)
	}
	defer os.Remove(tmpFile)

	fmt.Println("✓")

	// Find current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	fmt.Printf("Replacing %s... ", execPath)

	if err := replaceBinary(execPath, tmpFile); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Println("✓")
	output.Successf("Updated to %s", latest.TagName)

	return nil
}

func fetchLatestVersion() (*githubRelease, error) {
	resp, err := http.Get(githubAPIURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parsing release info: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("no releases found")
	}

	return &release, nil
}

func downloadBinary(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("binary not found at %s — this platform may not be supported", url)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "laevitas-update-*")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("writing download: %w", err)
	}

	tmp.Close()
	return tmp.Name(), nil
}

func replaceBinary(target, source string) error {
	// On Windows, can't overwrite a running exe — rename first
	if runtime.GOOS == "windows" {
		old := target + ".old"
		os.Remove(old) // clean up any previous .old file
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("backing up current binary: %w", err)
		}
		if err := copyFile(source, target); err != nil {
			// Try to restore backup
			os.Rename(old, target)
			return err
		}
		os.Remove(old)
		return nil
	}

	// Unix: write to temp in same dir, then atomic rename
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".laevitas-update-*")
	if err != nil {
		// Might not have write permission — try with source directly
		return copyAndReplace(source, target)
	}
	tmpPath := tmp.Name()
	tmp.Close()

	if err := copyFile(source, tmpPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}

func copyAndReplace(source, target string) error {
	if err := copyFile(source, target); err != nil {
		return err
	}
	return os.Chmod(target, 0755)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
