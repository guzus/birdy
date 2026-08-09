package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// User is an X account.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Followers   *int   `json:"followers,omitempty"`
	Following   *int   `json:"following,omitempty"`
	Tweets      *int   `json:"tweets,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// URL returns the profile permalink.
func (u User) URL() string {
	return "https://x.com/" + u.Username
}

// NormalizeHandle strips a leading @ and any profile-URL wrapper from a handle.
func NormalizeHandle(handle string) string {
	h := strings.TrimSpace(handle)
	h = strings.TrimPrefix(h, "@")

	// Accept a profile URL as well, e.g. https://x.com/SpaceX
	if idx := strings.Index(h, "x.com/"); idx >= 0 {
		h = h[idx+len("x.com/"):]
	} else if idx := strings.Index(h, "twitter.com/"); idx >= 0 {
		h = h[idx+len("twitter.com/"):]
	}
	if idx := strings.IndexAny(h, "/?#"); idx >= 0 {
		h = h[:idx]
	}
	return strings.TrimPrefix(strings.TrimSpace(h), "@")
}

// handlePattern is what X actually issues: letters, digits and underscore, at
// most 15 characters.
var handlePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)

// ValidHandle normalizes a handle and reports whether the result is one X could
// have issued.
//
// NormalizeHandle only strips wrappers, so it happily returns "not a handle!"
// unchanged. That is fine for a lookup, where X answers "no such user", but not
// for a command that builds a search query out of the value — a malformed
// handle would become a query for arbitrary text and quietly return the wrong
// results instead of an error.
func ValidHandle(handle string) (string, bool) {
	normalized := NormalizeHandle(handle)
	if !handlePattern.MatchString(normalized) {
		return "", false
	}
	return normalized, true
}

// UserByScreenName looks up an account by handle. Timeline operations key off
// the numeric user id, so this is the first hop for anything user-scoped.
func (c *Client) UserByScreenName(ctx context.Context, handle string) (*User, error) {
	name := NormalizeHandle(handle)
	if name == "" {
		return nil, fmt.Errorf("x api: empty username")
	}

	body, err := c.graphQL(ctx,
		"UserByScreenName",
		map[string]any{"screen_name": name, "withSafetyModeUserFields": true},
		userByScreenNameFeatures,
		map[string]bool{"withAuxiliaryUserLabels": false},
	)
	if err != nil {
		return nil, err
	}
	return parseUser(body, name)
}

// userResponse mirrors the parts of UserByScreenName we consume.
type userResponse struct {
	Data struct {
		User struct {
			Result *struct {
				RestID         string `json:"rest_id"`
				IsBlueVerified bool   `json:"is_blue_verified"`
				Legacy         *struct {
					ScreenName     string `json:"screen_name"`
					Name           string `json:"name"`
					Description    string `json:"description"`
					FollowersCount *int   `json:"followers_count"`
					FriendsCount   *int   `json:"friends_count"`
					StatusesCount  *int   `json:"statuses_count"`
					Verified       bool   `json:"verified"`
					CreatedAt      string `json:"created_at"`
				} `json:"legacy"`
				// Newer payloads move identity onto core and stats onto separate objects.
				Core *struct {
					ScreenName string `json:"screen_name"`
					Name       string `json:"name"`
					CreatedAt  string `json:"created_at"`
				} `json:"core"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func parseUser(body []byte, requested string) (*User, error) {
	var resp userResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding user response: " + err.Error()}
	}

	result := resp.Data.User.Result
	if result == nil || result.RestID == "" {
		if len(resp.Errors) > 0 {
			messages := make([]string, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return nil, &APIError{Message: strings.Join(messages, ", ")}
		}
		return nil, &APIError{Message: fmt.Sprintf("user %q not found", requested)}
	}

	user := &User{ID: result.RestID, Verified: result.IsBlueVerified}

	if l := result.Legacy; l != nil {
		user.Username = l.ScreenName
		user.Name = l.Name
		user.Description = l.Description
		user.Followers = l.FollowersCount
		user.Following = l.FriendsCount
		user.Tweets = l.StatusesCount
		user.CreatedAt = l.CreatedAt
		if l.Verified {
			user.Verified = true
		}
	}
	if c := result.Core; c != nil {
		if user.Username == "" {
			user.Username = c.ScreenName
		}
		if user.Name == "" {
			user.Name = c.Name
		}
		if user.CreatedAt == "" {
			user.CreatedAt = c.CreatedAt
		}
	}

	if user.Username == "" {
		user.Username = requested
	}
	if user.Name == "" {
		user.Name = user.Username
	}
	return user, nil
}
