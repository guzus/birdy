package xapi

import (
	"testing"
)

// conversationFixture mirrors the shape of a real TweetDetail response, trimmed
// to the fields we consume. It exercises, in order:
//   - a root tweet with a video (multiple mp4 bitrates plus an HLS variant)
//   - a reply wrapped in a TweetWithVisibilityResults envelope, with the newer
//     nested-core author shape and a long-form note_tweet body
//   - a module entry carrying a further reply with two photos
//   - a tombstone entry with no tweet payload
const conversationFixture = `{
  "data": { "threaded_conversation_with_injections_v2": { "instructions": [
    { "entries": [
      { "content": { "itemContent": { "tweet_results": { "result": {
        "rest_id": "100",
        "core": { "user_results": { "result": {
          "rest_id": "9001",
          "legacy": { "screen_name": "SpaceX", "name": "SpaceX" }
        } } },
        "legacy": {
          "full_text": "Falcon 9 has landed https://t.co/abc",
          "created_at": "Wed Aug 05 07:59:09 +0000 2026",
          "conversation_id_str": "100",
          "reply_count": 429, "retweet_count": 899, "favorite_count": 8658,
          "extended_entities": { "media": [ {
            "type": "video",
            "media_url_https": "https://pbs.twimg.com/amplify_video_thumb/1/img/x.jpg",
            "sizes": { "large": {"w": 2048, "h": 1152}, "small": {"w": 680, "h": 383} },
            "video_info": {
              "duration_millis": 18349,
              "variants": [
                {"content_type": "application/x-mpegURL", "url": "https://video.twimg.com/hls.m3u8"},
                {"content_type": "video/mp4", "url": "https://video.twimg.com/low.mp4", "bitrate": 632000},
                {"content_type": "video/mp4", "url": "https://video.twimg.com/high.mp4", "bitrate": 10368000},
                {"content_type": "video/mp4", "url": "https://video.twimg.com/mid.mp4", "bitrate": 2176000}
              ]
            }
          } ] }
        }
      } } } } },

      { "content": { "itemContent": { "tweet_results": { "result": {
        "tweet": {
          "rest_id": "101",
          "core": { "user_results": { "result": {
            "rest_id": "9002",
            "core": { "screen_name": "someone", "name": "Some One" }
          } } },
          "legacy": {
            "full_text": "truncated version that should lose",
            "conversation_id_str": "100",
            "in_reply_to_status_id_str": "100"
          },
          "note_tweet": { "note_tweet_results": { "result": {
            "text": "the full long-form body that should win"
          } } }
        }
      } } } } },

      { "content": { "items": [
        { "item": { "itemContent": { "tweet_results": { "result": {
          "rest_id": "102",
          "core": { "user_results": { "result": {
            "rest_id": "9003",
            "legacy": { "screen_name": "third", "name": "Third" }
          } } },
          "legacy": {
            "full_text": "two photos",
            "conversation_id_str": "100",
            "in_reply_to_status_id_str": "101",
            "extended_entities": { "media": [
              { "type": "photo", "media_url_https": "https://pbs.twimg.com/a.jpg",
                "sizes": { "large": {"w": 1200, "h": 800}, "small": {"w": 680, "h": 453} } },
              { "type": "photo", "media_url_https": "https://pbs.twimg.com/b.jpg" }
            ] }
          }
        } } } } } ] } },

      { "content": { "itemContent": {} } }
    ] }
  ] } }
}`

func TestParseConversation(t *testing.T) {
	tweets, err := parseConversation([]byte(conversationFixture))
	if err != nil {
		t.Fatalf("parseConversation returned error: %v", err)
	}
	if len(tweets) != 3 {
		t.Fatalf("len(tweets) = %d, want 3 (the tombstone must be skipped)", len(tweets))
	}

	t.Run("root tweet", func(t *testing.T) {
		root := tweets[0]
		if root.ID != "100" || root.Author.Username != "SpaceX" {
			t.Fatalf("root = %+v, want id 100 by @SpaceX", root)
		}
		if root.LikeCount != 8658 || root.ReplyCount != 429 {
			t.Errorf("engagement counts = %d likes / %d replies, want 8658 / 429", root.LikeCount, root.ReplyCount)
		}
		if root.IsReply() {
			t.Error("IsReply() = true for the conversation root")
		}
		if len(root.Media) != 1 {
			t.Fatalf("len(Media) = %d, want 1", len(root.Media))
		}

		m := root.Media[0]
		// Highest-bitrate mp4 must win, and the HLS playlist must never be chosen.
		if m.VideoURL != "https://video.twimg.com/high.mp4" {
			t.Errorf("VideoURL = %q, want the highest-bitrate mp4", m.VideoURL)
		}
		if m.DownloadURL() != m.VideoURL {
			t.Errorf("DownloadURL() = %q, want the mp4 rather than the thumbnail", m.DownloadURL())
		}
		if m.DurationMs != 18349 {
			t.Errorf("DurationMs = %d, want 18349", m.DurationMs)
		}
		if m.Width != 2048 || m.Height != 1152 {
			t.Errorf("dimensions = %dx%d, want 2048x1152 (from sizes.large)", m.Width, m.Height)
		}
		if m.PreviewURL != "https://pbs.twimg.com/amplify_video_thumb/1/img/x.jpg:small" {
			t.Errorf("PreviewURL = %q, want the :small suffix", m.PreviewURL)
		}
	})

	t.Run("visibility-wrapped reply with long-form text", func(t *testing.T) {
		reply := tweets[1]
		if reply.ID != "101" {
			t.Fatalf("tweets[1].ID = %q, want 101 (the visibility envelope must be unwrapped)", reply.ID)
		}
		// The newer payload puts the handle under core, not legacy.
		if reply.Author.Username != "someone" || reply.Author.Name != "Some One" {
			t.Errorf("author = %+v, want the nested-core handle", reply.Author)
		}
		// note_tweet supersedes the truncated legacy text.
		if reply.Text != "the full long-form body that should win" {
			t.Errorf("Text = %q, want the note_tweet body", reply.Text)
		}
		if !reply.IsReply() {
			t.Error("IsReply() = false for a reply")
		}
	})

	t.Run("module entry with photos", func(t *testing.T) {
		third := tweets[2]
		if third.ID != "102" {
			t.Fatalf("tweets[2].ID = %q, want 102 (module items must be collected)", third.ID)
		}
		if len(third.Media) != 2 {
			t.Fatalf("len(Media) = %d, want 2", len(third.Media))
		}
		if third.Media[0].IsVideo() {
			t.Error("photo reported IsVideo() = true")
		}
		if third.Media[0].DownloadURL() != "https://pbs.twimg.com/a.jpg" {
			t.Errorf("DownloadURL() = %q, want the photo url", third.Media[0].DownloadURL())
		}
		// No sizes block at all must not panic or invent a preview.
		if third.Media[1].PreviewURL != "" {
			t.Errorf("PreviewURL = %q, want empty when sizes are absent", third.Media[1].PreviewURL)
		}
	})
}

func TestParseConversationDeduplicates(t *testing.T) {
	// The same tweet can appear in more than one instruction.
	const dup = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[
		{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
			"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
			"legacy":{"full_text":"hello"}}}}}}]},
		{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
			"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
			"legacy":{"full_text":"hello"}}}}}}]}
	]}}}`

	tweets, err := parseConversation([]byte(dup))
	if err != nil {
		t.Fatalf("parseConversation returned error: %v", err)
	}
	if len(tweets) != 1 {
		t.Errorf("len(tweets) = %d, want 1 after de-duplication", len(tweets))
	}
}

// X returns partial errors alongside good data (a failed translation field, for
// example). Those must not fail the whole read.
func TestParseConversationToleratesPartialErrors(t *testing.T) {
	const partial = `{"errors":[{"message":"is_translatable failed"}],
		"data":{"threaded_conversation_with_injections_v2":{"instructions":[
		{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
			"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
			"legacy":{"full_text":"still fine"}}}}}}]}]}}}`

	tweets, err := parseConversation([]byte(partial))
	if err != nil {
		t.Fatalf("parseConversation returned error despite usable data: %v", err)
	}
	if len(tweets) != 1 {
		t.Errorf("len(tweets) = %d, want 1", len(tweets))
	}
}

func TestParseConversationFailsWhenOnlyErrors(t *testing.T) {
	const onlyErrors = `{"errors":[{"message":"Tweet not found"}],"data":{}}`
	if _, err := parseConversation([]byte(onlyErrors)); err == nil {
		t.Error("parseConversation = nil error, want the API error surfaced")
	}
}

func TestParseConversationRejectsGarbage(t *testing.T) {
	if _, err := parseConversation([]byte("not json")); err == nil {
		t.Error("parseConversation(garbage) = nil error, want error")
	}
}

// A tweet with no author or no text is not usable and must be dropped rather
// than surfacing as a blank entry.
func TestMapTweetSkipsUnusableNodes(t *testing.T) {
	cases := map[string]string{
		"no author": `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[
			{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
				"rest_id":"1","legacy":{"full_text":"orphan"}}}}}}]}]}}}`,
		"no text": `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[
			{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
				"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
				"legacy":{"full_text":"  "}}}}}}]}]}}}`,
		"no rest_id": `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[
			{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{
				"core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
				"legacy":{"full_text":"hi"}}}}}}]}]}}}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			tweets, err := parseConversation([]byte(body))
			if err != nil {
				t.Fatalf("parseConversation returned error: %v", err)
			}
			if len(tweets) != 0 {
				t.Errorf("len(tweets) = %d, want 0", len(tweets))
			}
		})
	}
}

func TestBestMP4VariantFallsBackWithoutBitrate(t *testing.T) {
	item := rawMedia{Type: "video"}
	item.VideoInfo = &struct {
		DurationMillis int64 `json:"duration_millis"`
		Variants       []struct {
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
			Bitrate     *int   `json:"bitrate"`
		} `json:"variants"`
	}{}
	item.VideoInfo.Variants = []struct {
		ContentType string `json:"content_type"`
		URL         string `json:"url"`
		Bitrate     *int   `json:"bitrate"`
	}{
		{ContentType: "application/x-mpegURL", URL: "https://video.twimg.com/hls.m3u8"},
		{ContentType: "video/mp4", URL: "https://video.twimg.com/only.mp4"},
	}

	if got := bestMP4Variant(item); got != "https://video.twimg.com/only.mp4" {
		t.Errorf("bestMP4Variant() = %q, want the sole mp4", got)
	}
}

func TestNewClientRequiresCredentials(t *testing.T) {
	for name, creds := range map[string]Credentials{
		"both empty":      {},
		"missing ct0":     {AuthToken: "tok"},
		"missing token":   {CT0: "csrf"},
		"whitespace only": {AuthToken: "  ", CT0: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(creds); err == nil {
				t.Error("NewClient = nil error, want error")
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	if !IsRateLimited(&APIError{StatusCode: 429, RateLimited: true}) {
		t.Error("IsRateLimited(429) = false, want true")
	}
	if IsRateLimited(&APIError{StatusCode: 500}) {
		t.Error("IsRateLimited(500) = true, want false")
	}
	if IsRateLimited(nil) {
		t.Error("IsRateLimited(nil) = true, want false")
	}
}
