package xapi

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

var userTweetsRoot = []string{"user", "result", "timeline", "timeline", "instructions"}

// A skipping read keeps the readable posts, returns every skipped one with
// its reason, and resumes after all of them. Skips do not count toward the
// limit, which is a target for readable tweets.
func TestSkippingUserTweetsReturnsSkipsAndAdvancesCursor(t *testing.T) {
	ps := newPagingServer(t, userTweetsRoot, []pageSpec{
		{tweetIDs: []string{"1", "2"}, malformed: 1, previews: []string{"80000"}, cursor: "C1"},
		// X repeats a pinned preview on a later page; it is reported once.
		{tweetIDs: []string{"3"}, previews: []string{"80000"}, cursor: "C2"},
	})

	tweets, next, skipped, err := ps.client(t).UserTweetsPageAlignedSkipping(context.Background(), "naval", 4, "", 2)
	if err != nil {
		t.Fatalf("UserTweetsPageAlignedSkipping: %v", err)
	}
	wantIDs(t, tweets, "1", "2", "3")
	want := []SkippedPost{{ID: "900", Reason: SkipUnreadable}, {ID: "80000", Reason: SkipPreviewOnly}}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", skipped, want)
	}
	if next != "C2" {
		t.Fatalf("next cursor = %q, want C2", next)
	}

	reqs := ps.timelineRequests()
	// Page 2 asks for 4-2 more: the two skipped entries did not count.
	wantCounts(t, reqs, 4, 2)
	if _, sent := reqs[0].variables["cursor"]; sent {
		t.Errorf("first page sent a cursor: %v", reqs[0].variables["cursor"])
	}
	if got, _ := reqs[1].variables["cursor"].(string); got != "C1" {
		t.Errorf("second page cursor = %q, want C1 (past the skipped entries)", got)
	}
}

// The same page read strictly still fails, and a page where every post is a
// preview is a reported success under skipping, never a silent empty one.
func TestSkippingIsPerCallAndAnAllPreviewPageIsReported(t *testing.T) {
	page := pageSpec{previews: []string{"80001", "80002"}, cursor: "C1"}
	ps := newPagingServer(t, userTweetsRoot, []pageSpec{page, page, page})
	c := ps.client(t)

	_, _, err := c.UserTweetsPageAlignedFrom(context.Background(), "naval", 1, "", 1)
	if err == nil || !strings.Contains(err.Error(), "preview-only") {
		t.Fatalf("strict read err = %v, want a preview-only failure", err)
	}

	tweets, next, skipped, err := c.UserTweetsPageAlignedSkipping(context.Background(), "naval", 1, "", 1)
	if err != nil {
		t.Fatalf("skipping read: %v", err)
	}
	if len(tweets) != 0 || next != "C1" {
		t.Fatalf("tweets = %v next = %q, want none and C1", ids(tweets), next)
	}
	want := []SkippedPost{{ID: "80001", Reason: SkipPreviewOnly}, {ID: "80002", Reason: SkipPreviewOnly}}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", skipped, want)
	}

	// The skipping call left no setting behind on the shared client.
	if _, _, err := c.UserTweetsPageAlignedFrom(context.Background(), "naval", 1, "", 1); err == nil {
		t.Fatal("a skipping call changed the next strict read on the same client")
	}
}

// Skipping covers exactly one case: a post that is itself unreadable and has
// an id to report it by. Everything else keeps failing closed.
func TestSkippingStillFailsWhatItCannotReport(t *testing.T) {
	author := `"core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"u","name":"U"}}}}`
	page := func(result string) []byte {
		return []byte(`[{"type":"TimelineAddEntries","entries":[{"content":{"itemContent":{"tweet_results":{"result":` + result + `}}}}]}]`)
	}
	cases := []struct {
		name   string
		result string
		want   string
	}{
		{"unreadable post without an id", `{"__typename":"Tweet"}`, "malformed tweet timeline entry"},
		{"readable post quoting an unreadable post",
			`{"rest_id":"10001",` + author + `,"legacy":{"full_text":"quote"},"quoted_status_result":{"result":{"rest_id":"70000"}}}`,
			`"70000"`},
		{"readable post quoting a preview",
			`{"rest_id":"10002",` + author + `,"legacy":{"full_text":"quote"},"quoted_status_result":{"result":{"__typename":"TweetPreviewDisplay","tweet":{"rest_id":"70001",` + author + `,"text":"teaser"}}}}`,
			"preview-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStrictTimelineInstructionsWith(page(tc.result), strictParseOptions{skipUnreadablePosts: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (page %+v), want failure mentioning %s", err, got, tc.want)
			}
		})
	}
}
