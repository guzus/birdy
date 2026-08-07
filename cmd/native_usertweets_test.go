package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// bird's user-tweets is the only command whose plain `-n` path can change its
// JSON shape: printTweetsResult wraps the array in {tweets, nextCursor}
// whenever `count > pageSize` (commands/user-tweets.js:84). Everything else —
// search, home, bookmarks, likes, list-timeline, mentions — is always a bare
// array. Live: `-n 20` is a JSON list, `-n 21` is a JSON dict.

// userTweetsServer serves n tweets per page plus an optional Bottom cursor.
func userTweetsServer(t *testing.T, cursors []string, perPage int) (*xapi.Client, *int32) {
	t.Helper()
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "UserByScreenName") {
			_, _ = w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"745273","legacy":{"screen_name":"naval","name":"Naval"}}}}}`))
			return
		}

		page := int(atomic.AddInt32(&calls, 1)) - 1
		var entries []string
		for i := 0; i < perPage; i++ {
			id := fmt.Sprint(page*perPage + i + 1)
			entries = append(entries, fmt.Sprintf(
				`{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"%s",`+
					`"core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"naval","name":"Naval"}}}},`+
					`"legacy":{"full_text":"t%s"}}}}}}`, id, id))
		}
		if page < len(cursors) && cursors[page] != "" {
			entries = append(entries, fmt.Sprintf(
				`{"content":{"cursorType":"Bottom","value":"%s"}}`, cursors[page]))
		}
		_, _ = w.Write([]byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[` +
			strings.Join(entries, ",") + `]}]}}}}}}`))
	}))
	t.Cleanup(srv.Close)

	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)
	// bird waits 1s between user-tweets pages; the tests assert the loop, not
	// the wait (internal/xapi covers that).
	c.SetUserTweetsPageDelay(0)
	return c, &calls
}

func runUserTweets(t *testing.T, c *xapi.Client, args nativeArgs) (string, error) {
	t.Helper()
	var out bytes.Buffer
	args.positional = "naval"
	args.command = "user-tweets"
	err := nativeUserTweets(context.Background(), c, args, &out)
	return out.String(), err
}

// -n 20 is one page and a bare array; -n 21 is two pages and the envelope.
func TestUserTweetsJSONShapeFlipsAboveOnePage(t *testing.T) {
	t.Run("bare array at 20", func(t *testing.T) {
		c, _ := userTweetsServer(t, []string{"C1", "C2"}, 20)
		got, err := runUserTweets(t, c, nativeArgs{count: 20, json: true, emoji: true})
		if err != nil {
			t.Fatalf("nativeUserTweets: %v", err)
		}
		var list []map[string]any
		if err := json.Unmarshal([]byte(got), &list); err != nil {
			t.Fatalf("-n 20 must stay a bare array, got:\n%s", got)
		}
		if len(list) != 20 {
			t.Errorf("got %d tweets, want 20", len(list))
		}
	})

	t.Run("envelope at 21", func(t *testing.T) {
		c, _ := userTweetsServer(t, []string{"C1", "C2"}, 20)
		got, err := runUserTweets(t, c, nativeArgs{count: 21, json: true, emoji: true})
		if err != nil {
			t.Fatalf("nativeUserTweets: %v", err)
		}

		var envelope struct {
			Tweets     []map[string]any `json:"tweets"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := json.Unmarshal([]byte(got), &envelope); err != nil {
			t.Fatalf("-n 21 must be {tweets, nextCursor}, got:\n%s", got)
		}
		if len(envelope.Tweets) != 21 {
			t.Errorf("got %d tweets, want 21", len(envelope.Tweets))
		}
		if envelope.NextCursor == nil || *envelope.NextCursor != "C2" {
			t.Errorf("nextCursor = %v, want C2", envelope.NextCursor)
		}
		// bird emits tweets first, nextCursor second. A Go map would sort the
		// keys and put nextCursor first.
		if strings.Index(got, `"tweets"`) > strings.Index(got, `"nextCursor"`) {
			t.Errorf("key order must be tweets then nextCursor, got:\n%s", got)
		}
	})
}

// bird emits `nextCursor: null` — present, not omitted — when the timeline ran
// out.
func TestUserTweetsEnvelopeEmitsNullCursor(t *testing.T) {
	c, _ := userTweetsServer(t, []string{"C1", ""}, 20)
	got, err := runUserTweets(t, c, nativeArgs{count: 21, json: true, emoji: true})
	if err != nil {
		t.Fatalf("nativeUserTweets: %v", err)
	}
	if !strings.Contains(got, `"nextCursor": null`) {
		t.Errorf("expected an explicit null cursor, got:\n%s", got)
	}
}

// bird validates the count before any network call and exits 2. birdy served
// -n 201 happily, returning one page.
func TestUserTweetsRejectsCountAboveTwoHundred(t *testing.T) {
	c, calls := userTweetsServer(t, []string{"C1"}, 20)
	_, err := runUserTweets(t, c, nativeArgs{count: 201, json: true, emoji: true})
	if err == nil {
		t.Fatal("expected -n 201 to be rejected")
	}
	const want = "Invalid --count. Max 200 tweets per run (safety cap: 10 pages). Use --cursor to continue."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Errorf("issued %d requests, want none before the validation", got)
	}
}

func TestUserTweetsAcceptsExactlyTwoHundred(t *testing.T) {
	c, _ := userTweetsServer(t, []string{"C1"}, 20)
	if _, err := runUserTweets(t, c, nativeArgs{count: 200, json: true, emoji: true}); err != nil {
		t.Errorf("-n 200 is bird's maximum and must be accepted: %v", err)
	}
}

// Non-JSON mode prints the resume hint to stderr, leaving stdout byte-identical.
func TestUserTweetsPrintsCursorHintToStderr(t *testing.T) {
	c, _ := userTweetsServer(t, []string{"C1", "C2"}, 20)

	var errBuf bytes.Buffer
	restore := nativeStderr
	nativeStderr = &errBuf
	t.Cleanup(func() { nativeStderr = restore })

	out, err := runUserTweets(t, c, nativeArgs{count: 21, emoji: true})
	if err != nil {
		t.Fatalf("nativeUserTweets: %v", err)
	}
	if strings.Contains(out, "More tweets available") {
		t.Error("the hint belongs on stderr; stdout must stay byte-identical to bird's")
	}
	const want = "ℹ️ More tweets available. Use --cursor \"C2\" to continue.\n"
	if errBuf.String() != want {
		t.Errorf("stderr = %q, want %q", errBuf.String(), want)
	}
}

func TestUserTweetsNoHintWhenExhausted(t *testing.T) {
	c, _ := userTweetsServer(t, []string{""}, 5)

	var errBuf bytes.Buffer
	restore := nativeStderr
	nativeStderr = &errBuf
	t.Cleanup(func() { nativeStderr = restore })

	if _, err := runUserTweets(t, c, nativeArgs{count: 20, emoji: true}); err != nil {
		t.Fatalf("nativeUserTweets: %v", err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("no cursor means no hint, got %q", errBuf.String())
	}
}

// The paging flags stay on the bird path; this patch must not make them look
// supported.
func TestPaginationFlagsStillFallBackToBird(t *testing.T) {
	for _, flag := range []string{"--all", "--max-pages", "--cursor", "--delay"} {
		if nativeSupportedFlags[flag] {
			t.Errorf("%s must not be in nativeSupportedFlags: native has no paged output shape for it", flag)
		}
		if nativeAcceptsFlags("user-tweets", []string{flag, "2"}) {
			t.Errorf("user-tweets %s must fall back to bird", flag)
		}
		if nativeAcceptsFlags("search", []string{flag, "2"}) {
			t.Errorf("search %s must fall back to bird", flag)
		}
	}
}

// `replies` takes no -n in bird, and birdy routes it without one; this pins
// that the paging change did not quietly add a count to it.
func TestRepliesHasNoCountRestriction(t *testing.T) {
	if _, ok := commandUnsupportedFlags["replies"]; ok {
		t.Error("replies gained an entry in commandUnsupportedFlags; that is a separate divergence, not this one")
	}
}
