package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// bird's getTweet has a second network call for articles: when TweetDetail
// returned a title but no body, it asks UserArticlesTweets for the plain text
// and overwrites the tweet's text with it. Without it, `read` on such an
// article prints only the headline.
//
// It is deliberately silent — any failure leaves the tweet as it was — so the
// tests cover both the success and the swallow.

// articleDetailBody is a TweetDetail response whose article has a title and no
// body of any kind: exactly the shape that triggers bird's fallback.
const articleDetailBody = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
  {"content":{"itemContent":{"tweet_results":{"result":{
    "rest_id":"555",
    "core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"XCreators","name":"Creators"}}}},
    "legacy":{"full_text":"https://t.co/short","conversation_id_str":"555"},
    "article":{"article_results":{"result":{"title":"Headline Only"}}}
  }}}}}
]}]}}}`

const userArticlesBody = `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[
  {"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"999","article":{"article_results":{"result":{"title":"Wrong","plain_text":"wrong body"}}}}}}}},
  {"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"555","article":{"article_results":{"result":{"title":"Headline Only","plain_text":"the recovered body"}}}}}}}}
]}]}}}}}}`

// articleFallbackClient routes TweetDetail and UserArticlesTweets to separate
// bodies and records how many times the fallback was asked for.
func articleFallbackClient(t *testing.T, articlesStatus int, articlesBody string) (*Client, *int32) {
	t.Helper()
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "UserArticlesTweets") {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(articlesStatus)
			_, _ = w.Write([]byte(articlesBody))
			return
		}
		_, _ = w.Write([]byte(articleDetailBody))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)
	return c, &calls
}

func TestReadRecoversArticleBodyFromUserArticlesTweets(t *testing.T) {
	c, calls := articleFallbackClient(t, http.StatusOK, userArticlesBody)

	tw, err := c.Tweet(context.Background(), "555")
	if err != nil {
		t.Fatalf("Tweet: %v", err)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("UserArticlesTweets called %d times, want 1", *calls)
	}
	if tw.Text != "Headline Only\n\nthe recovered body" {
		t.Errorf("text = %q, want the recovered body", tw.Text)
	}
	// The article header itself is unchanged by the fallback.
	if tw.Article == nil || tw.Article.Title != "Headline Only" {
		t.Errorf("article header changed: %+v", tw.Article)
	}
}

// bird swallows every failure here rather than failing the read.
func TestArticleFallbackFailureLeavesTheTweetAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"http error":     {http.StatusInternalServerError, `{"errors":[{"message":"nope"}]}`},
		"garbage":        {http.StatusOK, `not json`},
		"tweet missing":  {http.StatusOK, `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[]}}}}}}`},
		"no plain text":  {http.StatusOK, `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"555","article":{"article_results":{"result":{"title":"Headline Only"}}}}}}}}]}]}}}}}}`},
		"empty response": {http.StatusOK, `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := articleFallbackClient(t, tc.status, tc.body)
			tw, err := c.Tweet(context.Background(), "555")
			if err != nil {
				t.Fatalf("a failed article fallback must not fail the read: %v", err)
			}
			if tw.Text != "Headline Only" {
				t.Errorf("text = %q, want the unmodified title", tw.Text)
			}
		})
	}
}

// The fallback only fires when the detail response gave nothing but a title.
// An article that already rendered a body, and a tweet with no article at all,
// must not spend a second request.
func TestArticleFallbackIsNotCalledWhenTheBodyIsAlreadyThere(t *testing.T) {
	var calls int32
	const detail = `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"555",
	    "core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"a","name":"A"}}}},
	    "legacy":{"full_text":"https://t.co/short","conversation_id_str":"555"},
	    "article":{"article_results":{"result":{"title":"Headline","plain_text":"a real body"}}}
	  }}}}},
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"556",
	    "core":{"user_results":{"result":{"rest_id":"9001","legacy":{"screen_name":"a","name":"A"}}}},
	    "legacy":{"full_text":"an ordinary tweet","conversation_id_str":"555"}
	  }}}}}
	]}]}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "UserArticlesTweets") {
			atomic.AddInt32(&calls, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(detail))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)

	for _, id := range []string{"555", "556"} {
		if _, err := c.Tweet(context.Background(), id); err != nil {
			t.Fatalf("Tweet(%s): %v", id, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("UserArticlesTweets called %d times, want 0", got)
	}
}
