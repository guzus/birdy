package xapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthorProfileImageFromTweetResponse(t *testing.T) {
	for _, tc := range []struct{ name, user, want string }{
		{"legacy", `{"legacy":{"screen_name":"author","name":"Author","profile_image_url_https":"https://pbs.twimg.com/profile_images/1/legacy.jpg"}}`, "https://pbs.twimg.com/profile_images/1/legacy.jpg"},
		{"current", `{"core":{"screen_name":"author","name":"Author"},"avatar":{"image_url":"https://pbs.twimg.com/profile_images/1/current.jpg"}}`, "https://pbs.twimg.com/profile_images/1/current.jpg"},
		{"current preferred", `{"legacy":{"screen_name":"author","name":"Author","profile_image_url_https":"legacy"},"avatar":{"image_url":"current"}}`, "current"},
		{"missing", `{"legacy":{"screen_name":"author","name":"Author"}}`, ""},
		{"empty current falls back", `{"legacy":{"screen_name":"author","name":"Author","profile_image_url_https":"legacy"},"avatar":{"image_url":""}}`, "legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw tweetResult
			if err := json.Unmarshal([]byte(`{"rest_id":"10000","core":{"user_results":{"result":`+tc.user+`}},"legacy":{"full_text":"post"}}`), &raw); err != nil {
				t.Fatal(err)
			}
			for name, mapper := range map[string]func(*tweetResult) (Tweet, bool){"ordinary": mapTweet, "monitoring base": mapTweetWithoutRepost} {
				post, ok := mapper(&raw)
				if !ok || post.AuthorProfileImageURL() != tc.want {
					t.Fatalf("%s: got %q, want %q", name, post.AuthorProfileImageURL(), tc.want)
				}
				data, err := json.Marshal(post)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "profileImage") || strings.Contains(string(data), "ProfileImage") {
					t.Fatal("avatar changed legacy CLI JSON")
				}
			}
		})
	}
}
