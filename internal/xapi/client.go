// Package xapi is a native Go client for the subset of X's web GraphQL API that
// birdy needs to read tweets. It replaces shelling out to the bird CLI (a Node
// program) for the read paths, so a Go program embedding birdy needs no Node
// runtime.
//
// Authentication mirrors a logged-in browser session: X's public web bearer
// token identifies the app, while the caller's auth_token cookie and ct0 CSRF
// token identify the user.
package xapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// maxResponseBytes caps a single GraphQL response. Large conversations run to a
// few MB; this leaves generous headroom without allowing unbounded growth.
const maxResponseBytes = 32 << 20

// defaultTimeout bounds a single request when the caller's context has no deadline.
const defaultTimeout = 30 * time.Second

// Credentials are the cookies from a logged-in X session.
type Credentials struct {
	AuthToken string
	CT0       string
}

// APIError is a failure reported by X rather than by transport.
type APIError struct {
	StatusCode int
	Message    string
	// RateLimited reports an HTTP 429.
	RateLimited bool
	// Code is X's own error code from a v1.1 error envelope, when present.
	Code int
	// Terminal marks an answer that will not change on retry — blocked, user
	// not found, already in the requested state. Callers use it to stop
	// falling through to an alternate endpoint that would report the same
	// thing less clearly.
	Terminal bool
}

func (e *APIError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("x api: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return "x api: " + e.Message
}

// Client talks to X's GraphQL API on behalf of one account.
type Client struct {
	creds      Credentials
	httpClient *http.Client
	userAgent  string
	// baseURL is the GraphQL endpoint root. Overridable so tests and benchmarks
	// can run against a local server instead of the live API.
	baseURL string

	// clientUUID and deviceID are stable per client, matching how a browser
	// session presents itself across requests.
	clientUUID string
	deviceID   string

	// viewerEndpoints are the v1.1 account URLs CurrentUser tries, in order.
	// Overridable so tests can point them at a local server.
	viewerEndpoints []string
	// settingsPages are the HTML pages CurrentUser scrapes when the JSON
	// endpoints fail, which as of 2026-08-07 is always.
	settingsPages []string
	viewer        viewerCache

	// userTweetsPageDelay is the wait before every user-tweets page after the
	// first. bird's getUserTweetsPaged defaults it to 1000ms and it is the only
	// command with one. Overridable so tests do not sleep.
	userTweetsPageDelay time.Duration

	// friendshipEndpoints overrides the v1.1 follow/unfollow URLs. Tests only.
	friendshipEndpoints []string
	// userListRESTPaths overrides the v1.1 followers/friends URLs. Tests only.
	userListRESTPaths []string
}

// NewClient builds a client for the given credentials.
func NewClient(creds Credentials) (*Client, error) {
	if strings.TrimSpace(creds.AuthToken) == "" || strings.TrimSpace(creds.CT0) == "" {
		return nil, fmt.Errorf("x api: auth_token and ct0 are both required")
	}

	return &Client{
		creds:           creds,
		httpClient:      &http.Client{Timeout: defaultTimeout},
		userAgent:       defaultUserAgent,
		baseURL:         graphQLBase,
		clientUUID:      randomHex(16),
		deviceID:        randomHex(16),
		viewerEndpoints: defaultViewerEndpoints,
		settingsPages:   defaultSettingsPages,

		userTweetsPageDelay: time.Second,
	}, nil
}

// SetFriendshipEndpoints overrides the v1.1 follow/unfollow URLs.
// Intended for tests; production callers should leave the defaults in place.
func (c *Client) SetFriendshipEndpoints(endpoints []string) {
	c.friendshipEndpoints = endpoints
}

// SetUserListRESTPaths overrides the v1.1 followers/friends list URLs.
// Intended for tests; production callers should leave the defaults in place.
func (c *Client) SetUserListRESTPaths(paths []string) {
	c.userListRESTPaths = paths
}

// SetViewerEndpoints overrides the v1.1 account URLs CurrentUser tries.
// Intended for tests; production callers should leave the defaults in place.
func (c *Client) SetViewerEndpoints(endpoints []string) {
	c.viewerEndpoints = endpoints
	c.settingsPages = nil
	c.viewer.mu.Lock()
	c.viewer.seen = nil
	c.viewer.mu.Unlock()
}

// SetSettingsPages overrides the HTML pages CurrentUser scrapes. Tests only.
func (c *Client) SetSettingsPages(pages []string) {
	c.settingsPages = pages
	c.viewer.mu.Lock()
	c.viewer.seen = nil
	c.viewer.mu.Unlock()
}

// SetUserTweetsPageDelay overrides the wait between user-tweets pages.
// Intended for tests; production callers should leave bird's 1s default in
// place, because it is what keeps a 10-page walk from looking like a burst.
func (c *Client) SetUserTweetsPageDelay(d time.Duration) {
	c.userTweetsPageDelay = d
}

// SetBaseURL points the client at an alternate GraphQL root. Intended for tests
// and benchmarks; production callers should leave the default in place.
func (c *Client) SetBaseURL(base string) {
	c.baseURL = strings.TrimRight(base, "/")
}

// randomHex returns n random bytes hex-encoded. X only checks that these
// request-tracing headers are well-formed, not that they mean anything.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not recoverable, and these values are not
		// security-critical, so fall back to a fixed value rather than panicking.
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}

// Conversation fetches every tweet X returns for the conversation containing
// tweetID, in the order X returned them (the focal tweet's ancestors, the tweet
// itself, then its replies). Ancestry is expressed through InReplyToStatusID.
func (c *Client) Conversation(ctx context.Context, tweetID string) ([]Tweet, error) {
	tweetID = strings.TrimSpace(tweetID)
	if tweetID == "" {
		return nil, fmt.Errorf("x api: empty tweet id")
	}

	body, err := c.graphQL(ctx, "TweetDetail", tweetDetailVariables(tweetID), tweetDetailFeatures, tweetDetailFieldToggles)
	if err != nil {
		return nil, err
	}
	return parseConversation(body)
}

// Tweet fetches a single tweet by id.
func (c *Client) Tweet(ctx context.Context, tweetID string) (*Tweet, error) {
	tweets, err := c.Conversation(ctx, tweetID)
	if err != nil {
		return nil, err
	}
	for i := range tweets {
		if tweets[i].ID == tweetID {
			found := &tweets[i]
			c.recoverArticleBody(ctx, found)
			return found, nil
		}
	}
	return nil, &APIError{Message: fmt.Sprintf("tweet %s not found in the conversation response", tweetID)}
}

// recoverArticleBody fills in an X Article whose TweetDetail response carried a
// title and nothing else.
//
// This is bird's second network call in getTweet
// (lib/twitter-client-tweet-detail.js:158-181) and it looks skippable — every
// article sampled live returned content_state from TweetDetail. It is not: when
// X withholds the body there, the only other content is the t.co shortlink, so
// without this `read` prints a headline and nothing else. bird swallows every
// failure here rather than failing the read, and so does this: a stale query id
// or an empty response leaves the tweet exactly as it was.
//
// bird calls this only from getTweet, so `thread`, `replies` and the timelines
// deliberately do not pay for it.
func (c *Client) recoverArticleBody(ctx context.Context, t *Tweet) {
	if t.Article == nil || t.AuthorID == "" {
		return
	}
	// Only when the detail response yielded nothing beyond the headline.
	if strings.TrimSpace(t.Text) != strings.TrimSpace(t.Article.Title) {
		return
	}

	title, plain := c.articlePlainText(ctx, t.AuthorID, t.ID)
	if plain == "" {
		return
	}
	if title != "" {
		t.Text = title + "\n\n" + plain
		return
	}
	t.Text = plain
}

// articlePlainText asks UserArticlesTweets for one article's body. Any error is
// reported as an empty result, matching bird's silent catch.
func (c *Client) articlePlainText(ctx context.Context, userID, tweetID string) (title, plain string) {
	body, err := c.graphQL(ctx, "UserArticlesTweets", userArticlesVariables(userID), articleFeatures, articleFieldToggles)
	if err != nil {
		return "", ""
	}

	var resp struct {
		Data struct {
			User struct {
				Result struct {
					Timeline struct {
						Timeline struct {
							Instructions []instruction `json:"instructions"`
						} `json:"timeline"`
					} `json:"timeline"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", ""
	}

	for _, ins := range resp.Data.User.Result.Timeline.Timeline.Instructions {
		for _, e := range ins.Entries {
			ic := e.Content.ItemContent
			if ic == nil || ic.TweetResults == nil || ic.TweetResults.Result == nil {
				continue
			}
			raw := ic.TweetResults.Result.unwrap()
			if raw.RestID != tweetID {
				continue
			}
			node, res := articleParts(raw)
			if node == nil {
				return "", ""
			}
			return firstText(res.Title, node.Title), firstText(res.PlainText, node.PlainText)
		}
	}
	return "", ""
}

// operationQueryIDs returns the persisted-query hashes to try for an operation.
// BIRDY_<OPERATION>_QUERY_ID overrides the generated list, so a rotation can be
// worked around without a release.
func operationQueryIDs(operation string) []string {
	envKey := "BIRDY_" + strings.ToUpper(camelToSnake(operation)) + "_QUERY_ID"
	generated := queryIDs[operation]

	ids := make([]string, 0, len(generated)+2)
	if override := strings.TrimSpace(os.Getenv(envKey)); override != "" {
		ids = append(ids, override)
	}

	// Generated hashes come first even though discovered ones are newer. Our
	// variables and feature sets are matched to the hashes bird vetted; pairing
	// them with a newer hash is accepted by X but can return a differently
	// shaped response (observed: UserByScreenName moving follower counts, which
	// silently zeroed them). Discovery is a recovery path for rotation, not an
	// upgrade path.
	for _, id := range generated {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if discovered, ok := resolver.lookup(operation); ok && !slices.Contains(ids, discovered) {
		ids = append(ids, discovered)
	}
	return ids
}

// camelToSnake converts an operation name to the env-var form:
// "TweetDetail" -> "TWEET_DETAIL".
func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// get performs an authenticated GraphQL GET and returns the raw body.
func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("x api: building request: %w", err)
	}
	c.setHeaders(req)

	return c.do(req)
}

// do executes a prepared request and normalizes the response.
func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("x api: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("x api: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{
			StatusCode:  resp.StatusCode,
			Message:     truncate(strings.TrimSpace(string(body)), 200),
			RateLimited: resp.StatusCode == http.StatusTooManyRequests,
		}
	}
	return body, nil
}

// post performs an authenticated GraphQL POST and returns the raw body.
func (c *Client) post(ctx context.Context, endpoint string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("x api: building request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("content-type", "application/json")

	return c.do(req)
}

// setHeaders applies the headers X's web client sends. Omitting any of these
// generally produces a 400 rather than a useful error.
func (c *Client) setHeaders(req *http.Request) {
	h := req.Header
	h.Set("accept", "*/*")
	h.Set("accept-language", "en-US,en;q=0.9")
	h.Set("authorization", webBearerToken)
	h.Set("x-csrf-token", c.creds.CT0)
	h.Set("x-twitter-auth-type", "OAuth2Session")
	h.Set("x-twitter-active-user", "yes")
	h.Set("x-twitter-client-language", "en")
	h.Set("x-client-uuid", c.clientUUID)
	h.Set("x-twitter-client-deviceid", c.deviceID)
	// A per-request trace id. X only requires it to be present and well-formed.
	h.Set("x-client-transaction-id", randomHex(16))
	h.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", c.creds.AuthToken, c.creds.CT0))
	h.Set("user-agent", c.userAgent)
	h.Set("origin", "https://x.com")
	h.Set("referer", "https://x.com/")
}

// asAPIError reports whether err is an *APIError, assigning it to target.
func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if apiErr, ok := err.(*APIError); ok {
			*target = apiErr
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// IsRateLimited reports whether err represents an HTTP 429 from X.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if asAPIError(err, &apiErr) {
		return apiErr.RateLimited
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// SetDialContext routes every request through the given dialer.
//
// This is how --vpn works on the native path. bird needed a whole apparatus for
// the same job — a local HTTP CONNECT bridge, an embedded bootstrap.js, and an
// npm-installed undici — because Node's built-in fetch honors neither SOCKS5
// nor the proxy environment variables. Go's transport takes a dialer directly.
func (c *Client) SetDialContext(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	transport := &http.Transport{
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	c.httpClient = &http.Client{Timeout: c.httpClient.Timeout, Transport: transport}
}
