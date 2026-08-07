package tweet

import "github.com/guzus/birdy/internal/xapi"

// The types below are birdy's public wire and API contract. They are declared
// here as real structs rather than aliases into internal/xapi on purpose.
//
// An alias would make every edit to the internal parser's structs an instant,
// silent change to this package's public API — the `internal/` boundary offers
// no protection against that, because an alias is the same type. Declaring
// them separately means a field rename upstream produces a compile error in
// convertTweet (and a failure in TestPublicTypesCoverParserFields) instead of
// a breaking change nobody reviewed.
//
// The cost is the conversion below. That cost is the point: it is the place
// where someone has to decide, deliberately, that a parser change should be
// visible to callers.
//
// JSON tags are part of the contract too — `birdy --json` output and any
// caller marshalling these structs depend on them. Do not change a tag without
// treating it as a breaking change.

// Author identifies who posted a tweet.
type Author struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Media is one attachment on a tweet: a photo, video, or animated GIF.
//
// Field order is part of the contract: `birdy --json` must byte-match bird's
// output, and Go marshals struct fields in declaration order.
type Media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	PreviewURL string `json:"previewUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// Article is the header of an X "Article" (long-form) post.
type Article struct {
	Title       string `json:"title"`
	PreviewText string `json:"previewText,omitempty"`
}

// IsVideo reports whether this attachment has playable video.
func (m Media) IsVideo() bool {
	return m.VideoURL != ""
}

// DownloadURL returns the URL holding the actual asset bytes. For videos this
// is the mp4, never the still thumbnail.
func (m Media) DownloadURL() string {
	if m.VideoURL != "" {
		return m.VideoURL
	}
	return m.URL
}

// Tweet is a single post and its attachments.
//
// Engagement counts are best-effort: X omits them in some timeline responses,
// so a zero means "not reported here", not necessarily "zero". They are always
// emitted in JSON, including when zero, because bird emits them unconditionally
// and consumers diff the bytes.
//
// Field order mirrors bird's object literal and is part of the wire contract.
type Tweet struct {
	ID                string `json:"id"`
	Text              string `json:"text"`
	CreatedAt         string `json:"createdAt,omitempty"`
	ReplyCount        int    `json:"replyCount"`
	RetweetCount      int    `json:"retweetCount"`
	LikeCount         int    `json:"likeCount"`
	ConversationID    string `json:"conversationId,omitempty"`
	InReplyToStatusID string `json:"inReplyToStatusId,omitempty"`
	Author            Author `json:"author"`
	AuthorID          string `json:"authorId,omitempty"`
	// QuotedTweet is the tweet this one quotes, when it quotes one. Roughly a
	// third of a live timeline carries one, so a caller that ignores it is
	// reading the quoting text without the thing it refers to.
	QuotedTweet *Tweet  `json:"quotedTweet,omitempty"`
	Media       []Media `json:"media,omitempty"`
	// Article is set when the post is an X Article. Text then carries the
	// rendered article body rather than the tweet's shortlink.
	Article *Article `json:"article,omitempty"`
}

// IsReply reports whether this tweet sits below the root of a conversation.
func (t Tweet) IsReply() bool {
	if t.InReplyToStatusID != "" {
		return true
	}
	return t.ConversationID != "" && t.ConversationID != t.ID
}

// URL returns a canonical permalink for the tweet.
func (t Tweet) URL() string {
	if t.Author.Username != "" {
		return "https://x.com/" + t.Author.Username + "/status/" + t.ID
	}
	return "https://x.com/i/status/" + t.ID
}

// --- Conversion boundary -----------------------------------------------------

// convertTweet copies a parsed tweet into the public shape. Adding a field to
// the public type without extending this function leaves it silently zero, so
// TestPublicTypesCoverParserFields cross-checks both directions.
func convertTweet(t xapi.Tweet) Tweet {
	return Tweet{
		ID:                t.ID,
		Text:              t.Text,
		CreatedAt:         t.CreatedAt,
		ConversationID:    t.ConversationID,
		InReplyToStatusID: t.InReplyToStatusID,
		Author: Author{
			Username: t.Author.Username,
			Name:     t.Author.Name,
		},
		AuthorID:     t.AuthorID,
		Media:        convertMedia(t.Media),
		ReplyCount:   t.ReplyCount,
		RetweetCount: t.RetweetCount,
		LikeCount:    t.LikeCount,
		QuotedTweet:  convertQuoted(t.QuotedTweet),
		Article:      convertArticle(t.Article),
	}
}

// convertArticle copies the article header, so mutating the parser's value
// afterwards cannot reach through into the public one.
func convertArticle(a *xapi.Article) *Article {
	if a == nil {
		return nil
	}
	return &Article{Title: a.Title, PreviewText: a.PreviewText}
}

// convertQuoted walks the quote chain. It is depth-bounded upstream, in the
// parser, so this recursion terminates on whatever the parser produced.
func convertQuoted(q *xapi.Tweet) *Tweet {
	if q == nil {
		return nil
	}
	converted := convertTweet(*q)
	return &converted
}

// convertTweets converts a slice, preserving order and distinguishing a nil
// input from an empty one so `[]` and `null` keep marshalling as they did.
func convertTweets(in []xapi.Tweet) []Tweet {
	if in == nil {
		return nil
	}
	out := make([]Tweet, 0, len(in))
	for _, t := range in {
		out = append(out, convertTweet(t))
	}
	return out
}

func convertMedia(in []xapi.Media) []Media {
	if in == nil {
		return nil
	}
	out := make([]Media, 0, len(in))
	for _, m := range in {
		out = append(out, Media{
			Type:       m.Type,
			URL:        m.URL,
			PreviewURL: m.PreviewURL,
			VideoURL:   m.VideoURL,
			Width:      m.Width,
			Height:     m.Height,
			DurationMs: m.DurationMs,
		})
	}
	return out
}
