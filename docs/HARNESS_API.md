# Birdy Harness API

`POST /api/harness/chat` is the narrow server boundary for Birdy Web's Chrome
MV3 extension. It is deliberately separate from `/api/chat`: a harness token
grants no command, PTY, private-timeline, or general chat-tool capability, and
the Birdy Web invite code does not grant harness access.

The extension reads the current tab with the user's local browser session and
sends a small normalized representation of posts already rendered there. The
server never receives X cookies, never logs in to X, and never resolves tweet
IDs. All submitted tweet metadata and text is treated as untrusted quoted model
input, not as authenticated X data or proof that a post was visible.

## Provision and revoke an installation

The endpoint is disabled with HTTP `503` until scoped installation tokens are
configured. It never falls back to invite-code authentication. Generate one
high-entropy token per installation and configure only its SHA-256 hash:

```bash
TOKEN="$(openssl rand -hex 32)"
printf '%s' "$TOKEN" | shasum -a 256
```

```text
BIRDY_HARNESS_TOKEN_HASHES={"laptop-a":"<64-hex-sha256>","laptop-b":"<64-hex-sha256>"}
```

Installation IDs must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`. Raw tokens must
be 32–256 bytes. The JSON object may contain at most 256 installations. Restart
after changing it. Revoke one installation by deleting its entry or replacing
its hash. Malformed mappings, duplicate tokens, or a token that hashes to the
host invite code fail host startup.

Optional settings:

```text
BIRDY_HARNESS_BACKEND=claude-code
BIRDY_HARNESS_MODEL=sonnet
BIRDY_HARNESS_TRUST_PROXY=1
```

The backend and model are fixed by the server; request fields cannot select
or override either. The default remains Claude Code with `sonnet`, preserving
the original deployment contract. Claude model identifiers retain the existing
bounded server-side format.

The only alternative provider/model pair is the locally verified OpenCode Go
route for DeepSeek V4 Flash:

```text
BIRDY_HARNESS_BACKEND=opencode
BIRDY_HARNESS_MODEL=opencode-go/deepseek-v4-flash
OPENCODE_API_KEY=<OpenCode Go subscription key>
```

`OpenCode Go` is the provider/subscription route, not a model name. The exact
supported model identifier is `opencode-go/deepseek-v4-flash`; the similarly
worded “DeepSeek V2 Flash” premise is not a supported identifier. Birdy's
runtime pins `opencode-ai` 1.18.3, matching the JSON event contract tested by
the server. An absent model value defaults to that exact model when the
backend is `opencode`; any other OpenCode model, missing key, or unknown
backend fails host startup.

There is deliberately no automatic cross-provider fallback. A provider error
becomes the existing sanitized SSE error and terminal `done`; Birdy never
mixes two model answers or silently changes model provenance. To switch back,
restore `BIRDY_HARNESS_BACKEND=claude-code` (or remove it) and use the desired
Claude model.

`BIRDY_HARNESS_TRUST_PROXY=1` is safe only when a trusted reverse proxy
overwrites `X-Forwarded-For`; otherwise forwarding headers are ignored.

`BIRDY_HARNESS_ACCOUNTS` has been removed. Delete it during the v2 migration.
The general `BIRDY_ACCOUNTS` setting remains for Birdy's other commands, but
the harness handler neither reads it nor creates an X client.

## Version 2 request

Send the token from the MV3 service worker or an extension page:

```http
POST /api/harness/chat HTTP/1.1
Authorization: Bearer <per-install-raw-token>
X-Birdy-Harness-Install-ID: laptop-a
Content-Type: application/json

{
  "version": "2",
  "page_url": "https://x.com/home",
  "visible_tweets": [
    {
      "id": "2084912076502282341",
      "url": "https://x.com/example/status/2084912076502282341",
      "author_handle": "example",
      "author_name": "Example",
      "text": "Text rendered in the visible post",
      "created_at": "2026-08-09T03:00:00Z",
      "truncated": false,
      "reply_to_id": "2084000000000000000",
      "quoted_tweet_id": "2083000000000000000",
      "repost_of_id": "2082000000000000000"
    }
  ],
  "prompt": "Summarize the claim and the evidence.",
  "selected_text": "Optional text the user explicitly selected"
}
```

The schema is exact at every nesting level. Unknown fields, trailing JSON,
cookies, auth values, raw HTML, whole-DOM text, commands, client-selected
models, and arbitrary fetch URLs are rejected.

| Field | Contract |
| --- | --- |
| `version` | Required string, exactly `"2"` |
| `page_url` | Required HTTPS URL on exactly `x.com` or `twitter.com`; no subdomain, port, userinfo, or fragment; maximum 2 KiB |
| `visible_tweets` | Required array of zero to 12 normalized objects in page order; use `[]` for selection-only requests |
| `prompt` | Required non-whitespace UTF-8 text; maximum 4 KiB |
| `selected_text` | Optional explicit browser selection; maximum 8 KiB |

Each visible tweet has these exact fields:

| Field | Contract |
| --- | --- |
| `id` | Required decimal tweet ID, 5–25 digits, no leading zero |
| `url` | Required exact `https://x.com/<handle>/status/<id>` or `https://twitter.com/<handle>/status/<id>` source URL; `i` may replace the handle; ID must match; no query, fragment, port, userinfo, suffix path, or encoding |
| `author_handle` | Optional handle without `@`; 1–15 ASCII letters, digits, or underscores |
| `author_name` | Optional UTF-8 display name; maximum 256 bytes |
| `text` | Required non-whitespace UTF-8 visible text; maximum 8 KiB |
| `created_at` | Optional RFC3339 timestamp; normalized to UTC before model input |
| `truncated` | Optional boolean indicating that the local extractor intentionally bounded the visible text |
| `reply_to_id` | Optional validated tweet ID; may refer to a post outside the submitted set |
| `quoted_tweet_id` | Optional validated tweet ID; may refer to a post outside the submitted set |
| `repost_of_id` | Optional validated tweet ID; may refer to a post outside the submitted set |

Relations may coexist because they report independently observed structure, but
none may self-reference. The server does not fetch relation targets. Tweet text
is limited to 32 KiB in aggregate and the complete body to 64 KiB. At least one
visible tweet or a non-empty explicit selection is required.

Input order is preserved. Exact duplicate IDs with identical normalized
content are collapsed to their first occurrence. A duplicate ID with conflicting
content is rejected as `conflicting_duplicate_tweet`; the server never guesses
which copy is true. Clients should submit only rendered post cards and the
user's explicit selection, never `document.body.innerText` or hidden DOM.

Structural validation does not establish provenance. A token holder can invent
otherwise well-formed text, URLs, authors, timestamps, and relations. The model
is instructed accordingly, and responses must not be treated as authenticated
copies of X content.

## Cutover from version 1

Version 2 is intentionally incompatible. Version 1 requests receive
`400 unsupported_version`; the removed `visible_tweet_ids` field receives
`400 invalid_json`. There is no dual-read fallback and the server never turns
an ID into an X request. Deploy a v2-capable server and extension as one
coordinated cutover; old clients stop working rather than regaining server-side
cookie access.

Remove `BIRDY_HARNESS_ACCOUNTS` from deployment secrets. No replacement X
credential is required. Keep `BIRDY_HARNESS_TOKEN_HASHES`, and rotate only if
the client installation/token relationship itself changed.

## Response and limits

Every response has a server-generated `X-Request-ID` and
`X-Birdy-Harness-Version: 2`. Pre-stream failures are JSON:

```json
{"ok":false,"error":"invalid_page_url","message":"page_url must be an HTTPS URL on exactly x.com or twitter.com","request_id":"..."}
```

Contract and authentication failures are `4xx`; disabled configuration and
capacity exhaustion are `503`. Once accepted, responses use the existing SSE
events. Every event carries the same request ID:

```text
event: token
data: {"type":"token","text":"...","request_id":"..."}

event: done
data: {"type":"done","request_id":"..."}
```

Only `snapshot`, `token`, sanitized `error`, and exactly one terminal `done`
are public. Tool events are suppressed and converted to a sanitized error.
Cancellation and the six-minute request deadline remain in force.

Requests use fixed one-minute windows: 10 per installation and 30 per client
IP. Authentication failures consume the IP budget. At most four chats execute
concurrently; excess requests fail fast with `503`. Limits are process-local,
so a public deployment should remain at one replica unless they move to a
shared edge or datastore.

## Model and network boundaries

Claude runs with tools disabled and a single-turn limit. Bird Box is used only
for Claude; selecting OpenCode never falls through to Bird Box or Claude.

OpenCode runs one turn with the exact provider/model above, `--pure`, a
mode-0600 config, and wildcard tool denial at both global and named-agent
levels. Its prompt travels over stdin, not process arguments. HOME and every
XDG directory point at one disposable request directory that is removed when
the process exits. Persistent OpenCode auth, plugins, MCP servers, project
instructions, sharing, and all other providers are unavailable. The child
environment is an allowlist containing the OpenCode key, basic runtime/CA
settings, and no Claude credential, Birdy account pool, X auth/CSRF values,
host invite code, or harness token hashes. Provider error payloads and stderr
are never returned to the client.

As with Claude, normalized visible post content is sent to the selected model
provider to answer the request. The isolation above prevents that content from
being copied into argv, persistent OpenCode state, tools, or unrelated model
providers; it does not make the configured model provider local. Codex's
host-local tool path is never used.

Declare the exact Birdy host under MV3 `host_permissions`, then message the
service worker from the content script and let the worker perform `fetch`.
Extension-worker requests covered by `host_permissions` need no server CORS.
The endpoint therefore emits no `Access-Control-Allow-Origin` and provides no
permissive preflight route.
