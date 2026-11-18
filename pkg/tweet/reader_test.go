package tweet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNoCookieReader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", "malformed accounts must never be opened")
	opts := ReaderOptions{NoCookie: true, AccountOptions: Options{AccountsJSON: "invalid"}}
	reader, err := NewReader(opts)
	if err != nil {
		t.Fatal(err)
	}
	hosted, ok := reader.(publicReader)
	if !ok || hosted.endpoint != "https://birdy.guzus.xyz/v1/posts/" || hosted.client.Jar != nil {
		t.Fatal("expected cookie-free hosted client")
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/posts/20" || r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Invite-Code") != "" {
			t.Errorf("unexpected public request: %s %s", r.Method, r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "ignored", Value: "test"})
		fmt.Fprint(w, `{"id":"20","text":"hello","author_handle":"alice","author_name":"Alice"}`)
	}))
	defer srv.Close()
	hosted.endpoint = srv.URL + "/v1/posts/"
	for _, ref := range []string{"20", "https://x.com/alice/status/20?s=20"} {
		post, err := hosted.Read(t.Context(), ref)
		if err != nil || post.ID != "20" || post.Text != "hello" || post.Author.Name != "Alice" || post.URL() != "https://x.com/alice/status/20" {
			t.Fatalf("read %s = %+v, %v", ref, post, err)
		}
	}
	for _, ref := range []string{"0", "01", "18446744073709551616", "https://example.com/alice/status/20"} {
		if _, err := hosted.Read(t.Context(), ref); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
	if calls != 2 {
		t.Errorf("got %d network calls", calls)
	}
	if failed, err := NewReader(ReaderOptions{AccountOptions: Options{AccountsJSON: "invalid"}}); failed != nil || err == nil {
		t.Fatalf("failed constructor = %v, %v", failed, err)
	}
	native, err := NewReader(ReaderOptions{AccountOptions: Options{AccountsJSON: testAccounts}})
	if _, ok := native.(*Client); err != nil || !ok {
		t.Fatalf("expected native client: %T %v", native, err)
	}
}

func TestPublicReaderFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		response string
	}{
		{"missing", 404, "{}"}, {"quota", 429, "{}"}, {"down", 503, "{}"}, {"mismatch", 200, `{"id":"21","author_handle":"alice"}`}, {"malformed", 200, "{}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.response)
			}))
			defer s.Close()
			r := publicReader{client: s.Client(), endpoint: s.URL + "/"}
			p, err := r.Read(t.Context(), "20")
			if p != nil || err == nil {
				t.Fatalf("expected error, got %+v %v", p, err)
			}
			var apiErr *PublicPostError
			if tt.status != 200 && (!errors.As(err, &apiErr) || apiErr.StatusCode != tt.status || apiErr.RetryAfter != 12*time.Second) {
				t.Errorf("unexpected HTTP error: %v", err)
			}
			if IsRateLimited(fmt.Errorf("outer: %w", err)) != (tt.status == 429) {
				t.Errorf("wrong limit classification: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := r.Read(ctx, "20"); !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation: %v", err)
			}
		})
	}
}
