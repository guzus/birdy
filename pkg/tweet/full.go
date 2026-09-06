package tweet

import (
	"context"

	"github.com/guzus/birdy/internal/xapi"
)

// TweetMetrics are the engagement and language fields X reports that the
// frozen Tweet type never carried.
type TweetMetrics struct {
	QuoteCount    int
	ViewCount     int
	BookmarkCount int
	Lang          string
}

// FullTweet is the enriched view of a post: every Tweet field, in Tweet's
// order and with Tweet's JSON tags, followed by the permalink, an RFC 3339
// timestamp, the extra metrics, and the relation flags. It marshals to exactly
// what `birdy ... --json-full` prints.
//
// It is a separate type rather than fields appended to Tweet because Tweet's
// layout is frozen (see the unkeyed literal in types_test.go): even an
// unexported field there would be a source break for a v1 caller. FullTweet
// is additive — nothing existing changes shape.
type FullTweet struct {
	ID                string     `json:"id"`
	Text              string     `json:"text"`
	CreatedAt         string     `json:"createdAt,omitempty"`
	ReplyCount        int        `json:"replyCount"`
	RetweetCount      int        `json:"retweetCount"`
	LikeCount         int        `json:"likeCount"`
	ConversationID    string     `json:"conversationId,omitempty"`
	InReplyToStatusID string     `json:"inReplyToStatusId,omitempty"`
	Author            Author     `json:"author"`
	AuthorID          string     `json:"authorId,omitempty"`
	QuotedTweet       *FullTweet `json:"quotedTweet,omitempty"`
	Media             []Media    `json:"media,omitempty"`
	Article           *Article   `json:"article,omitempty"`
	RepostedTweet     *FullTweet `json:"repostedTweet,omitempty"`

	// URL is the canonical permalink (https://x.com/<handle>/status/<id>).
	URL string `json:"url"`
	// CreatedAtISO is CreatedAt as RFC 3339 UTC; empty when CreatedAt is
	// absent or unparsable.
	CreatedAtISO string `json:"createdAtIso,omitempty"`
	// ViewCount, QuoteCount and BookmarkCount are 0 both when X reported zero
	// and when X omitted the value; the payload does not distinguish them.
	ViewCount     int    `json:"viewCount"`
	QuoteCount    int    `json:"quoteCount"`
	BookmarkCount int    `json:"bookmarkCount"`
	Lang          string `json:"lang,omitempty"`
	IsRepost      bool   `json:"isRepost"`
	IsReply       bool   `json:"isReply"`
	IsQuote       bool   `json:"isQuote"`
}

// Metrics returns the extra counts as one value.
func (t FullTweet) Metrics() TweetMetrics {
	return TweetMetrics{
		QuoteCount:    t.QuoteCount,
		ViewCount:     t.ViewCount,
		BookmarkCount: t.BookmarkCount,
		Lang:          t.Lang,
	}
}

// Tweet projects the enriched view back onto the frozen Tweet shape.
func (t FullTweet) Tweet() Tweet {
	out := Tweet{
		ID:                t.ID,
		Text:              t.Text,
		CreatedAt:         t.CreatedAt,
		ReplyCount:        t.ReplyCount,
		RetweetCount:      t.RetweetCount,
		LikeCount:         t.LikeCount,
		ConversationID:    t.ConversationID,
		InReplyToStatusID: t.InReplyToStatusID,
		Author:            t.Author,
		AuthorID:          t.AuthorID,
		Media:             t.Media,
		Article:           t.Article,
	}
	if t.QuotedTweet != nil {
		quoted := t.QuotedTweet.Tweet()
		out.QuotedTweet = &quoted
	}
	return out
}

// FullTimelinePage is TimelinePage with enriched entries.
type FullTimelinePage struct {
	Tweets     []FullTweet `json:"tweets"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// ReadFull fetches one tweet in the enriched shape. Like ReadPost it keeps the
// structured repost target. ref may be a status URL or a bare tweet ID.
func (c *Client) ReadFull(ctx context.Context, ref string) (*FullTweet, error) {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}
	var result *FullTweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		post, err := api.MonitoringTweet(ctx, tweetID)
		if err != nil {
			return err
		}
		converted := convertFullTweet(post.Full())
		result = &converted
		return nil
	})
	return result, err
}

// SearchFull is Search with enriched entries.
func (c *Client) SearchFull(ctx context.Context, query string, count int) ([]FullTweet, error) {
	count = defaultCount(count, 20)
	var result []FullTweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Search(ctx, query, count)
		if err != nil {
			return err
		}
		result = convertFullTweets(tweets)
		return nil
	})
	return result, err
}

// UserTimelineFull is UserTimeline with enriched entries. Paging, limits and
// the author check are identical.
func (c *Client) UserTimelineFull(ctx context.Context, handle string, opts UserTimelineOptions) (FullTimelinePage, error) {
	tweets, cursor, err := c.userTimelineRaw(ctx, handle, opts)
	if err != nil {
		return FullTimelinePage{}, err
	}
	return FullTimelinePage{Tweets: convertFullTweets(tweets), NextCursor: cursor}, nil
}

// --- Conversion boundary -----------------------------------------------------

// convertFullTweet copies the parser's enriched view into the public shape.
// TestFullTweetMatchesParserView pins the two layouts together.
func convertFullTweet(t xapi.FullTweet) FullTweet {
	out := FullTweet{
		ID:                t.ID,
		Text:              t.Text,
		CreatedAt:         t.CreatedAt,
		ReplyCount:        t.ReplyCount,
		RetweetCount:      t.RetweetCount,
		LikeCount:         t.LikeCount,
		ConversationID:    t.ConversationID,
		InReplyToStatusID: t.InReplyToStatusID,
		Author:            Author{Username: t.Author.Username, Name: t.Author.Name},
		AuthorID:          t.AuthorID,
		Media:             convertMedia(t.Media),
		Article:           convertArticle(t.Article),
		URL:               t.URL,
		CreatedAtISO:      t.CreatedAtISO,
		ViewCount:         t.ViewCount,
		QuoteCount:        t.QuoteCount,
		BookmarkCount:     t.BookmarkCount,
		Lang:              t.Lang,
		IsRepost:          t.IsRepost,
		IsReply:           t.IsReply,
		IsQuote:           t.IsQuote,
	}
	if t.QuotedTweet != nil {
		quoted := convertFullTweet(*t.QuotedTweet)
		out.QuotedTweet = &quoted
	}
	if t.RepostedTweet != nil {
		reposted := convertFullTweet(*t.RepostedTweet)
		out.RepostedTweet = &reposted
	}
	return out
}

func convertFullTweets(in []xapi.Tweet) []FullTweet {
	if in == nil {
		return nil
	}
	out := make([]FullTweet, 0, len(in))
	for _, t := range in {
		out = append(out, convertFullTweet(t.Full()))
	}
	return out
}
