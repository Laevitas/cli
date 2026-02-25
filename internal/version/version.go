package version

// Set via ldflags at build time:
//
//	go build -ldflags "-X github.com/laevitas/cli/internal/version.Version=1.0.0"
var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)
