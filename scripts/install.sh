#!/bin/sh
# Laevitas CLI installer
# Usage: curl -sSL https://cli.laevitas.ch/install.sh | sh
#
# Detects OS/arch, downloads the latest release, installs to /usr/local/bin

set -e

REPO="laevitas/cli"
BINARY_NAME="laevitas"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { printf "${GREEN}▸${NC} %s\n" "$1"; }
warn()  { printf "${YELLOW}▸${NC} %s\n" "$1"; }
error() { printf "${RED}▸${NC} %s\n" "$1" >&2; exit 1; }

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) error "Unsupported OS: $(uname -s)" ;;
    esac
}

# Detect architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) error "Unsupported architecture: $(uname -m)" ;;
    esac
}

# Pick install directory based on OS
detect_install_dir() {
    case "$1" in
        windows)
            # Git Bash / MINGW — use ~/bin which is typically in PATH
            echo "$HOME/bin" ;;
        *)
            echo "/usr/local/bin" ;;
    esac
}

main() {
    OS=$(detect_os)
    ARCH=$(detect_arch)
    INSTALL_DIR=$(detect_install_dir "$OS")

    info "Detected: ${OS}/${ARCH}"

    # Get latest release tag
    LATEST=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$LATEST" ]; then
        error "Could not determine latest version"
    fi

    info "Latest version: ${LATEST}"

    # Build download URL
    SUFFIX=""
    if [ "$OS" = "windows" ]; then
        SUFFIX=".exe"
    fi
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST}/${BINARY_NAME}-${OS}-${ARCH}${SUFFIX}"

    # Download
    TMP_DIR=$(mktemp -d)
    TMP_FILE="${TMP_DIR}/${BINARY_NAME}${SUFFIX}"

    info "Downloading ${DOWNLOAD_URL}..."
    curl -sSL -o "$TMP_FILE" "$DOWNLOAD_URL" || error "Download failed"

    # Install
    chmod +x "$TMP_FILE"
    mkdir -p "$INSTALL_DIR"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}${SUFFIX}"
    else
        info "Requesting sudo to install to ${INSTALL_DIR}..."
        sudo mv "$TMP_FILE" "${INSTALL_DIR}/${BINARY_NAME}${SUFFIX}"
    fi

    rm -rf "$TMP_DIR"

    info "Installed ${BINARY_NAME} ${LATEST} to ${INSTALL_DIR}/${BINARY_NAME}${SUFFIX}"
    echo ""

    # Check if install dir is in PATH
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            warn "${INSTALL_DIR} is not in your PATH."
            warn "Add it with:  export PATH=\"${INSTALL_DIR}:\$PATH\""
            echo "" ;;
    esac

    info "Get started:"
    echo "  ${BINARY_NAME} config init          # Set up your API key"
    echo "  ${BINARY_NAME} futures snapshot      # Your first query"
    echo "  ${BINARY_NAME} --help                # See all commands"
    echo ""
}

main
