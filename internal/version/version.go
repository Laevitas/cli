package version

import (
	"os/exec"
	"strings"
	"time"
)

// Set via ldflags at build time:
//
//	go build -ldflags "-X github.com/laevitas/cli/internal/version.Version=1.0.0"
var (
	Version   = ""
	CommitSHA = ""
	BuildDate = ""
)

func init() {
	if Version == "" {
		Version = gitDescribe()
	}
	if CommitSHA == "" {
		CommitSHA = gitCommit()
	}
	if BuildDate == "" {
		BuildDate = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
}

func gitDescribe() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		return "dev"
	}
	v := strings.TrimSpace(string(out))
	// Strip leading "v" so callers can format as "v0.1.0" without double-v
	return strings.TrimPrefix(v, "v")
}

func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
