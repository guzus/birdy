package tweet

import (
	"context"
	"fmt"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

const maxUserTimelineTweets = 200

// UserTimelineOptions controls a bounded user-timeline read.
type UserTimelineOptions struct {
	// Limit is the parseable-tweet target. It defaults to 20 and cannot exceed
	// 200. The full terminal page is returned, so the result may be larger.
	Limit int
	// Cursor resumes after a cursor returned by an earlier call. Empty starts
	// at the newest page.
	Cursor string
	// MaxPages optionally stops before Limit is reached. Zero uses the number
	// of pages implied by Limit. A stopped result carries NextCursor.
	MaxPages int
}

// TimelinePage is a bounded slice of a user's timeline.
type TimelinePage struct {
	Tweets     []TimelineTweet `json:"tweets"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// TimelineTweet adds monitoring-only relations without changing Tweet's
// frozen layout. Tweet is embedded so ID, Text, Author, and helpers remain
// directly accessible and JSON stays flat.
type TimelineTweet struct {
	Tweet
	RepostedTweet *Tweet `json:"repostedTweet,omitempty"`
}

// ReadPost fetches one tweet with the monitoring relation shape. Unlike Read,
// it preserves the structured repost target in addition to Tweet's reply and
// quote fields. Account selection still obeys the Client's configured
// MonitoringOptions pool and rotation strategy.
func (c *Client) ReadPost(ctx context.Context, ref string) (*TimelineTweet, error) {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}

	var result *TimelineTweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		post, err := api.MonitoringTweet(ctx, tweetID)
		if err != nil {
			return err
		}
		converted := convertTimelineTweets([]xapi.Tweet{*post})[0]
		result = &converted
		return nil
	})
	return result, err
}

// UserTimeline returns X's Posts profile timeline. It does not promise replies;
// poll UserReplies separately when replies are part of the monitored activity.
// The terminal response page is never truncated, so Tweets may exceed Limit
// and NextCursor always resumes after every returned entry.
func (c *Client) UserTimeline(ctx context.Context, handle string, opts UserTimelineOptions) (TimelinePage, error) {
	limit, err := timelineLimit(opts)
	if err != nil {
		return TimelinePage{}, err
	}
	normalized, ok := xapi.ValidHandle(handle)
	if !ok {
		return TimelinePage{}, fmt.Errorf("invalid timeline author handle %q", handle)
	}

	var result TimelinePage
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		var (
			tweets []xapi.Tweet
			cursor string
			err    error
		)
		tweets, cursor, err = api.UserTweetsPageAlignedFrom(ctx, handle, limit, strings.TrimSpace(opts.Cursor), opts.MaxPages)
		if err != nil {
			return err
		}
		for _, post := range tweets {
			if !strings.EqualFold(post.Author.Username, normalized) {
				return fmt.Errorf("user timeline returned tweet %s by @%s while tracking @%s", post.ID, post.Author.Username, normalized)
			}
		}
		result = TimelinePage{Tweets: convertTimelineTweets(tweets), NextCursor: cursor}
		return nil
	})
	return result, err
}

func timelineLimit(opts UserTimelineOptions) (int, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > maxUserTimelineTweets {
		return 0, fmt.Errorf("user timeline limit %d exceeds maximum %d", limit, maxUserTimelineTweets)
	}
	if opts.MaxPages < 0 {
		return 0, fmt.Errorf("user timeline MaxPages must not be negative")
	}
	return limit, nil
}

// UserReplies returns recent indexed replies authored by handle from X's
// reverse-chronological Latest Search. It has an independent cursor from
// UserTimeline; consumers merge the two streams by tweet ID and CreatedAt.
func (c *Client) UserReplies(ctx context.Context, handle string, opts UserTimelineOptions) (TimelinePage, error) {
	limit, err := timelineLimit(opts)
	if err != nil {
		return TimelinePage{}, err
	}
	normalized, ok := xapi.ValidHandle(handle)
	if !ok {
		return TimelinePage{}, fmt.Errorf("invalid reply author handle %q", handle)
	}
	var result TimelinePage
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, cursor, err := api.SearchPageAlignedFrom(ctx, "from:"+normalized+" filter:replies", limit, strings.TrimSpace(opts.Cursor), opts.MaxPages)
		if err != nil {
			return err
		}
		for _, post := range tweets {
			if !strings.EqualFold(post.Author.Username, normalized) {
				return fmt.Errorf("reply search returned tweet %s by @%s while tracking @%s", post.ID, post.Author.Username, normalized)
			}
			if post.InReplyToStatusID == "" && (post.ConversationID == "" || post.ConversationID == post.ID) {
				return fmt.Errorf("reply search returned non-reply tweet %s", post.ID)
			}
		}
		result = TimelinePage{Tweets: convertTimelineTweets(tweets), NextCursor: cursor}
		return nil
	})
	return result, err
}

func convertTimelineTweets(in []xapi.Tweet) []TimelineTweet {
	if in == nil {
		return nil
	}
	out := make([]TimelineTweet, 0, len(in))
	for _, post := range in {
		converted := TimelineTweet{Tweet: convertTweet(post)}
		if post.RepostedTweet != nil {
			reposted := convertTweet(*post.RepostedTweet)
			converted.RepostedTweet = &reposted
		}
		out = append(out, converted)
	}
	return out
}

// UserProfile is the stable profile data returned by X's handle lookup.
type UserProfile struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Followers   *int   `json:"followers,omitempty"`
	Following   *int   `json:"following,omitempty"`
	Tweets      *int   `json:"tweets,omitempty"`
	Verified    bool   `json:"verified"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// UserProfile looks up typed public profile identity and counts. Nil count
// pointers mean X omitted the value; a pointer to zero means reported zero.
func (c *Client) UserProfile(ctx context.Context, handle string) (UserProfile, error) {
	var result UserProfile
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		user, err := api.UserByScreenName(ctx, handle)
		if err != nil {
			return err
		}
		result = UserProfile{
			ID: user.ID, Username: user.Username, Name: user.Name,
			Description: user.Description, Followers: user.Followers,
			Following: user.Following, Tweets: user.Tweets,
			Verified: user.Verified, CreatedAt: user.CreatedAt,
		}
		return nil
	})
	return result, err
}

// FollowingOptions controls a following-graph walk.
type FollowingOptions struct {
	// PageSize defaults to 100 and is clamped by X when necessary.
	PageSize int
	// MaxPages bounds requests. Zero walks until X reports the end. When the
	// cap is reached, Complete is false and NextCursor can resume the walk.
	MaxPages int
	// Cursor resumes a prior incomplete walk. A resumed suffix can never claim
	// Complete=true because it does not contain the pages before this cursor.
	Cursor string
}

// FollowingUser is an account in a following-graph snapshot. Description and
// count pointers distinguish reported empty/zero values from data X omitted.
type FollowingUser struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Name            string  `json:"name"`
	Description     *string `json:"description,omitempty"`
	FollowersCount  *int    `json:"followersCount,omitempty"`
	FollowingCount  *int    `json:"followingCount,omitempty"`
	IsBlueVerified  *bool   `json:"isBlueVerified,omitempty"`
	ProfileImageURL string  `json:"profileImageUrl,omitempty"`
	CreatedAt       string  `json:"createdAt,omitempty"`
	// Unavailable marks an account X would not render — suspended, deactivated,
	// or hidden from the reading account. Only ID is populated. The follow edge
	// is real, so a consumer diffing snapshots must treat it as still followed
	// and must not overwrite identity it already holds with these blanks.
	Unavailable bool `json:"unavailable,omitempty"`
}

// FollowingSnapshot is a deterministic, API-order following-graph walk.
type FollowingSnapshot struct {
	Users      []FollowingUser `json:"users"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Complete   bool            `json:"complete"`
	Pages      int             `json:"pages"`
}

// Following walks accounts followed by userID using one selected Birdy
// account for the entire cursor chain. Complete is true only when the walk
// started at the newest page and X reported the end. A page cap returns a
// usable incomplete result rather than pretending omitted users were absent.
func (c *Client) Following(ctx context.Context, userID string, opts FollowingOptions) (FollowingSnapshot, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return FollowingSnapshot{}, fmt.Errorf("following user id is required")
	}
	if opts.MaxPages < 0 {
		return FollowingSnapshot{}, fmt.Errorf("following MaxPages must not be negative")
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	startCursor := strings.TrimSpace(opts.Cursor)

	var result FollowingSnapshot
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		cursor := startCursor
		seenUsers := make(map[string]struct{})
		seenCursors := make(map[string]struct{})
		for {
			page, err := api.Following(ctx, userID, pageSize, cursor)
			if err != nil {
				return err
			}
			result.Pages++
			for _, user := range page.Users {
				if _, duplicate := seenUsers[user.ID]; duplicate {
					continue
				}
				seenUsers[user.ID] = struct{}{}
				result.Users = append(result.Users, convertFollowingUser(user))
			}

			next := strings.TrimSpace(page.NextCursor)
			if next == "" {
				result.NextCursor = ""
				result.Complete = startCursor == ""
				return nil
			}
			if next == cursor {
				return fmt.Errorf("following cursor did not advance after page %d", result.Pages)
			}
			if _, repeated := seenCursors[next]; repeated {
				return fmt.Errorf("following cursor repeated after page %d", result.Pages)
			}
			seenCursors[next] = struct{}{}
			result.NextCursor = next
			if opts.MaxPages > 0 && result.Pages >= opts.MaxPages {
				return nil
			}
			cursor = next
		}
	})
	if err != nil {
		return FollowingSnapshot{}, err
	}
	return result, nil
}

func convertFollowingUser(user xapi.ListedUser) FollowingUser {
	return FollowingUser{
		ID: user.ID, Username: user.Username, Name: user.Name,
		Description: user.Description, FollowersCount: user.FollowersCount,
		FollowingCount: user.FollowingCount, IsBlueVerified: user.IsBlueVerified,
		ProfileImageURL: user.ProfileImageURL, CreatedAt: user.CreatedAt,
		Unavailable: user.Unavailable,
	}
}
