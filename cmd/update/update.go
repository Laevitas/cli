package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

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

	osTok, archTok, err := platformAssetTokens()
	if err != nil {
		return err
	}
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	archiveName := fmt.Sprintf("%s_%s_%s_%s.%s", binaryName, latestClean, osTok, archTok, ext)
	archiveURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest.TagName, archiveName)

	fmt.Printf("Downloading %s... ", archiveName)
	archiveData, err := downloadBytes(archiveURL)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("downloading update: %w", err)
	}
	fmt.Println("✓")

	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", repo, latest.TagName)
	if expected, ok := lookupChecksum(checksumsURL, archiveName); ok {
		sum := sha256.Sum256(archiveData)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
			return fmt.Errorf("checksum mismatch for %s", archiveName)
		}
		fmt.Println("Checksum verified ✓")
	}

	binTarget := binaryName
	if runtime.GOOS == "windows" {
		binTarget += ".exe"
	}
	binData, err := extractBinary(archiveData, ext, binTarget)
	if err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "laevitas-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	if _, err := tmpFile.Write(binData); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return fmt.Errorf("writing extracted binary: %w", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	fmt.Printf("Replacing %s... ", execPath)

	if err := replaceBinary(execPath, tmpFile.Name()); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Println("✓")
	output.Successf("Updated to %s", latest.TagName)

	return nil
}

func platformAssetTokens() (osTok, archTok string, err error) {
	switch runtime.GOOS {
	case "linux":
		osTok = "Linux"
	case "darwin":
		osTok = "macOS"
	case "windows":
		osTok = "Windows"
	default:
		return "", "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		archTok = "x86_64"
	case "arm64":
		archTok = "arm64"
	default:
		return "", "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	return osTok, archTok, nil
}

func extractBinary(data []byte, ext, target string) ([]byte, error) {
	switch ext {
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == target {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
	case "tar.gz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			if filepath.Base(hdr.Name) == target {
				return io.ReadAll(tr)
			}
		}
	default:
		return nil, fmt.Errorf("unknown archive format: %s", ext)
	}
	return nil, fmt.Errorf("binary %q not found in archive", target)
}

func lookupChecksum(url, file string) (string, bool) {
	data, err := downloadBytes(url)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == file {
			return fields[0], true
		}
	}
	return "", false
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

func downloadBytes(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found at %s — this platform may not be supported", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

func replaceBinary(target, source string) error {
	if runtime.GOOS == "windows" {
		old := target + ".old"
		os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("backing up current binary: %w", err)
		}
		if err := copyFile(source, target); err != nil {
			os.Rename(old, target)
			return err
		}
		os.Remove(old)
		return nil
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".laevitas-update-*")
	if err != nil {
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
