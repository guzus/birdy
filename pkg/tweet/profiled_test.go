package tweet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const profiledNode = `{"rest_id":"10000","core":{"user_results":{"result":{"rest_id":"9001","core":{"screen_name":"outer","name":"Outer"},"avatar":{"image_url":"https://pbs.twimg.com/profile_images/1/outer.jpg"}}}},"legacy":{"full_text":"outer post","retweeted_status_result":{"result":{"rest_id":"30000","core":{"user_results":{"result":{"rest_id":"9003","core":{"screen_name":"original","name":"Original"},"avatar":{"image_url":"https://pbs.twimg.com/profile_images/3/original.jpg"}}}},"legacy":{"full_text":"original post"},"quoted_status_result":{"result":{"rest_id":"40000","core":{"user_results":{"result":{"rest_id":"9004","core":{"screen_name":"original_quote","name":"Original quote"},"avatar":{"image_url":"https://pbs.twimg.com/profile_images/4/original-quote.jpg"}}}},"legacy":{"full_text":"original quoted post"}}}}}},"quoted_status_result":{"result":{"rest_id":"20000","core":{"user_results":{"result":{"rest_id":"9002","legacy":{"screen_name":"quoted","name":"Quoted","profile_image_url_https":"https://pbs.twimg.com/profile_images/2/quoted.jpg"}}}},"legacy":{"full_text":"quoted post"}}}}`

func TestProfiledReadsKeepAuthorPhotosWithoutExtraRequests(t *testing.T) {
	t.Setenv("BIRDY_QUOTE_DEPTH", "1")
	for _, mode := range []string{"read", "search"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			entry := `{"content":{"itemContent":{"tweet_results":{"result":` + profiledNode + `}}}}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if mode == "read" && strings.Contains(r.URL.Path, "TweetDetail") {
					fmt.Fprint(w, `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[`+entry+`]}]}}}`)
				} else if mode == "search" && strings.Contains(r.URL.Path, "SearchTimeline") {
					fmt.Fprint(w, `{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[`+entry+`]}]}}}}}`)
				} else {
					t.Errorf("unexpected extra lookup: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := monitoringClient(t, server.URL)
			var post *ProfiledTweet
			var err error
			if mode == "read" {
				post, err = client.ReadProfiled(t.Context(), "10000")
			} else {
				var posts []ProfiledTweet
				posts, err = client.SearchProfiled(t.Context(), "topic", 1)
				if len(posts) == 1 {
					post = &posts[0]
				}
			}
			if err != nil || post == nil {
				t.Fatalf("read failed: %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("profiled read made %d requests", requests.Load())
			}
			if post.AuthorProfileImageURL != "https://pbs.twimg.com/profile_images/1/outer.jpg" || post.Author.Username != "outer" || post.Text != "outer post" {
				t.Fatalf("author/photo mismatch: %+v", post)
			}
			if post.QuotedTweet == nil || post.QuotedTweet.AuthorProfileImageURL != "https://pbs.twimg.com/profile_images/2/quoted.jpg" || post.QuotedTweet.Author.Username != "quoted" {
				t.Fatalf("quoted photo dropped: %+v", post.QuotedTweet)
			}
			if post.RepostedTweet == nil || post.RepostedTweet.AuthorProfileImageURL != "https://pbs.twimg.com/profile_images/3/original.jpg" || post.RepostedTweet.Author.Username != "original" {
				t.Fatalf("repost author/photo mismatch: %+v", post.RepostedTweet)
			}
			if post.RepostedTweet.QuotedTweet == nil || post.RepostedTweet.QuotedTweet.AuthorProfileImageURL != "https://pbs.twimg.com/profile_images/4/original-quote.jpg" || post.RepostedTweet.QuotedTweet.Author.Username != "original_quote" {
				t.Fatalf("original quote author/photo mismatch: %+v", post.RepostedTweet.QuotedTweet)
			}
			encoded, err := json.Marshal(post)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				ID, AuthorProfileImageURL string
				Author                    Author
				QuotedTweet               *struct{ AuthorProfileImageURL string }
				RepostedTweet             *struct {
					AuthorProfileImageURL string
					QuotedTweet           *struct{ AuthorProfileImageURL string }
				}
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ID != "10000" || decoded.Author.Username != "outer" || decoded.AuthorProfileImageURL != post.AuthorProfileImageURL || decoded.QuotedTweet == nil || decoded.QuotedTweet.AuthorProfileImageURL != post.QuotedTweet.AuthorProfileImageURL {
				t.Fatalf("profiled JSON lost fields: %s", encoded)
			}
			if decoded.RepostedTweet == nil || decoded.RepostedTweet.AuthorProfileImageURL != post.RepostedTweet.AuthorProfileImageURL || decoded.RepostedTweet.QuotedTweet == nil || decoded.RepostedTweet.QuotedTweet.AuthorProfileImageURL != post.RepostedTweet.QuotedTweet.AuthorProfileImageURL {
				t.Fatalf("profiled JSON lost repost photos: %s", encoded)
			}
			legacy, _ := json.Marshal(post.Tweet)
			if strings.Contains(string(legacy), "authorProfileImageUrl") {
				t.Fatal("frozen Tweet changed JSON shape")
			}
		})
	}
}

func TestProfiledReadMissingPhotoOmitsField(t *testing.T) {
	var raw ProfiledTweet
	if err := json.Unmarshal([]byte(`{"id":"1","text":"post","author":{"username":"a","name":"A"}}`), &raw); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if raw.ID != "1" || raw.AuthorProfileImageURL != "" || strings.Contains(string(encoded), "authorProfileImageUrl") {
		t.Fatalf("missing photo not preserved: %s", encoded)
	}
}
