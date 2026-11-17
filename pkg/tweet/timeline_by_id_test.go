package tweet

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type timelineExchange struct {
	operation string
	identity  string
	cursor    string
	body      string
}

func timelineFixtureClient(t *testing.T, exchanges ...timelineExchange) *Client {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		op := path.Base(r.URL.Path)
		if index >= len(exchanges) {
			t.Errorf("unexpected request %d to %s", index+1, op)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		want := exchanges[index]
		var vars struct {
			UserID     string `json:"userId"`
			ScreenName string `json:"screen_name"`
			Cursor     string `json:"cursor"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("variables")), &vars); err != nil {
			t.Errorf("request %d variables: %v", index+1, err)
		}
		identity := vars.UserID
		if op == "UserByScreenName" {
			identity = vars.ScreenName
		}
		if op != want.operation || identity != want.identity || vars.Cursor != want.cursor {
			t.Errorf("request %d: got (%s, %q, %q), want (%s, %q, %q)",
				index+1, op, identity, vars.Cursor, want.operation, want.identity, want.cursor)
		}
		_, _ = io.WriteString(w, want.body)
	}))
	t.Cleanup(func() {
		server.Close()
		if n := int(calls.Load()); n != len(exchanges) {
			t.Errorf("got %d requests, want %d", n, len(exchanges))
		}
	})
	return monitoringClient(t, server.URL)
}

func TestUserTimelineByIDResumesAfterRename(t *testing.T) {
	const cursor = "opaque+/="
	c := timelineFixtureClient(t,
		timelineExchange{"UserTweets", "42", "", userTweetsBody(cursor, readableEntry("20001", "oldname"))},
		timelineExchange{"UserTweets", "42", cursor, userTweetsBody("", readableEntry("20002", "newname"))},
	)
	opts := UserTimelineOptions{Limit: 1, MaxPages: 1}
	first, err := c.UserTimelineByID(t.Context(), " 42 ", opts)
	if err != nil || first.NextCursor != cursor || len(first.Tweets) != 1 || first.Tweets[0].AuthorID != "42" {
		t.Fatalf("first page = %+v, err = %v", first, err)
	}
	opts.Cursor = first.NextCursor
	next, err := c.UserTimelineFullByID(t.Context(), "42", opts)
	if err != nil || next.NextCursor != "" || len(next.Tweets) != 1 || next.Tweets[0].ID != "20002" || next.Tweets[0].Author.Username != "newname" {
		t.Fatalf("resumed page = %+v, err = %v", next, err)
	}
}

func TestUserTimelineDigitsStillResolveAsHandle(t *testing.T) {
	c := timelineFixtureClient(t,
		timelineExchange{"UserByScreenName", "123", "", `{"data":{"user":{"result":{"rest_id":"42","legacy":{"screen_name":"123","name":"N"}}}}}`},
		timelineExchange{"UserTweets", "42", "", userTweetsBody("", readableEntry("20001", "123"))},
	)
	page, err := c.UserTimeline(t.Context(), "123", UserTimelineOptions{Limit: 1})
	if err != nil || len(page.Tweets) != 1 || page.Tweets[0].AuthorID != "42" {
		t.Fatalf("numeric handle page = %+v, err = %v", page, err)
	}
}

func TestUserTimelineByIDRejectsUnverifiedAuthors(t *testing.T) {
	for _, author := range []string{"", "7"} {
		t.Run(fmt.Sprintf("author_id_%s", author), func(t *testing.T) {
			entry := strings.Replace(readableEntry("20002", "samehandle"), `"rest_id":"42"`, fmt.Sprintf(`"rest_id":%q`, author), 1)
			reply := timelineExchange{"UserTweets", "42", "", userTweetsBody("NEXT", readableEntry("20001", "samehandle"), entry)}
			c := timelineFixtureClient(t, reply, reply)
			opts := UserTimelineOptions{Limit: 2}
			page, err := c.UserTimelineByID(t.Context(), "42", opts)
			if err == nil || len(page.Tweets) != 0 || page.NextCursor != "" {
				t.Fatalf("strict accepted unverified outer author: page = %+v, err = %v", page, err)
			}
			skipping, err := c.UserTimelineByIDSkippingUnreadable(t.Context(), "42", opts)
			if err == nil || len(skipping.Tweets) != 0 || skipping.NextCursor != "" || len(skipping.Skipped) != 0 {
				t.Fatalf("skipping accepted unverified outer author: page = %+v, err = %v", skipping, err)
			}
		})
	}
}

func TestUserTimelineByIDPreservesSkippingPolicy(t *testing.T) {
	reply := timelineExchange{"UserTweets", "42", "", userTweetsBody("NEXT", previewEntry("20001", "target"), readableEntry("20002", "target"))}
	c := timelineFixtureClient(t, reply, reply, reply)
	opts := UserTimelineOptions{Limit: 2}
	strict, err := c.UserTimelineFullByID(t.Context(), "42", opts)
	if err == nil || len(strict.Tweets) != 0 || strict.NextCursor != "" {
		t.Fatalf("strict = %+v, err = %v; want rejected preview", strict, err)
	}
	want := []SkippedPost{{ID: "20001", Reason: SkipPreviewOnly}}
	page, err := c.UserTimelineByIDSkippingUnreadable(t.Context(), "42", opts)
	if err != nil || page.NextCursor != "NEXT" || len(page.Tweets) != 1 || page.Tweets[0].ID != "20002" || !slices.Equal(page.Skipped, want) {
		t.Fatalf("skipping page = %+v, err = %v", page, err)
	}
	full, err := c.UserTimelineFullByIDSkippingUnreadable(t.Context(), "42", opts)
	if err != nil || full.NextCursor != page.NextCursor || len(full.Tweets) != 1 || full.Tweets[0].ID != page.Tweets[0].ID || !slices.Equal(full.Skipped, want) {
		t.Fatalf("full skipping page = %+v, err = %v", full, err)
	}
}

func TestUserTimelineByIDValidatesBeforeRequest(t *testing.T) {
	c := timelineFixtureClient(t)
	for _, id := range []string{"", "0", "042", "+42", "-42", "@42", "４２", "18446744073709551616"} {
		if _, err := c.UserTimelineByID(t.Context(), id, UserTimelineOptions{}); err == nil {
			t.Errorf("accepted invalid user ID %q", id)
		}
	}
	if _, err := c.UserTimelineByID(t.Context(), "42", UserTimelineOptions{Limit: 201}); err == nil {
		t.Error("accepted out-of-range limit")
	}
}
