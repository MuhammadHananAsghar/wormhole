#!/bin/sh
set -e

REPO="MuhammadHananAsghar/wormhole"
INSTALL_DIR="/usr/local/bin"
BINARY="wormhole"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    linux) OS="linux" ;;
    darwin) OS="darwin" ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

ARCHIVE="wormhole_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"

echo "Downloading wormhole for ${OS}/${ARCH}..."

# Download
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "$URL"
elif command -v wget >/dev/null 2>&1; then
    wget -qO "${TMPDIR}/${ARCHIVE}" "$URL"
else
    echo "Error: curl or wget is required"
    exit 1
fi

# Extract
tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}"
chmod +x "${TMPDIR}/${BINARY}"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
    echo "Installing to ${INSTALL_DIR} (requires sudo)..."
    sudo mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

echo ""
echo "  wormhole installed successfully!"
echo ""
echo "  Run: wormhole http 3000"
echo ""
