package xapi

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Author identifies who posted a tweet.
type Author struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Media is a single attachment on a tweet. For photos, URL is the image. For
// videos and animated GIFs, URL is the still thumbnail and VideoURL is the
// playable mp4.
//
// Field order is bird's assignment order in extractMedia
// (lib/twitter-client-utils.js:316-349) because `--json` is a byte-for-byte
// contract and Go marshals in declaration order. The omitempty tags are correct
// here and must stay: bird assigns width/height only when X sent a size block,
// previewUrl only for sizes.small, and videoUrl/durationMs only for video and
// animated_gif, so "absent" is the right encoding rather than a zero.
type Media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	PreviewURL string `json:"previewUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// Article is the header of an X "Article" (long-form) post. Title is always set
// when Article is non-nil — bird emits no article object at all without one.
// PreviewText is only served by timeline responses.
type Article struct {
	Title       string `json:"title"`
	PreviewText string `json:"previewText,omitempty"`
}

// Tweet is a single post.
//
// Field order mirrors bird's mapTweetResult literal
// (lib/twitter-client-utils.js:395-405). The engagement counts carry no
// omitempty on purpose: bird reads them from legacy, where they are always
// present numbers, so a genuine 0 is emitted rather than dropped. Go's
// omitempty conflates "zero" with "absent", a distinction bird makes and live
// payloads exercise constantly.
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
	// QuotedTweet is the tweet this one quotes, when it quotes one.
	QuotedTweet *Tweet  `json:"quotedTweet,omitempty"`
	Media       []Media `json:"media,omitempty"`
	// Article is set when the post is an X Article. Text then carries the
	// rendered article body, not legacy.full_text — which for an article is
	// only a t.co shortlink.
	Article *Article `json:"article,omitempty"`
	// RepostedTweet is the original post when this timeline entry is a repost.
	// X carries it as legacy.retweeted_status_result; keeping the relation
	// explicit avoids inferring reposts from localized display text.
	RepostedTweet *Tweet `json:"repostedTweet,omitempty"`
}

// --- Raw response shapes -----------------------------------------------------
//
// These mirror only the parts of X's GraphQL payload we consume. Optional
// fields are pointers so a missing object is distinguishable from a zero value.

type graphQLResponse struct {
	Data struct {
		Conversation *struct {
			Instructions []instruction `json:"instructions"`
		} `json:"threaded_conversation_with_injections_v2"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type instruction struct {
	Entries []entry `json:"entries"`
}

type entry struct {
	Content struct {
		TypeName    string       `json:"__typename"`
		EntryType   string       `json:"entryType"`
		ItemContent *itemContent `json:"itemContent"`
		// A pagination cursor lives in its own entry, alongside the tweets. A
		// page can therefore be full and still be the last one, or empty and
		// still have more.
		CursorType string `json:"cursorType"`
		Value      string `json:"value"`
		Item       *struct {
			ItemContent *itemContent `json:"itemContent"`
		} `json:"item"`
		Items *[]timelineModuleItem `json:"items"`
	} `json:"content"`
}

type itemContent struct {
	TypeName     string `json:"__typename"`
	ItemType     string `json:"itemType"`
	TweetResults *struct {
		Result *tweetResult `json:"result"`
	} `json:"tweet_results"`
}

type timelineModuleItem struct {
	ItemContent *itemContent `json:"itemContent"`
	Item        *struct {
		ItemContent *itemContent `json:"itemContent"`
	} `json:"item"`
	Content *struct {
		ItemContent *itemContent `json:"itemContent"`
	} `json:"content"`
}

// tweetResult is one tweet node. X sometimes wraps the real tweet in a
// TweetWithVisibilityResults envelope, in which case the payload lives under
// "tweet" — see unwrap.
type tweetResult struct {
	TypeName string       `json:"__typename"`
	RestID   string       `json:"rest_id"`
	Tweet    *tweetResult `json:"tweet"`

	QuotedStatusResult *struct {
		Result *tweetResult `json:"result"`
	} `json:"quoted_status_result"`

	Core *struct {
		UserResults struct {
			Result *struct {
				RestID string `json:"rest_id"`
				Legacy *struct {
					ScreenName string `json:"screen_name"`
					Name       string `json:"name"`
				} `json:"legacy"`
				Core *struct {
					ScreenName string `json:"screen_name"`
					Name       string `json:"name"`
				} `json:"core"`
			} `json:"result"`
		} `json:"user_results"`
	} `json:"core"`

	Legacy *struct {
		FullText            string `json:"full_text"`
		CreatedAt           string `json:"created_at"`
		ReplyCount          int    `json:"reply_count"`
		RetweetCount        int    `json:"retweet_count"`
		FavoriteCount       int    `json:"favorite_count"`
		ConversationIDStr   string `json:"conversation_id_str"`
		InReplyToStatusIDSt string `json:"in_reply_to_status_id_str"`
		ExtendedEntities    *struct {
			Media []rawMedia `json:"media"`
		} `json:"extended_entities"`
		Entities *struct {
			Media []rawMedia `json:"media"`
		} `json:"entities"`
		RetweetedStatusResult *struct {
			Result *tweetResult `json:"result"`
		} `json:"retweeted_status_result"`
	} `json:"legacy"`

	// NoteTweet carries long-form post text, which is truncated in
	// legacy.full_text. This is X's "long post" feature and is NOT the same
	// thing as Article below.
	NoteTweet *struct {
		NoteTweetResults struct {
			Result *struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`

	// Article carries an X Article (long-form, rich formatting). Timeline
	// responses give title + preview_text only; TweetDetail additionally gives
	// plain_text and the Draft.js content_state. See article.go.
	Article *articleNode `json:"article"`
}

type rawMedia struct {
	Type          string `json:"type"`
	MediaURLHTTPS string `json:"media_url_https"`
	Sizes         *struct {
		Large  *mediaSize `json:"large"`
		Medium *mediaSize `json:"medium"`
		Small  *mediaSize `json:"small"`
	} `json:"sizes"`
	VideoInfo *struct {
		DurationMillis int64 `json:"duration_millis"`
		Variants       []struct {
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
			Bitrate     *int   `json:"bitrate"`
		} `json:"variants"`
	} `json:"video_info"`
}

type mediaSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// --- Mapping -----------------------------------------------------------------

// unwrap peels the TweetWithVisibilityResults envelope when present.
func (t *tweetResult) unwrap() *tweetResult {
	if t == nil {
		return nil
	}
	if t.Tweet != nil {
		return t.Tweet
	}
	return t
}

// mapTweet converts a raw node into a Tweet. It returns false for nodes that
// are not usable tweets (tombstones, ads, missing author or text).
func mapTweet(raw *tweetResult) (Tweet, bool) {
	raw = raw.unwrap()
	if raw == nil || raw.RestID == "" {
		return Tweet{}, false
	}

	username, name, authorID := mapAuthor(raw)
	if username == "" {
		return Tweet{}, false
	}

	text := extractText(raw)
	if text == "" {
		return Tweet{}, false
	}

	t := Tweet{
		ID:       raw.RestID,
		Text:     text,
		AuthorID: authorID,
		Author:   Author{Username: username, Name: name},
		Media:    extractMedia(raw),
		Article:  extractArticleMetadata(raw),
	}

	if raw.Legacy != nil {
		t.CreatedAt = raw.Legacy.CreatedAt
		t.ReplyCount = raw.Legacy.ReplyCount
		t.RetweetCount = raw.Legacy.RetweetCount
		t.LikeCount = raw.Legacy.FavoriteCount
		t.ConversationID = raw.Legacy.ConversationIDStr
		t.InReplyToStatusID = raw.Legacy.InReplyToStatusIDSt
	}

	// A quote carries the tweet it quotes. Roughly a third of a live timeline
	// has one, and dropping it silently strips the context that makes the
	// quoting tweet mean anything — a consumer scoring "is this a quote" reads
	// constant-false. Depth is bounded because quotes nest.
	if depth := quoteDepth(); depth > 0 {
		t.QuotedTweet = mapQuoted(raw, depth)
	}
	t.RepostedTweet = mapReposted(raw)
	return t, true
}

// mapReposted resolves the one original post carried by a repost entry. X does
// not nest reposts, so one level represents the relation without inventing a
// recursive contract. The original can itself quote another post.
func mapReposted(raw *tweetResult) *Tweet {
	if raw == nil || raw.Legacy == nil || raw.Legacy.RetweetedStatusResult == nil {
		return nil
	}
	inner := raw.Legacy.RetweetedStatusResult.Result.unwrap()
	if inner == nil {
		return nil
	}
	reposted, ok := mapTweetWithoutRepost(inner)
	if !ok {
		return nil
	}
	return &reposted
}

func mapTweetWithoutRepost(raw *tweetResult) (Tweet, bool) {
	if raw == nil || raw.RestID == "" {
		return Tweet{}, false
	}
	username, name, authorID := mapAuthor(raw)
	text := extractText(raw)
	if username == "" || text == "" {
		return Tweet{}, false
	}
	t := Tweet{
		ID: raw.RestID, Text: text, AuthorID: authorID,
		Author: Author{Username: username, Name: name},
		Media:  extractMedia(raw), Article: extractArticleMetadata(raw),
	}
	if raw.Legacy != nil {
		t.CreatedAt = raw.Legacy.CreatedAt
		t.ReplyCount = raw.Legacy.ReplyCount
		t.RetweetCount = raw.Legacy.RetweetCount
		t.LikeCount = raw.Legacy.FavoriteCount
		t.ConversationID = raw.Legacy.ConversationIDStr
		t.InReplyToStatusID = raw.Legacy.InReplyToStatusIDSt
	}
	if depth := quoteDepth(); depth > 0 {
		t.QuotedTweet = mapQuoted(raw, depth)
	}
	return t, true
}

const monitoringRelationDepth = 8

// mapMonitoringTweet preserves relation presence independently of the legacy
// BIRDY_QUOTE_DEPTH display preference. Present-but-malformed relations fail;
// they never degrade into an ordinary post.
func mapMonitoringTweet(raw *tweetResult) (Tweet, error) {
	return mapMonitoringTweetDepth(raw, monitoringRelationDepth)
}

func mapMonitoringTweetDepth(raw *tweetResult, depth int) (Tweet, error) {
	raw = raw.unwrap()
	if raw == nil {
		return Tweet{}, &APIError{Message: "malformed monitoring tweet"}
	}
	post, ok := mapTweetWithoutRepost(raw)
	if !ok {
		return Tweet{}, &APIError{Message: fmt.Sprintf("malformed tweet timeline entry for id %q", raw.RestID)}
	}
	// mapTweetWithoutRepost follows the legacy quote-depth environment. Clear
	// that result and rebuild the monitoring relation deterministically.
	post.QuotedTweet = nil
	if raw.QuotedStatusResult != nil {
		if depth <= 0 || raw.QuotedStatusResult.Result == nil {
			return Tweet{}, &APIError{Message: fmt.Sprintf("tweet %q has malformed quoted_status_result", raw.RestID)}
		}
		quoted, err := mapMonitoringTweetDepth(raw.QuotedStatusResult.Result, depth-1)
		if err != nil {
			return Tweet{}, err
		}
		post.QuotedTweet = &quoted
	}
	if raw.Legacy != nil && raw.Legacy.RetweetedStatusResult != nil {
		if depth <= 0 || raw.Legacy.RetweetedStatusResult.Result == nil {
			return Tweet{}, &APIError{Message: fmt.Sprintf("tweet %q has malformed retweeted_status_result", raw.RestID)}
		}
		reposted, err := mapMonitoringTweetDepth(raw.Legacy.RetweetedStatusResult.Result, depth-1)
		if err != nil {
			return Tweet{}, err
		}
		post.RepostedTweet = &reposted
	}
	return post, nil
}

// mapQuoted resolves the quoted tweet, descending at most depth levels.
func mapQuoted(raw *tweetResult, depth int) *Tweet {
	if depth <= 0 || raw.QuotedStatusResult == nil || raw.QuotedStatusResult.Result == nil {
		return nil
	}
	inner := raw.QuotedStatusResult.Result.unwrap()
	if inner == nil || inner.RestID == "" {
		return nil
	}

	username, name, authorID := mapAuthor(inner)
	if username == "" {
		return nil
	}
	text := extractText(inner)
	if text == "" {
		return nil
	}

	quoted := Tweet{
		ID:       inner.RestID,
		Text:     text,
		AuthorID: authorID,
		Author:   Author{Username: username, Name: name},
		Media:    extractMedia(inner),
		Article:  extractArticleMetadata(inner),
	}
	if inner.Legacy != nil {
		quoted.CreatedAt = inner.Legacy.CreatedAt
		quoted.ReplyCount = inner.Legacy.ReplyCount
		quoted.RetweetCount = inner.Legacy.RetweetCount
		quoted.LikeCount = inner.Legacy.FavoriteCount
		quoted.ConversationID = inner.Legacy.ConversationIDStr
		quoted.InReplyToStatusID = inner.Legacy.InReplyToStatusIDSt
	}
	quoted.QuotedTweet = mapQuoted(inner, depth-1)
	return &quoted
}

// quoteDepth mirrors bird's --quote-depth global: one level by default, and 0
// disables quote resolution entirely.
func quoteDepth() int {
	raw := strings.TrimSpace(os.Getenv("BIRDY_QUOTE_DEPTH"))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 1
	}
	return n
}

// mapAuthor reads the handle from whichever of the two shapes X returns.
func mapAuthor(raw *tweetResult) (username, name, authorID string) {
	if raw.Core == nil || raw.Core.UserResults.Result == nil {
		return "", "", ""
	}
	user := raw.Core.UserResults.Result
	authorID = user.RestID

	if user.Legacy != nil {
		username = user.Legacy.ScreenName
		name = user.Legacy.Name
	}
	// Newer payloads moved these onto a nested "core" object.
	if username == "" && user.Core != nil {
		username = user.Core.ScreenName
	}
	if name == "" && user.Core != nil {
		name = user.Core.Name
	}
	if name == "" {
		name = username
	}
	return username, name, authorID
}

// extractText is bird's extractTweetText precedence: an X Article's rendered
// body first, then long-form note text, then the truncated legacy full_text.
//
// The article branch has to come first and cannot be skipped: for an article
// post legacy.full_text is a t.co shortlink, not a shortened body, so falling
// through to it returns a wrong answer rather than a degraded one.
func extractText(raw *tweetResult) string {
	if text := extractArticleText(raw); text != "" {
		return text
	}
	if raw.NoteTweet != nil && raw.NoteTweet.NoteTweetResults.Result != nil {
		if text := strings.TrimSpace(raw.NoteTweet.NoteTweetResults.Result.Text); text != "" {
			return text
		}
	}
	if raw.Legacy != nil {
		return strings.TrimSpace(raw.Legacy.FullText)
	}
	return ""
}

// extractMedia maps attachments. extended_entities is preferred because only it
// carries video_info; plain entities omits video variants entirely.
func extractMedia(raw *tweetResult) []Media {
	if raw.Legacy == nil {
		return nil
	}

	var rawItems []rawMedia
	switch {
	case raw.Legacy.ExtendedEntities != nil && len(raw.Legacy.ExtendedEntities.Media) > 0:
		rawItems = raw.Legacy.ExtendedEntities.Media
	case raw.Legacy.Entities != nil:
		rawItems = raw.Legacy.Entities.Media
	}
	if len(rawItems) == 0 {
		return nil
	}

	media := make([]Media, 0, len(rawItems))
	for _, item := range rawItems {
		if item.Type == "" || item.MediaURLHTTPS == "" {
			continue
		}

		m := Media{Type: item.Type, URL: item.MediaURLHTTPS}

		if item.Sizes != nil {
			if item.Sizes.Large != nil {
				m.Width, m.Height = item.Sizes.Large.W, item.Sizes.Large.H
			} else if item.Sizes.Medium != nil {
				m.Width, m.Height = item.Sizes.Medium.W, item.Sizes.Medium.H
			}
			if item.Sizes.Small != nil {
				m.PreviewURL = item.MediaURLHTTPS + ":small"
			}
		}

		if item.Type == "video" || item.Type == "animated_gif" {
			m.VideoURL = bestMP4Variant(item)
			if item.VideoInfo != nil {
				m.DurationMs = item.VideoInfo.DurationMillis
			}
		}

		media = append(media, m)
	}
	if len(media) == 0 {
		return nil
	}
	return media
}

// bestMP4Variant picks the highest-bitrate mp4. Variants without a bitrate
// (HLS playlists are filtered out by content type) fall back to the first mp4.
func bestMP4Variant(item rawMedia) string {
	if item.VideoInfo == nil {
		return ""
	}

	type variant struct {
		url     string
		bitrate int
		ranked  bool
	}
	var mp4s []variant
	for _, v := range item.VideoInfo.Variants {
		if v.ContentType != "video/mp4" || v.URL == "" {
			continue
		}
		vr := variant{url: v.URL}
		if v.Bitrate != nil {
			vr.bitrate, vr.ranked = *v.Bitrate, true
		}
		mp4s = append(mp4s, vr)
	}
	if len(mp4s) == 0 {
		return ""
	}

	ranked := make([]variant, 0, len(mp4s))
	for _, v := range mp4s {
		if v.ranked {
			ranked = append(ranked, v)
		}
	}
	if len(ranked) > 0 {
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].bitrate > ranked[j].bitrate })
		return ranked[0].url
	}
	return mp4s[0].url
}

// --- Instruction walking -----------------------------------------------------

// collectFromEntry gathers every tweet node an entry can hold. A conversation
// entry may carry a single tweet or a module of several (thread continuations).
func collectFromEntry(e entry) ([]*tweetResult, error) {
	var out []*tweetResult
	module := e.Content.TypeName == "TimelineTimelineModule" || e.Content.EntryType == "TimelineTimelineModule"
	if module && e.Content.Items == nil {
		return nil, &APIError{Message: "malformed timeline module: missing items collection"}
	}
	if module && e.Content.ItemContent != nil {
		return nil, &APIError{Message: "malformed timeline module: mixed direct itemContent and items"}
	}
	if e.Content.Items != nil && len(*e.Content.Items) == 0 && !module {
		return nil, &APIError{Message: "unrecognized timeline entry: untyped empty items collection"}
	}

	push := func(ic *itemContent) error {
		if ic.TweetResults == nil {
			if isKnownNonTweetItemContent(ic) {
				return nil
			}
			return &APIError{Message: fmt.Sprintf("unrecognized timeline item content type %q: missing tweet_results", firstNonEmpty(ic.ItemType, ic.TypeName))}
		}
		if ic.TweetResults.Result == nil {
			// This is X's explicit deleted/withheld tombstone shape. An
			// untyped tweet_results:{} is not safe: it is indistinguishable
			// from a renamed or moved result field.
			if ic.TypeName == "TimelineTweet" || ic.ItemType == "TimelineTweet" {
				return nil
			}
			return &APIError{Message: fmt.Sprintf("unrecognized timeline item content type %q: missing tweet result", firstNonEmpty(ic.ItemType, ic.TypeName))}
		}
		out = append(out, ic.TweetResults.Result)
		return nil
	}

	if e.Content.ItemContent != nil {
		if err := push(e.Content.ItemContent); err != nil {
			return nil, err
		}
	}
	if e.Content.Item != nil {
		if e.Content.Item.ItemContent == nil {
			return nil, &APIError{Message: "malformed timeline item wrapper: missing itemContent"}
		}
		if err := push(e.Content.Item.ItemContent); err != nil {
			return nil, err
		}
	}
	if e.Content.Items != nil {
		for index, item := range *e.Content.Items {
			var contents []*itemContent
			if item.ItemContent != nil {
				contents = append(contents, item.ItemContent)
			}
			if item.Item != nil && item.Item.ItemContent != nil {
				contents = append(contents, item.Item.ItemContent)
			}
			if item.Content != nil && item.Content.ItemContent != nil {
				contents = append(contents, item.Content.ItemContent)
			}
			if len(contents) == 0 {
				return nil, &APIError{Message: fmt.Sprintf("malformed timeline module item %d: missing itemContent", index)}
			}
			for _, content := range contents {
				if err := push(content); err != nil {
					return nil, err
				}
			}
		}
	}
	if e.Content.ItemContent == nil && e.Content.Item == nil && e.Content.Items == nil && e.Content.CursorType == "" {
		if !isKnownNonTweetEntryType(e.Content.TypeName) && !isKnownNonTweetEntryType(e.Content.EntryType) {
			return nil, &APIError{Message: "unrecognized timeline entry: no item content"}
		}
	}
	return out, nil
}

func isKnownNonTweetItemContent(item *itemContent) bool {
	switch firstNonEmpty(item.ItemType, item.TypeName) {
	case "TimelineMessagePrompt", "TimelineLabel", "TimelineSpelling", "TimelineShowAlert":
		return true
	default:
		return false
	}
}

func isKnownNonTweetEntryType(typeName string) bool {
	switch typeName {
	case "TimelineTimelineCursor", "TimelineMessagePrompt", "TimelineShowAlert":
		return true
	default:
		return false
	}
}

// tweetsFromInstructions maps every usable tweet out of a timeline's
// instructions, de-duplicated, preserving X's ordering.
func tweetsFromInstructions(instructions []instruction) ([]Tweet, error) {
	var tweets []Tweet
	seen := make(map[string]bool)
	for _, ins := range instructions {
		for _, e := range ins.Entries {
			nodes, err := collectFromEntry(e)
			if err != nil {
				return nil, err
			}
			for _, node := range nodes {
				t, ok := mapTweet(node)
				if !ok {
					if isKnownNonTweetResult(node) {
						continue
					}
					return nil, &APIError{Message: fmt.Sprintf("malformed tweet timeline entry for id %q", node.unwrap().RestID)}
				}
				if seen[t.ID] {
					continue
				}
				seen[t.ID] = true
				tweets = append(tweets, t)
			}
		}
	}
	return tweets, nil
}

func isKnownNonTweetResult(raw *tweetResult) bool {
	raw = raw.unwrap()
	if raw == nil {
		return true
	}
	switch raw.TypeName {
	case "TweetTombstone", "TweetUnavailable", "TimelineTweetTombstone", "TimelineTweetUnavailable":
		return true
	default:
		return false
	}
}

// bottomCursorFromInstructions mirrors bird's extractCursorFromInstructions:
// the first Bottom cursor with a non-empty value, scanning ONLY
// instruction.entries.
//
// That narrowness is deliberate, not an oversight to improve on. X moves the
// Bottom cursor into TimelineReplaceEntry.entry (singular) on later search
// pages, and bird never looks there — which is precisely why `bird search -n 50`
// stops at 40. A more thorough extractor would page further than bird and
// return a different result set.
func bottomCursorFromInstructions(instructions []instruction) string {
	for _, ins := range instructions {
		for _, e := range ins.Entries {
			if e.Content.CursorType == "Bottom" && e.Content.Value != "" {
				return e.Content.Value
			}
		}
	}
	return ""
}

// parseConversation maps every tweet in a TweetDetail response.
func parseConversation(body []byte) ([]Tweet, error) {
	var resp graphQLResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding response: " + err.Error()}
	}

	var instructions []instruction
	if resp.Data.Conversation != nil {
		instructions = resp.Data.Conversation.Instructions
	}

	// X often returns partial errors (a failed translation field, say) alongside
	// perfectly good tweet data. Only treat errors as fatal when nothing usable
	// came back.
	if len(instructions) == 0 && len(resp.Errors) > 0 {
		messages := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return nil, &APIError{Message: strings.Join(messages, ", ")}
	}

	return tweetsFromInstructions(instructions)
}

// --- Type helpers ------------------------------------------------------------

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
