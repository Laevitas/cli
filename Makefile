APP_NAME    := laevitas
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS     := -s -w \
	-X github.com/laevitas/cli/internal/version.Version=$(VERSION) \
	-X github.com/laevitas/cli/internal/version.CommitSHA=$(COMMIT) \
	-X github.com/laevitas/cli/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build install test clean release release-snapshot lint fmt mod-download

## Download modules (overrides GOPROXY=off for environments that disable it)
mod-download:
	GOPROXY=https://proxy.golang.org,direct go mod download

## Build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) .

## Install to $GOPATH/bin
install:
	go build -ldflags "$(LDFLAGS)" -o $(shell go env GOPATH)/bin/$(APP_NAME)$(shell go env GOEXE) .

## Run tests
test:
	go test ./... -v

## Cross-compile for all platforms via GoReleaser. Triggered automatically on
## "git push <vX.Y.Z tag>" by .github/workflows/release.yml — this target is
## for local dry-runs only.
release:
	goreleaser release --clean --skip=publish

## Snapshot build — produces a dist/ tree without tagging or publishing.
## Use this to verify the release config without cutting a real version.
release-snapshot:
	goreleaser release --snapshot --clean

## Lint
lint:
	golangci-lint run ./...

## Format
fmt:
	gofmt -s -w .

## Clean build artifacts
clean:
	rm -rf bin/ dist/
