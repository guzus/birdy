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
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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

	// clientUUID and deviceID are stable per client, matching how a browser
	// session presents itself across requests.
	clientUUID string
	deviceID   string
}

// NewClient builds a client for the given credentials.
func NewClient(creds Credentials) (*Client, error) {
	if strings.TrimSpace(creds.AuthToken) == "" || strings.TrimSpace(creds.CT0) == "" {
		return nil, fmt.Errorf("x api: auth_token and ct0 are both required")
	}

	return &Client{
		creds:      creds,
		httpClient: &http.Client{Timeout: defaultTimeout},
		userAgent:  defaultUserAgent,
		clientUUID: randomHex(16),
		deviceID:   randomHex(16),
	}, nil
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
// tweetID, in the order X returned them (the focal tweet's thread first, then
// replies). Ancestry is expressed through each tweet's InReplyToStatusID.
func (c *Client) Conversation(ctx context.Context, tweetID string) ([]Tweet, error) {
	tweetID = strings.TrimSpace(tweetID)
	if tweetID == "" {
		return nil, fmt.Errorf("x api: empty tweet id")
	}

	query, err := buildTweetDetailQuery(tweetID)
	if err != nil {
		return nil, err
	}

	// Query IDs rotate; a stale one 404s, so try each until one is accepted.
	var lastErr error
	for _, queryID := range tweetDetailQueryIDList() {
		endpoint := fmt.Sprintf("%s/%s/TweetDetail?%s", graphQLBase, queryID, query)

		body, err := c.get(ctx, endpoint)
		if err != nil {
			var apiErr *APIError
			if ok := asAPIError(err, &apiErr); ok && apiErr.StatusCode == http.StatusNotFound {
				lastErr = err
				continue // stale query id, try the next
			}
			return nil, err
		}

		return parseConversation(body)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("x api: every known TweetDetail query id was rejected "+
			"(X likely rotated them; set BIRDY_TWEET_DETAIL_QUERY_ID): %w", lastErr)
	}
	return nil, fmt.Errorf("x api: no TweetDetail query id configured")
}

// Tweet fetches a single tweet by id.
func (c *Client) Tweet(ctx context.Context, tweetID string) (*Tweet, error) {
	tweets, err := c.Conversation(ctx, tweetID)
	if err != nil {
		return nil, err
	}
	for i := range tweets {
		if tweets[i].ID == tweetID {
			return &tweets[i], nil
		}
	}
	return nil, &APIError{Message: fmt.Sprintf("tweet %s not found in the conversation response", tweetID)}
}

// tweetDetailQueryIDList returns the query IDs to try, honoring an override.
func tweetDetailQueryIDList() []string {
	if override := strings.TrimSpace(os.Getenv("BIRDY_TWEET_DETAIL_QUERY_ID")); override != "" {
		return append([]string{override}, tweetDetailQueryIDs...)
	}
	return tweetDetailQueryIDs
}

// buildTweetDetailQuery encodes the variables/features/fieldToggles triplet.
func buildTweetDetailQuery(tweetID string) (string, error) {
	variables, err := json.Marshal(tweetDetailVariables(tweetID))
	if err != nil {
		return "", fmt.Errorf("x api: encoding variables: %w", err)
	}
	features, err := json.Marshal(tweetDetailFeatures)
	if err != nil {
		return "", fmt.Errorf("x api: encoding features: %w", err)
	}
	fieldToggles, err := json.Marshal(tweetDetailFieldToggles)
	if err != nil {
		return "", fmt.Errorf("x api: encoding fieldToggles: %w", err)
	}

	params := url.Values{}
	params.Set("variables", string(variables))
	params.Set("features", string(features))
	params.Set("fieldToggles", string(fieldToggles))
	return params.Encode(), nil
}

// get performs an authenticated GraphQL GET and returns the raw body.
func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("x api: building request: %w", err)
	}
	c.setHeaders(req)

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
