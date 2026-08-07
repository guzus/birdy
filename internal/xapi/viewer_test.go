package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func newViewerTestClient(t *testing.T, endpoints []string) *Client {
	t.Helper()
	c, err := NewClient(Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetViewerEndpoints(endpoints)
	return c
}

func TestCurrentUserFallsThroughPartialResponses(t *testing.T) {
	var hits int32
	// settings.json shape: a screen_name but no numeric id. bird treats that as
	// a miss, and so must we, or `likes` gets a Viewer it cannot use.
	partial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"screen_name":"guzus"}`))
	}))
	defer partial.Close()

	full := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"id_str":"123","screen_name":"guzus","name":"Guzus"}`))
	}))
	defer full.Close()

	c := newViewerTestClient(t, []string{partial.URL, full.URL})
	viewer, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if viewer.ID != "123" || viewer.Username != "guzus" || viewer.Name != "Guzus" {
		t.Fatalf("unexpected viewer: %+v", viewer)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("expected both endpoints tried, got %d hits", got)
	}
}

func TestCurrentUserAcceptsNestedAndNumericIDs(t *testing.T) {
	cases := []struct {
		name, body, wantID, wantName string
	}{
		{"nested user object", `{"user":{"id_str":"9","screen_name":"a","name":"A"}}`, "9", "A"},
		{"numeric user_id", `{"user_id":42,"screen_name":"a","name":"A"}`, "42", "A"},
		{"user_id_str", `{"user_id_str":"7","screen_name":"a","name":"A"}`, "7", "A"},
		// bird falls back to the username when no display name is present.
		{"missing name", `{"id_str":"5","screen_name":"solo"}`, "5", "solo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			viewer, err := newViewerTestClient(t, []string{srv.URL}).CurrentUser(context.Background())
			if err != nil {
				t.Fatalf("CurrentUser: %v", err)
			}
			if viewer.ID != tc.wantID || viewer.Name != tc.wantName {
				t.Errorf("got %+v, want id=%s name=%s", viewer, tc.wantID, tc.wantName)
			}
		})
	}
}

func TestCurrentUserCaches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte(`{"id_str":"1","screen_name":"a","name":"A"}`))
	}))
	defer srv.Close()

	c := newViewerTestClient(t, []string{srv.URL})
	for range 3 {
		if _, err := c.CurrentUser(context.Background()); err != nil {
			t.Fatalf("CurrentUser: %v", err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected 1 request, got %d — the viewer should be memoized", got)
	}
}

// A 429 is the account's rate limit, not a bad endpoint. Continuing down the
// candidate list would spend the remaining ones to earn the same 429.
func TestCurrentUserStopsOnRateLimit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newViewerTestClient(t, []string{srv.URL, srv.URL, srv.URL})
	_, err := c.CurrentUser(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsRateLimited(err) {
		t.Errorf("expected a rate-limit error, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected to stop after the first 429, got %d hits", got)
	}
}

func TestCurrentUserReportsWhenNothingResolves(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := newViewerTestClient(t, []string{srv.URL}).CurrentUser(context.Background()); err == nil {
		t.Fatal("expected an error when no endpoint yields a username and id")
	}
}

// Every v1.1 account endpoint answers 404 for a cookie session as of
// 2026-08-07, so the settings-page scrape is the only path that resolves the
// viewer. This is a regression test for a real outage: the scrape was
// originally left unported on the theory that reading verify_credentials'
// top-level id_str made it redundant. It does not — the endpoint is gone, and
// whoami/likes/mentions/lists all broke against live X until it came back.
func TestCurrentUserFallsBackToSettingsPage(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"message":"Sorry, that page does not exist","code":34}]}`))
	}))
	defer dead.Close()

	var sawBearer bool
	settings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "" || r.Header.Get("x-csrf-token") != "" {
			sawBearer = true
		}
		w.Write([]byte(`<html><script>{"screen_name":"guzus","user_id":"123","name":"Guzus"}</script></html>`))
	}))
	defer settings.Close()

	c := newViewerTestClient(t, []string{dead.URL})
	c.SetSettingsPages([]string{settings.URL})

	viewer, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if viewer.Username != "guzus" || viewer.ID != "123" || viewer.Name != "Guzus" {
		t.Errorf("scraped viewer wrong: %+v", viewer)
	}
	// The settings page is served to a browser session; sending the API bearer
	// and CSRF headers gets it rejected.
	if sawBearer {
		t.Error("settings page must be fetched with cookies only, not API headers")
	}
}

func TestParseViewerHTML(t *testing.T) {
	// A display name with an escaped quote arrives JSON-escaped in the markup.
	viewer, ok := parseViewerHTML(`{"screen_name":"a","user_id":"1","name":"He said \"hi\""}`)
	if !ok {
		t.Fatal("expected a parse")
	}
	if viewer.Name != `He said "hi"` {
		t.Errorf("name = %q, want the unescaped form", viewer.Name)
	}

	// Without both a username and an id there is nothing usable.
	if _, ok := parseViewerHTML(`{"screen_name":"a"}`); ok {
		t.Error("a page with no user_id must not parse")
	}
	// Falls back to the username when no display name is present.
	viewer, _ = parseViewerHTML(`{"screen_name":"solo","user_id":"9"}`)
	if viewer.Name != "solo" {
		t.Errorf("name = %q, want the username fallback", viewer.Name)
	}
}
