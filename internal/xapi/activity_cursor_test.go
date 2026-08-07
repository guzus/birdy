package xapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bird's activity emits a nextCursor per section, and its non-paginated
// fetchers do return one (twitter-client-post-activity.js:57, :174). birdy
// discarded the cursor entries, so all three were hardcoded null in --json.

func actorTimelineBody(root, cursor string) string {
	entries := []string{
		`{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"1",` +
			`"legacy":{"screen_name":"a","name":"A","description":""}}}}}}`,
		`{"content":{"cursorType":"Top","value":"TOP"}}`,
	}
	if cursor != "" {
		entries = append(entries, `{"content":{"cursorType":"Bottom","value":"`+cursor+`"}}`)
	}
	return `{"data":{"` + root + `":{"timeline":{"instructions":[{"entries":[` +
		strings.Join(entries, ",") + `]}]}}}}`
}

func TestFavoritersAndRetweetersReportACursor(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		call       func(*Client) (*ActorPage, error)
	}{
		{"likes", "favoriters_timeline", func(c *Client) (*ActorPage, error) {
			return c.Favoriters(context.Background(), "1", 5)
		}},
		{"reposts", "retweeters_timeline", func(c *Client) (*ActorPage, error) {
			return c.Retweeters(context.Background(), "1", 5)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(actorTimelineBody(tc.root, "CUR-1")))
			}))
			t.Cleanup(srv.Close)

			c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
			if err != nil {
				t.Fatal(err)
			}
			c.SetBaseURL(srv.URL)

			page, err := tc.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(page.Users) != 1 || page.Users[0].Username != "a" {
				t.Errorf("users = %+v", page.Users)
			}
			if page.NextCursor != "CUR-1" {
				t.Errorf("nextCursor = %q, want CUR-1", page.NextCursor)
			}
			// The cursor entry must not be mistaken for a user.
			if len(page.Users) != 1 {
				t.Errorf("cursor entries leaked into the user list: %+v", page.Users)
			}
		})
	}
}

func TestActorTimelineWithoutACursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(actorTimelineBody("favoriters_timeline", "")))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	c.SetBaseURL(srv.URL)

	page, err := c.Favoriters(context.Background(), "1", 5)
	if err != nil {
		t.Fatalf("Favoriters: %v", err)
	}
	if page.NextCursor != "" {
		t.Errorf("nextCursor = %q, want empty", page.NextCursor)
	}
}

// bird's quote lookup is its own SearchTimeline call, not the generic search:
// querySource "tdqt" and product "Top", where birdy's Search sends
// "typed_query"/"Latest". The two return partially disjoint sets, observed live.
func TestQuoteTweetsUsesBirdsSearchVariables(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &seen)
		_, _ = w.Write([]byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[` +
			`{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"7",` +
			`"core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"q","name":"Q"}}}},` +
			`"legacy":{"full_text":"a quote"}}}}}},` +
			`{"content":{"cursorType":"Bottom","value":"QC1"}}` +
			`]}]}}}}}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	c.SetBaseURL(srv.URL)

	page, err := c.QuoteTweets(context.Background(), "123", 3)
	if err != nil {
		t.Fatalf("QuoteTweets: %v", err)
	}
	if len(page.Tweets) != 1 || page.Tweets[0].ID != "7" {
		t.Errorf("tweets = %+v", page.Tweets)
	}
	if page.NextCursor != "QC1" {
		t.Errorf("nextCursor = %q, want QC1", page.NextCursor)
	}

	want := map[string]any{
		"rawQuery":                               "quoted_tweet_id:123",
		"count":                                  float64(3),
		"querySource":                            "tdqt",
		"product":                                "Top",
		"withGrokTranslatedBio":                  true,
		"withQuickPromoteEligibilityTweetFields": false,
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("variables[%q] = %v, want %v (full: %v)", k, seen[k], v, seen)
		}
	}
	if _, present := seen["cursor"]; present {
		t.Error("the first page must omit the cursor key")
	}
}
