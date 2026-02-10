#!/usr/bin/env bash
set -euo pipefail

# Ensure birdy is installed and on PATH.
#
# This script is intended to be run by an agent before executing birdy commands.
# It uses birdy's installer, which currently requires the GitHub CLI (`gh`).

if command -v birdy >/dev/null 2>&1; then
  exit 0
fi

echo "birdy not found on PATH." >&2

if ! command -v gh >/dev/null 2>&1; then
  echo "Install GitHub CLI first (required by birdy installer): https://cli.github.com" >&2
  echo "Then install birdy:" >&2
  echo "  curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash" >&2
  exit 1
fi

curl -fsSL "https://raw.githubusercontent.com/guzus/birdy/main/install.sh?cachebust=$(date +%s)" | bash

if ! command -v birdy >/dev/null 2>&1; then
  echo "birdy install completed but birdy is still not on PATH." >&2
  exit 1
fi

