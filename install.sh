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
LEGACY_BINARY="birdy_${OS}_${ARCH}"

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

# GoReleaser typically archives the binary as "birdy", but older archives used
# an OS/ARCH suffix. Support both.
BIN_SRC="$TMPDIR/birdy"
if [ ! -f "$BIN_SRC" ]; then
  BIN_SRC="$TMPDIR/$LEGACY_BINARY"
fi
if [ ! -f "$BIN_SRC" ]; then
  echo "Error: birdy binary not found after extracting $ASSET" >&2
  echo "Expected $TMPDIR/birdy or $TMPDIR/$LEGACY_BINARY" >&2
  exit 1
fi

sudo install -m 755 "$BIN_SRC" "$INSTALL_DIR/birdy"

echo "birdy installed to $INSTALL_DIR/birdy"

# The release archive no longer carries the bird CLI or its node_modules, so
# there is nothing to unpack here and no Node runtime to require. birdy serves
# every command itself.
#
# `--bird` still works for anyone who wants to diff the two engines against each
# other; it resolves bird from BIRDY_BIRD_PATH or PATH. Installing bird is a
# maintainer's choice now, not a precondition for birdy working.

birdy version
