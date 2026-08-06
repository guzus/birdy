package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// Viewer is the account a set of credentials belongs to.
type Viewer struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// defaultViewerEndpoints mirrors bird's candidate list, in the same order.
//
// This is the one lookup that is not GraphQL: X still answers the v1.1 account
// endpoints for cookie sessions, and there is no GraphQL operation that reports
// "who am I" without already knowing the user id. settings.json usually omits
// the numeric id, so in practice the verify_credentials entries are what
// resolve it — the earlier URLs are kept because they are cheaper when they do
// carry both fields.
var defaultViewerEndpoints = []string{
	"https://x.com/i/api/account/settings.json",
	"https://api.twitter.com/1.1/account/settings.json",
	"https://x.com/i/api/account/verify_credentials.json?skip_status=true&include_entities=false",
	"https://api.twitter.com/1.1/account/verify_credentials.json?skip_status=true&include_entities=false",
}

// viewerCache memoizes CurrentUser per client. The answer cannot change for a
// fixed pair of cookies, and `likes` would otherwise repeat the lookup.
type viewerCache struct {
	mu   sync.Mutex
	seen *Viewer
}

// viewerPayload covers both response shapes. X reports the same identity at the
// top level (verify_credentials) or nested under "user" (settings), and the id
// arrives as a string in some payloads and a number in others.
//
// IDStr/ID are a deliberate divergence from bird, which reads only user_id,
// user_id_str, user.id_str and user.id. verify_credentials.json reports the id
// as a top-level id_str, so bird's chain misses it and falls through to
// scraping the HTML settings page for a regex match. Reading the field X
// actually sends produces the same printed output without that fallback, so
// birdy does not implement the scrape.
type viewerPayload struct {
	ScreenName string      `json:"screen_name"`
	Name       string      `json:"name"`
	UserID     flexibleID  `json:"user_id"`
	UserIDStr  string      `json:"user_id_str"`
	IDStr      string      `json:"id_str"`
	ID         flexibleID  `json:"id"`
	User       *viewerUser `json:"user"`
}

type viewerUser struct {
	ScreenName string     `json:"screen_name"`
	Name       string     `json:"name"`
	ID         flexibleID `json:"id"`
	IDStr      string     `json:"id_str"`
}

// flexibleID decodes an id that X may send quoted or bare.
type flexibleID string

func (f *flexibleID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexibleID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	// Reject a float that lost precision rather than reporting a wrong id.
	if _, err := strconv.ParseUint(n.String(), 10, 64); err != nil {
		return nil
	}
	*f = flexibleID(n.String())
	return nil
}

// CurrentUser resolves which account the client's cookies belong to.
//
// Each candidate endpoint is tried in turn and the first response carrying both
// a username and a numeric id wins; a partial answer is treated as a miss, the
// way bird does, because a Viewer without an id cannot drive a timeline lookup.
func (c *Client) CurrentUser(ctx context.Context) (*Viewer, error) {
	c.viewer.mu.Lock()
	defer c.viewer.mu.Unlock()
	if c.viewer.seen != nil {
		return c.viewer.seen, nil
	}

	var lastErr error
	for _, endpoint := range c.viewerEndpoints {
		body, err := c.get(ctx, endpoint)
		if err != nil {
			lastErr = err
			// A 429 here is the account's rate limit, not a bad endpoint —
			// trying the rest would burn the remaining candidates for nothing.
			if IsRateLimited(err) {
				return nil, err
			}
			continue
		}

		viewer, ok := parseViewer(body)
		if !ok {
			lastErr = &APIError{Message: "could not determine current user from response"}
			continue
		}
		c.viewer.seen = viewer
		return viewer, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("x api: determining current user: %w", lastErr)
	}
	return nil, &APIError{Message: "determining current user: no endpoints configured"}
}

func parseViewer(body []byte) (*Viewer, bool) {
	var payload viewerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}

	username := payload.ScreenName
	if username == "" && payload.User != nil {
		username = payload.User.ScreenName
	}

	name := payload.Name
	if name == "" && payload.User != nil {
		name = payload.User.Name
	}

	id := firstNonEmpty(string(payload.UserID), payload.UserIDStr, payload.IDStr, string(payload.ID))
	if id == "" && payload.User != nil {
		id = firstNonEmpty(payload.User.IDStr, string(payload.User.ID))
	}

	if username == "" || id == "" {
		return nil, false
	}
	if name == "" {
		name = username
	}
	return &Viewer{ID: id, Username: username, Name: name}, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
