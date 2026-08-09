package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// conversationServer serves a fixed TweetDetail body for every request.
func conversationServer(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)
	return c
}

func repliesIDs(t *testing.T, c *Client, focal string) []string {
	t.Helper()
	tweets, err := c.Replies(context.Background(), focal)
	if err != nil {
		t.Fatalf("Replies(%s) returned error: %v", focal, err)
	}
	if tweets == nil {
		t.Fatalf("Replies(%s) returned a nil slice; it must stay non-nil so --json prints [] not null", focal)
	}
	ids := make([]string, 0, len(tweets))
	for _, tw := range tweets {
		ids = append(ids, tw.ID)
	}
	return ids
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got ids %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got ids %v, want %v", got, want)
		}
	}
}

// bird's `replies` is a positive selection — tweets whose
// in_reply_to_status_id_str equals the focal id — not "the conversation minus
// the focal tweet and its ancestors". The two agree only when the conversation
// is exactly one level deep. The fixture's 102 replies to 101, so a subtraction
// leaks it into the replies of 100.
func TestRepliesReturnsOnlyDirectReplies(t *testing.T) {
	c := conversationServer(t, conversationFixture)

	assertIDs(t, repliesIDs(t, c, "100"), []string{"101"})
	// From 101 the only direct reply is 102 — the root 100 is an ancestor and
	// must not appear.
	assertIDs(t, repliesIDs(t, c, "101"), []string{"102"})
	// A leaf has no replies at all, and the result must still be non-nil.
	assertIDs(t, repliesIDs(t, c, "102"), nil)
}

// bird's replies preserves X's entry order and never sorts (unlike its
// `thread`, which sorts by createdAt). A filter over the parsed conversation
// reproduces that; anything that reorders does not.
func TestRepliesPreservesEntryOrder(t *testing.T) {
	const fixture = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"100",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"root","name":"Root"}}}},
	    "legacy":{"full_text":"root","conversation_id_str":"100","created_at":"Wed Aug 05 07:00:00 +0000 2026"}}}}}},
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"103",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"b","name":"B"}}}},
	    "legacy":{"full_text":"later but listed first","conversation_id_str":"100","in_reply_to_status_id_str":"100","created_at":"Wed Aug 05 09:00:00 +0000 2026"}}}}}},
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"101",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"a","name":"A"}}}},
	    "legacy":{"full_text":"earlier but listed second","conversation_id_str":"100","in_reply_to_status_id_str":"100","created_at":"Wed Aug 05 08:00:00 +0000 2026"}}}}}}
	]}]}}}`

	c := conversationServer(t, fixture)
	assertIDs(t, repliesIDs(t, c, "100"), []string{"103", "101"})
}

// A direct reply can be deleted while its own reply survives. X still renders
// the module, with a tombstone where the parent was. bird's filter drops the
// orphaned grandchild because its parent is the dead tweet, not the focal one.
// A subtraction has nothing to exclude it with and emits it as a "reply".
func TestRepliesDropsOrphanedGrandchildren(t *testing.T) {
	const fixture = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"100",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"root","name":"Root"}}}},
	    "legacy":{"full_text":"root","conversation_id_str":"100"}}}}}},
	  {"content":{"itemContent":{"__typename":"TimelineTweet","tweet_results":{}}}},
	  {"content":{"items":[{"item":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"102",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"c","name":"C"}}}},
	    "legacy":{"full_text":"reply to a deleted reply","conversation_id_str":"100","in_reply_to_status_id_str":"101"}}}}}}]}}
	]}]}}}`

	c := conversationServer(t, fixture)
	assertIDs(t, repliesIDs(t, c, "100"), nil)
}

// The argument is trimmed before the request, so the filter must compare
// against the same trimmed id or a padded argument fetches fine and matches
// nothing.
func TestRepliesTrimsTheFocalID(t *testing.T) {
	c := conversationServer(t, conversationFixture)
	assertIDs(t, repliesIDs(t, c, "  100  "), []string{"101"})
}
