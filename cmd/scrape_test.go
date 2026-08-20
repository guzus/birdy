package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/scrape"
	"github.com/guzus/birdy/internal/xapi"
)

type fakeScrapeFetcher struct {
	searches  []xapi.SearchProduct
	tweets    map[string]xapi.Tweet
	profiles  map[string][]xapi.Tweet
	queries   map[string][]xapi.Tweet
	replies   map[string][]xapi.Tweet
	actors    map[string][]xapi.ListedUser
	searchErr error
}

func (f *fakeScrapeFetcher) SearchWith(_ context.Context, query string, _ int, product xapi.SearchProduct) ([]xapi.Tweet, error) {
	f.searches = append(f.searches, product)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return append([]xapi.Tweet(nil), f.queries[query]...), nil
}
func (f *fakeScrapeFetcher) UserTweets(_ context.Context, handle string, _ int) ([]xapi.Tweet, string, error) {
	return append([]xapi.Tweet(nil), f.profiles[handle]...), "", nil
}
func (f *fakeScrapeFetcher) Tweet(_ context.Context, id string) (*xapi.Tweet, error) {
	t, ok := f.tweets[id]
	if !ok {
		return nil, fmt.Errorf("missing tweet %s", id)
	}
	return &t, nil
}
func (f *fakeScrapeFetcher) Conversation(_ context.Context, id string) ([]xapi.Tweet, error) {
	return []xapi.Tweet{f.tweets[id]}, nil
}
func (f *fakeScrapeFetcher) Replies(_ context.Context, id string) ([]xapi.Tweet, error) {
	return append([]xapi.Tweet(nil), f.replies[id]...), nil
}
func (f *fakeScrapeFetcher) QuoteTweets(context.Context, string, int) (*xapi.QuotePage, error) {
	return &xapi.QuotePage{}, nil
}
func (f *fakeScrapeFetcher) ListTimeline(context.Context, string, int) ([]xapi.Tweet, error) {
	return nil, nil
}
func (f *fakeScrapeFetcher) Likes(context.Context, string, int) ([]xapi.Tweet, error) {
	return nil, nil
}
func (f *fakeScrapeFetcher) Favoriters(context.Context, string, int) (*xapi.ActorPage, error) {
	return &xapi.ActorPage{Users: f.actors["favoriters"]}, nil
}
func (f *fakeScrapeFetcher) Retweeters(context.Context, string, int) (*xapi.ActorPage, error) {
	return &xapi.ActorPage{Users: f.actors["retweeters"]}, nil
}

func TestCollectScrapeRowsDedupsAndCaps(t *testing.T) {
	fake := &fakeScrapeFetcher{
		profiles: map[string][]xapi.Tweet{
			"nasa": {
				{ID: "1", Text: "one", Author: xapi.Author{Username: "nasa"}},
				{ID: "2", Text: "two", Author: xapi.Author{Username: "nasa"}},
			},
		},
		queries: map[string][]xapi.Tweet{
			"moon": {
				{ID: "2", Text: "two again", Author: xapi.Author{Username: "nasa"}},
				{ID: "3", Text: "three", Author: xapi.Author{Username: "nasa"}},
			},
		},
	}
	jobs := []scrape.Job{
		{Kind: scrape.KindProfile, Value: "nasa", Limit: 20},
		{Kind: scrape.KindSearch, Value: "moon", Query: "moon", Limit: 20, Sort: scrape.SortLatest},
	}
	rows, err := collectScrapeRows(context.Background(), fake, jobs, scrape.Request{MaxItems: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "1" || rows[1].ID != "2" {
		t.Fatalf("rows = %+v, want ids 1 then 2", rows)
	}
}

func TestCollectScrapeRowsSearchBothMerges(t *testing.T) {
	fake := &fakeScrapeFetcher{
		queries: map[string][]xapi.Tweet{
			"AI": {
				{ID: "10", Text: "latest", Author: xapi.Author{Username: "a"}},
			},
		},
	}
	// SearchWith is called twice with the same query map, so both products
	// return the same tweet; merge should keep one.
	rows, err := collectScrapeRows(context.Background(), fake, []scrape.Job{{
		Kind: scrape.KindSearch, Query: "AI", Sort: scrape.SortBoth, Limit: 10,
	}}, scrape.Request{MaxItems: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "10" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(fake.searches) != 2 || fake.searches[0] != xapi.SearchLatest || fake.searches[1] != xapi.SearchTop {
		t.Fatalf("products = %v, want Latest then Top", fake.searches)
	}
}

func TestCollectScrapeRowsDirectRepliesOnly(t *testing.T) {
	fake := &fakeScrapeFetcher{
		replies: map[string][]xapi.Tweet{
			"100": {
				{ID: "101", Text: "direct", InReplyToStatusID: "100", Author: xapi.Author{Username: "a"}},
				{ID: "102", Text: "nested", InReplyToStatusID: "101", Author: xapi.Author{Username: "b"}},
			},
		},
	}
	rows, err := collectScrapeRows(context.Background(), fake, []scrape.Job{{
		Kind: scrape.KindRepliesJob, Value: "100", Limit: 20,
	}}, scrape.Request{MaxItems: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "101" {
		t.Fatalf("rows = %+v, want only the direct reply", rows)
	}
}

func TestCollectScrapeRowsKeepsLatestWhenTopFails(t *testing.T) {
	fake := &searchErrAfterFirst{
		fakeScrapeFetcher: &fakeScrapeFetcher{
			queries: map[string][]xapi.Tweet{
				"AI": {{ID: "10", Text: "latest", Author: xapi.Author{Username: "a"}}},
			},
		},
		failOn: 2,
		err:    errors.New("top failed"),
	}
	rows, err := collectScrapeRows(context.Background(), fake, []scrape.Job{{
		Kind: scrape.KindSearch, Query: "AI", Sort: scrape.SortBoth, Limit: 10,
	}}, scrape.Request{MaxItems: 10})
	if err == nil {
		t.Fatal("expected top error")
	}
	if len(rows) != 1 || rows[0].ID != "10" {
		t.Fatalf("latest rows discarded on top error: rows=%+v err=%v", rows, err)
	}
}

type searchErrAfterFirst struct {
	*fakeScrapeFetcher
	n      int
	failOn int
	err    error
}

func (f *searchErrAfterFirst) SearchWith(ctx context.Context, query string, count int, product xapi.SearchProduct) ([]xapi.Tweet, error) {
	f.n++
	if f.n == f.failOn {
		return nil, f.err
	}
	return f.fakeScrapeFetcher.SearchWith(ctx, query, count, product)
}

func TestWriteScrapeOutputJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeScrapeOutput(&buf, []scrape.Row{{ID: "1", Text: "hi"}}, "json"); err != nil {
		t.Fatal(err)
	}
	var rows []scrape.Row
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "1" {
		t.Fatalf("decoded %v", rows)
	}
}

func TestScrapeHelpIsRegistered(t *testing.T) {
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		resetScrapeFlags()
	})
	rootCmd.SetArgs([]string{"scrape", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String()+errOut.String(), "structured filters") &&
		!strings.Contains(out.String()+errOut.String(), "mixed targets") {
		t.Fatalf("help missing scrape description: %s%s", out.String(), errOut.String())
	}
}
