#!/usr/bin/env sh
set -eu

PORT="${PORT:-8787}"
INVITE_CODE="${BIRDY_HOST_INVITE_CODE:-${BIRDY_HOST_TOKEN:-}}"

if [ -z "${INVITE_CODE}" ]; then
  echo "BIRDY_HOST_INVITE_CODE is required (or legacy BIRDY_HOST_TOKEN)" >&2
  exit 1
fi

if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${ANTHROPIC_AUTH_TOKEN:-}" ]; then
  echo "warning: no Claude auth env var set (CLAUDE_CODE_OAUTH_TOKEN / ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN)" >&2
fi

mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/birdy"

exec /usr/local/bin/birdy host --addr "0.0.0.0:${PORT}" --invite-code "${INVITE_CODE}"
