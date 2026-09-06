package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// bird pages every timeline command: `-n N` means "keep fetching 20-tweet pages
// until N *parseable* tweets are in hand, or the timeline runs out". X's count
// is a page-size hint over ENTRIES, so a single page under-delivers whenever it
// contains an unparseable one (a deleted/withheld tweet arrives as an empty
// `tweet_results: {}`), and it caps out at 20 for several operations. A
// one-shot request plus client-side truncation therefore under-returns.
//
// These tests drive the loop against a scripted server and assert the exact
// request sequence, because the request shape is the contract: page size,
// cursor presence, and when to stop.

// pageSpec is one scripted response.
type pageSpec struct {
	// tweetIDs become parseable tweet entries, in order.
	tweetIDs []string
	// tombstones is how many `tweet_results: {}` entries to append. bird and
	// birdy both drop these, which is the whole point.
	tombstones int
	// cursor is the Bottom cursor entry; empty means no cursor entry at all.
	cursor string
	// status, when non-zero, is served instead of a body.
	status    int
	malformed int // tweet-typed entries mapTweet cannot read
}

// recordedRequest is what the client actually sent.
type recordedRequest struct {
	operation string
	variables map[string]any
	at        time.Time
}

type pagingServer struct {
	mu       sync.Mutex
	requests []recordedRequest
	pages    []pageSpec
	root     []string
	srv      *httptest.Server
}

func newPagingServer(t *testing.T, root []string, pages []pageSpec) *pagingServer {
	t.Helper()
	ps := &pagingServer{pages: pages, root: root}

	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		operation := segments[len(segments)-1]

		// The user-scoped timelines resolve a handle first.
		if operation == "UserByScreenName" {
			_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"745273","legacy":{"screen_name":"naval","name":"Naval"}}}}}`))
			return
		}

		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)

		ps.mu.Lock()
		index := 0
		for _, req := range ps.requests {
			if req.operation == operation {
				index++
			}
		}
		ps.requests = append(ps.requests, recordedRequest{operation: operation, variables: variables, at: time.Now()})
		ps.mu.Unlock()

		if index >= len(ps.pages) {
			// Running off the end of the script means the loop asked for more
			// pages than the test expected; make that loud rather than hanging.
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unscripted page"}]}`))
			return
		}
		page := ps.pages[index]
		if page.status != 0 {
			w.WriteHeader(page.status)
			_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
			return
		}
		_, _ = w.Write([]byte(timelinePageBody(ps.root, page)))
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

func (ps *pagingServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(ps.srv.URL)
	c.userTweetsPageDelay = 0
	return c
}

// timelineRequests returns every request that was not the handle lookup.
func (ps *pagingServer) timelineRequests() []recordedRequest {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]recordedRequest, 0, len(ps.requests))
	for _, r := range ps.requests {
		if r.operation != "UserByScreenName" {
			out = append(out, r)
		}
	}
	return out
}

func timelinePageBody(root []string, page pageSpec) string {
	var entries []string
	for _, id := range page.tweetIDs {
		entries = append(entries, fmt.Sprintf(
			`{"entryId":"tweet-%s","content":{"itemContent":{"tweet_results":{"result":{`+
				`"rest_id":"%s","core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"u","name":"U"}}}},`+
				`"legacy":{"full_text":"tweet %s"}}}}}}`, id, id, id))
	}
	for i := 0; i < page.malformed; i++ {
		// A tweet-typed result with an id but neither author nor text: the
		// shape that failed whole accounts in production on 2026-09-06.
		entries = append(entries, fmt.Sprintf(
			`{"entryId":"tweet-bad-%d","content":{"itemContent":{"tweet_results":{"result":{"__typename":"Tweet","rest_id":"90%d"}}}}}`, i, i))
	}
	for i := 0; i < page.tombstones; i++ {
		// The exact shape X serves for a deleted or withheld tweet.
		entries = append(entries, `{"entryId":"tweet-dead","content":{"__typename":"TimelineTimelineItem","itemContent":{"__typename":"TimelineTweet","tweet_results":{}}}}`)
	}
	// A Top cursor is always present and must never be mistaken for the Bottom one.
	entries = append(entries, `{"entryId":"cursor-top","content":{"entryType":"TimelineTimelineCursor","cursorType":"Top","value":"TOP"}}`)
	if page.cursor != "" {
		entries = append(entries, fmt.Sprintf(
			`{"entryId":"cursor-bottom","content":{"entryType":"TimelineTimelineCursor","cursorType":"Bottom","value":"%s"}}`, page.cursor))
	}

	payload := `[{"type":"TimelineAddEntries","entries":[` + strings.Join(entries, ",") + `]}]`
	for i := len(root) - 1; i >= 0; i-- {
		payload = `{"` + root[i] + `":` + payload + `}`
	}
	return `{"data":` + payload + `}`
}

func ids(tweets []Tweet) []string {
	out := make([]string, 0, len(tweets))
	for _, t := range tweets {
		out = append(out, t.ID)
	}
	return out
}

func wantIDs(t *testing.T, got []Tweet, want ...string) {
	t.Helper()
	if strings.Join(ids(got), ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
}

// countOf reads variables.count off a recorded request.
func countOf(t *testing.T, r recordedRequest) int {
	t.Helper()
	n, ok := r.variables["count"].(float64)
	if !ok {
		t.Fatalf("request %s had no numeric count: %v", r.operation, r.variables)
	}
	return int(n)
}

func wantCounts(t *testing.T, reqs []recordedRequest, want ...int) {
	t.Helper()
	if len(reqs) != len(want) {
		t.Fatalf("got %d requests, want %d", len(reqs), len(want))
	}
	for i, w := range want {
		if got := countOf(t, reqs[i]); got != w {
			t.Errorf("request %d count = %d, want %d", i, got, w)
		}
	}
}

// The reported likes bug: an unparseable entry inside the first `count` entries
// costs the caller one result, because there is no second page to top it up.
func TestLikesPagesPastAnUnparseableEntry(t *testing.T) {
	ps := newPagingServer(t, []string{"user", "result", "timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1", "2", "3", "4"}, tombstones: 1, cursor: "C1"},
		{tweetIDs: []string{"5"}, cursor: "C2"},
	})
	c := ps.client(t)

	tweets, err := c.Likes(context.Background(), "naval", 5)
	if err != nil {
		t.Fatalf("Likes: %v", err)
	}
	wantIDs(t, tweets, "1", "2", "3", "4", "5")

	reqs := ps.timelineRequests()
	wantCounts(t, reqs, 5, 1)
	// bird spreads the cursor conditionally, so page 1 must carry no cursor KEY
	// at all — X rejects an explicit null.
	if _, present := reqs[0].variables["cursor"]; present {
		t.Error("page 1 must omit the cursor key entirely, not send null")
	}
	if reqs[1].variables["cursor"] != "C1" {
		t.Errorf("page 2 cursor = %v, want C1", reqs[1].variables["cursor"])
	}
	// bird sends withBirdwatchNotes on Likes; birdy omitted it.
	if reqs[0].variables["withBirdwatchNotes"] != false {
		t.Errorf("likes variables missing withBirdwatchNotes: %v", reqs[0].variables)
	}
}

func TestBookmarksPagesPastAnUnparseableEntry(t *testing.T) {
	ps := newPagingServer(t, []string{"bookmark_timeline_v2", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1", "2", "3", "4"}, tombstones: 1, cursor: "C1"},
		{tweetIDs: []string{"5"}, cursor: "C2"},
	})
	c := ps.client(t)

	tweets, err := c.Bookmarks(context.Background(), 5)
	if err != nil {
		t.Fatalf("Bookmarks: %v", err)
	}
	wantIDs(t, tweets, "1", "2", "3", "4", "5")

	reqs := ps.timelineRequests()
	wantCounts(t, reqs, 5, 1)
	if _, present := reqs[0].variables["userId"]; present {
		t.Error("Bookmarks takes no userId")
	}
	if reqs[0].variables["withDownvotePerspective"] != false {
		t.Errorf("bookmark variables wrong: %v", reqs[0].variables)
	}
}

// Page size is bird's Math.min(20, limit - got), so a large -n walks in 20s and
// finishes with the remainder. birdy's old clampCount capped the single request
// at 100, which made -n 150 unreachable on any engine.
func TestSearchPageSizeCapsAt20(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{tweetIDs: seqIDs(21, 20), cursor: "C2"},
		{tweetIDs: seqIDs(41, 5), cursor: "C3"},
	})
	c := ps.client(t)

	tweets, err := c.Search(context.Background(), "golang", 45)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(tweets) != 45 {
		t.Fatalf("got %d tweets, want 45", len(tweets))
	}
	wantCounts(t, ps.timelineRequests(), 20, 20, 5)
}

func TestSearchWithSendsTopProduct(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1"}},
	})
	if _, err := ps.client(t).SearchWith(context.Background(), "AI", 1, SearchTop); err != nil {
		t.Fatal(err)
	}
	reqs := ps.timelineRequests()
	if len(reqs) != 1 {
		t.Fatalf("got %d search requests, want 1", len(reqs))
	}
	if got := reqs[0].variables["product"]; got != "Top" {
		t.Fatalf("product = %v, want Top", got)
	}
}

func TestPageAlignedSearchDoesNotLoseOverdeliveredEntries(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1", "2", "3"}, cursor: "C1"},
		{tweetIDs: []string{"4"}},
	})
	c := ps.client(t)

	first, cursor, err := c.SearchPageAlignedFrom(context.Background(), "from:a filter:replies", 2, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, first, "1", "2", "3")
	if cursor != "C1" {
		t.Fatalf("cursor = %q, want C1", cursor)
	}

	second, cursor, err := c.SearchPageAlignedFrom(context.Background(), "from:a filter:replies", 2, cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, second, "4")
	if cursor != "" {
		t.Fatalf("terminal cursor = %q, want empty", cursor)
	}
	reqs := ps.timelineRequests()
	if got := reqs[1].variables["cursor"]; got != "C1" {
		t.Fatalf("resume cursor = %v, want C1", got)
	}
}

func TestPageAlignedSearchAdvancesPastDuplicateAndTombstonePages(t *testing.T) {
	root := []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}
	t.Run("all duplicate page", func(t *testing.T) {
		ps := newPagingServer(t, root, []pageSpec{
			{tweetIDs: []string{"1"}, cursor: "C1"},
			{tweetIDs: []string{"1"}, cursor: "C2"},
			{tweetIDs: []string{"2"}},
		})
		tweets, cursor, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 2, "", 3)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, tweets, "1", "2")
		if cursor != "" {
			t.Fatalf("cursor = %q, want exhausted", cursor)
		}
	})

	t.Run("zero parseable tombstone page", func(t *testing.T) {
		ps := newPagingServer(t, root, []pageSpec{
			{tombstones: 1, cursor: "C1"},
			{tweetIDs: []string{"2"}},
		})
		tweets, cursor, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, tweets, "2")
		if cursor != "" {
			t.Fatalf("cursor = %q, want exhausted", cursor)
		}
	})

	t.Run("cap preserves advancing cursor", func(t *testing.T) {
		ps := newPagingServer(t, root, []pageSpec{
			{tweetIDs: []string{"1"}, cursor: "C1"},
			{tweetIDs: []string{"1"}, cursor: "C2"},
		})
		tweets, cursor, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 2, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, tweets, "1")
		if cursor != "C2" {
			t.Fatalf("cursor = %q, want resumable C2", cursor)
		}
	})
}

func TestPageAlignedSearchDefaultPageBudgetIsFinite(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tombstones: 1, cursor: "C1"},
		{tombstones: 1, cursor: "C2"},
	})
	tweets, cursor, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 21, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tweets) != 0 || cursor != "C2" {
		t.Fatalf("tweets=%v cursor=%q, want capped empty result at C2", ids(tweets), cursor)
	}
	if got := len(ps.timelineRequests()); got != 2 {
		t.Fatalf("requests = %d, want ceil(21/20)=2", got)
	}
}

func TestPageAlignedSearchRejectsMultiStepCursorCycle(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1"}, cursor: "A"},
		{tweetIDs: []string{"2"}, cursor: "B"},
		{tweetIDs: []string{"3"}, cursor: "A"},
	})
	tweets, cursor, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 10, "", 3)
	if err == nil || len(tweets) != 0 || cursor != "" {
		t.Fatalf("cycle tweets=%v cursor=%q err=%v, want discarded error", ids(tweets), cursor, err)
	}
}

func seqIDs(start, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprint(start+i))
	}
	return out
}

// The four termination conditions are bird's, evaluated after the append pass.
// Each one has to stop the loop on its own or a pathological timeline spins.
func TestPagingTerminationConditions(t *testing.T) {
	root := []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}

	cases := map[string]struct {
		pages     []pageSpec
		wantIDs   []string
		wantPages int
	}{
		"no cursor on page 1": {
			pages:     []pageSpec{{tweetIDs: []string{"1", "2"}}},
			wantIDs:   []string{"1", "2"},
			wantPages: 1,
		},
		"repeated cursor": {
			pages: []pageSpec{
				{tweetIDs: []string{"1", "2"}, cursor: "C1"},
				{tweetIDs: []string{"3"}, cursor: "C1"},
			},
			wantIDs:   []string{"1", "2", "3"},
			wantPages: 2,
		},
		"page with no tweets": {
			pages: []pageSpec{
				{tweetIDs: []string{"1", "2"}, cursor: "C1"},
				{cursor: "C2"},
			},
			wantIDs:   []string{"1", "2"},
			wantPages: 2,
		},
		"page of pure duplicates": {
			pages: []pageSpec{
				{tweetIDs: []string{"1", "2"}, cursor: "C1"},
				{tweetIDs: []string{"1", "2"}, cursor: "C2"},
			},
			wantIDs:   []string{"1", "2"},
			wantPages: 2,
		},
		"page of pure tombstones": {
			pages: []pageSpec{
				{tweetIDs: []string{"1"}, cursor: "C1"},
				{tombstones: 3, cursor: "C2"},
			},
			wantIDs:   []string{"1"},
			wantPages: 2,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ps := newPagingServer(t, root, tc.pages)
			tweets, err := ps.client(t).Search(context.Background(), "q", 30)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			wantIDs(t, tweets, tc.wantIDs...)
			if got := len(ps.timelineRequests()); got != tc.wantPages {
				t.Errorf("issued %d requests, want %d", got, tc.wantPages)
			}
		})
	}
}

// bird discards everything it accumulated when a page fails, and reports the
// failure. Returning page 1's tweets would be a new divergence.
func TestPageErrorDiscardsPartialResults(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{status: http.StatusInternalServerError},
	})

	tweets, err := ps.client(t).Search(context.Background(), "q", 30)
	if err == nil {
		t.Fatal("a failed page must fail the call")
	}
	if len(tweets) != 0 {
		t.Errorf("got %d tweets on error, want none", len(tweets))
	}
}

// Dedup is across pages, not within one: overlapping cursor pages would
// otherwise double-count toward the limit and return fewer distinct tweets.
func TestPagingDedupesAcrossPages(t *testing.T) {
	ps := newPagingServer(t, []string{"home", "home_timeline_urt", "instructions"}, []pageSpec{
		{tweetIDs: []string{"1", "2", "3"}, cursor: "C1"},
		{tweetIDs: []string{"3", "4", "5"}, cursor: "C2"},
	})

	tweets, err := ps.client(t).Home(context.Background(), 5, false)
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	wantIDs(t, tweets, "1", "2", "3", "4", "5")
}

func TestHomePagesToTheRequestedCount(t *testing.T) {
	ps := newPagingServer(t, []string{"home", "home_timeline_urt", "instructions"}, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{tweetIDs: seqIDs(21, 20), cursor: "C2"},
		{tweetIDs: seqIDs(41, 20), cursor: "C3"},
	})

	tweets, err := ps.client(t).Home(context.Background(), 60, false)
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(tweets) != 60 {
		t.Errorf("got %d tweets, want 60", len(tweets))
	}
	wantCounts(t, ps.timelineRequests(), 20, 20, 20)
}

func TestListTimelinePagesPastTheOldHundredCap(t *testing.T) {
	pages := make([]pageSpec, 0, 5)
	for i := 0; i < 5; i++ {
		pages = append(pages, pageSpec{tweetIDs: seqIDs(1+i*20, 20), cursor: fmt.Sprintf("C%d", i+1)})
	}
	ps := newPagingServer(t, []string{"list", "tweets_timeline", "timeline", "instructions"}, pages)

	tweets, err := ps.client(t).ListTimeline(context.Background(), "1585430245762441216", 100)
	if err != nil {
		t.Fatalf("ListTimeline: %v", err)
	}
	if len(tweets) != 100 {
		t.Errorf("got %d tweets, want 100 (clampCount used to cap the single page at 100 entries)", len(tweets))
	}
}

// user-tweets is the one variant: a ceil(n/20) page budget capped at 10, and
// it reports a nextCursor.
func TestUserTweetsPageBudget(t *testing.T) {
	root := []string{"user", "result", "timeline", "timeline", "instructions"}

	t.Run("one page for -n 20", func(t *testing.T) {
		ps := newPagingServer(t, root, []pageSpec{
			{tweetIDs: seqIDs(1, 19), cursor: "C1"},
			{tweetIDs: seqIDs(20, 20), cursor: "C2"},
		})
		tweets, next, err := ps.client(t).UserTweets(context.Background(), "naval", 20)
		if err != nil {
			t.Fatalf("UserTweets: %v", err)
		}
		if len(tweets) != 19 {
			t.Errorf("got %d tweets, want 19 (ceil(20/20) == 1 page)", len(tweets))
		}
		if got := len(ps.timelineRequests()); got != 1 {
			t.Fatalf("issued %d requests, want 1", got)
		}
		if next != "C1" {
			t.Errorf("nextCursor = %q, want C1", next)
		}
	})

	t.Run("two pages for -n 21", func(t *testing.T) {
		ps := newPagingServer(t, root, []pageSpec{
			{tweetIDs: seqIDs(1, 20), cursor: "C1"},
			{tweetIDs: seqIDs(21, 20), cursor: "C2"},
		})
		tweets, next, err := ps.client(t).UserTweets(context.Background(), "naval", 21)
		if err != nil {
			t.Fatalf("UserTweets: %v", err)
		}
		if len(tweets) != 21 {
			t.Errorf("got %d tweets, want 21", len(tweets))
		}
		wantCounts(t, ps.timelineRequests(), 20, 1)
		// The loop reached the limit on a page that still had a cursor. bird
		// assigns nextCursor at the bottom of that iteration, before the while
		// condition ends the loop, so it must be reported.
		if next != "C2" {
			t.Errorf("nextCursor = %q, want C2", next)
		}
	})

	t.Run("hard cap of 10 pages", func(t *testing.T) {
		pages := make([]pageSpec, 0, 20)
		for i := 0; i < 20; i++ {
			pages = append(pages, pageSpec{tweetIDs: seqIDs(1+i*20, 20), cursor: fmt.Sprintf("C%d", i+1)})
		}
		ps := newPagingServer(t, root, pages)
		tweets, next, err := ps.client(t).UserTweets(context.Background(), "naval", 400)
		if err != nil {
			t.Fatalf("UserTweets: %v", err)
		}
		if got := len(ps.timelineRequests()); got != 10 {
			t.Errorf("issued %d requests, want the 10-page hard cap", got)
		}
		if len(tweets) != 200 {
			t.Errorf("got %d tweets, want 200", len(tweets))
		}
		if next == "" {
			t.Error("stopping at the page cap must report a cursor to resume from")
		}
	})
}

// user-tweets is the only command with an inter-page delay: 1s before every
// page after the first.
func TestUserTweetsDelaysBetweenPages(t *testing.T) {
	root := []string{"user", "result", "timeline", "timeline", "instructions"}
	ps := newPagingServer(t, root, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{tweetIDs: seqIDs(21, 20), cursor: "C2"},
	})
	c := ps.client(t)
	c.userTweetsPageDelay = 80 * time.Millisecond

	start := time.Now()
	if _, _, err := c.UserTweets(context.Background(), "naval", 21); err != nil {
		t.Fatalf("UserTweets: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("two pages took %v, want at least one inter-page delay", elapsed)
	}
}

func TestUserTweetsDelayDefaultsToOneSecond(t *testing.T) {
	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if c.userTweetsPageDelay != time.Second {
		t.Errorf("page delay = %v, want 1s (bird's pageDelayMs default)", c.userTweetsPageDelay)
	}
}

// A cancelled context must abort during the wait rather than sleeping it out.
func TestUserTweetsDelayHonorsContextCancellation(t *testing.T) {
	root := []string{"user", "result", "timeline", "timeline", "instructions"}
	ps := newPagingServer(t, root, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{tweetIDs: seqIDs(21, 20), cursor: "C2"},
	})
	c := ps.client(t)
	c.userTweetsPageDelay = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, _, err := c.UserTweets(ctx, "naval", 21); err == nil {
		t.Fatal("expected the cancelled context to surface as an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation took %v; the sleep is not watching the context", elapsed)
	}
}

// bird's extractCursorFromInstructions scans only instruction.entries. On later
// search pages X moves the Bottom cursor into TimelineReplaceEntry.entry
// (singular), and bird does not read it — which is exactly what caps `search`
// at two pages. Reading it here would make birdy fetch MORE than bird.
func TestBottomCursorFromInstructions(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"first Bottom wins": {
			`[{"entries":[{"content":{"cursorType":"Bottom","value":"A"}},{"content":{"cursorType":"Bottom","value":"B"}}]}]`, "A",
		},
		"Top is ignored": {
			`[{"entries":[{"content":{"cursorType":"Top","value":"T"}},{"content":{"cursorType":"Bottom","value":"B"}}]}]`, "B",
		},
		"empty value ignored": {
			`[{"entries":[{"content":{"cursorType":"Bottom","value":""}}]}]`, "",
		},
		"absent": {`[{"entries":[]}]`, ""},
		"TimelineReplaceEntry.entry is deliberately invisible": {
			`[{"type":"TimelineReplaceEntry","entry":{"entryId":"cursor-bottom-0","content":{"cursorType":"Bottom","value":"R"}}}]`, "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var instructions []instruction
			if err := json.Unmarshal([]byte(tc.body), &instructions); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if got := bottomCursorFromInstructions(instructions); got != tc.want {
				t.Errorf("bottomCursorFromInstructions = %q, want %q", got, tc.want)
			}
		})
	}
}

// bird's non---all `activity` quotes is a SINGLE page (getQuoteTweets ->
// fetchQuoteTweetsPage, no loop). Now that Search pages, QuoteTweets must not
// inherit that or activity silently starts fetching more than bird does.
func TestQuoteTweetsIssuesExactlyOneRequest(t *testing.T) {
	ps := newPagingServer(t, []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}, []pageSpec{
		{tweetIDs: seqIDs(1, 20), cursor: "C1"},
		{tweetIDs: seqIDs(21, 20), cursor: "C2"},
	})

	page, err := ps.client(t).QuoteTweets(context.Background(), "123", 40)
	if err != nil {
		t.Fatalf("QuoteTweets: %v", err)
	}
	if got := len(ps.timelineRequests()); got != 1 {
		t.Errorf("issued %d requests, want exactly 1", got)
	}
	if len(page.Tweets) != 20 {
		t.Errorf("got %d tweets, want the single page's 20", len(page.Tweets))
	}
}

// parseTimeline's existing behavior must survive the split into a page-aware
// core: root fallthrough, empty timelines, and X's own errors.
func TestParseTimelineUnchangedAfterRefactor(t *testing.T) {
	searchPath := []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}

	tweets, err := parseTimeline(timelineFixture(searchPath), opSearch.roots)
	if err != nil || len(tweets) != 1 || tweets[0].ID != "1" {
		t.Fatalf("parseTimeline = %v, %v", tweets, err)
	}

	altPath := []string{"user", "result", "timeline_v2", "timeline", "instructions"}
	if tweets, err := parseTimeline(timelineFixture(altPath), opUserTweets.roots); err != nil || len(tweets) != 1 {
		t.Errorf("alternate root broke: %v, %v", tweets, err)
	}

	if _, err := parseTimeline([]byte(`{"errors":[{"message":"Rate limit exceeded"}],"data":{}}`), opSearch.roots); err == nil {
		t.Error("X's error must still surface when no root matches")
	}
}

func TestMalformedTimelineEntryIsSkippedNotFatal(t *testing.T) {
	root := []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}
	var skipped []string
	prev := MalformedEntryHook
	MalformedEntryHook = func(id string) { skipped = append(skipped, id) }
	t.Cleanup(func() { MalformedEntryHook = prev })

	// Two identical scripted pages: the strict-default call consumes the
	// first, the opted-in call the second.
	page := pageSpec{tweetIDs: []string{"1", "2"}, malformed: 1}
	ps := newPagingServer(t, root, []pageSpec{page, page})
	// Default (monitoring contract): the page fails closed and nothing is
	// reported, so a malformed item can never become an empty success.
	if _, _, err := ps.client(t).SearchPageAlignedFrom(context.Background(), "q", 5, "", 1); err == nil {
		t.Fatal("strict default must reject the page")
	}
	if len(skipped) != 0 {
		t.Fatalf("strict default must not report skips, got %v", skipped)
	}

	// Opt-in (CLI bulk reads): the readable posts survive, the bad one is reported.
	c := ps.client(t)
	c.SetSkipUnreadablePosts(true)
	tweets, _, err := c.SearchPageAlignedFrom(context.Background(), "q", 5, "", 1)
	if err != nil {
		t.Fatalf("a single malformed entry must not fail the page when opted in: %v", err)
	}
	wantIDs(t, tweets, "1", "2")
	if len(skipped) != 1 || skipped[0] != "900" {
		t.Fatalf("hook should have seen the malformed id once, got %v", skipped)
	}
}
