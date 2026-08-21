package tweet

import (
	"context"
	"fmt"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

func defaultCount(n, fallback int) int {
	if n <= 0 {
		return fallback
	}
	return n
}

// Search returns tweets matching query, most recent first.
func (c *Client) Search(ctx context.Context, query string, count int) ([]Tweet, error) {
	count = defaultCount(count, 20)
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Search(ctx, query, count)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Home returns the authenticated account's home timeline.
// latest=true is reverse-chronological; false is ranked For You.
func (c *Client) Home(ctx context.Context, count int, latest bool) ([]Tweet, error) {
	count = defaultCount(count, 20)
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Home(ctx, count, latest)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Bookmarks returns the authenticated account's bookmarked tweets.
func (c *Client) Bookmarks(ctx context.Context, count int) ([]Tweet, error) {
	count = defaultCount(count, 20)
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Bookmarks(ctx, count)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Replies returns replies to a tweet. ref is a status URL or tweet ID.
func (c *Client) Replies(ctx context.Context, ref string) ([]Tweet, error) {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}
	var result []Tweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Replies(ctx, tweetID)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Likes returns tweets liked by handle. Empty handle uses the authenticated viewer.
func (c *Client) Likes(ctx context.Context, handle string, count int) ([]Tweet, error) {
	count = defaultCount(count, 20)
	handle = strings.TrimSpace(strings.TrimPrefix(handle, "@"))
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		var tweets []xapi.Tweet
		var err error
		if handle == "" {
			tweets, err = api.ViewerLikes(ctx, count)
		} else {
			tweets, err = api.Likes(ctx, handle, count)
		}
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// News returns trending news items from X.
func (c *Client) News(ctx context.Context, count int) ([]NewsItem, error) {
	count = defaultCount(count, 20)
	var result []NewsItem
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		items, err := api.News(ctx, count, false, nil)
		if err != nil {
			return err
		}
		result = convertNews(items)
		return nil
	})
	return result, err
}

// NewsItem is one trending-news card.
type NewsItem struct {
	ID          string `json:"id,omitempty"`
	Headline    string `json:"headline"`
	Category    string `json:"category,omitempty"`
	TimeAgo     string `json:"timeAgo,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

func convertNews(in []xapi.NewsItem) []NewsItem {
	if in == nil {
		return nil
	}
	out := make([]NewsItem, 0, len(in))
	for _, n := range in {
		out = append(out, NewsItem{
			ID: n.ID, Headline: n.Headline, Category: n.Category,
			TimeAgo: n.TimeAgo, Description: n.Description, URL: n.URL,
		})
	}
	return out
}

// ListTimeline returns tweets from a list ID.
func (c *Client) ListTimeline(ctx context.Context, listID string, count int) ([]Tweet, error) {
	count = defaultCount(count, 20)
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.ListTimeline(ctx, listID, count)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Mentions returns tweets that mention handle. An empty handle uses the
// authenticated viewer. This is a Latest Search for "@handle", matching the
// CLI: X has no public mentions-timeline GraphQL operation here.
func (c *Client) Mentions(ctx context.Context, handle string, count int) ([]Tweet, error) {
	count = defaultCount(count, 10)
	handle = strings.TrimSpace(strings.TrimPrefix(handle, "@"))
	var result []Tweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		target := handle
		if target == "" {
			viewer, err := api.CurrentUser(ctx)
			if err != nil {
				return fmt.Errorf("could not determine current user (%w); pass a handle", err)
			}
			target = viewer.Username
		}
		normalized, ok := xapi.ValidHandle(target)
		if !ok {
			return fmt.Errorf("invalid handle %q", target)
		}
		tweets, err := api.Search(ctx, "@"+normalized, count)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// Viewer is the authenticated account a Client's cookies belong to.
type Viewer struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Whoami resolves the authenticated account for the selected credentials.
func (c *Client) Whoami(ctx context.Context) (*Viewer, error) {
	var result *Viewer
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		v, err := api.CurrentUser(ctx)
		if err != nil {
			return err
		}
		if v == nil {
			return fmt.Errorf("current user is empty")
		}
		result = &Viewer{ID: v.ID, Username: v.Username, Name: v.Name}
		return nil
	})
	return result, err
}

// List is an X list the authenticated account owns or belongs to.
type List struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     *string    `json:"description,omitempty"`
	MemberCount     *int       `json:"memberCount,omitempty"`
	SubscriberCount *int       `json:"subscriberCount,omitempty"`
	IsPrivate       bool       `json:"isPrivate"`
	CreatedAt       int64      `json:"createdAt,omitempty"`
	Owner           *ListOwner `json:"owner,omitempty"`
}

// ListOwner is the account a list belongs to.
type ListOwner struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Lists returns lists the authenticated account owns, or memberships when
// memberOf is true. count defaults to 100, matching the CLI.
func (c *Client) Lists(ctx context.Context, memberOf bool, count int) ([]List, error) {
	count = defaultCount(count, 100)
	var result []List
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		var (
			raw []xapi.List
			err error
		)
		if memberOf {
			raw, err = api.ListMemberships(ctx, count)
		} else {
			raw, err = api.OwnedLists(ctx, count)
		}
		if err != nil {
			return err
		}
		result = convertLists(raw)
		return nil
	})
	return result, err
}

func convertLists(in []xapi.List) []List {
	if in == nil {
		return nil
	}
	out := make([]List, 0, len(in))
	for _, l := range in {
		item := List{
			ID: l.ID, Name: l.Name, Description: l.Description,
			MemberCount: l.MemberCount, SubscriberCount: l.SubscriberCount,
			IsPrivate: l.IsPrivate, CreatedAt: l.CreatedAt,
		}
		if l.Owner != nil {
			item.Owner = &ListOwner{ID: l.Owner.ID, Username: l.Owner.Username, Name: l.Owner.Name}
		}
		out = append(out, item)
	}
	return out
}

// About returns X's "About this account" panel for a handle.
func (c *Client) About(ctx context.Context, handle string) (*AboutProfile, error) {
	var result *AboutProfile
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		p, err := api.AboutAccount(ctx, handle)
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		result = &AboutProfile{
			AccountBasedIn:         p.AccountBasedIn,
			Source:                 p.Source,
			CreatedCountryAccurate: p.CreatedCountryAccurate,
			LocationAccurate:       p.LocationAccurate,
			LearnMoreURL:           p.LearnMoreURL,
		}
		return nil
	})
	return result, err
}

// AboutProfile is X's origin/location panel for an account.
type AboutProfile struct {
	AccountBasedIn         string `json:"accountBasedIn,omitempty"`
	Source                 string `json:"source,omitempty"`
	CreatedCountryAccurate *bool  `json:"createdCountryAccurate,omitempty"`
	LocationAccurate       *bool  `json:"locationAccurate,omitempty"`
	LearnMoreURL           string `json:"learnMoreUrl,omitempty"`
}

// CreateTweet posts text. replyTo is a tweet ID to reply to, or empty.
func (c *Client) CreateTweet(ctx context.Context, text, replyTo string) (string, error) {
	var id string
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		posted, err := api.CreateTweet(ctx, text, replyTo)
		if err != nil {
			return err
		}
		id = posted
		return nil
	})
	return id, err
}

// Follow follows a user by handle or user ID.
func (c *Client) Follow(ctx context.Context, handleOrID string) error {
	return c.friendship(ctx, handleOrID, true)
}

// Unfollow unfollows a user by handle or user ID.
func (c *Client) Unfollow(ctx context.Context, handleOrID string) error {
	return c.friendship(ctx, handleOrID, false)
}

func (c *Client) friendship(ctx context.Context, handleOrID string, follow bool) error {
	handleOrID = strings.TrimSpace(strings.TrimPrefix(handleOrID, "@"))
	if handleOrID == "" {
		return fmt.Errorf("empty user")
	}
	return c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		userID := handleOrID
		if !allDigits(handleOrID) {
			u, err := api.UserByScreenName(ctx, handleOrID)
			if err != nil {
				return err
			}
			userID = u.ID
		}
		var err error
		if follow {
			_, err = api.Follow(ctx, userID)
		} else {
			_, err = api.Unfollow(ctx, userID)
		}
		return err
	})
}

// DeleteBookmark removes a tweet from the authenticated account's bookmarks.
func (c *Client) DeleteBookmark(ctx context.Context, ref string) error {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return err
	}
	return c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		return api.DeleteBookmark(ctx, tweetID)
	})
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
