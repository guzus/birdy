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
