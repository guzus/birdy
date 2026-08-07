package xapi

import (
	"context"
	"encoding/json"
	"fmt"
)

// Favoriters returns accounts that liked a tweet.
func (c *Client) Favoriters(ctx context.Context, tweetID string, count int) ([]ListedUser, error) {
	return c.tweetActors(ctx, "Favoriters", "favoriters_timeline", tweetID, count)
}

// Retweeters returns accounts that reposted a tweet.
func (c *Client) Retweeters(ctx context.Context, tweetID string, count int) ([]ListedUser, error) {
	return c.tweetActors(ctx, "Retweeters", "retweeters_timeline", tweetID, count)
}

func (c *Client) tweetActors(ctx context.Context, operation, root, tweetID string, count int) ([]ListedUser, error) {
	variables := map[string]any{
		"tweetId":                tweetID,
		"count":                  clampCount(count),
		"enableRanking":          true,
		"includePromotedContent": true,
	}
	body, err := c.graphQL(ctx, operation, variables, timelineFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseActorTimeline(body, root)
}

// parseActorTimeline reads a user timeline rooted at a per-operation key rather
// than under "user", which is why it cannot reuse parseUserList.
func parseActorTimeline(body []byte, root string) ([]ListedUser, error) {
	var envelope struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &APIError{Message: "decoding activity response: " + err.Error()}
	}

	raw, ok := envelope.Data[root]
	if !ok {
		if len(envelope.Errors) > 0 {
			messages := make([]string, 0, len(envelope.Errors))
			for _, e := range envelope.Errors {
				messages = append(messages, e.Message)
			}
			return nil, &APIError{Message: joinMessages(messages)}
		}
		return nil, nil
	}

	var timeline struct {
		Timeline struct {
			Instructions []struct {
				Entries []userEntry `json:"entries"`
			} `json:"instructions"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(raw, &timeline); err != nil {
		return nil, &APIError{Message: fmt.Sprintf("decoding %s: %s", root, err.Error())}
	}

	var users []ListedUser
	for _, instruction := range timeline.Timeline.Instructions {
		for _, entry := range instruction.Entries {
			user, ok := mapUser(entry.Content.ItemContent.UserResults.Result.unwrap())
			if !ok {
				continue
			}
			users = append(users, user)
		}
	}
	return users, nil
}

// QuoteTweets returns tweets quoting a tweet.
//
// X has no dedicated operation for this; bird runs a search for
// "quoted_tweet_id:<id>", so birdy does the same rather than inventing a
// different result set.
func (c *Client) QuoteTweets(ctx context.Context, tweetID string, count int) ([]Tweet, error) {
	return c.Search(ctx, "quoted_tweet_id:"+tweetID, count)
}
