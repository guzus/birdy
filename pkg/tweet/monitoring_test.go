package tweet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func monitoringClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	c, err := NewClient(Options{AccountsJSON: testAccounts, Account: "main"})
	if err != nil {
		t.Fatal(err)
	}
	account, _ := c.store.Get("main")
	api, err := c.apiClientFor(account)
	if err != nil {
		t.Fatal(err)
	}
	api.SetBaseURL(serverURL)
	api.SetUserListRESTPaths([]string{serverURL})
	api.SetUserTweetsPageDelay(0)
	return c
}

func TestFollowingReportsCompletenessAndResumeCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		cursor, _ := variables["cursor"].(string)
		if cursor == "" {
			_, _ = w.Write([]byte(followingBody([][2]string{{"1", "first"}}, "C1")))
			return
		}
		if cursor != "C1" {
			t.Errorf("cursor = %q, want C1", cursor)
		}
		_, _ = w.Write([]byte(followingBody([][2]string{{"1", "first"}, {"2", "second"}}, "")))
	}))
	defer server.Close()

	t.Run("walks to a complete snapshot", func(t *testing.T) {
		got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
		if err != nil {
			t.Fatalf("Following: %v", err)
		}
		if !got.Complete || got.NextCursor != "" || got.Pages != 2 || len(got.Users) != 2 || got.Users[0].ID != "1" || got.Users[1].ID != "2" {
			t.Fatalf("snapshot = %+v, want complete two-page result", got)
		}
	})

	t.Run("page cap is explicitly incomplete", func(t *testing.T) {
		got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{MaxPages: 1})
		if err != nil {
			t.Fatalf("Following: %v", err)
		}
		if got.Complete || got.NextCursor != "C1" || got.Pages != 1 || len(got.Users) != 1 {
			t.Fatalf("snapshot = %+v, want incomplete result with C1", got)
		}
	})

	t.Run("resumed suffix never claims full completeness", func(t *testing.T) {
		got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{Cursor: "C1"})
		if err != nil {
			t.Fatalf("Following: %v", err)
		}
		if got.Complete || got.NextCursor != "" || len(got.Users) != 2 || got.Users[0].ID != "1" || got.Users[1].ID != "2" {
			t.Fatalf("snapshot = %+v, want exhausted but partial suffix", got)
		}
	})
}

func followingBody(users [][2]string, cursor string) string {
	entries := make([]string, 0, len(users)+1)
	for _, user := range users {
		entries = append(entries, fmt.Sprintf(`{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":%q,"legacy":{"screen_name":%q,"name":%q,"followers_count":0,"friends_count":0}}}}}}`, user[0], user[1], strings.ToUpper(user[1])))
	}
	if cursor != "" {
		entries = append(entries, fmt.Sprintf(`{"content":{"cursorType":"Bottom","value":%q}}`, cursor))
	}
	return `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[` + strings.Join(entries, ",") + `]}]}}}}}}`
}

func TestUserTimelineForwardsOpaqueCursorAndReturnsStructuredRepost(t *testing.T) {
	var gotCursor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if op == "UserByScreenName" {
			_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}}}`))
			return
		}
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		if promoted, ok := variables["includePromotedContent"].(bool); !ok || promoted {
			t.Errorf("includePromotedContent = %v, want false", variables["includePromotedContent"])
		}
		gotCursor, _ = variables["cursor"].(string)
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[
			{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"RT @source: original","retweeted_status_result":{"result":{"rest_id":"1","core":{"user_results":{"result":{"rest_id":"7","legacy":{"screen_name":"source","name":"Source"}}}},"legacy":{"full_text":"original"}}}}}}}}},
			{"content":{"cursorType":"Bottom","value":"NEXT"}}
		] }]}}}}}}`))
	}))
	defer server.Close()

	c := monitoringClient(t, server.URL)
	got, err := c.UserTimeline(t.Context(), "@target", UserTimelineOptions{Limit: 1, Cursor: "OPAQUE", MaxPages: 1})
	if err != nil {
		t.Fatalf("UserTimeline: %v", err)
	}
	if gotCursor != "OPAQUE" {
		t.Errorf("request cursor = %q, want opaque cursor forwarded unchanged", gotCursor)
	}
	if got.NextCursor != "NEXT" || len(got.Tweets) != 1 || got.Tweets[0].RepostedTweet == nil || got.Tweets[0].RepostedTweet.ID != "1" {
		t.Fatalf("timeline = %+v, want structured repost and NEXT cursor", got)
	}
}

func TestUserTimelineRejectsForeignOuterAuthor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if op == "UserByScreenName" {
			_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[
			{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"9","core":{"user_results":{"result":{"rest_id":"7","legacy":{"screen_name":"advertiser","name":"Advertiser"}}}},"legacy":{"full_text":"promoted"}}}}}}
		]}]}}}}}}`))
	}))
	defer server.Close()

	page, err := monitoringClient(t, server.URL).UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 1})
	if err == nil || len(page.Tweets) != 0 || page.NextCursor != "" {
		t.Fatalf("foreign author page=%+v err=%v, want discarded snapshot", page, err)
	}
}

func TestUserRepliesUsesLatestSearchAndMapsReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if op != "SearchTimeline" {
			t.Errorf("operation = %q, want SearchTimeline", op)
		}
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		if got := variables["rawQuery"]; got != "from:target filter:replies" {
			t.Errorf("rawQuery = %v", got)
		}
		_, _ = w.Write([]byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[
			{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"a reply","conversation_id_str":"1","in_reply_to_status_id_str":"1"}}}}}},
			{"content":{"cursorType":"Bottom","value":"NEXT"}}
		]}]}}}}}`))
	}))
	defer server.Close()

	page, err := monitoringClient(t, server.URL).UserReplies(t.Context(), "@target", UserTimelineOptions{Limit: 1, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "NEXT" || len(page.Tweets) != 1 || !page.Tweets[0].IsReply() {
		t.Fatalf("reply page = %+v", page)
	}
}

func TestUserRepliesReadsReplacementEntryCursor(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		id := fmt.Sprint(hits)
		cursor := fmt.Sprintf("C%d", hits)
		_, _ = w.Write([]byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[
			{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"` + id + `","core":{"user_results":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"reply","conversation_id_str":"root","in_reply_to_status_id_str":"root"}}}}}}]},
			{"type":"TimelineReplaceEntry","entry":{"content":{"entryType":"TimelineTimelineCursor","cursorType":"Bottom","value":"` + cursor + `"}}}
		]}}}}}`))
	}))
	defer server.Close()

	page, err := monitoringClient(t, server.URL).UserReplies(t.Context(), "target", UserTimelineOptions{Limit: 2, MaxPages: 2})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 || len(page.Tweets) != 2 || page.NextCursor != "C2" {
		t.Fatalf("replacement paging hits=%d page=%+v", hits, page)
	}
}

func TestUserRepliesDefaultPageBudgetIsFinite(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		cursor := fmt.Sprintf("C%d", hits)
		_, _ = w.Write([]byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[` +
			`{"content":{"itemContent":{"__typename":"TimelineTweet","tweet_results":{}}}},` +
			fmt.Sprintf(`{"content":{"cursorType":"Bottom","value":%q}}`, cursor) +
			`] }]}}}}}`))
	}))
	defer server.Close()

	page, err := monitoringClient(t, server.URL).UserReplies(t.Context(), "target", UserTimelineOptions{Limit: 21})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 || len(page.Tweets) != 0 || page.NextCursor != "C2" {
		t.Fatalf("hits=%d page=%+v, want finite two-page budget ending at C2", hits, page)
	}
}

func TestMalformedTweetItemsCannotBecomeSuccessfulEmptyPages(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) (TimelinePage, error)
		body string
	}{
		{
			name: "posts",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","legacy":{"full_text":"text whose author shape moved"}}}}}}]}]}}}}}}`,
		},
		{
			name: "replies",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserReplies(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","legacy":{"full_text":"text whose author shape moved"}}}}}}]}]}}}}}`,
		},
		{
			name: "posts missing tweet_results",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"__typename":"TimelineTweet"}}}]}]}}}}}}`,
		},
		{
			name: "replies untyped empty tweet_results",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserReplies(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{}}}}]}]}}}}}`,
		},
		{
			name: "posts malformed quote relation",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","core":{"user_results":{"result":{"legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"quote"},"quoted_status_result":{}}}}}}]}]}}}}}}`,
		},
		{
			name: "replies malformed repost relation",
			call: func(c *Client) (TimelinePage, error) {
				return c.UserReplies(t.Context(), "target", UserTimelineOptions{Limit: 1})
			},
			body: `{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"2","core":{"user_results":{"result":{"legacy":{"screen_name":"target","name":"Target"}}}},"legacy":{"full_text":"reply","conversation_id_str":"1","in_reply_to_status_id_str":"1","retweeted_status_result":{}}}}}}]}]}}}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				if op == "UserByScreenName" {
					_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}}}`))
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			page, err := tc.call(monitoringClient(t, server.URL))
			if err == nil {
				t.Fatal("malformed tweet-shaped item was reported as a successful empty page")
			}
			if len(page.Tweets) != 0 || page.NextCursor != "" {
				t.Fatalf("error leaked partial page: %+v", page)
			}
		})
	}
}

func TestUserProfileMapsNativeLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","is_blue_verified":true,"legacy":{"screen_name":"target","name":"Target","description":"bio","followers_count":12,"friends_count":3,"statuses_count":44,"created_at":"2020"}}}}}`))
	}))
	defer server.Close()
	profile, err := monitoringClient(t, server.URL).UserProfile(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "42" || profile.Username != "target" || profile.Followers == nil || *profile.Followers != 12 || profile.Following == nil || *profile.Following != 3 || profile.Tweets == nil || *profile.Tweets != 44 || !profile.Verified {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestUserTimelineRejectsUnsafeBoundsBeforeNetwork(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UserTimeline(t.Context(), "target", UserTimelineOptions{Limit: 201}); err == nil {
		t.Fatal("limit above 200 must fail")
	}
	if _, err := c.Following(t.Context(), "", FollowingOptions{}); err == nil {
		t.Fatal("blank following user id must fail")
	}
}

func TestFollowingDiscardsPartialResultOnSecondPageFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		if variables["cursor"] == nil {
			_, _ = w.Write([]byte(followingBody([][2]string{{"1", "first"}}, "C1")))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
	if !IsRateLimited(err) {
		t.Fatalf("error = %v, want public rate-limit classification", err)
	}
	if len(got.Users) != 0 || got.Pages != 0 || got.Complete || got.NextCursor != "" {
		t.Fatalf("failed walk leaked partial snapshot: %+v", got)
	}
}

func TestFollowingRejectsCursorCycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		cursor, _ := variables["cursor"].(string)
		if cursor == "" {
			_, _ = w.Write([]byte(followingBody([][2]string{{"1", "first"}}, "C1")))
			return
		}
		_, _ = w.Write([]byte(followingBody([][2]string{{"2", "second"}}, "C1")))
	}))
	defer server.Close()

	got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
	if err == nil || !strings.Contains(err.Error(), "cursor did not advance") {
		t.Fatalf("cycle error = %v", err)
	}
	if len(got.Users) != 0 {
		t.Fatalf("cycle leaked partial users: %+v", got.Users)
	}
}

func TestFollowingMalformedUserCannotBecomeCompleteEmptySnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(followingBodyRaw(`{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"7"}}}}}`)))
	}))
	defer server.Close()

	got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
	if err == nil {
		t.Fatal("malformed user entry was reconciled as success")
	}
	if got.Complete || len(got.Users) != 0 || got.Pages != 0 {
		t.Fatalf("malformed response leaked a mass-unfollow-shaped snapshot: %+v", got)
	}
}

func TestFollowingNestedModulesCannotHideGraphMembership(t *testing.T) {
	tests := []struct {
		name      string
		entry     string
		wantUser  string
		wantError bool
	}{
		{
			name: "valid nested user is preserved",
			entry: `{"content":{"entryType":"TimelineTimelineModule","items":[
				{"item":{"itemContent":{"user_results":{"result":{
					"__typename":"User","rest_id":"8","legacy":{"screen_name":"nested","name":"Nested"}
				}}}}}
			]}}`,
			wantUser: "nested",
		},
		{
			name: "malformed nested user fails closed",
			entry: `{"content":{"entryType":"TimelineTimelineModule","items":[
				{"item":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"8"}}}}}
			]}}`,
			wantError: true,
		},
		{
			name: "known nested decoration is empty",
			entry: `{"content":{"entryType":"TimelineTimelineModule","items":[
				{"itemContent":{"__typename":"TimelineMessagePrompt"}}
			]}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(followingBodyRaw(tc.entry)))
			}))
			defer server.Close()

			got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
			if tc.wantError {
				if err == nil || got.Complete || len(got.Users) != 0 || got.Pages != 0 {
					t.Fatalf("malformed module became a completed graph: got=%+v err=%v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.Complete {
				t.Fatalf("exhausted module page not complete: %+v", got)
			}
			if tc.wantUser == "" {
				if len(got.Users) != 0 {
					t.Fatalf("decoration produced users: %+v", got.Users)
				}
			} else if len(got.Users) != 1 || got.Users[0].Username != tc.wantUser {
				t.Fatalf("nested user lost: %+v", got.Users)
			}
		})
	}
}

func TestFollowingPreservesOptionalFieldPresence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(followingBodyRaw(`
			{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"1","legacy":{"screen_name":"omitted","name":"Omitted"}}}}}},
			{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"2","is_blue_verified":false,"legacy":{"screen_name":"zero","name":"Zero","followers_count":0,"friends_count":0}}}}}}
		`)))
	}))
	defer server.Close()

	got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
	if err != nil || !got.Complete || len(got.Users) != 2 {
		t.Fatalf("following = %+v, %v", got, err)
	}
	if got.Users[0].FollowersCount != nil || got.Users[0].FollowingCount != nil || got.Users[0].IsBlueVerified != nil {
		t.Fatalf("omitted fields became values: %+v", got.Users[0])
	}
	if got.Users[1].FollowersCount == nil || *got.Users[1].FollowersCount != 0 || got.Users[1].FollowingCount == nil || *got.Users[1].FollowingCount != 0 || got.Users[1].IsBlueVerified == nil || *got.Users[1].IsBlueVerified {
		t.Fatalf("explicit zero/false lost: %+v", got.Users[1])
	}
}

func TestFollowingUnavailableIdentityCannotBecomeCompleteEmptySnapshot(t *testing.T) {
	var restHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user_id") != "" {
			restHits++
			_, _ = w.Write([]byte(`{"users":[],"next_cursor_str":"0"}`))
			return
		}
		_, _ = w.Write([]byte(followingBodyRaw(`{"content":{"itemContent":{"user_results":{"result":{"__typename":"UserUnavailable"}}}}}`)))
	}))
	defer server.Close()

	got, err := monitoringClient(t, server.URL).Following(t.Context(), "42", FollowingOptions{})
	if err == nil {
		t.Fatal("unavailable following identity was reconciled as success")
	}
	if restHits != 0 {
		t.Fatalf("schema error fell through to REST %d times", restHits)
	}
	if got.Complete || len(got.Users) != 0 || got.Pages != 0 {
		t.Fatalf("unavailable response leaked a mass-unfollow-shaped snapshot: %+v", got)
	}
}

func TestFollowingRESTMalformedIdentityCannotBecomeCompleteEmptySnapshot(t *testing.T) {
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer graph.Close()
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"users":[{"screen_name":"missing-id"}],"next_cursor_str":"0"}`))
	}))
	defer rest.Close()

	c := monitoringClient(t, graph.URL)
	account, _ := c.store.Get("main")
	api, _ := c.apiClientFor(account)
	api.SetUserListRESTPaths([]string{rest.URL})
	got, err := c.Following(t.Context(), "42", FollowingOptions{})
	if err == nil {
		t.Fatal("malformed REST following identity was reconciled as success")
	}
	if got.Complete || len(got.Users) != 0 || got.Pages != 0 {
		t.Fatalf("malformed REST response leaked a mass-unfollow-shaped snapshot: %+v", got)
	}
}

func followingBodyRaw(entries string) string {
	return `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[` + entries + `]}]}}}}}}`
}

func TestFollowingContextCancellationDiscardsPartialResult(t *testing.T) {
	second := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var variables map[string]any
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables)
		if variables["cursor"] == nil {
			_, _ = w.Write([]byte(followingBody([][2]string{{"1", "first"}}, "C1")))
			return
		}
		close(second)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	client := monitoringClient(t, server.URL)
	done := make(chan struct{})
	var (
		got FollowingSnapshot
		err error
	)
	go func() {
		defer close(done)
		got, err = client.Following(ctx, "42", FollowingOptions{})
	}()
	select {
	case <-second:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("second page never started")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not abort following walk")
	}
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation error = %v", err)
	}
	if len(got.Users) != 0 {
		t.Fatalf("cancellation leaked partial snapshot: %+v", got)
	}
}

func TestConcurrentCallsReserveDifferentAccounts(t *testing.T) {
	var (
		mu     sync.Mutex
		tokens []string
	)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("x-csrf-token"))
		mu.Unlock()
		arrived <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}}}`))
	}))
	defer server.Close()

	c, err := NewClient(Options{AccountsJSON: testAccounts, Strategy: "quota-aware"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range c.Accounts() {
		account, _ := c.store.Get(name)
		api, _ := c.apiClientFor(account)
		api.SetBaseURL(server.URL)
	}

	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := c.UserProfile(t.Context(), "target")
			errs <- err
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("synchronized calls did not both arrive")
		}
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(tokens)
	if strings.Join(tokens, ",") != "ct1,ct2" {
		t.Fatalf("reserved accounts = %v, want one call per account", tokens)
	}
}

func TestHTTP200RateLimitCoolsAccountAndPublicClassifierWorks(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-csrf-token")
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
		if token == "ct1" {
			_, _ = w.Write([]byte(`{"errors":[{"code":88,"message":"Rate limit exceeded"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"target","name":"Target"}}}}}`))
	}))
	defer server.Close()

	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range c.Accounts() {
		account, _ := c.store.Get(name)
		api, _ := c.apiClientFor(account)
		api.SetBaseURL(server.URL)
	}
	if _, err := c.UserProfile(t.Context(), "target"); !IsRateLimited(err) {
		t.Fatalf("first error = %v, want rate limit", err)
	}
	profile, err := c.UserProfile(t.Context(), "target")
	if err != nil || profile.ID != "42" {
		t.Fatalf("second call = %+v, %v", profile, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(tokens, ",") != "ct1,ct2" {
		t.Fatalf("tokens = %v, want cooled ct1 then ct2", tokens)
	}
}

func TestPublicReaderDependencyGraphExcludesLegacyRunner(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := string(out)
	for _, forbidden := range []string{"github.com/guzus/birdy/internal/runner", "@steipete/bird"} {
		if strings.Contains(deps, forbidden) {
			t.Fatalf("pkg/tweet dependency graph contains legacy runner %q", forbidden)
		}
	}
}

func TestMonitoringJSONContract(t *testing.T) {
	zero := 0
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"timeline", TimelinePage{Tweets: []TimelineTweet{}, NextCursor: "C"}, `{"tweets":[],"nextCursor":"C"}`},
		{"following", FollowingSnapshot{Users: []FollowingUser{{ID: "1", Username: "u", Name: "U", FollowersCount: &zero}}, NextCursor: "C", Pages: 1}, `{"users":[{"id":"1","username":"u","name":"U","followersCount":0}],"nextCursor":"C","complete":false,"pages":1}`},
		{"profile", UserProfile{ID: "1", Username: "u", Name: "U", Followers: &zero}, `{"id":"1","username":"u","name":"U","followers":0,"verified":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("JSON = %s, want %s", got, tc.want)
			}
		})
	}
}
