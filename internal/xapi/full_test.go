package xapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// tweetJSONContract is every exported field of Tweet with its tag, in
// declaration order. It is the `--json` byte contract with bird, spelled out:
// if this test fails, `--json` output changed and something downstream that
// diffs those bytes will break. FullTweet exists precisely so the extras never
// have to be added here.
var tweetJSONContract = []struct{ name, tag string }{
	{"ID", `json:"id"`},
	{"Text", `json:"text"`},
	{"CreatedAt", `json:"createdAt,omitempty"`},
	{"ReplyCount", `json:"replyCount"`},
	{"RetweetCount", `json:"retweetCount"`},
	{"LikeCount", `json:"likeCount"`},
	{"ConversationID", `json:"conversationId,omitempty"`},
	{"InReplyToStatusID", `json:"inReplyToStatusId,omitempty"`},
	{"Author", `json:"author"`},
	{"AuthorID", `json:"authorId,omitempty"`},
	{"QuotedTweet", `json:"quotedTweet,omitempty"`},
	{"Media", `json:"media,omitempty"`},
	{"Article", `json:"article,omitempty"`},
	{"RepostedTweet", `json:"repostedTweet,omitempty"`},
}

func exportedFields(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f)
		}
	}
	return out
}

func TestTweetJSONContractIsUnchanged(t *testing.T) {
	fields := exportedFields(reflect.TypeOf(Tweet{}))
	if len(fields) != len(tweetJSONContract) {
		t.Fatalf("Tweet has %d exported fields, contract has %d — an exported field on Tweet changes --json; put it on FullTweet",
			len(fields), len(tweetJSONContract))
	}
	for i, want := range tweetJSONContract {
		if fields[i].Name != want.name || string(fields[i].Tag) != want.tag {
			t.Errorf("Tweet field %d = %s %q, want %s %q", i, fields[i].Name, fields[i].Tag, want.name, want.tag)
		}
	}
}

// TestFullTweetPrefixMirrorsTweet pins that FullTweet starts with exactly
// Tweet's exported fields — same names, tags, order, and types (with *Tweet
// swapped for *FullTweet) — and only then appends its extras.
func TestFullTweetPrefixMirrorsTweet(t *testing.T) {
	base := exportedFields(reflect.TypeOf(Tweet{}))
	full := exportedFields(reflect.TypeOf(FullTweet{}))
	if len(full) <= len(base) {
		t.Fatalf("FullTweet has %d fields, Tweet %d; FullTweet must append extras", len(full), len(base))
	}
	for i, want := range base {
		got := full[i]
		if got.Name != want.Name || got.Tag != want.Tag {
			t.Errorf("FullTweet field %d = %s %q, want %s %q", i, got.Name, got.Tag, want.Name, want.Tag)
		}
		wantType := strings.ReplaceAll(want.Type.String(), "xapi.Tweet", "xapi.FullTweet")
		if got.Type.String() != wantType {
			t.Errorf("FullTweet.%s type = %s, want %s", got.Name, got.Type, wantType)
		}
	}
	var extras []string
	for _, f := range full[len(base):] {
		extras = append(extras, f.Tag.Get("json"))
	}
	want := []string{
		"url", "createdAtIso,omitempty", "viewCount", "quoteCount",
		"bookmarkCount", "lang,omitempty", "isRepost", "isReply", "isQuote",
	}
	if !reflect.DeepEqual(extras, want) {
		t.Errorf("appended keys = %v, want %v", extras, want)
	}
}

func TestFullTweetFromFixture(t *testing.T) {
	tweets, err := parseConversation([]byte(conversationFixture))
	if err != nil {
		t.Fatal(err)
	}
	root := tweets[0].Full()

	if root.URL != "https://x.com/SpaceX/status/100" {
		t.Errorf("URL = %q", root.URL)
	}
	if root.CreatedAtISO != "2026-08-05T07:59:09Z" {
		t.Errorf("CreatedAtISO = %q", root.CreatedAtISO)
	}
	if root.ViewCount != 1_200_000 || root.QuoteCount != 80 || root.BookmarkCount != 12 || root.Lang != "en" {
		t.Errorf("metrics not propagated: %+v", root)
	}
	if root.IsReply || root.IsQuote || root.IsRepost {
		t.Errorf("root flags = reply %v quote %v repost %v, want all false", root.IsReply, root.IsQuote, root.IsRepost)
	}
	if len(root.Media) != 1 || root.Media[0].VideoURL == "" {
		t.Errorf("media not carried: %+v", root.Media)
	}

	reply := tweets[1].Full()
	if !reply.IsReply {
		t.Error("reply.IsReply = false")
	}
	// The reply fixture carries no created_at, so the ISO field must be absent
	// rather than a zero time.
	if reply.CreatedAtISO != "" {
		t.Errorf("CreatedAtISO for missing createdAt = %q, want empty", reply.CreatedAtISO)
	}
}

func TestFullTweetJSONAppendsAfterPrefix(t *testing.T) {
	tw := Tweet{
		ID:        "2",
		Text:      "rt",
		CreatedAt: "Sat Sep 05 07:09:06 +0000 2026",
		Author:    Author{Username: "a", Name: "A"},
		QuotedTweet: &Tweet{
			ID:            "1",
			Text:          "q",
			Author:        Author{Username: "b", Name: "B"},
			viewCount:     7,
			lang:          "ja",
			quoteCount:    1,
			bookmarkCount: 2,
		},
		RepostedTweet: &Tweet{ID: "0", Text: "orig", Author: Author{Username: "c", Name: "C"}},
		viewCount:     42,
	}

	const want = `{
  "id": "2",
  "text": "rt",
  "createdAt": "Sat Sep 05 07:09:06 +0000 2026",
  "replyCount": 0,
  "retweetCount": 0,
  "likeCount": 0,
  "author": {
    "username": "a",
    "name": "A"
  },
  "quotedTweet": {
    "id": "1",
    "text": "q",
    "replyCount": 0,
    "retweetCount": 0,
    "likeCount": 0,
    "author": {
      "username": "b",
      "name": "B"
    },
    "url": "https://x.com/b/status/1",
    "viewCount": 7,
    "quoteCount": 1,
    "bookmarkCount": 2,
    "lang": "ja",
    "isRepost": false,
    "isReply": false,
    "isQuote": false
  },
  "repostedTweet": {
    "id": "0",
    "text": "orig",
    "replyCount": 0,
    "retweetCount": 0,
    "likeCount": 0,
    "author": {
      "username": "c",
      "name": "C"
    },
    "url": "https://x.com/c/status/0",
    "viewCount": 0,
    "quoteCount": 0,
    "bookmarkCount": 0,
    "isRepost": false,
    "isReply": false,
    "isQuote": false
  },
  "url": "https://x.com/a/status/2",
  "createdAtIso": "2026-09-05T07:09:06Z",
  "viewCount": 42,
  "quoteCount": 0,
  "bookmarkCount": 0,
  "isRepost": true,
  "isReply": false,
  "isQuote": true
}
`
	got, err := json.MarshalIndent(tw.Full(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != want {
		t.Errorf("--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestParseCreatedAt(t *testing.T) {
	if _, ok := ParseCreatedAt(""); ok {
		t.Error("empty parsed")
	}
	if _, ok := ParseCreatedAt("2026-09-05"); ok {
		t.Error("ISO date must not parse as X's legacy format")
	}
	ts, ok := ParseCreatedAt("Sat Sep 05 07:09:06 +0900 2026")
	if !ok || ts.UTC().Format("2006-01-02T15:04:05Z07:00") != "2026-09-04T22:09:06Z" {
		t.Errorf("offset not honored: %v %v", ts, ok)
	}
}

func TestFullTweetsPreservesNil(t *testing.T) {
	if FullTweets(nil) != nil {
		t.Error("nil in must be nil out")
	}
	if got := FullTweets([]Tweet{}); got == nil || len(got) != 0 {
		t.Errorf("empty in = %v", got)
	}
}
