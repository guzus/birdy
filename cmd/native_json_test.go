package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// bird's --json is JSON.stringify(value, null, 2) over an object literal, so
// key ORDER is the literal's order and a key is omitted only when its value is
// `undefined` — never when it is 0 or "". Go marshals struct fields in
// declaration order, so both properties are decided by the struct layout in
// internal/xapi. These tests pin the bytes against output captured from bird
// running against live X.

func encodeNative(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeNativeJSON(&buf, v); err != nil {
		t.Fatalf("writeNativeJSON: %v", err)
	}
	return buf.String()
}

// TestNativeTweetJSONMatchesBird pins the full key sequence of a tweet.
// bird's literal (lib/twitter-client-utils.js:395-405) is:
// id, text, createdAt, replyCount, retweetCount, likeCount, conversationId,
// inReplyToStatusId, author, authorId, quotedTweet, media, article.
func TestNativeTweetJSONMatchesBird(t *testing.T) {
	tw := xapi.Tweet{
		ID:                "2085581653037232453",
		Text:              "hello",
		CreatedAt:         "Wed Aug 05 07:59:09 +0000 2026",
		ConversationID:    "2085581653037232453",
		InReplyToStatusID: "2085581653037232400",
		Author:            xapi.Author{Username: "steipete", Name: "Peter"},
		AuthorID:          "9001",
		// Zero reply/like counts are real values in bird's output, not absences.
		ReplyCount:   0,
		RetweetCount: 1,
		LikeCount:    0,
		QuotedTweet: &xapi.Tweet{
			ID:     "1",
			Text:   "quoted",
			Author: xapi.Author{Username: "other", Name: "Other"},
		},
		Media: []xapi.Media{{
			Type:       "photo",
			URL:        "https://pbs.twimg.com/a.jpg",
			Width:      1200,
			Height:     800,
			PreviewURL: "https://pbs.twimg.com/a.jpg:small",
		}},
	}

	const want = `{
  "id": "2085581653037232453",
  "text": "hello",
  "createdAt": "Wed Aug 05 07:59:09 +0000 2026",
  "replyCount": 0,
  "retweetCount": 1,
  "likeCount": 0,
  "conversationId": "2085581653037232453",
  "inReplyToStatusId": "2085581653037232400",
  "author": {
    "username": "steipete",
    "name": "Peter"
  },
  "authorId": "9001",
  "quotedTweet": {
    "id": "1",
    "text": "quoted",
    "replyCount": 0,
    "retweetCount": 0,
    "likeCount": 0,
    "author": {
      "username": "other",
      "name": "Other"
    }
  },
  "media": [
    {
      "type": "photo",
      "url": "https://pbs.twimg.com/a.jpg",
      "width": 1200,
      "height": 800,
      "previewUrl": "https://pbs.twimg.com/a.jpg:small"
    }
  ]
}
`

	if got := encodeNative(t, tw); got != want {
		t.Errorf("tweet JSON mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A video carries every media key. bird assigns them in the order
// type, url, width, height, previewUrl, videoUrl, durationMs
// (lib/twitter-client-utils.js:316-349).
func TestNativeMediaKeyOrder(t *testing.T) {
	m := xapi.Media{
		Type:       "video",
		URL:        "https://pbs.twimg.com/thumb.jpg",
		Width:      2048,
		Height:     1152,
		PreviewURL: "https://pbs.twimg.com/thumb.jpg:small",
		VideoURL:   "https://video.twimg.com/high.mp4",
		DurationMs: 53291,
	}

	const want = `{
  "type": "video",
  "url": "https://pbs.twimg.com/thumb.jpg",
  "width": 2048,
  "height": 1152,
  "previewUrl": "https://pbs.twimg.com/thumb.jpg:small",
  "videoUrl": "https://video.twimg.com/high.mp4",
  "durationMs": 53291
}
`
	if got := encodeNative(t, m); got != want {
		t.Errorf("media JSON mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// bird assigns width/height only when a size block exists and videoUrl only for
// video/animated_gif, so those keys are genuinely absent for a size-less photo.
// Dropping omitempty from Media would over-correct.
func TestNativeMediaPhotoOmitsInapplicableKeys(t *testing.T) {
	m := xapi.Media{Type: "photo", URL: "https://pbs.twimg.com/b.jpg"}

	const want = `{
  "type": "photo",
  "url": "https://pbs.twimg.com/b.jpg"
}
`
	if got := encodeNative(t, m); got != want {
		t.Errorf("photo JSON mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// bird reads a listed user's description from legacy.description, which is a
// present string that is very often "". Live: 12 of 15 sampled users. Go's
// omitempty conflated that with "absent" and dropped the key.
func TestListedUserDescriptionPresence(t *testing.T) {
	empty := ""
	present := "bio"

	if got := encodeNative(t, xapi.ListedUser{ID: "1", Username: "a", Name: "A", Description: &empty}); got != `{
  "id": "1",
  "username": "a",
  "name": "A",
  "description": ""
}
` {
		t.Errorf("empty description should be emitted, got:\n%s", got)
	}

	if got := encodeNative(t, xapi.ListedUser{ID: "1", Username: "a", Name: "A", Description: &present}); got != `{
  "id": "1",
  "username": "a",
  "name": "A",
  "description": "bio"
}
` {
		t.Errorf("description not emitted, got:\n%s", got)
	}

	// No legacy block at all: bird emits `description: undefined`, i.e. no key.
	if got := encodeNative(t, xapi.ListedUser{ID: "1", Username: "a", Name: "A"}); got != `{
  "id": "1",
  "username": "a",
  "name": "A"
}
` {
		t.Errorf("absent description should omit the key, got:\n%s", got)
	}
}

func TestListDescriptionPresence(t *testing.T) {
	empty := ""

	if got := encodeNative(t, xapi.List{ID: "1", Name: "L", Description: &empty}); got != `{
  "id": "1",
  "name": "L",
  "description": "",
  "isPrivate": false
}
` {
		t.Errorf("empty list description should be emitted, got:\n%s", got)
	}

	if got := encodeNative(t, xapi.List{ID: "1", Name: "L"}); got != `{
  "id": "1",
  "name": "L",
  "isPrivate": false
}
` {
		t.Errorf("absent list description should omit the key, got:\n%s", got)
	}
}

// bird's activity prints `nextCursor: page.nextCursor ?? null` per section.
// birdy never assigned them, so all three were permanently null.
func TestActivityNextCursorIsAStringWhenPresent(t *testing.T) {
	var report activityReport
	report.TweetID = "1"
	report.Likes.NextCursor = optionalString("LC")
	report.Reposts.NextCursor = optionalString("")
	report.Quotes.NextCursor = optionalString("QC")
	report.normalize()

	got := encodeNative(t, report)
	for _, want := range []string{`"nextCursor": "LC"`, `"nextCursor": null`, `"nextCursor": "QC"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	// All three sections are always present regardless of --types, as in bird.
	for _, key := range []string{`"likes"`, `"reposts"`, `"quotes"`, `"tweetId"`} {
		if !strings.Contains(got, key) {
			t.Errorf("missing %s in:\n%s", key, got)
		}
	}
}
