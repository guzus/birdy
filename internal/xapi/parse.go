package xapi

import (
	"encoding/json"
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
type Media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	PreviewURL string `json:"previewUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// Tweet is a single post.
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
	// QuotedTweet is the tweet this one quotes, when it quotes one.
	QuotedTweet *Tweet `json:"quotedTweet,omitempty"`
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
		ItemContent *itemContent `json:"itemContent"`
		Item        *struct {
			ItemContent *itemContent `json:"itemContent"`
		} `json:"item"`
		Items []struct {
			ItemContent *itemContent `json:"itemContent"`
			Item        *struct {
				ItemContent *itemContent `json:"itemContent"`
			} `json:"item"`
			Content *struct {
				ItemContent *itemContent `json:"itemContent"`
			} `json:"content"`
		} `json:"items"`
	} `json:"content"`
}

type itemContent struct {
	TweetResults *struct {
		Result *tweetResult `json:"result"`
	} `json:"tweet_results"`
}

// tweetResult is one tweet node. X sometimes wraps the real tweet in a
// TweetWithVisibilityResults envelope, in which case the payload lives under
// "tweet" — see unwrap.
type tweetResult struct {
	RestID string       `json:"rest_id"`
	Tweet  *tweetResult `json:"tweet"`

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
	} `json:"legacy"`

	// NoteTweet carries long-form ("article") post text, which is truncated in
	// legacy.full_text.
	NoteTweet *struct {
		NoteTweetResults struct {
			Result *struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
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
	return t, true
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

// extractText prefers long-form note text, which supersedes the truncated
// legacy full_text when a post exceeds the classic character limit.
func extractText(raw *tweetResult) string {
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
func collectFromEntry(e entry) []*tweetResult {
	var out []*tweetResult

	push := func(ic *itemContent) {
		if ic == nil || ic.TweetResults == nil || ic.TweetResults.Result == nil {
			return
		}
		out = append(out, ic.TweetResults.Result)
	}

	push(e.Content.ItemContent)
	if e.Content.Item != nil {
		push(e.Content.Item.ItemContent)
	}
	for _, item := range e.Content.Items {
		push(item.ItemContent)
		if item.Item != nil {
			push(item.Item.ItemContent)
		}
		if item.Content != nil {
			push(item.Content.ItemContent)
		}
	}
	return out
}

// tweetsFromInstructions maps every usable tweet out of a timeline's
// instructions, de-duplicated, preserving X's ordering.
func tweetsFromInstructions(instructions []instruction) []Tweet {
	var tweets []Tweet
	seen := make(map[string]bool)
	for _, ins := range instructions {
		for _, e := range ins.Entries {
			for _, node := range collectFromEntry(e) {
				t, ok := mapTweet(node)
				if !ok || seen[t.ID] {
					continue
				}
				seen[t.ID] = true
				tweets = append(tweets, t)
			}
		}
	}
	return tweets
}

// ancestorChain walks up from targetID through InReplyToStatusID, root-first.
// The target itself is excluded. A missing parent stops the walk and cyclic
// data is cut short rather than looping.
func ancestorChain(thread []Tweet, targetID string) []Tweet {
	byID := make(map[string]Tweet, len(thread))
	for _, t := range thread {
		byID[t.ID] = t
	}

	target, ok := byID[targetID]
	if !ok {
		return nil
	}

	var chain []Tweet
	seen := map[string]bool{targetID: true}
	for parentID := target.InReplyToStatusID; parentID != ""; {
		if seen[parentID] {
			break
		}
		seen[parentID] = true

		parent, found := byID[parentID]
		if !found {
			break
		}
		chain = append(chain, parent)
		parentID = parent.InReplyToStatusID
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
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

	return tweetsFromInstructions(instructions), nil
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
