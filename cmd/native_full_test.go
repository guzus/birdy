package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/xapi"
)

// --json-full is opt-in enrichment on top of --json. Two properties are
// load-bearing and pinned here:
//
//  1. --json output does not change by a byte. The enrichment lives on a
//     separate type (xapi.FullTweet), so the bird-era struct — and therefore
//     the bytes — is untouched.
//  2. --json-full is a strict superset: every --json key appears first, in the
//     same order, with the same value; the extras follow.

// fullFixture exercises every enrichment path: a reply that quotes a tweet
// with metrics, a repost, an unparsable date, and a bare tweet.
func fullFixture() []xapi.Tweet {
	return []xapi.Tweet{
		{
			ID:                "3",
			Text:              "reply quoting",
			CreatedAt:         "Sat Sep 05 07:09:06 +0000 2026",
			ReplyCount:        1,
			RetweetCount:      2,
			LikeCount:         30,
			ConversationID:    "1",
			InReplyToStatusID: "1",
			Author:            xapi.Author{Username: "alice", Name: "Alice"},
			AuthorID:          "11",
			QuotedTweet: &xapi.Tweet{
				ID:        "2",
				Text:      "quoted",
				CreatedAt: "Fri Sep 04 00:00:00 +0000 2026",
				LikeCount: 500,
				Author:    xapi.Author{Username: "bob", Name: "Bob"},
			},
			Media: []xapi.Media{{Type: "photo", URL: "https://pbs.twimg.com/p.jpg"}},
		},
		{
			ID:            "5",
			Text:          "RT @carol: original",
			CreatedAt:     "not a date",
			Author:        xapi.Author{Username: "dave", Name: "Dave"},
			RepostedTweet: &xapi.Tweet{ID: "4", Text: "original", Author: xapi.Author{Username: "carol", Name: "Carol"}},
		},
		{
			ID:        "6",
			Text:      "plain",
			CreatedAt: "Sat Sep 05 09:00:00 +0000 2026",
			LikeCount: 5,
			Author:    xapi.Author{Username: "erin", Name: "Erin"},
		},
	}
}

func renderList(t *testing.T, tweets []xapi.Tweet, args nativeArgs) string {
	t.Helper()
	var buf bytes.Buffer
	if err := renderTweets(&buf, tweets, args); err != nil {
		t.Fatalf("renderTweets: %v", err)
	}
	return buf.String()
}

// TestJSONOutputUnchangedByJSONFull pins --json bytes for the fixture. Its
// twin in internal/xapi (TestTweetJSONContractIsUnchanged) pins the struct
// layout that produces them; this one proves the CLI path still marshals the
// bird-era type and nothing else.
func TestJSONOutputUnchangedByJSONFull(t *testing.T) {
	got := renderList(t, fullFixture(), nativeArgs{json: true, command: "search"})

	const want = `[
  {
    "id": "3",
    "text": "reply quoting",
    "createdAt": "Sat Sep 05 07:09:06 +0000 2026",
    "replyCount": 1,
    "retweetCount": 2,
    "likeCount": 30,
    "conversationId": "1",
    "inReplyToStatusId": "1",
    "author": {
      "username": "alice",
      "name": "Alice"
    },
    "authorId": "11",
    "quotedTweet": {
      "id": "2",
      "text": "quoted",
      "createdAt": "Fri Sep 04 00:00:00 +0000 2026",
      "replyCount": 0,
      "retweetCount": 0,
      "likeCount": 500,
      "author": {
        "username": "bob",
        "name": "Bob"
      }
    },
    "media": [
      {
        "type": "photo",
        "url": "https://pbs.twimg.com/p.jpg"
      }
    ]
  },
  {
    "id": "5",
    "text": "RT @carol: original",
    "createdAt": "not a date",
    "replyCount": 0,
    "retweetCount": 0,
    "likeCount": 0,
    "author": {
      "username": "dave",
      "name": "Dave"
    },
    "repostedTweet": {
      "id": "4",
      "text": "original",
      "replyCount": 0,
      "retweetCount": 0,
      "likeCount": 0,
      "author": {
        "username": "carol",
        "name": "Carol"
      }
    }
  },
  {
    "id": "6",
    "text": "plain",
    "createdAt": "Sat Sep 05 09:00:00 +0000 2026",
    "replyCount": 0,
    "retweetCount": 0,
    "likeCount": 5,
    "author": {
      "username": "erin",
      "name": "Erin"
    }
  }
]
`
	if got != want {
		t.Errorf("--json changed\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// No enrichment key may leak into --json. (media carries its own "url"
	// key, so the permalink is matched by value.)
	for _, key := range []string{`"url": "https://x.com/`, `"createdAtIso"`, `"viewCount"`, `"isReply"`} {
		if strings.Contains(got, key) {
			t.Errorf("--json output contains %s", key)
		}
	}
}

// orderedKeys returns an object's keys in wire order, which encoding/json's
// map decoding would lose.
func orderedKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// assertSuperset checks that full begins with exactly base's keys, in order,
// with identical values, then recurses into the nested tweet objects.
func assertSuperset(t *testing.T, path string, base, full json.RawMessage) {
	t.Helper()
	baseKeys, fullKeys := orderedKeys(t, base), orderedKeys(t, full)
	if len(fullKeys) <= len(baseKeys) {
		t.Fatalf("%s: full has %d keys, base %d; expected extras", path, len(fullKeys), len(baseKeys))
	}
	for i, k := range baseKeys {
		if fullKeys[i] != k {
			t.Fatalf("%s: key %d = %q in full, %q in base; prefix order broken", path, i, fullKeys[i], k)
		}
	}
	var baseObj, fullObj map[string]json.RawMessage
	if err := json.Unmarshal(base, &baseObj); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(full, &fullObj); err != nil {
		t.Fatal(err)
	}
	for k, v := range baseObj {
		switch k {
		case "quotedTweet", "repostedTweet":
			assertSuperset(t, path+"."+k, v, fullObj[k])
		default:
			if !bytes.Equal(v, fullObj[k]) {
				t.Errorf("%s.%s: value differs: base %s, full %s", path, k, v, fullObj[k])
			}
		}
	}
}

func TestJSONFullIsStrictSupersetOfJSON(t *testing.T) {
	base := renderList(t, fullFixture(), nativeArgs{json: true, command: "search"})
	full := renderList(t, fullFixture(), nativeArgs{json: true, jsonFull: true, command: "search"})

	var baseList, fullList []json.RawMessage
	if err := json.Unmarshal([]byte(base), &baseList); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(full), &fullList); err != nil {
		t.Fatal(err)
	}
	if len(baseList) != len(fullList) {
		t.Fatalf("lengths differ: %d vs %d", len(baseList), len(fullList))
	}
	for i := range baseList {
		assertSuperset(t, "tweet["+strconv.Itoa(i)+"]", baseList[i], fullList[i])
	}

	// Spot-check the appended values.
	var decoded []xapi.FullTweet
	if err := json.Unmarshal([]byte(full), &decoded); err != nil {
		t.Fatal(err)
	}
	first := decoded[0]
	if first.URL != "https://x.com/alice/status/3" || first.CreatedAtISO != "2026-09-05T07:09:06Z" {
		t.Errorf("url/iso = %q %q", first.URL, first.CreatedAtISO)
	}
	if !first.IsReply || !first.IsQuote || first.IsRepost {
		t.Errorf("flags = reply %v quote %v repost %v", first.IsReply, first.IsQuote, first.IsRepost)
	}
	if first.QuotedTweet == nil || first.QuotedTweet.URL != "https://x.com/bob/status/2" {
		t.Errorf("nested quotedTweet not enriched: %+v", first.QuotedTweet)
	}
	second := decoded[1]
	if !second.IsRepost || second.RepostedTweet == nil || second.RepostedTweet.URL != "https://x.com/carol/status/4" {
		t.Errorf("repost not enriched: %+v", second)
	}
	if second.CreatedAtISO != "" {
		t.Errorf("unparsable createdAt must omit createdAtIso, got %q", second.CreatedAtISO)
	}
	if strings.Contains(full, `"createdAtIso": ""`) {
		t.Error("createdAtIso emitted empty instead of omitted")
	}
}

func TestJSONFullEmptyListIsEmptyArray(t *testing.T) {
	got := renderList(t, nil, nativeArgs{json: true, jsonFull: true, command: "search"})
	if got != "[]\n" {
		t.Errorf("got %q, want []", got)
	}
}

// A single `read --json-full` emits one enriched object, not a list.
func TestReadJSONFull(t *testing.T) {
	const body = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2085581653037232453",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"nasa","name":"NASA"}}}},
	    "views":{"count":"1234"},
	    "legacy":{"full_text":"hi","created_at":"Sat Sep 05 07:09:06 +0000 2026","conversation_id_str":"2085581653037232453",
	      "favorite_count":3,"quote_count":4,"bookmark_count":5,"lang":"en"}}}}}}]}]}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)

	var buf bytes.Buffer
	args := nativeArgs{json: true, jsonFull: true, command: "read", positional: "2085581653037232453"}
	if err := nativeRead(context.Background(), c, args, &buf); err != nil {
		t.Fatalf("nativeRead: %v", err)
	}
	var got xapi.FullTweet
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not a single object: %v\n%s", err, buf.String())
	}
	if got.URL != "https://x.com/nasa/status/2085581653037232453" || got.ViewCount != 1234 || got.QuoteCount != 4 ||
		got.BookmarkCount != 5 || got.Lang != "en" || got.CreatedAtISO != "2026-09-05T07:09:06Z" {
		t.Errorf("enrichment missing: %+v", got)
	}

	// Plain --json on the same read stays the bird shape.
	buf.Reset()
	args.jsonFull = false
	if err := nativeRead(context.Background(), c, args, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"url": "https://x.com/`) || strings.Contains(buf.String(), `"viewCount"`) {
		t.Errorf("--json leaked enrichment:\n%s", buf.String())
	}
}

// user-tweets keeps its {tweets, nextCursor} envelope above one page; under
// --json-full the entries inside it are enriched.
func TestUserTweetsJSONFullEnvelope(t *testing.T) {
	c, _ := userTweetsServer(t, []string{"C1", "C2"}, 20)
	got, err := runUserTweets(t, c, nativeArgs{count: 21, json: true, jsonFull: true, emoji: true})
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Tweets     []xapi.FullTweet `json:"tweets"`
		NextCursor *string          `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, got)
	}
	// -n 21 spans two pages and is then trimmed to 21, exactly as under --json.
	if len(env.Tweets) != 21 || env.NextCursor == nil || *env.NextCursor != "C2" {
		t.Errorf("envelope = %d tweets, cursor %v", len(env.Tweets), env.NextCursor)
	}
	if env.Tweets[0].URL != "https://x.com/naval/status/1" {
		t.Errorf("entry not enriched: %+v", env.Tweets[0])
	}
	if !strings.HasPrefix(got, "{\n  \"tweets\": [") {
		t.Errorf("envelope key order changed:\n%.60s", got)
	}
}

// --- Filters -----------------------------------------------------------------

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"24h", now.Add(-24 * time.Hour)},
		{"90m", now.Add(-90 * time.Minute)},
		{"7d", now.AddDate(0, 0, -7)},
		{"2w", now.AddDate(0, 0, -14)},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{"2026-09-05T07:09:06Z", time.Date(2026, 9, 5, 7, 9, 6, 0, time.UTC)},
		{"2026-09-05T16:09:06+09:00", time.Date(2026, 9, 5, 7, 9, 6, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, err := parseSince(tc.in, now)
		if err != nil {
			t.Errorf("parseSince(%q): %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("parseSince(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "yesterday", "-24h", "2026/09/01", "d"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestParseNativeArgsFilters(t *testing.T) {
	nativeNow = func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nativeNow = func() time.Time { return time.Now().UTC() } })

	got := parseNativeArgs([]string{"golang", "--min-likes", "10", "--min-retweets=2", "--min-views", "1000", "--since=24h", "--json-full"})
	if got.positional != "golang" {
		t.Errorf("positional = %q", got.positional)
	}
	if got.minLikes != 10 || got.minRetweets != 2 || got.minViews != 1000 {
		t.Errorf("thresholds = %d/%d/%d", got.minLikes, got.minRetweets, got.minViews)
	}
	if !got.sinceSet || !got.since.Equal(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("since = %v (set %v)", got.since, got.sinceSet)
	}
	if !got.json || !got.jsonFull {
		t.Error("--json-full must imply json")
	}
	if got.filterErr != nil {
		t.Errorf("filterErr = %v", got.filterErr)
	}

	// Values that are not usable fail loudly rather than filtering nothing.
	for _, args := range [][]string{
		{"--min-likes", "ten"},
		{"--min-likes=-1"},
		{"--since", "soon"},
	} {
		if parsed := parseNativeArgs(args); parsed.filterErr == nil {
			t.Errorf("parseNativeArgs(%v) accepted a bad filter value", args)
		}
	}

	// Without filters nothing is set, so output cannot change.
	if parseNativeArgs([]string{"golang", "--json"}).hasFilter() {
		t.Error("hasFilter true with no filter flags")
	}
}

func TestFilterTweets(t *testing.T) {
	tweets := fullFixture() // likes 30 / 0 / 5; retweets 2 / 0 / 0; dates ok / bad / ok
	ids := func(in []xapi.Tweet) string {
		var out []string
		for _, t := range in {
			out = append(out, t.ID)
		}
		return strings.Join(out, ",")
	}

	if got := filterTweets(tweets, nativeArgs{}); len(got) != 3 {
		t.Errorf("no filter dropped tweets: %s", ids(got))
	}
	if got := filterTweets(nil, nativeArgs{}); got != nil {
		t.Error("no filter must return the input untouched, nil included")
	}
	if got := ids(filterTweets(tweets, nativeArgs{minLikes: 5})); got != "3,6" {
		t.Errorf("min-likes 5 kept %s", got)
	}
	if got := ids(filterTweets(tweets, nativeArgs{minRetweets: 1})); got != "3" {
		t.Errorf("min-retweets 1 kept %s", got)
	}
	// No fixture tweet reports views, so any positive floor drops them all —
	// an unreported count is 0, not "unknown, keep".
	if got := ids(filterTweets(tweets, nativeArgs{minViews: 1})); got != "" {
		t.Errorf("min-views 1 kept %s", got)
	}
	since := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	if got := ids(filterTweets(tweets, nativeArgs{since: since, sinceSet: true})); got != "6" {
		t.Errorf("since 08:00 kept %s (the unparsable date must be dropped)", got)
	}
	early := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := ids(filterTweets(tweets, nativeArgs{since: early, sinceSet: true})); got != "3,6" {
		t.Errorf("since 09-01 kept %s", got)
	}
	if got := ids(filterTweets(tweets, nativeArgs{minLikes: 5, since: since, sinceSet: true})); got != "6" {
		t.Errorf("combined kept %s", got)
	}
}

// Filters apply to the text renderer too, and an all-filtered list prints the
// command's empty wording rather than nothing.
func TestFiltersApplyInTextMode(t *testing.T) {
	out := renderList(t, fullFixture(), nativeArgs{plain: true, command: "search", minLikes: 20})
	if !strings.Contains(out, "@alice") || strings.Contains(out, "@dave") || strings.Contains(out, "@erin") {
		t.Errorf("text output not filtered:\n%s", out)
	}
	out = renderList(t, fullFixture(), nativeArgs{plain: true, command: "search", minLikes: 1000})
	if out != "No tweets found.\n" {
		t.Errorf("all-filtered output = %q", out)
	}
	out = renderList(t, fullFixture(), nativeArgs{json: true, command: "search", minLikes: 1000})
	if out != "[]\n" {
		t.Errorf("all-filtered json = %q", out)
	}
}

func TestTweetFlagsAcceptedPerCommand(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		accept  bool
	}{
		{"read", []string{"123", "--json-full"}, true},
		{"read", []string{"123", "--min-likes", "5"}, false},
		{"thread", []string{"123", "--json-full", "--since", "24h"}, true},
		{"home", []string{"--json-full", "--min-views=100"}, true},
		{"user-tweets", []string{"naval", "--min-likes=10", "--json-full"}, true},
		{"mentions", []string{"--json-full"}, true},
		{"likes", []string{"--since", "7d"}, true},
		// activity has its own JSON report; enrichment there would be a lie.
		{"activity", []string{"123", "--json-full"}, false},
		{"activity", []string{"123", "--min-likes", "5"}, false},
		{"whoami", []string{"--json-full"}, false},
		{"followers", []string{"--user", "1", "--json-full"}, false},
		{"news", []string{"--json-full"}, false},
	}
	for _, tc := range cases {
		if got := nativeAcceptsFlags(tc.command, tc.args); got != tc.accept {
			t.Errorf("nativeAcceptsFlags(%q, %v) = %v, want %v", tc.command, tc.args, got, tc.accept)
		}
	}
	// A filter's value must not be read as a flag in its own right.
	if !nativeAcceptsFlags("search", []string{"--since", "2026-09-01", "golang"}) {
		t.Error("--since value mistaken for a flag")
	}
}
