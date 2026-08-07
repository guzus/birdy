package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func mutateClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := NewClient(Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetBaseURL(srv.URL)
	return c
}

// A 404 on the per-operation URL means the hash rotated. X still accepts the
// same payload at the bare GraphQL root, where the query id in the body routes
// it — so the fallback must be tried before giving up.
func TestGraphQLMutateFallsBackToTheRoot(t *testing.T) {
	var paths []string
	c := mutateClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		// Anything with an operation segment 404s; the bare root succeeds.
		if strings.Contains(r.URL.Path, "CreateTweet") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":{"create_tweet":{"tweet_results":{"result":{"rest_id":"7"}}}}}`))
	})

	id, err := c.CreateTweet(context.Background(), "hi", "")
	if err != nil {
		t.Fatalf("CreateTweet: %v", err)
	}
	if id != "7" {
		t.Errorf("id = %q, want 7", id)
	}
	if len(paths) < 2 {
		t.Fatalf("expected a fallback request, got paths %v", paths)
	}
}

// A non-404 failure is a real error. Retrying it risks a duplicate post, so it
// must surface after exactly one attempt.
func TestGraphQLMutateDoesNotRetryNon404(t *testing.T) {
	var hits atomic.Int32
	c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errors":[{"message":"nope"}]}`))
	})

	if _, err := c.CreateTweet(context.Background(), "hi", ""); err == nil {
		t.Fatal("expected an error")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("made %d requests; a write must be attempted once", got)
	}
}

func TestCreateTweetRejectsEmptyText(t *testing.T) {
	var hits atomic.Int32
	c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{}`))
	})

	if _, err := c.CreateTweet(context.Background(), "   ", ""); err == nil {
		t.Fatal("expected an error for whitespace-only text")
	}
	if hits.Load() != 0 {
		t.Error("empty text must not reach X")
	}
}

func TestParseCreatedTweetIDSurfacesErrorCodes(t *testing.T) {
	_, err := parseCreatedTweetID([]byte(`{"errors":[{"message":"duplicate","code":187}]}`))
	if err == nil {
		t.Fatal("expected an error")
	}
	// bird formats a coded error as "message (code)"; the code is the part that
	// tells a caller a duplicate from a rate limit.
	if !strings.Contains(err.Error(), "duplicate (187)") {
		t.Errorf("got %v, want the code included", err)
	}
}

// Code 160 means the requested end state already holds. For an idempotent verb
// that is success, not failure — following someone already followed is a no-op.
func TestFollowTreatsAlreadyFollowingAsSuccess(t *testing.T) {
	c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":[{"code":160,"message":"You've already requested to follow"}]}`))
	})
	c.SetFriendshipEndpoints([]string{c.baseURL + "/friendships/create.json"})

	if _, err := c.Follow(context.Background(), "123"); err != nil {
		t.Errorf("code 160 should be success, got %v", err)
	}
}

// A definite answer must not fall through to the GraphQL retry, which would
// report the same thing less clearly and spend another request.
func TestFollowStopsOnTerminalErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"blocked", `{"errors":[{"code":162,"message":"blocked"}]}`, "blocked from following"},
		{"not found", `{"errors":[{"code":108,"message":"no such user"}]}`, "User not found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(tc.body))
			})
			c.SetFriendshipEndpoints([]string{c.baseURL + "/friendships/create.json"})

			_, err := c.Follow(context.Background(), "123")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want it to mention %q", err, tc.want)
			}
			if got := hits.Load(); got != 1 {
				t.Errorf("a terminal answer must not retry; got %d requests", got)
			}
		})
	}
}

func TestFollowReturnsCanonicalScreenName(t *testing.T) {
	c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id_str":"123","screen_name":"Guzus"}`))
	})
	c.SetFriendshipEndpoints([]string{c.baseURL + "/friendships/create.json"})

	name, err := c.Follow(context.Background(), "123")
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if name != "Guzus" {
		t.Errorf("screen name = %q, want the canonical casing X returned", name)
	}
}

// An errors array alongside a 200 is still a failure.
func TestDeleteBookmarkSurfaces200Errors(t *testing.T) {
	c := mutateClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"not bookmarked"}]}`))
	})
	if err := c.DeleteBookmark(context.Background(), "1900000000000000001"); err == nil {
		t.Fatal("expected an error reported alongside HTTP 200")
	}
}
