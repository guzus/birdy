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
		// An empty relation is X withholding the target only when the quote
		// names it; otherwise it could be a moved field.
		{"readable post with an empty repost relation",
			readablePost("10003", `,"retweeted_status_result":{}`),
			"malformed retweeted_status_result"},
		{"quote relation without a result, though the quote names its target",
			`{"rest_id":"10004",` + testAuthor + `,"legacy":{"full_text":"quote","quoted_status_id_str":"70004"},"quoted_status_result":{"result":null}}`,
			"malformed quoted_status_result"},
		{"quote relation with an unknown field, though the quote names its target",
			`{"rest_id":"10005",` + testAuthor + `,"legacy":{"full_text":"quote","quoted_status_id_str":"70005"},"quoted_status_result":{"tweet_result":{}}}`,
			"malformed quoted_status_result"},
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

// The live @jukan05 shape (2026-10-21, ids anonymised): a profile
// conversation whose first post quotes a post X withholds. X sends an empty
// quoted_status_result while the post still names its target; the self-reply
// beside it is ordinary.
const withheldQuoteModule = `[{"type":"TimelineAddEntries","entries":[{"entryId":"profile-conversation-1",` +
	`"content":{"__typename":"TimelineTimelineModule","entryType":"TimelineTimelineModule","displayType":"VerticalConversation","items":[` +
	`{"entryId":"profile-conversation-1-tweet-10101","item":{"itemContent":{"__typename":"TimelineTweet","itemType":"TimelineTweet","tweetDisplayType":"Tweet",` +
	`"tweet_results":{"result":{"__typename":"Tweet","rest_id":"10101",` + testAuthor + `,` +
	`"legacy":{"conversation_id_str":"10101","full_text":"Honestly, I can't help but laugh","is_quote_status":true,"quoted_status_id_str":"70101",` +
	`"quoted_status_permalink":{"display":"x.com/someone…","expanded":"https://twitter.com/someone/status/70101","url":"https://t.co/x"}},` +
	`"quoted_status_result":{}}}}}},` +
	`{"entryId":"profile-conversation-1-tweet-10102","item":{"itemContent":{"__typename":"TimelineTweet","itemType":"TimelineTweet","tweetDisplayType":"Tweet",` +
	`"tweet_results":{"result":{"__typename":"Tweet","rest_id":"10102",` + testAuthor + `,` +
	`"legacy":{"conversation_id_str":"10101","full_text":"There's nothing more laughable","in_reply_to_status_id_str":"10101","is_quote_status":false}}}}}}` +
	`]}}]}]`

// A quote or repost of a post X will not show (deleted, suspended or
// protected author) is ordinary X, not drift. A skipping read leaves the post
// out under its own reason; a strict read fails with a typed error.
func TestSkippingLeavesOutPostsWhoseRelationXWillNotShow(t *testing.T) {
	page, err := parseStrictTimelineInstructionsWith([]byte(withheldQuoteModule), strictParseOptions{skipUnreadablePosts: true})
	if err != nil {
		t.Fatalf("skipping parse of the live shape: %v", err)
	}
	wantIDs(t, page.tweets, "10102")
	if !reflect.DeepEqual(page.skipped, []SkippedPost{{ID: "10101", Reason: SkipQuotesUnavailable}}) {
		t.Fatalf("skipped = %+v", page.skipped)
	}

	tombstone := `{"result":{"__typename":"TweetTombstone","tombstone":{"text":{"text":"This Post was deleted by the Post author."}}}}`
	unavailable := `{"result":{"__typename":"TweetUnavailable","reason":"Suspended"}}`
	quotesDeleted := readablePost("10201", `,"is_quote_status":true,"quoted_status_id_str":"70201"`)
	quotesDeleted = quotesDeleted[:len(quotesDeleted)-1] + `,"quoted_status_result":` + tombstone + `}`
	repostsSuspended := readablePost("10202", `,"retweeted_status_result":`+unavailable)
	repostsQuoterOfDeleted := readablePost("10203", `,"retweeted_status_result":{"result":`+quotesDeleted+`}`)

	page, err = parseStrictTimelineInstructionsWith(strictPage(quotesDeleted, repostsSuspended, repostsQuoterOfDeleted, readablePost("10204", "")), strictParseOptions{skipUnreadablePosts: true})
	if err != nil {
		t.Fatalf("skipping parse: %v", err)
	}
	wantIDs(t, page.tweets, "10204")
	want := []SkippedPost{
		{ID: "10201", Reason: SkipQuotesUnavailable},
		{ID: "10202", Reason: SkipRepostsUnavailable},
		// The outermost hop names the reason: 10203 reposts a post that quotes one.
		{ID: "10203", Reason: SkipRepostsUnavailable},
	}
	if !reflect.DeepEqual(page.skipped, want) {
		t.Fatalf("skipped = %+v, want %+v", page.skipped, want)
	}

	strict := []struct {
		page                         []byte
		postID, via, targetID, shape string
	}{
		{[]byte(withheldQuoteModule), "10101", "quote", "70101", "empty quoted_status_result"},
		{strictPage(quotesDeleted), "10201", "quote", "70201", "TweetTombstone"},
		{strictPage(repostsSuspended), "10202", "repost", "", "TweetUnavailable"},
		{strictPage(repostsQuoterOfDeleted), "10203", "repost", "70201", "TweetTombstone"},
	}
	for _, tc := range strict {
		_, err := parseStrictTimelineInstructionsWith(tc.page, strictParseOptions{})
		var unavailableErr *relationUnavailableError
		if !errors.As(err, &unavailableErr) {
			t.Fatalf("strict err = %v, want a relationUnavailableError", err)
		}
		if got := *unavailableErr; got.postID != tc.postID || got.via != tc.via || got.targetID != tc.targetID || got.shape != tc.shape {
			t.Fatalf("strict error = %+v, want %s %s %q (%s)", got, tc.postID, tc.via, tc.targetID, tc.shape)
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
