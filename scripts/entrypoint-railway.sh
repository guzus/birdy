#!/usr/bin/env sh
set -eu

PORT="${PORT:-8787}"
INVITE_CODE="${BIRDY_HOST_INVITE_CODE:-${BIRDY_HOST_TOKEN:-}}"

if [ -z "${INVITE_CODE}" ]; then
  echo "BIRDY_HOST_INVITE_CODE is required (or legacy BIRDY_HOST_TOKEN)" >&2
  exit 1
fi

if [ -z "${BIRDY_E2B_TEMPLATE:-}" ]; then
  echo "BIRDY_E2B_TEMPLATE is required for bird-box (use a custom E2B template with birdy and Claude Code preinstalled)" >&2
  exit 1
fi

if [ -z "${E2B_API_KEY:-}" ]; then
  echo "E2B_API_KEY is required" >&2
  exit 1
fi

if [ -z "${BIRDY_ACCOUNTS:-}" ]; then
  echo "BIRDY_ACCOUNTS is required so bird-box can run birdy" >&2
  exit 1
fi

if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && [ -z "${ANTHROPIC_API_KEY:-}" ] && [ -z "${ANTHROPIC_AUTH_TOKEN:-}" ]; then
  echo "Claude authentication is required (set CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY, or ANTHROPIC_AUTH_TOKEN)" >&2
  exit 1
fi

mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/birdy"

exec /usr/local/bin/birdy host --addr "0.0.0.0:${PORT}" --invite-code "${INVITE_CODE}"
