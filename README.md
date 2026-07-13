<p align="center">
  <img src="assets/birdy-logo.svg" alt="birdy logo" width="240">
</p>

# birdy

Multi-account proxy for the [bird](https://github.com/steipete/bird) CLI. Store multiple X/Twitter auth tokens and automatically rotate between accounts to reduce rate-limit risk.

## First Run (Install + Open TUI)

```bash
curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash && birdy account add main && birdy tui
```

`birdy account add main` will prompt you for `auth_token` and `ct0` from your X/Twitter browser session.

## Interactive TUI

Launch the full-screen terminal interface with AI-powered chat:

```bash
birdy tui
```

![birdy TUI](assets/tui.png)

The TUI features:
- **Chat** — Ask birdy to read your timeline, search tweets, post, and more via Claude or Codex
- **Deep browsing** — Say "dive deeper" and birdy will autonomously explore threads, replies, and user profiles
- **Model switching** — Press `Ctrl+T` to cycle `sonnet`, `opus`, `haiku`, and `codex`
- **Account management** — Add, remove, and view accounts with `tab`
- **Chat history** — Conversations are saved as markdown in `~/.config/birdy/chats/` (set `BIRDY_TUI_HIDE_HISTORY=1` to disable)

## Hosted Web TUI

Run birdy as a browser-accessible terminal session:

```bash
birdy host --addr 0.0.0.0:8787
```

birdy will print the local URL:

```text
http://127.0.0.1:8787
```

Users must enter an invite code to connect.

Notes:
- The host runs the same `birdy tui` session in a web terminal.
- Set invite code with `--invite-code` or `BIRDY_HOST_INVITE_CODE`.
- For public deployments, set `BIRDY_READ_ONLY=1`.
- This is a shared session: everyone who knows the invite code can see/control the same TUI.

## Deploy on Railway

This repo now includes a Railway-ready container setup:
- `Dockerfile`
- `scripts/entrypoint-railway.sh`
- `.env.railway.example`

### 1. Create service

Create a Railway service from this repo. Railway will detect and build the `Dockerfile`.

### 2. Add required variables

Set these in Railway Variables:

```bash
# Required: invite code users enter in the browser
BIRDY_HOST_INVITE_CODE=replace-with-long-random-secret

# Recommended for public deployments: disable write actions
BIRDY_READ_ONLY=1

# Optional: lock websocket origins to specific public domains
# BIRDY_HOST_ALLOWED_ORIGINS=https://your-domain.example,https://<railway-domain>

# Optional: hide/disable local chat history UI + persistence (recommended for public deploys)
BIRDY_TUI_HIDE_HISTORY=1

# Required: X/Twitter accounts as JSON (single line)
BIRDY_ACCOUNTS=[{"name":"main","auth_token":"x_auth_token_here","ct0":"x_ct0_here"}]

# Required for AI chat (pick one auth method)
CLAUDE_CODE_OAUTH_TOKEN=replace-with-claude-code-oauth-token
# or
# ANTHROPIC_API_KEY=replace-with-anthropic-api-key
# or
# ANTHROPIC_AUTH_TOKEN=replace-with-anthropic-auth-token
```

### 3. Add persistent volume

Mount a Railway volume at:

```text
/data
```

This preserves:
- `~/.config/birdy/accounts.json`
- `~/.config/birdy/state.json`
- `~/.config/birdy/chats/`

### 4. Deploy and open

After deploy, open your Railway public URL and enter your invite code:

```text
https://<your-service-domain>
```

### Railway notes

- Keep this service at `1` replica (session is shared and stateful).
- The container uses Node 22 + Claude Code CLI + bundled bird CLI.
- Rotate `BIRDY_HOST_INVITE_CODE` if it leaks.

## How it works

birdy sits in front of the `bird` CLI. When you run a bird command through birdy, it:

1. Picks an account from your stored credentials using a rotation strategy
2. Injects the `AUTH_TOKEN` and `CT0` environment variables
3. Forwards the command to `bird`
4. Tracks usage per account for smart rotation

## VPN routing (`--vpn`)

X's bot detection blocks by IP, so rotating auth tokens across 5 accounts from the same machine still trips Cloudflare after ~30 lookups. `--vpn` routes each bird call through a SOCKS5 endpoint (NordVPN's service credentials work out of the box), with per-invocation server selection so you can rotate exits as well as accounts.

```bash
# One-time setup
birdy vpn install-deps                              # installs Node 'undici' into ~/.cache/birdy
birdy vpn set --user <NORDVPN_SERVICE_USERNAME> --pass <NORDVPN_SERVICE_PASSWORD>
birdy vpn pool add us9876.nordvpn.com               # add as many as you want
birdy vpn pool add jp14.nordvpn.com

# Per-invocation
birdy --vpn user-tweets @handle                     # random server from pool
birdy --vpn-server us9876.nordvpn.com whoami        # pin specific exit
birdy vpn test                                      # show the egress IP
birdy vpn status                                    # config (password masked)
```

How it works under the hood:

1. `--vpn` spins up an in-process HTTP CONNECT proxy on `127.0.0.1:<random>` that forwards to the chosen SOCKS5 endpoint.
2. The bird subprocess gets `HTTPS_PROXY=http://127.0.0.1:<port>` + `NODE_OPTIONS=--require=<bootstrap.js>` + `BIRDY_UNDICI_PATH=<undici install dir>`.
3. The bootstrap loads userspace undici (installed by `install-deps`), calls `setGlobalDispatcher(new ProxyAgent(HTTPS_PROXY))`, and **overwrites `globalThis.fetch` with the userspace undici's fetch**. The override is critical: Node 22+'s built-in `fetch()` uses an *internal* undici instance that ignores user-space `setGlobalDispatcher`. Without overwriting `globalThis.fetch`, the bootstrap loads but the proxy is silently bypassed (verified empirically on Node 26).

NordVPN service credentials are different from your account login — find them in the NordVPN dashboard under **Services → NordVPN → Set up NordVPN manually**.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/guzus/birdy/main/install.sh | bash
```

Requires the [GitHub CLI](https://cli.github.com) (`gh`). To install a specific version, pass it as an argument: `... | bash -s v0.2.0`

### Alternatives

```bash
# From source (requires Go)
go install github.com/guzus/birdy@latest

# Build locally
git clone https://github.com/guzus/birdy.git && cd birdy && make build

# Maintainer verification
make verify
```

If you install via `go install`, only the `birdy` binary is installed, so you still need [bird](https://github.com/steipete/bird) available on your PATH (or set `BIRDY_BIRD_PATH`).

If you build from a git clone, bird is vendored under `third_party/@steipete/bird/` (requires Node `>= 22`).

## Prerequisites

- The installer bundles the upstream [bird](https://github.com/steipete/bird) CLI and installs it as `birdy-bird` (birdy will auto-detect it). The bundled bird requires Node `>= 22`.
- [Claude Code](https://claude.ai/claude-code) (`claude` CLI) or [Codex CLI](https://github.com/openai/codex) (`codex` CLI) — required for the interactive TUI (`birdy tui`)

Optional:
- `BIRDY_TUI_CODEX_MODEL` — override the Codex model used by the `codex` TUI slot (default: `gpt-5.4-mini`)

To force a specific bird binary, set `BIRDY_BIRD_PATH=/path/to/bird`.

## Quick start

```bash
# Add accounts (you'll be prompted for auth_token and ct0)
birdy account add personal
birdy account add work
birdy account add alt

# Or pass credentials directly
birdy account add bot --auth-token "xxx" --ct0 "yyy"

# Restrict a specific account to read-only bird commands
birdy account update bot --read-only

# Now use bird commands through birdy - accounts rotate automatically
birdy read 1234567890
birdy search "golang"
birdy home
birdy mentions
birdy activity 1234567890 --types likes,reposts --json

# See which account was used with --verbose
birdy -v home

# Use a specific account
birdy --account personal whoami

# Check rotation status
birdy status

# List accounts
birdy account list
```

## Following Overlap

Find accounts followed by at least N accounts from a seed set:

```bash
birdy following-overlap elonmusk sama --min 2
```

The default output is tab-separated:

```text
2	@example	Example Account	1000000 followers	elonmusk,sama
```

Use `--json` for scripts, `--max-pages N` for a bounded sample, and `--page-size N` to tune pagination. By default, birdy fetches all following pages for each seed account and rotates through configured birdy accounts just like normal forwarded bird commands.

## Account management

```bash
birdy account add <name>        # Add account (interactive or with --auth-token/--ct0)
birdy account add <name> --read-only
birdy account list              # List all accounts with access mode + usage stats
birdy account update <name>     # Update credentials for an account
birdy account update <name> --read-only
birdy account update <name> --read-write
birdy account remove <name>     # Remove an account
```

Use `--read-only` on add or update to keep a specific account available for reads while blocking mutating bird commands like `tweet`, `reply`, `follow`, `unfollow`, and `unbookmark`. Use `--read-write` to lift that restriction.

## Rotation strategies

Control how birdy picks the next account with `--strategy` / `-s`:

| Strategy | Description |
|---|---|
| `round-robin` (default) | Cycles through accounts in order |
| `least-recently-used` | Picks the account used longest ago |
| `least-used` | Picks the account with the fewest total uses |
| `random` | Picks a random account |

```bash
birdy -s least-used search "rust"
birdy -s random home
```

## Getting auth tokens

You need two cookies from an active X/Twitter web session:

1. Open X/Twitter in your browser and log in
2. Open Developer Tools (F12) > Application > Cookies > `https://x.com`
3. Copy the values of `auth_token` and `ct0`

Repeat for each account you want to add.

## GitHub Actions / CI

In CI environments where there's no interactive terminal, set the `BIRDY_ACCOUNTS` env var with a JSON array of accounts:

```bash
export BIRDY_ACCOUNTS='[{"name":"bot1","auth_token":"xxx","ct0":"yyy","read_only":true},{"name":"bot2","auth_token":"aaa","ct0":"bbb"}]'
birdy -v read 1234567890
```

When `BIRDY_ACCOUNTS` is set and no accounts file exists on disk, birdy runs in ephemeral mode — accounts are loaded from the env var and nothing is written to disk.

If an accounts file also exists, env accounts are merged in (overriding any file account with the same name).

### GitHub Actions example

Store the JSON as a repository secret named `BIRDY_ACCOUNTS`, then use it in your workflow:

```yaml
- name: Read a tweet
  env:
    BIRDY_ACCOUNTS: ${{ secrets.BIRDY_ACCOUNTS }}
  run: birdy -v read 1234567890
```

See [`.github/workflows/example.yml`](.github/workflows/example.yml) for a full workflow.

## Config location

Accounts are stored in `~/.config/birdy/accounts.json` with `0600` permissions (owner-only read/write). Rotation state is tracked in `~/.config/birdy/state.json`.

## License

MIT
