package xapi

import (
	"context"
	"errors"
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

const testAuthor = `"core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"u","name":"U"}}}}`

// strictPage wraps tweet results as the entries of one strict timeline page.
func strictPage(results ...string) []byte {
	entries := make([]string, 0, len(results))
	for _, result := range results {
		entries = append(entries, `{"content":{"itemContent":{"tweet_results":{"result":`+result+`}}}}`)
	}
	return []byte(`[{"type":"TimelineAddEntries","entries":[` + strings.Join(entries, ",") + `]}]`)
}

func readablePost(id, extra string) string {
	return `{"rest_id":"` + id + `",` + testAuthor + `,"legacy":{"full_text":"post ` + id + `"` + extra + `}}`
}

// teaser is a TweetPreviewDisplay around an inner tweet with the given body
// fields; the live shape has none, only a top-level teaser "text".
func teaser(id, body string) string {
	return `{"__typename":"TweetPreviewDisplay","cta":{"title":"Subscribe to unlock"},` +
		`"tweet":{"rest_id":"` + id + `",` + testAuthor + `,"text":"teaser"` + body + `}}`
}

// Skipping covers posts it can name: one that is itself unreadable, or that
// is or reaches a preview. Everything else keeps failing closed.
func TestSkippingStillFailsWhatItCannotReport(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   string
	}{
		{"unreadable post without an id", `{"__typename":"Tweet"}`, "malformed tweet timeline entry"},
		{"readable post quoting an unreadable post",
			`{"rest_id":"10001",` + testAuthor + `,"legacy":{"full_text":"quote"},"quoted_status_result":{"result":{"rest_id":"70000"}}}`,
			`"70000"`},
		{"readable post with an empty quote relation",
			`{"rest_id":"10002",` + testAuthor + `,"legacy":{"full_text":"quote"},"quoted_status_result":{}}`,
			"malformed quoted_status_result"},
		{"preview with no teaser to take an id from", `{"__typename":"TweetPreviewDisplay"}`, "preview-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStrictTimelineInstructionsWith(strictPage(tc.result), strictParseOptions{skipUnreadablePosts: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (page %+v), want failure mentioning %s", err, got, tc.want)
			}
		})
	}
}

// Creators promote a locked post by quoting or reposting it, often pinned.
// A skipping read leaves such a post out under its own reason instead of
// failing the page; a strict read fails with a typed error naming both posts.
func TestSkippingLeavesOutPostsThatQuoteOrRepostAPreview(t *testing.T) {
	quotes := readablePost("10001", "") // quote relation lives outside legacy
	quotes = quotes[:len(quotes)-1] + `,"quoted_status_result":{"result":` + teaser("70001", "") + `}}`
	reposts := readablePost("10002", `,"retweeted_status_result":{"result":`+teaser("70002", "")+`}`)
	inner := readablePost("10003", "")
	inner = inner[:len(inner)-1] + `,"quoted_status_result":{"result":` + teaser("70003", "") + `}}`
	repostsQuoter := readablePost("10004", `,"retweeted_status_result":{"result":`+inner+`}`)

	page, err := parseStrictTimelineInstructionsWith(strictPage(quotes, reposts, repostsQuoter, readablePost("10005", "")), strictParseOptions{skipUnreadablePosts: true})
	if err != nil {
		t.Fatalf("skipping parse: %v", err)
	}
	wantIDs(t, page.tweets, "10005")
	want := []SkippedPost{
		{ID: "10001", Reason: SkipQuotesPreviewOnly},
		{ID: "10002", Reason: SkipRepostsPreviewOnly},
		// The outermost hop names the reason: 10004 reposts a post that quotes one.
		{ID: "10004", Reason: SkipRepostsPreviewOnly},
	}
	if !reflect.DeepEqual(page.skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", page.skipped, want)
	}

	strict := []struct {
		result, postID, via, previewID string
	}{
		{quotes, "10001", "quote", "70001"},
		{reposts, "10002", "repost", "70002"},
		{repostsQuoter, "10004", "repost", "70003"},
	}
	for _, tc := range strict {
		_, err := parseStrictTimelineInstructionsWith(strictPage(tc.result), strictParseOptions{})
		var preview *previewOnlyError
		if !errors.As(err, &preview) {
			t.Fatalf("strict err = %v, want a previewOnlyError", err)
		}
		if preview.postID != tc.postID || preview.via != tc.via || preview.previewID != tc.previewID {
			t.Fatalf("strict error = %+v, want post %s %s preview %s", preview, tc.postID, tc.via, tc.previewID)
		}
	}
}

// The wrapper type decides, not whether the teaser happens to read: a teaser
// with a body must not come back as the post under the real id, and a
// preview inside a visibility envelope is still a preview.
func TestPreviewIsDecidedByItsWrapper(t *testing.T) {
	cases := []struct {
		name, result, id string
	}{
		{"teaser with a legacy body", teaser("70011", `,"legacy":{"full_text":"looks like the whole post"}`), "70011"},
		{"teaser with a note body", teaser("70012", `,"note_tweet":{"note_tweet_results":{"result":{"text":"long body"}}}`), "70012"},
		{"preview in a visibility envelope", `{"__typename":"TweetWithVisibilityResults","tweet":` + teaser("70013", "") + `}`, "70013"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStrictTimelineInstructionsWith(strictPage(tc.result), strictParseOptions{})
			var preview *previewOnlyError
			if !errors.As(err, &preview) || preview.previewID != tc.id || preview.via != "" {
				t.Fatalf("strict err = %v, want preview-only %s", err, tc.id)
			}
			page, err := parseStrictTimelineInstructionsWith(strictPage(tc.result), strictParseOptions{skipUnreadablePosts: true})
			if err != nil {
				t.Fatalf("skipping parse: %v", err)
			}
			if len(page.tweets) != 0 || !reflect.DeepEqual(page.skipped, []SkippedPost{{ID: tc.id, Reason: SkipPreviewOnly}}) {
				t.Fatalf("page = %+v, want only %s skipped as preview-only", page, tc.id)
			}
		})
	}
}
