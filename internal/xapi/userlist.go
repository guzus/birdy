package xapi

import (
	"context"
	"encoding/json"
)

// ListedUser is a user as it appears in a followers/following listing.
//
// This is deliberately not xapi.User. A listing carries different presence
// semantics — X omits counts for suspended and some protected accounts, and
// "0 followers" is a different answer from "not reported" — so the optional
// fields are pointers. The JSON tags match bird's listing payload exactly,
// which is a different shape from the profile lookup's.
type ListedUser struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	FollowersCount  *int   `json:"followersCount,omitempty"`
	FollowingCount  *int   `json:"followingCount,omitempty"`
	IsBlueVerified  *bool  `json:"isBlueVerified,omitempty"`
	ProfileImageURL string `json:"profileImageUrl,omitempty"`
	CreatedAt       string `json:"createdAt,omitempty"`
}

// UserListPage is one page of a user-list timeline (followers, following).
//
// NextCursor is empty at the end of the list. X signals that by omitting the
// bottom cursor entry, not by repeating the last one.
type UserListPage struct {
	Users      []ListedUser
	NextCursor string
}

// Following returns accounts the given user id follows.
func (c *Client) Following(ctx context.Context, userID string, count int, cursor string) (*UserListPage, error) {
	return c.userList(ctx, "Following", userID, count, cursor)
}

// Followers returns accounts following the given user id.
func (c *Client) Followers(ctx context.Context, userID string, count int, cursor string) (*UserListPage, error) {
	return c.userList(ctx, "Followers", userID, count, cursor)
}

func (c *Client) userList(ctx context.Context, operation, userID string, count int, cursor string) (*UserListPage, error) {
	variables := map[string]any{
		"userId":                 userID,
		"count":                  clampCount(count),
		"includePromotedContent": false,
	}
	// X rejects an explicit null cursor, so the key is only present when set.
	if cursor != "" {
		variables["cursor"] = cursor
	}

	body, err := c.graphQL(ctx, operation, variables, followingFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseUserList(body)
}

// userTimelineResponse covers the two roots X uses for these operations.
type userTimelineResponse struct {
	Data struct {
		User struct {
			Result struct {
				Timeline struct {
					Timeline *userInstructions `json:"timeline"`
				} `json:"timeline"`
				// Newer payloads drop the doubled "timeline" nesting.
				TimelineDirect *userInstructions `json:"timeline_v2"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type userInstructions struct {
	Instructions []struct {
		Entries []userEntry `json:"entries"`
	} `json:"instructions"`
}

type userEntry struct {
	Content struct {
		CursorType  string `json:"cursorType"`
		Value       string `json:"value"`
		ItemContent struct {
			UserResults struct {
				Result *rawUser `json:"result"`
			} `json:"user_results"`
		} `json:"itemContent"`
	} `json:"content"`
}

// rawUser mirrors the shape X returns for a user entry. Identity moved from
// legacy to core in newer payloads and both still appear, so each field falls
// back across the two.
type rawUser struct {
	TypeName       string `json:"__typename"`
	RestID         string `json:"rest_id"`
	IsBlueVerified bool   `json:"is_blue_verified"`
	// UserWithVisibilityResults wraps the real user one level deeper.
	User   *rawUser `json:"user"`
	Legacy *struct {
		ScreenName           string `json:"screen_name"`
		Name                 string `json:"name"`
		Description          string `json:"description"`
		FollowersCount       int    `json:"followers_count"`
		FriendsCount         int    `json:"friends_count"`
		StatusesCount        int    `json:"statuses_count"`
		Verified             bool   `json:"verified"`
		CreatedAt            string `json:"created_at"`
		ProfileImageURLHTTPS string `json:"profile_image_url_https"`
	} `json:"legacy"`
	Core *struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
		CreatedAt  string `json:"created_at"`
	} `json:"core"`
	Avatar *struct {
		ImageURL string `json:"image_url"`
	} `json:"avatar"`
}

// unwrap resolves the UserWithVisibilityResults wrapper.
func (r *rawUser) unwrap() *rawUser {
	if r != nil && r.TypeName == "UserWithVisibilityResults" && r.User != nil {
		return r.User
	}
	return r
}

func parseUserList(body []byte) (*UserListPage, error) {
	var resp userTimelineResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding user list response: " + err.Error()}
	}

	timeline := resp.Data.User.Result.Timeline.Timeline
	if timeline == nil {
		timeline = resp.Data.User.Result.TimelineDirect
	}
	if timeline == nil {
		if len(resp.Errors) > 0 {
			messages := make([]string, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return nil, &APIError{Message: joinMessages(messages)}
		}
		// An empty list is a valid answer, not a failure.
		return &UserListPage{}, nil
	}

	page := &UserListPage{}
	for _, instruction := range timeline.Instructions {
		for _, entry := range instruction.Entries {
			if entry.Content.CursorType == "Bottom" && entry.Content.Value != "" {
				page.NextCursor = entry.Content.Value
				continue
			}
			user, ok := mapUser(entry.Content.ItemContent.UserResults.Result.unwrap())
			if !ok {
				continue
			}
			page.Users = append(page.Users, user)
		}
	}
	return page, nil
}

func mapUser(raw *rawUser) (ListedUser, bool) {
	if raw == nil || raw.TypeName != "User" || raw.RestID == "" {
		return ListedUser{}, false
	}

	verified := raw.IsBlueVerified
	user := ListedUser{ID: raw.RestID, IsBlueVerified: &verified}

	// The counts come from legacy only. When X sends a core-only payload they
	// are genuinely absent, and the pointers stay nil so the caller can tell.
	if raw.Legacy != nil {
		followers, following := raw.Legacy.FollowersCount, raw.Legacy.FriendsCount
		user.Username = raw.Legacy.ScreenName
		user.Name = raw.Legacy.Name
		user.Description = raw.Legacy.Description
		user.FollowersCount = &followers
		user.FollowingCount = &following
		user.ProfileImageURL = raw.Legacy.ProfileImageURLHTTPS
		user.CreatedAt = raw.Legacy.CreatedAt
	}
	if raw.Core != nil {
		if user.Username == "" {
			user.Username = raw.Core.ScreenName
		}
		if user.Name == "" {
			user.Name = raw.Core.Name
		}
		if user.CreatedAt == "" {
			user.CreatedAt = raw.Core.CreatedAt
		}
	}
	if user.ProfileImageURL == "" && raw.Avatar != nil {
		user.ProfileImageURL = raw.Avatar.ImageURL
	}

	if user.Username == "" {
		return ListedUser{}, false
	}
	if user.Name == "" {
		user.Name = user.Username
	}
	return user, true
}
