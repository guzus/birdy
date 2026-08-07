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
type Media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	PreviewURL string `json:"previewUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
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
// so a zero means "not reported here", not necessarily "zero".
type Tweet struct {
	ID                string  `json:"id"`
	Text              string  `json:"text"`
	CreatedAt         string  `json:"createdAt,omitempty"`
	ConversationID    string  `json:"conversationId,omitempty"`
	InReplyToStatusID string  `json:"inReplyToStatusId,omitempty"`
	Author            Author  `json:"author"`
	AuthorID          string  `json:"authorId,omitempty"`
	Media             []Media `json:"media,omitempty"`
	ReplyCount        int     `json:"replyCount,omitempty"`
	RetweetCount      int     `json:"retweetCount,omitempty"`
	LikeCount         int     `json:"likeCount,omitempty"`
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
	}
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
