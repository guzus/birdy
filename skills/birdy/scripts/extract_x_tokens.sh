#!/usr/bin/env bash
set -euo pipefail

# Extract X/Twitter cookies (auth_token + ct0) from local browser profiles.
#
# Requires:
# - birdy installed via install.sh (so the bundled bird package exists next to birdy-bird)
# - node >= 22
#
# Usage:
#   bash skills/birdy/scripts/extract_x_tokens.sh
#   bash skills/birdy/scripts/extract_x_tokens.sh --browsers chrome
#   bash skills/birdy/scripts/extract_x_tokens.sh --format json

HERE="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

FORMAT="env"
BROWSERS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --format) FORMAT="${2:-}"; shift 2 ;;
    --format=*) FORMAT="${1#*=}"; shift 1 ;;
    --browsers) BROWSERS="${2:-}"; shift 2 ;;
    --browsers=*) BROWSERS="${1#*=}"; shift 1 ;;
    -h|--help)
      echo "Usage: extract_x_tokens.sh [--format env|json] [--browsers chrome,safari,firefox,edge]" >&2
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

bash "$HERE/ensure_birdy.sh"

if ! command -v node >/dev/null 2>&1; then
  echo "Error: node is required (node >= 22 recommended)." >&2
  exit 1
fi

BIN_DIR=""
if command -v birdy-bird >/dev/null 2>&1; then
  BIN_DIR="$(dirname "$(command -v birdy-bird)")"
elif command -v birdy >/dev/null 2>&1; then
  BIN_DIR="$(dirname "$(command -v birdy)")"
else
  echo "Error: birdy not found on PATH after ensure_birdy.sh" >&2
  exit 1
fi

# birdy installer copies the bird package directory next to the binary.
BIRD_DIR="$BIN_DIR/bird"

SWEET_COOKIE_MODULE=""
if [[ -f "$BIRD_DIR/node_modules/@steipete/sweet-cookie/dist/index.js" ]]; then
  SWEET_COOKIE_MODULE="$BIRD_DIR/node_modules/@steipete/sweet-cookie/dist/index.js"
elif [[ -f "$BIRD_DIR/node_modules/@steipete/sweet-cookie/dist/public.js" ]]; then
  SWEET_COOKIE_MODULE="$BIRD_DIR/node_modules/@steipete/sweet-cookie/dist/index.js"
elif [[ -f "$(pwd)/third_party/@steipete/bird/node_modules/@steipete/sweet-cookie/dist/index.js" ]]; then
  SWEET_COOKIE_MODULE="$(pwd)/third_party/@steipete/bird/node_modules/@steipete/sweet-cookie/dist/index.js"
fi

if [[ -z "$SWEET_COOKIE_MODULE" ]]; then
  echo "Error: could not locate @steipete/sweet-cookie. Expected it under $BIRD_DIR/node_modules/..." >&2
  echo "Tip: install birdy via install.sh so the bundled bird package is installed next to birdy-bird." >&2
  exit 1
fi

export SWEET_COOKIE_MODULE

ARGS=( "--format=$FORMAT" )
if [[ -n "$BROWSERS" ]]; then
  ARGS+=( "--browsers=$BROWSERS" )
fi

node "$HERE/extract_x_tokens.mjs" "${ARGS[@]}"

