package tweet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// readableEntry is an ordinary post by handle.
func readableEntry(id, handle string) string {
	return fmt.Sprintf(`{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"%s",`+
		`"core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"%s","name":"N"}}}},`+
		`"legacy":{"full_text":"post %s"}}}}}}`, id, handle, id)
}

// previewEntry is X's "Subscribe to unlock" stand-in for a subscriber-only
// post by handle, trimmed from the shape served live on 2026-10-21: a
// TweetPreviewDisplay whose inner tweet has an author and a teaser but no
// legacy body.
func previewEntry(id, handle string) string {
	return `{"content":{"entryType":"TimelineTimelineItem","itemContent":{"itemType":"TimelineTweet",` +
		`"__typename":"TimelineTweet","tweet_results":{"result":` + previewResult(id, handle) + `}}}}`
}

func previewResult(id, handle string) string {
	return fmt.Sprintf(`{"__typename":"TweetPreviewDisplay","cta":{"title":"Subscribe to unlock"},"tweet":{"rest_id":"%s",`+
		`"core":{"user_results":{"result":{"rest_id":"42","core":{"screen_name":"%s","name":"N"}}}},`+
		`"created_at":"Wed Oct 21 01:27:40 +0000 2026","text":"For members only…"}}`, id, handle)
}

// promotingEntry is a readable post by handle that quotes (or, with repost,
// reposts) the preview-only post previewID — how creators advertise a locked
// post, often pinned.
func promotingEntry(id, handle, previewID string, repost bool) string {
	relation := `"legacy":{"full_text":"new post for members"},"quoted_status_result":{"result":` + previewResult(previewID, handle) + `}`
	if repost {
		relation = `"legacy":{"full_text":"RT","retweeted_status_result":{"result":` + previewResult(previewID, handle) + `}}`
	}
	return fmt.Sprintf(`{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"%s",`+
		`"core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"%s","name":"N"}}}},%s}}}}}`, id, handle, relation)
}

func userTweetsBody(cursor string, entries ...string) string {
	if cursor != "" {
		entries = append(entries, `{"content":{"cursorType":"Bottom","value":"`+cursor+`"}}`)
	}
	return `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[` +
		strings.Join(entries, ",") + `]}]}}}}}}`
}

// timelineServer answers UserByScreenName for every handle in pages and then
// serves that handle's UserTweets body, routed by the userId it resolved to.
func timelineServer(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	ids := make(map[string]string, len(pages))
	bodies := make(map[string]string, len(pages))
	n := 0
	for handle, body := range pages {
		n++
		id := fmt.Sprintf("%d", 1000+n)
		ids[handle], bodies[id] = id, body
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if op == "UserByScreenName" {
			handle, _ := variables["screen_name"].(string)
			_, _ = fmt.Fprintf(w, `{"data":{"user":{"result":{"rest_id":"%s","legacy":{"screen_name":"%s","name":"N"}}}}}`, ids[handle], handle)
			return
		}
		userID, _ := variables["userId"].(string)
		body, ok := bodies[userID]
		if !ok {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestUserTimelineSkippingUnreadableKeepsReadablePosts(t *testing.T) {
	server := timelineServer(t, map[string]string{
		"target": userTweetsBody("NEXT",
			readableEntry("20001", "target"),
			previewEntry("20002", "target"),
			readableEntry("20003", "target")),
	})
	c := monitoringClient(t, server.URL)
	opts := UserTimelineOptions{Limit: 20}

	// The default stays strict, and now names the cause.
	strict, err := c.UserTimeline(t.Context(), "target", opts)
	if err == nil || !strings.Contains(err.Error(), "preview-only") || len(strict.Tweets) != 0 {
		t.Fatalf("strict UserTimeline = %+v, %v; want a preview-only failure", strict, err)
	}

	page, err := c.UserTimelineSkippingUnreadable(t.Context(), "@target", opts)
	if err != nil {
		t.Fatalf("UserTimelineSkippingUnreadable: %v", err)
	}
	var got []string
	for _, post := range page.Tweets {
		got = append(got, post.ID)
	}
	if strings.Join(got, ",") != "20001,20003" || page.NextCursor != "NEXT" {
		t.Fatalf("tweets = %v next = %q, want 20001,20003 and NEXT", got, page.NextCursor)
	}
	want := []SkippedPost{{ID: "20002", Reason: SkipPreviewOnly}}
	if !reflect.DeepEqual(page.Skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", page.Skipped, want)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"nextCursor":"NEXT","skipped":[{"id":"20002","reason":"preview-only"}]}`) {
		t.Fatalf("JSON = %s", raw)
	}

	full, err := c.UserTimelineFullSkippingUnreadable(t.Context(), "target", opts)
	if err != nil {
		t.Fatalf("UserTimelineFullSkippingUnreadable: %v", err)
	}
	if len(full.Tweets) != 2 || full.Tweets[1].ID != "20003" || full.NextCursor != "NEXT" || !reflect.DeepEqual(full.Skipped, want) {
		t.Fatalf("full = %+v", full)
	}
}

// A post quoting or reposting a locked post is left out under its own
// reason, so promoting a subscriber-only post cannot darken the profile.
func TestUserTimelineSkipsPostsPromotingAPreview(t *testing.T) {
	server := timelineServer(t, map[string]string{
		"target": userTweetsBody("NEXT",
			promotingEntry("23001", "target", "23901", false),
			promotingEntry("23002", "target", "23902", true),
			readableEntry("23003", "target")),
	})
	c := monitoringClient(t, server.URL)

	if _, err := c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 20}); err == nil || !strings.Contains(err.Error(), `quotes tweet "23901"`) {
		t.Fatalf("strict UserTimeline err = %v, want the quote of a preview to fail", err)
	}
	page, err := c.UserTimelineSkippingUnreadable(t.Context(), "target", UserTimelineOptions{Limit: 20})
	if err != nil {
		t.Fatalf("UserTimelineSkippingUnreadable: %v", err)
	}
	want := []SkippedPost{{ID: "23001", Reason: SkipQuotesPreviewOnly}, {ID: "23002", Reason: SkipRepostsPreviewOnly}}
	if len(page.Tweets) != 1 || page.Tweets[0].ID != "23003" || !reflect.DeepEqual(page.Skipped, want) {
		t.Fatalf("tweets = %+v skipped = %+v, want 23003 and %+v", page.Tweets, page.Skipped, want)
	}
}

// A quote of a post X withholds arrives as an empty quoted_status_result
// while the quote still names its target (live on 2026-10-21). It is skipped
// as quotes-unavailable rather than darkening the profile.
func TestUserTimelineSkipsPostsQuotingAPostXWillNotShow(t *testing.T) {
	withheld := fmt.Sprintf(`{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"24001",`+
		`"core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"N"}}}},`+
		`"legacy":{"full_text":"look at this","is_quote_status":true,"quoted_status_id_str":"%s"},"quoted_status_result":{}}}}}}`, "24901")
	server := timelineServer(t, map[string]string{
		"target": userTweetsBody("NEXT", withheld, readableEntry("24002", "target")),
	})
	c := monitoringClient(t, server.URL)

	if _, err := c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 20}); err == nil || !strings.Contains(err.Error(), `quotes tweet "24901" that X will not show`) {
		t.Fatalf("strict UserTimeline err = %v", err)
	}
	page, err := c.UserTimelineSkippingUnreadable(t.Context(), "target", UserTimelineOptions{Limit: 20})
	if err != nil {
		t.Fatalf("UserTimelineSkippingUnreadable: %v", err)
	}
	want := []SkippedPost{{ID: "24001", Reason: SkipQuotesUnavailable}}
	if len(page.Tweets) != 1 || page.Tweets[0].ID != "24002" || !reflect.DeepEqual(page.Skipped, want) {
		t.Fatalf("tweets = %+v skipped = %+v, want 24002 and %+v", page.Tweets, page.Skipped, want)
	}
}

// TestMalformedTweetItemsCannotBecomeSuccessfulEmptyPages, for the opt-in:
// each malformed item either still fails the read or is named in Skipped.
// None may come back as a silent empty success.
func TestSkippingReadsNeverHideMalformedItems(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantSkipped []SkippedPost // nil means the read must fail
	}{
		{
			name:        "posts",
			body:        `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","legacy":{"full_text":"text whose author shape moved"}}}}}}]}]}}}}}}`,
			wantSkipped: []SkippedPost{{ID: "2", Reason: SkipUnreadable}},
		},
		{
			name: "posts missing tweet_results",
			body: `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"__typename":"TimelineTweet"}}}]}]}}}}}}`,
		},
		{
			name: "posts malformed quote relation",
			body: `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","core":{"user_results":{"result":{"legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"quote"},"quoted_status_result":{}}}}}}]}]}}}}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := timelineServer(t, map[string]string{"target": tc.body})
			page, err := monitoringClient(t, server.URL).UserTimelineSkippingUnreadable(t.Context(), "target", UserTimelineOptions{Limit: 1})
			if tc.wantSkipped == nil {
				if err == nil {
					t.Fatalf("malformed item became a successful page: %+v", page)
				}
				if len(page.Tweets) != 0 || page.NextCursor != "" || page.Skipped != nil {
					t.Fatalf("error leaked a partial page: %+v", page)
				}
				return
			}
			if err != nil {
				t.Fatalf("UserTimelineSkippingUnreadable: %v", err)
			}
			if len(page.Tweets) != 0 || !reflect.DeepEqual(page.Skipped, tc.wantSkipped) {
				t.Fatalf("page = %+v, want no tweets and skipped %+v", page, tc.wantSkipped)
			}
		})
	}
}

// Skipping is a per-call choice. Strict and skipping reads share one Client
// (and so one cached X client per account) concurrently, alongside a second
// Client, and each result carries exactly its own skips: nothing leaks
// through shared state the way a process-wide hook would.
func TestSkippingAndStrictReadsDoNotCrossTalk(t *testing.T) {
	server := timelineServer(t, map[string]string{
		"alpha": userTweetsBody("A", readableEntry("21001", "alpha"), previewEntry("21002", "alpha")),
		"beta":  userTweetsBody("B", readableEntry("22001", "beta"), previewEntry("22002", "beta")),
	})
	shared, other := monitoringClient(t, server.URL), monitoringClient(t, server.URL)
	opts := UserTimelineOptions{Limit: 20}

	check := func(c *Client, handle, readable, skipped string) {
		page, err := c.UserTimelineSkippingUnreadable(t.Context(), handle, opts)
		if err != nil {
			t.Errorf("%s: %v", handle, err)
			return
		}
		want := []SkippedPost{{ID: skipped, Reason: SkipPreviewOnly}}
		if len(page.Tweets) != 1 || page.Tweets[0].ID != readable || !reflect.DeepEqual(page.Skipped, want) {
			t.Errorf("%s: tweets=%+v skipped=%+v, want %s and %+v", handle, page.Tweets, page.Skipped, readable, want)
		}
	}

	var wg sync.WaitGroup
	for range 25 {
		wg.Add(4)
		go func() {
			defer wg.Done()
			if page, err := shared.UserTimeline(t.Context(), "alpha", opts); err == nil {
				t.Errorf("strict read succeeded beside skipping reads: %+v", page)
			}
		}()
		go func() { defer wg.Done(); check(shared, "beta", "22001", "22002") }()
		go func() { defer wg.Done(); check(shared, "alpha", "21001", "21002") }()
		go func() { defer wg.Done(); check(other, "beta", "22001", "22002") }()
	}
	wg.Wait()
}

func TestSkipReasonsMirrorParser(t *testing.T) {
	pairs := map[xapi.SkipReason]SkipReason{
		xapi.SkipPreviewOnly:        SkipPreviewOnly,
		xapi.SkipQuotesPreviewOnly:  SkipQuotesPreviewOnly,
		xapi.SkipRepostsPreviewOnly: SkipRepostsPreviewOnly,
		xapi.SkipQuotesUnavailable:  SkipQuotesUnavailable,
		xapi.SkipRepostsUnavailable: SkipRepostsUnavailable,
		xapi.SkipUnreadable:         SkipUnreadable,
	}
	for parser, public := range pairs {
		if SkipReason(parser) != public {
			t.Errorf("parser reason %q is published as %q", parser, public)
		}
	}
	got := convertSkippedPosts([]xapi.SkippedPost{{ID: "1", Reason: xapi.SkipPreviewOnly}})
	if !reflect.DeepEqual(got, []SkippedPost{{ID: "1", Reason: SkipPreviewOnly}}) {
		t.Fatalf("convertSkippedPosts = %+v", got)
	}
	if convertSkippedPosts(nil) != nil {
		t.Fatal("no skips must stay nil so the JSON omits them")
	}
}
