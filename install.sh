#!/bin/bash
set -euo pipefail

REPO="guzus/birdy"
VERSION="${1:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

ASSET="birdy_${OS}_${ARCH}.tar.gz"
BINARY="birdy_${OS}_${ARCH}"

echo "Installing birdy ${VERSION} (${OS}/${ARCH})..."

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# Use gh CLI for private repo access
if command -v gh &>/dev/null; then
  if [ "$VERSION" = "latest" ]; then
    gh release download --repo "$REPO" --pattern "$ASSET" --dir "$TMPDIR"
  else
    gh release download "$VERSION" --repo "$REPO" --pattern "$ASSET" --dir "$TMPDIR"
  fi
else
  echo "Error: gh CLI required for private repo. Install: https://cli.github.com"
  exit 1
fi

tar xzf "$TMPDIR/$ASSET" -C "$TMPDIR"
sudo install -m 755 "$TMPDIR/$BINARY" "$INSTALL_DIR/birdy"

echo "birdy installed to $INSTALL_DIR/birdy"
birdy version
