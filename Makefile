APP_NAME    := laevitas
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS     := -s -w \
	-X github.com/laevitas/cli/internal/version.Version=$(VERSION) \
	-X github.com/laevitas/cli/internal/version.CommitSHA=$(COMMIT) \
	-X github.com/laevitas/cli/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build install test clean release lint fmt

## Build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) .

## Install to $GOPATH/bin
install:
	go build -ldflags "$(LDFLAGS)" -o $(shell go env GOPATH)/bin/$(APP_NAME)$(shell go env GOEXE) .

## Run tests
test:
	go test ./... -v

## Cross-compile for all platforms
release:
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(APP_NAME)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(APP_NAME)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(APP_NAME)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(APP_NAME)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(APP_NAME)-windows-amd64.exe .

## Lint
lint:
	golangci-lint run ./...

## Format
fmt:
	gofmt -s -w .

## Clean build artifacts
clean:
	rm -rf bin/ dist/
