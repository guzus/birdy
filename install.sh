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

if [ -f "$TMPDIR/bird/dist/cli.js" ]; then
  # Install the vendored bird npm package next to birdy and create a small wrapper
  # so birdy can exec `birdy-bird` without needing the user to install bird.
  sudo rm -rf "$INSTALL_DIR/bird"
  sudo mkdir -p "$INSTALL_DIR/bird"
  sudo cp -R "$TMPDIR/bird/." "$INSTALL_DIR/bird/"
  sudo chmod +x "$INSTALL_DIR/bird/dist/cli.js" 2>/dev/null || true

  WRAPPER="$TMPDIR/birdy-bird"
  cat >"$WRAPPER" <<'EOF'
#!/bin/sh
set -e

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
CLI="$ROOT/bird/dist/cli.js"

if [ ! -f "$CLI" ]; then
  echo "Error: bundled bird CLI not found at $CLI" >&2
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  echo "Error: node (>= 22) is required to run the bundled bird CLI." >&2
  exit 1
fi

exec node "$CLI" "$@"
EOF
  chmod +x "$WRAPPER"
  sudo install -m 755 "$WRAPPER" "$INSTALL_DIR/birdy-bird"

  echo "bird (bundled) installed to $INSTALL_DIR/bird (wrapper: $INSTALL_DIR/birdy-bird)"
else
  echo "Warning: bundled bird package not found in the release archive."
  echo "Install bird separately from https://github.com/steipete/bird"
fi

birdy version
