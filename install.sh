#!/bin/sh
# Laevitas CLI installer for macOS / Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/laevitas/cli/main/install.sh | sh
#
# Environment overrides:
#   LAEVITAS_VERSION  — install a specific version (e.g. v0.5.0). Defaults to latest.
#   LAEVITAS_PREFIX   — install directory. Defaults to $HOME/.local/bin (falls back to /usr/local/bin if writable).

set -eu

REPO="laevitas/cli"
BIN_NAME="laevitas"

err() { printf '\033[31m✗\033[0m %s\n' "$1" >&2; exit 1; }
info() { printf '\033[36m→\033[0m %s\n' "$1"; }
ok() { printf '\033[32m✓\033[0m %s\n' "$1"; }

# ─── detect OS / arch ────────────────────────────────────────────────────────
os_raw=$(uname -s)
case "$os_raw" in
  Darwin) OS="macOS" ;;
  Linux)  OS="Linux" ;;
  *)      err "Unsupported OS: $os_raw" ;;
esac

arch_raw=$(uname -m)
case "$arch_raw" in
  x86_64|amd64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) err "Unsupported architecture: $arch_raw" ;;
esac

# ─── pick version ────────────────────────────────────────────────────────────
VERSION="${LAEVITAS_VERSION:-}"
if [ -z "$VERSION" ]; then
  info "Resolving latest release..."
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
  [ -z "$VERSION" ] && err "Could not determine latest release tag."
fi
# Strip leading v for the archive filename, but keep it for the URL.
VERSION_BARE="${VERSION#v}"

# ─── pick install dir ────────────────────────────────────────────────────────
PREFIX="${LAEVITAS_PREFIX:-}"
if [ -z "$PREFIX" ]; then
  if [ -w "/usr/local/bin" ] 2>/dev/null; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="$HOME/.local/bin"
  fi
fi
mkdir -p "$PREFIX"

# ─── download + extract ──────────────────────────────────────────────────────
ARCHIVE="${BIN_NAME}_${VERSION_BARE}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

info "Downloading $URL"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

if ! curl -fsSL "$URL" -o "$TMP/$ARCHIVE"; then
  err "Download failed. Check that ${VERSION} exists at https://github.com/${REPO}/releases"
fi

# Verify checksum if checksums.txt is published.
SUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if curl -fsSL "$SUMS_URL" -o "$TMP/checksums.txt" 2>/dev/null; then
  EXPECTED=$(grep " ${ARCHIVE}\$" "$TMP/checksums.txt" | awk '{print $1}')
  if [ -n "$EXPECTED" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      ACTUAL=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
    else
      ACTUAL=""
    fi
    if [ -n "$ACTUAL" ] && [ "$EXPECTED" != "$ACTUAL" ]; then
      err "Checksum mismatch for $ARCHIVE"
    fi
    [ -n "$ACTUAL" ] && info "Checksum verified."
  fi
fi

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
[ -f "$TMP/$BIN_NAME" ] || err "Archive did not contain expected binary '$BIN_NAME'."

install -m 0755 "$TMP/$BIN_NAME" "$PREFIX/$BIN_NAME" 2>/dev/null \
  || { mv "$TMP/$BIN_NAME" "$PREFIX/$BIN_NAME" && chmod +x "$PREFIX/$BIN_NAME"; }

ok "Installed $BIN_NAME $VERSION → $PREFIX/$BIN_NAME"

# Warn if prefix isn't on PATH.
case ":$PATH:" in
  *":$PREFIX:"*) : ;;
  *)
    printf '\033[33m!\033[0m %s is not on your PATH. Add this to your shell profile:\n\n' "$PREFIX"
    printf '    export PATH="%s:$PATH"\n\n' "$PREFIX"
    ;;
esac

"$PREFIX/$BIN_NAME" version 2>/dev/null || true
