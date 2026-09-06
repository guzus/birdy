package tweet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// The public FullTweet must marshal to exactly what the CLI's --json-full
// prints, so a Go caller and a shell caller see one contract.
func TestFullTweetMatchesParserView(t *testing.T) {
	src := xapi.Tweet{
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
		QuotedTweet:       &xapi.Tweet{ID: "2", Text: "q", Author: xapi.Author{Username: "bob", Name: "Bob"}},
		RepostedTweet:     &xapi.Tweet{ID: "0", Text: "o", Author: xapi.Author{Username: "carol", Name: "Carol"}},
		Media:             []xapi.Media{{Type: "photo", URL: "https://pbs.twimg.com/p.jpg", Width: 1, Height: 2}},
		Article:           &xapi.Article{Title: "T", PreviewText: "P"},
	}
	want, err := json.Marshal(src.Full())
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(convertFullTweet(src.Full()))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("public FullTweet drifted from parser view\n got: %s\nwant: %s", got, want)
	}

	full := convertFullTweet(src.Full())
	if full.URL != "https://x.com/alice/status/3" || full.CreatedAtISO != "2026-09-05T07:09:06Z" ||
		!full.IsReply || !full.IsQuote || !full.IsRepost {
		t.Errorf("enrichment lost: %+v", full)
	}
	// Projecting back yields the frozen shape with the same values.
	back := full.Tweet()
	if back.ID != "3" || back.QuotedTweet == nil || back.QuotedTweet.ID != "2" || back.Article == nil {
		t.Errorf("Tweet() projection lost fields: %+v", back)
	}
	if !strings.Contains(string(got), `"quotedTweet":{"id":"2"`) {
		t.Errorf("nested quote not enriched: %s", got)
	}
}

func TestConvertFullTweetsPreservesNil(t *testing.T) {
	if convertFullTweets(nil) != nil {
		t.Error("nil in must be nil out")
	}
	if got := convertFullTweets([]xapi.Tweet{}); got == nil || len(got) != 0 {
		t.Errorf("empty in = %v", got)
	}
}

func fullTestClient(t *testing.T, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "UserByScreenName") {
			_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"9001","legacy":{"screen_name":"outer","name":"Outer"}}}}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client, err := NewMonitoringClient(MonitoringOptions{AccountsJSON: testAccounts, AccountPool: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	account, _ := client.store.Get("main")
	api, err := client.apiClientFor(account)
	if err != nil {
		t.Fatal(err)
	}
	api.SetBaseURL(server.URL)
	api.SetUserTweetsPageDelay(0)
	return client
}

const fullDetailBody = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	{"content":{"itemContent":{"tweet_results":{"result":{
		"rest_id":"20000",
		"core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"outer","name":"Outer"}}}},
		"views":{"count":"555"},
		"legacy":{"full_text":"RT @source: original","created_at":"Sat Sep 05 07:09:06 +0000 2026",
			"conversation_id_str":"20000","quote_count":3,"bookmark_count":4,"lang":"en",
			"retweeted_status_result":{"result":{"rest_id":"40000","core":{"user_results":{"result":{"rest_id":"9004","legacy":{"screen_name":"source","name":"Source"}}}},"legacy":{"full_text":"original","created_at":"Fri Sep 04 00:00:00 +0000 2026"}}}
		}
	}}}}}
]}]}}}`

func TestReadFull(t *testing.T) {
	t.Setenv("BIRDY_QUOTE_DEPTH", "0")
	client := fullTestClient(t, fullDetailBody)

	got, err := client.ReadFull(t.Context(), "https://x.com/outer/status/20000")
	if err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if got.URL != "https://x.com/outer/status/20000" || got.ViewCount != 555 || got.QuoteCount != 3 ||
		got.BookmarkCount != 4 || got.Lang != "en" || got.CreatedAtISO != "2026-09-05T07:09:06Z" {
		t.Errorf("enrichment missing: %+v", got)
	}
	if !got.IsRepost || got.RepostedTweet == nil || got.RepostedTweet.URL != "https://x.com/source/status/40000" ||
		got.RepostedTweet.CreatedAtISO != "2026-09-04T00:00:00Z" {
		t.Errorf("repost not enriched: %+v", got.RepostedTweet)
	}
}

func TestUserTimelineFullMatchesUserTimeline(t *testing.T) {
	const body = `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[
		{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"1",
			"core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"outer","name":"Outer"}}}},
			"views":{"count":"10"},
			"legacy":{"full_text":"a","created_at":"Sat Sep 05 07:09:06 +0000 2026","favorite_count":7}}}}}},
		{"content":{"cursorType":"Bottom","value":"NEXT"}}
	]}]}}}}}}`
	client := fullTestClient(t, body)
	opts := UserTimelineOptions{Limit: 1, MaxPages: 1}

	plain, err := client.UserTimeline(t.Context(), "outer", opts)
	if err != nil {
		t.Fatalf("UserTimeline: %v", err)
	}
	full, err := client.UserTimelineFull(t.Context(), "outer", opts)
	if err != nil {
		t.Fatalf("UserTimelineFull: %v", err)
	}
	if len(full.Tweets) != len(plain.Tweets) || full.NextCursor != plain.NextCursor {
		t.Fatalf("paging diverged: full %d/%q, plain %d/%q", len(full.Tweets), full.NextCursor, len(plain.Tweets), plain.NextCursor)
	}
	if full.Tweets[0].ID != plain.Tweets[0].ID || full.Tweets[0].LikeCount != 7 {
		t.Errorf("entry mismatch: %+v vs %+v", full.Tweets[0], plain.Tweets[0])
	}
	if full.Tweets[0].ViewCount != 10 || full.Tweets[0].URL != "https://x.com/outer/status/1" {
		t.Errorf("entry not enriched: %+v", full.Tweets[0])
	}

	// The author check is shared, so a foreign tweet fails both the same way.
	if _, err := client.UserTimelineFull(t.Context(), "someone-else!", opts); err == nil {
		t.Error("invalid handle accepted")
	}
}

func TestSearchFull(t *testing.T) {
	const body = `{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"type":"TimelineAddEntries","entries":[
		{"entryId":"tweet-1","content":{"entryType":"TimelineTimelineItem","itemContent":{"itemType":"TimelineTweet","tweet_results":{"result":{"rest_id":"1",
			"core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"outer","name":"Outer"}}}},
			"views":{"count":"99"},
			"legacy":{"full_text":"hit","created_at":"Sat Sep 05 07:09:06 +0000 2026","lang":"ko"}}}}}}
	]}]}}}}}`
	client := fullTestClient(t, body)
	got, err := client.SearchFull(t.Context(), "hit", 1)
	if err != nil {
		t.Fatalf("SearchFull: %v", err)
	}
	if len(got) != 1 || got[0].ViewCount != 99 || got[0].Lang != "ko" || got[0].URL != "https://x.com/outer/status/1" {
		t.Errorf("search entry not enriched: %+v", got)
	}
}
