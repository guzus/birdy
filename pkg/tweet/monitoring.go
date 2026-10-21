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
	tweets, cursor, err := c.userTimelineRaw(ctx, handle, opts, nil)
	if err != nil {
		return TimelinePage{}, err
	}
	return TimelinePage{Tweets: convertTimelineTweets(tweets), NextCursor: cursor}, nil
}

// SkippedPost is a post a skipping read left out of Tweets, instead of failing
// the whole read, because X served it in a form the reading account cannot
// read in full.
type SkippedPost struct {
	ID     string     `json:"id"`
	Reason SkipReason `json:"reason"`
}

// SkipReason says why a skipping read left a post out. Treat a reason you do
// not recognize like SkipUnreadable.
type SkipReason string

const (
	// SkipPreviewOnly means X served only a preview of the post
	// (TweetPreviewDisplay, "Subscribe to unlock"), as it does for a
	// subscriber-only post the reading account has not unlocked. The teaser is
	// not the post, so it is never returned as one.
	SkipPreviewOnly SkipReason = "preview-only"
	// SkipQuotesPreviewOnly means the post is readable but quotes a post X
	// serves only as a preview, directly or further along the quote's own
	// relations. Returning it without its quote would misrepresent it.
	SkipQuotesPreviewOnly SkipReason = "quotes-preview-only"
	// SkipRepostsPreviewOnly is SkipQuotesPreviewOnly for a repost.
	SkipRepostsPreviewOnly SkipReason = "reposts-preview-only"
	// SkipQuotesUnavailable means the post is readable but quotes a post X
	// will not show: deleted, or by a suspended or protected author. That is
	// ordinary X, not a payload change.
	SkipQuotesUnavailable SkipReason = "quotes-unavailable"
	// SkipRepostsUnavailable is SkipQuotesUnavailable for a repost.
	SkipRepostsUnavailable SkipReason = "reposts-unavailable"
	// SkipUnreadable means the post had no readable author or text in any
	// shape birdy recognizes. Unlike the preview reasons this can mean X
	// changed its payload, so it deserves a look rather than just a log line.
	SkipUnreadable SkipReason = "unreadable"
)

// SkippingTimelinePage is a TimelinePage from a read that skips unreadable
// posts. TimelinePage is embedded, so Tweets and NextCursor stay directly
// accessible and the JSON stays flat, without changing TimelinePage's frozen
// layout.
type SkippingTimelinePage struct {
	TimelinePage
	// Skipped lists, once each and in timeline order, the posts left out of
	// Tweets. NextCursor already resumes after them, so a caller that persists
	// cursors will not be shown them again. A read that skipped every post is
	// still a success; Skipped is what tells it apart from an empty timeline.
	Skipped []SkippedPost `json:"skipped,omitempty"`
}

// UserTimelineSkippingUnreadable is UserTimeline for a caller that would
// rather lose a post than the whole read. A post X serves in a form the
// reading account cannot read in full — most often a subscriber-only post,
// which X replaces with a preview, or a post quoting or reposting one or a
// post X will not show — is left out of Tweets and listed in Skipped instead
// of failing the call. Under UserTimeline one such post pinned to a profile
// fails every read of that profile.
//
// Everything else matches UserTimeline: Limit counts readable tweets only,
// the page budget is the same, NextCursor resumes after skipped and returned
// entries alike, and any other malformed quote or repost relation still
// fails the call. The choice is per call, so strict and skipping reads can
// share one Client concurrently without seeing each other's skips.
func (c *Client) UserTimelineSkippingUnreadable(ctx context.Context, handle string, opts UserTimelineOptions) (SkippingTimelinePage, error) {
	var skipped []xapi.SkippedPost
	tweets, cursor, err := c.userTimelineRaw(ctx, handle, opts, &skipped)
	if err != nil {
		return SkippingTimelinePage{}, err
	}
	return SkippingTimelinePage{
		TimelinePage: TimelinePage{Tweets: convertTimelineTweets(tweets), NextCursor: cursor},
		Skipped:      convertSkippedPosts(skipped),
	}, nil
}

// userTimelineRaw is the fetch behind UserTimeline, UserTimelineFull and their
// skipping variants: the same limits, paging, and author check, before any
// public shape is built. A non-nil skipped opts this call into skipping
// unreadable posts and receives them; nil keeps the strict read.
func (c *Client) userTimelineRaw(ctx context.Context, handle string, opts UserTimelineOptions, skipped *[]xapi.SkippedPost) ([]xapi.Tweet, string, error) {
	limit, err := timelineLimit(opts)
	if err != nil {
		return nil, "", err
	}
	normalized, ok := xapi.ValidHandle(handle)
	if !ok {
		return nil, "", fmt.Errorf("invalid timeline author handle %q", handle)
	}

	var (
		tweets []xapi.Tweet
		cursor string
	)
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		var (
			page    []xapi.Tweet
			next    string
			dropped []xapi.SkippedPost
			err     error
		)
		if skipped != nil {
			page, next, dropped, err = api.UserTweetsPageAlignedSkipping(ctx, handle, limit, strings.TrimSpace(opts.Cursor), opts.MaxPages)
		} else {
			page, next, err = api.UserTweetsPageAlignedFrom(ctx, handle, limit, strings.TrimSpace(opts.Cursor), opts.MaxPages)
		}
		if err != nil {
			return err
		}
		for _, post := range page {
			if !strings.EqualFold(post.Author.Username, normalized) {
				return fmt.Errorf("user timeline returned tweet %s by @%s while tracking @%s", post.ID, post.Author.Username, normalized)
			}
		}
		tweets, cursor = page, next
		if skipped != nil {
			*skipped = dropped
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return tweets, cursor, nil
}

func convertSkippedPosts(in []xapi.SkippedPost) []SkippedPost {
	if len(in) == 0 {
		return nil
	}
	out := make([]SkippedPost, 0, len(in))
	for _, s := range in {
		out = append(out, SkippedPost{ID: s.ID, Reason: SkipReason(s.Reason)})
	}
	return out
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

type userListPageFn func(ctx context.Context, api *xapi.Client, userID string, pageSize int, cursor string) (*xapi.UserListPage, error)

// Following walks accounts followed by userID using one selected Birdy
// account for the entire cursor chain. Complete is true only when the walk
// started at the newest page and X reported the end. A page cap returns a
// usable incomplete result rather than pretending omitted users were absent.
//
// userID may be a numeric rest id or a @handle; a handle is resolved once
// against the same account that walks the list.
func (c *Client) Following(ctx context.Context, userID string, opts FollowingOptions) (FollowingSnapshot, error) {
	return c.walkUserList(ctx, userID, opts, "following", func(ctx context.Context, api *xapi.Client, id string, pageSize int, cursor string) (*xapi.UserListPage, error) {
		return api.Following(ctx, id, pageSize, cursor)
	})
}

// Followers walks accounts that follow userID with the same completeness
// contract as Following.
func (c *Client) Followers(ctx context.Context, userID string, opts FollowingOptions) (FollowingSnapshot, error) {
	return c.walkUserList(ctx, userID, opts, "followers", func(ctx context.Context, api *xapi.Client, id string, pageSize int, cursor string) (*xapi.UserListPage, error) {
		return api.Followers(ctx, id, pageSize, cursor)
	})
}

func (c *Client) walkUserList(ctx context.Context, userID string, opts FollowingOptions, kind string, fetch userListPageFn) (FollowingSnapshot, error) {
	userID = strings.TrimSpace(strings.TrimPrefix(userID, "@"))
	if userID == "" {
		return FollowingSnapshot{}, fmt.Errorf("%s user id is required", kind)
	}
	if opts.MaxPages < 0 {
		return FollowingSnapshot{}, fmt.Errorf("%s MaxPages must not be negative", kind)
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	startCursor := strings.TrimSpace(opts.Cursor)

	var result FollowingSnapshot
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		id := userID
		if !allDigits(id) {
			u, err := api.UserByScreenName(ctx, id)
			if err != nil {
				return err
			}
			id = u.ID
		}
		cursor := startCursor
		seenUsers := make(map[string]struct{})
		seenCursors := make(map[string]struct{})
		for {
			page, err := fetch(ctx, api, id, pageSize, cursor)
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
				return fmt.Errorf("%s cursor did not advance after page %d", kind, result.Pages)
			}
			if _, repeated := seenCursors[next]; repeated {
				return fmt.Errorf("%s cursor repeated after page %d", kind, result.Pages)
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
