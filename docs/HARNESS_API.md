# Birdy Harness API

`POST /api/harness/chat` is the narrow server boundary for a Chrome MV3
extension. It is deliberately separate from `/api/chat`: a harness token grants
no access to commands, the PTY, private timelines, or the general chat tool
surface, and the Birdy Web invite code does not grant harness access.

The endpoint is disabled with HTTP `503` until scoped installation tokens are
configured. It never falls back to invite-code authentication. If a token
mapping is present but malformed, collides with the host invite code, reuses a
token across installations, or lacks a valid dedicated account pool, `birdy
host` fails startup instead of reporting a healthy but silently disabled API.

## Provision and revoke an installation

Generate one high-entropy token per extension installation. Give the raw token
to that installation once; configure only its SHA-256 hash on the server:

```bash
TOKEN="$(openssl rand -hex 32)"
printf '%s' "$TOKEN" | shasum -a 256
```

Set the digest under a stable installation ID:

```text
BIRDY_HARNESS_TOKEN_HASHES={"laptop-a":"<64-hex-sha256>","laptop-b":"<64-hex-sha256>"}
BIRDY_HARNESS_ACCOUNTS=[{"name":"harness-public","auth_token":"...","ct0":"...","read_only":true}]
```

Installation IDs must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`. Tokens must be
32–256 bytes. The JSON object may contain at most 256 installations. Restart or
redeploy after changing the environment. Revoke one installation by deleting
its entry or replacing its hash; other installations keep working.

`BIRDY_HARNESS_ACCOUNTS` is a separate, required X account pool whenever
harness tokens are configured. Birdy validates it at startup and never falls
back to the general `BIRDY_ACCOUNTS` pool. Use a dedicated read-only account
that follows no protected accounts and has no private timeline access. The
endpoint can fetch any submitted tweet ID; `page_url` is validated context, not
cryptographic proof that an ID was visible in the tab. If strict page-visibility
provenance is required, send explicit `selected_text` and no tweet IDs.

This manual provisioning is intentional. A remotely exposed pairing or token
minting endpoint would add an enrollment credential and a larger attack
surface. A future pairing flow must preserve the same per-install scope and
revocation properties.

Optional server settings:

```text
BIRDY_HARNESS_MODEL=sonnet
BIRDY_HARNESS_TRUST_PROXY=1
```

The model is selected only by the server and defaults to `sonnet`.
`BIRDY_HARNESS_TRUST_PROXY=1` should be set only when a trusted reverse proxy
overwrites (rather than appends user input to) `X-Forwarded-For`. Otherwise the
server ignores forwarding headers for rate limiting.

## Request

Send the token from the MV3 service worker or an extension page, not from a
content script:

```http
POST /api/harness/chat HTTP/1.1
Authorization: Bearer <per-install-raw-token>
X-Birdy-Harness-Install-ID: laptop-a
Content-Type: application/json

{
  "version": "1",
  "page_url": "https://x.com/home",
  "visible_tweet_ids": ["2084912076502282341"],
  "prompt": "Summarize the claim and the evidence.",
  "selected_text": "Optional text the user explicitly selected"
}
```

The version 1 schema is strict; unknown fields and trailing JSON values are
rejected. In particular, the endpoint has no `model`, `command`, `account`,
`cookies`, `html`, `dom`, or general `url` field.

| Field | Contract |
| --- | --- |
| `version` | Required string, exactly `"1"` |
| `page_url` | Required HTTPS URL whose host is exactly `x.com` or `twitter.com`; no subdomain, port, userinfo, or fragment; maximum 2 KiB |
| `visible_tweet_ids` | Zero to 12 raw decimal tweet IDs; input order is preserved and repeated IDs are fetched once |
| `prompt` | Required, non-whitespace UTF-8 text; maximum 4 KiB measured in bytes |
| `selected_text` | Optional text from an explicit user selection; maximum 8 KiB measured in bytes |

At least one visible tweet ID or non-empty explicit selection is required. The
server fetches only the listed tweet IDs through birdy's quota-aware read API.
It never consumes X cookies or auth values from the request and it does not
accept page HTML, whole-DOM text, hidden nodes, or a client-selected fetch URL.
Clients should source `selected_text` directly from the user's current browser
selection, never from `document.body.innerText`.

The complete JSON body is limited to 16 KiB. Fetched tweet text is sanitized to
a small public shape and bounded to 8 KiB per tweet before model execution. The
entire exact-ID fetch phase has a 45-second deadline before model streaming.

## Response

Every response has a server-generated `X-Request-ID` and
`X-Birdy-Harness-Version: 1`. Pre-stream failures are JSON:

```json
{
  "ok": false,
  "error": "invalid_page_url",
  "message": "page_url must be an HTTPS URL on exactly x.com or twitter.com",
  "request_id": "9fcbd57a-3a7f-45f2-a4d9-60757bc37b71"
}
```

Successful requests use the existing server-sent event framing. Each JSON event
also carries `request_id`:

```text
event: token
data: {"type":"token","text":"...","request_id":"..."}

event: done
data: {"type":"done","request_id":"..."}
```

The public route emits only `snapshot`, `token`, sanitized `error`, and terminal
`done` events. It suppresses tool commands. A backend that exits without a
`done` event is repaired by the handler so clients always get one terminal
event while the connection remains writable.

Requests are limited in fixed one-minute windows, separately to 10 per
installation token and 30 per client IP. Authentication failures consume the IP
budget. Exact-ID reads have additional budgets of 60 per installation and 120
for the whole process per minute. At most four harness chats execute
concurrently; excess requests fail fast with `503` rather than queueing. Limits
are process-local, so a public deployment should remain at one replica unless
these controls move to a shared edge or datastore.

### Tweet-fetch diagnostics

An X fetch deadline returns the generic `504 tweet_context_timeout`; other X
or transport failures return the generic `502 tweet_context_unavailable`.
Neither response exposes upstream details. The server emits a structured
`harness tweet context fetch failed` process log correlated by `request_id`.
Its fields are limited to `failure_class`, `stage`, aggregate `tweet_count`,
`elapsed_ms`, and numeric `upstream_status` when X supplied one. It never logs
the underlying error string, credentials, account name, tweet IDs or text,
page URL, selection, or prompt. Failure classes are `configuration`,
`transport`, `upstream_http`, `upstream_response`,
`upstream_rate_limited`, `timeout`, `canceled`, or `unknown`.

A configured harness pool must contain at least one enabled read-only account.
Disabled accounts may remain in the pool, but an all-disabled pool fails host
startup instead of turning every request into an unexplained runtime `502`.

## Model capability boundary

The host resolves the exact tweet IDs before starting the model. Claude then
runs with its tool set disabled and a single-turn limit. The model subprocess or
Bird Box receives neither `BIRDY_ACCOUNTS` nor X auth/CSRF values, the host
invite code, or harness token hashes. Codex's host-local tool path is never used
for this endpoint. Tweet and selected text are explicitly marked as untrusted
quoted data in the system prompt.

## Chrome MV3 networking

Declare the exact Birdy host under `host_permissions`, then have the content
script send a narrow message containing the structured fields above to the MV3
service worker. The service worker performs `fetch`.

Extension service-worker and extension-page requests covered by
`host_permissions` do not require server CORS. Accordingly, this endpoint does
not emit `Access-Control-Allow-Origin` and does not implement a permissive
preflight route. A content-script fetch runs in the page origin and is the
wrong transport for this contract.
