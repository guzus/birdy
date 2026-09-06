---
name: birdy
description: Install, operate, and troubleshoot birdy (multi-account X/Twitter CLI that calls X directly, rotating between auth-cookie accounts). Use when configuring birdy accounts/auth cookies, selecting rotation strategies, running X commands (home, search, read, tweet, ...), setting up CI via BIRDY_ACCOUNTS, or debugging the optional --bird engine when bird cannot be found or executed.
---

# Birdy

## Workflow

Use birdy to run X/Twitter commands through a rotating pool of sessions (auth cookies), reducing rate-limit risk. birdy calls X itself — there is no Node runtime and no separate CLI to install.

### 0. Preflight (CLI Required)

If you need to run commands, ensure the `birdy` CLI is installed first:

```bash
bash skills/birdy/scripts/ensure_birdy.sh
birdy version
```

### 1. Install

```bash
brew trust guzus/tap && brew install guzus/tap/birdy
```

Alternative installs:

```bash
curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash  # requires the GitHub CLI `gh`
go install github.com/guzus/birdy@latest
```

All three give a single self-contained binary: no Node, no bundled bird.

### 2. Add Accounts

birdy needs two cookies per account: `auth_token` and `ct0`.

Optional: extract tokens automatically from your local browser cookies:

```bash
# Default tries Chrome, Safari, Firefox
bash skills/birdy/scripts/extract_x_tokens.sh

# Force a specific browser backend
bash skills/birdy/scripts/extract_x_tokens.sh --browsers chrome

# Pick a Chrome profile interactively (arrow keys)
bash skills/birdy/scripts/extract_x_tokens.sh --interactive
```

```bash
birdy account add personal
birdy account add work --auth-token "xxx" --ct0 "yyy"
birdy account list
```

Stored by default:

- `~/.config/birdy/accounts.json`
- `~/.config/birdy/state.json`

### 3. Run X Commands Through Birdy

birdy serves every command natively using the selected account. A flag birdy
does not implement (`--all`, `--max-pages`, `--cursor`, `--media`) is refused
rather than ignored; add `--bird` to run the original Node engine instead,
which requires installing bird separately.

`--json` never changes shape. `--json-full` appends `url`, `createdAtIso`
(RFC 3339 UTC), `viewCount`, `quoteCount`, `bookmarkCount`, `lang`, and
`isRepost`/`isReply`/`isQuote` after the `--json` keys, so prefer it over
rebuilding permalinks or parsing `createdAt` by hand. List commands also take
`--min-likes`, `--min-retweets`, `--min-views <n>` and `--since <24h|7d|RFC3339|YYYY-MM-DD>`;
filters apply after the fetch (raise `-n` to widen the pool) and `--since`
drops tweets with no parsable date.

```bash
# Auto-rotate accounts
birdy home
birdy search "golang"
birdy scrape --handle nasa --search "moon" -n 50
birdy read 1234567890

# Show which account was used
birdy -v home

# Force an account and skip rotation
birdy --account personal whoami

# Choose rotation strategy
birdy --strategy least-used home
```

### 4. Use In CI (Non-Interactive)

Provide accounts via `BIRDY_ACCOUNTS` JSON:

```bash
export BIRDY_ACCOUNTS='[{"name":"bot1","auth_token":"xxx","ct0":"yyy"}]'
birdy -v home
```

### 5. Troubleshoot The Optional `--bird` Engine

Only relevant under `--bird` / `BIRDY_USE_BIRD=1`, which runs the original Node
bird CLI instead of birdy's own implementation — useful for diffing the two
engines. bird is not installed by birdy; you provide it. birdy resolves it in
this order:

1. `BIRDY_BIRD_PATH` (explicit override)
2. `birdy-bird`, then `bird`, on `PATH`
3. next to the running `birdy` binary
4. `third_party/@steipete/bird/dist/cli.js` (git clone only)

Fixes:

- `bird not found`: install bird yourself, or point `BIRDY_BIRD_PATH` at it — or just drop `--bird` and use the native engine.
- bird found but failing to start: it is a Node program; ensure `node --version` is `>= 22`.

### Security

- Treat `auth_token` and `ct0` as secrets.
- Avoid pasting tokens into logs; prefer environment variables and secrets managers in CI.
