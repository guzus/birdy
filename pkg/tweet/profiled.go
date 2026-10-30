package tweet

import (
	"context"

	"github.com/guzus/birdy/internal/xapi"
)

// ProfiledTweet adds the author's profile photo to the frozen Tweet shape.
// The URL comes from the same X response as the tweet; no profile lookup is
// made. It is empty when X omits the photo. Callers rendering remote images
// should apply their own URL policy.
type ProfiledTweet struct {
	Tweet
	AuthorProfileImageURL string         `json:"authorProfileImageUrl,omitempty"`
	QuotedTweet           *ProfiledTweet `json:"quotedTweet,omitempty"`
	RepostedTweet         *ProfiledTweet `json:"repostedTweet,omitempty"`
}

// ReadProfiled is Read with author profile photos, including quoted posts.
func (c *Client) ReadProfiled(ctx context.Context, ref string) (*ProfiledTweet, error) {
	id, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}
	var result *ProfiledTweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		post, err := api.Tweet(ctx, id)
		if err != nil {
			return err
		}
		converted := convertProfiledTweet(*post)
		result = &converted
		return nil
	})
	return result, err
}

// SearchProfiled is Search with author profile photos from the same response.
func (c *Client) SearchProfiled(ctx context.Context, query string, count int) ([]ProfiledTweet, error) {
	count = defaultCount(count, 20)
	var result []ProfiledTweet
	err := c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		posts, err := api.Search(ctx, query, count)
		if err != nil {
			return err
		}
		if posts != nil {
			result = make([]ProfiledTweet, 0, len(posts))
			for _, post := range posts {
				result = append(result, convertProfiledTweet(post))
			}
		}
		return nil
	})
	return result, err
}

func convertProfiledTweet(post xapi.Tweet) ProfiledTweet {
	result := ProfiledTweet{Tweet: convertTweet(post), AuthorProfileImageURL: post.AuthorProfileImageURL()}
	if post.QuotedTweet != nil {
		quoted := convertProfiledTweet(*post.QuotedTweet)
		result.QuotedTweet = &quoted
	}
	if post.RepostedTweet != nil {
		reposted := convertProfiledTweet(*post.RepostedTweet)
		result.RepostedTweet = &reposted
	}
	return result
}
