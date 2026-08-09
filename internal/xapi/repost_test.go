package xapi

import "testing"

func TestParseTimelineMapsStructuredRepost(t *testing.T) {
	body := []byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[
		{"content":{"itemContent":{"tweet_results":{"result":{
			"rest_id":"200","core":{"user_results":{"result":{"rest_id":"1","legacy":{"screen_name":"reposter","name":"Reposter"}}}},
			"legacy":{"full_text":"RT @source: original","retweeted_status_result":{"result":{
				"rest_id":"100","core":{"user_results":{"result":{"rest_id":"2","legacy":{"screen_name":"source","name":"Source"}}}},
				"legacy":{"full_text":"original","favorite_count":7}
			}}}
		}}}}}
	] }]}}}}}}`)

	tweets, err := parseTimeline(body, opUserTweets.roots)
	if err != nil {
		t.Fatalf("parseTimeline: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("got %d tweets, want 1", len(tweets))
	}
	repost := tweets[0]
	if repost.RepostedTweet == nil {
		t.Fatal("structured repost relation was dropped")
	}
	if got := repost.RepostedTweet; got.ID != "100" || got.Author.Username != "source" || got.Text != "original" || got.LikeCount != 7 {
		t.Fatalf("RepostedTweet = %+v, want original post", got)
	}
}
