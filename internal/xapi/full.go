package xapi

import "time"

// FullTweet is the `--json-full` view of a Tweet.
//
// It is a separate type, not extra fields on Tweet, because `--json` is a
// byte-for-byte contract with the bird CLI: any exported field added to Tweet
// would leak into that output. FullTweet instead COPIES Tweet's fields — same
// names, tags, and declaration order — and appends the extras after them, so a
// `--json` consumer that ignores unknown keys reads `--json-full` unchanged and
// the shared key prefix is emitted in the same order. TestFullTweetPrefixMirrorsTweet
// pins the mirror; Go marshals in declaration order, so the layout is the
// contract.
//
// Embedding Tweet would not work: encoding/json orders promoted fields by index
// path, so the outer QuotedTweet/RepostedTweet overrides needed for nested
// enrichment would sort after every embedded key and break the prefix.
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

	// Everything below is appended after the `--json` prefix.

	// URL is the canonical permalink, so consumers stop rebuilding
	// https://x.com/<handle>/status/<id> by hand.
	URL string `json:"url"`
	// CreatedAtISO is CreatedAt re-expressed as RFC 3339 in UTC. It is omitted
	// when CreatedAt is absent or does not parse, never guessed.
	CreatedAtISO string `json:"createdAtIso,omitempty"`
	// ViewCount, QuoteCount and BookmarkCount are always emitted; 0 means X
	// omitted the value as much as it means zero, which is the same convention
	// the bird-era counts follow.
	ViewCount     int    `json:"viewCount"`
	QuoteCount    int    `json:"quoteCount"`
	BookmarkCount int    `json:"bookmarkCount"`
	Lang          string `json:"lang,omitempty"`
	IsRepost      bool   `json:"isRepost"`
	IsReply       bool   `json:"isReply"`
	IsQuote       bool   `json:"isQuote"`
}

// Full returns the enriched view of t. Nested quoted and reposted tweets are
// enriched the same way.
func (t Tweet) Full() FullTweet {
	metrics := t.Metrics()
	full := FullTweet{
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

		URL:           t.URL(),
		ViewCount:     metrics.ViewCount,
		QuoteCount:    metrics.QuoteCount,
		BookmarkCount: metrics.BookmarkCount,
		Lang:          metrics.Lang,
		IsRepost:      t.RepostedTweet != nil,
		IsReply:       t.IsReply(),
		IsQuote:       t.QuotedTweet != nil,
	}
	if t.Media != nil {
		full.Media = append([]Media(nil), t.Media...)
	}
	if t.Article != nil {
		article := *t.Article
		full.Article = &article
	}
	if t.QuotedTweet != nil {
		quoted := t.QuotedTweet.Full()
		full.QuotedTweet = &quoted
	}
	if t.RepostedTweet != nil {
		reposted := t.RepostedTweet.Full()
		full.RepostedTweet = &reposted
	}
	if ts, ok := ParseCreatedAt(t.CreatedAt); ok {
		full.CreatedAtISO = ts.UTC().Format(time.RFC3339)
	}
	return full
}

// FullTweets converts a slice, preserving order and nil-ness.
func FullTweets(in []Tweet) []FullTweet {
	if in == nil {
		return nil
	}
	out := make([]FullTweet, 0, len(in))
	for _, t := range in {
		out = append(out, t.Full())
	}
	return out
}

// ParseCreatedAt reads X's legacy created_at format
// ("Sat Sep 05 07:09:06 +0000 2026"). ok is false for an empty or malformed
// value; callers decide whether that is an ordering detail or a reason to
// drop the tweet.
func ParseCreatedAt(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RubyDate, value)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
