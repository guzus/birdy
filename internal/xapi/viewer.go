package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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
// This is the one lookup that is not GraphQL, and as of 2026-08-07 every one of
// these endpoints answers 404 ("Sorry, that page does not exist", code 34) for
// a cookie session. They are kept because they cost one request each, are the
// documented way to ask, and would be the right answer if X restores them —
// but the settings-page fallback below is what actually resolves the viewer
// today. Do not remove it on the theory that reading the REST response more
// carefully makes it redundant; the responses are 404s, not misread payloads.
var defaultViewerEndpoints = []string{
	"https://x.com/i/api/account/settings.json",
	"https://api.twitter.com/1.1/account/settings.json",
	"https://x.com/i/api/account/verify_credentials.json?skip_status=true&include_entities=false",
	"https://api.twitter.com/1.1/account/verify_credentials.json?skip_status=true&include_entities=false",
}

// settingsPages carry the viewer's identity inline in the HTML. This is the
// working path, not a last resort.
var defaultSettingsPages = []string{
	"https://x.com/settings/account",
	"https://twitter.com/settings/account",
}

// The identity fields X embeds in the settings page markup.
var (
	settingsScreenName = regexp.MustCompile(`"screen_name":"([^"]+)"`)
	settingsUserID     = regexp.MustCompile(`"user_id"\s*:\s*"(\d+)"`)
	settingsName       = regexp.MustCompile(`"name":"([^"\\]*(?:\\.[^"\\]*)*)"`)
)

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

	// Every JSON endpoint failed. Scrape the authenticated settings page, which
	// embeds the same identity in its markup and is currently the only path
	// that works.
	viewer, err := c.viewerFromSettingsPage(ctx)
	if err == nil {
		c.viewer.seen = viewer
		return viewer, nil
	}
	if lastErr == nil {
		lastErr = err
	}
	return nil, fmt.Errorf("x api: determining current user: %w", lastErr)
}

// viewerFromSettingsPage reads the viewer out of the settings page HTML.
//
// This request is deliberately not sent with setHeaders: the page is served to
// a browser session, and the API bearer and CSRF headers get it rejected. Only
// the cookie and a browser user-agent go out.
func (c *Client) viewerFromSettingsPage(ctx context.Context) (*Viewer, error) {
	var lastErr error
	for _, page := range c.settingsPages {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
		if err != nil {
			return nil, fmt.Errorf("x api: building request: %w", err)
		}
		req.Header.Set("cookie", fmt.Sprintf("auth_token=%s; ct0=%s", c.creds.AuthToken, c.creds.CT0))
		req.Header.Set("user-agent", c.userAgent)

		body, err := c.do(req)
		if err != nil {
			lastErr = err
			if IsRateLimited(err) {
				return nil, err
			}
			continue
		}

		viewer, ok := parseViewerHTML(string(body))
		if !ok {
			lastErr = &APIError{Message: "could not parse settings page for user info"}
			continue
		}
		return viewer, nil
	}
	if lastErr == nil {
		lastErr = &APIError{Message: "no settings pages configured"}
	}
	return nil, lastErr
}

func parseViewerHTML(html string) (*Viewer, bool) {
	username := firstSubmatch(settingsScreenName, html)
	id := firstSubmatch(settingsUserID, html)
	if username == "" || id == "" {
		return nil, false
	}

	name := firstSubmatch(settingsName, html)
	// The markup is JSON-escaped, so an embedded quote arrives as \".
	name = strings.ReplaceAll(name, `\"`, `"`)
	if name == "" {
		name = username
	}
	return &Viewer{ID: id, Username: username, Name: name}, true
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
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
