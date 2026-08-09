package xapi

import (
	"context"
	"encoding/json"
	"fmt"
)

// ActorPage is one page of accounts that acted on a tweet.
//
// NextCursor is empty at the end. It is carried rather than dropped because
// bird's `activity --json` prints a nextCursor per section, and a hardcoded
// null there is a value divergence a consumer sees on every call.
type ActorPage struct {
	Users      []ListedUser
	NextCursor string
}

// QuotePage is one page of tweets quoting a tweet.
type QuotePage struct {
	Tweets     []Tweet
	NextCursor string
}

// Favoriters returns accounts that liked a tweet.
func (c *Client) Favoriters(ctx context.Context, tweetID string, count int) (*ActorPage, error) {
	return c.tweetActors(ctx, "Favoriters", "favoriters_timeline", tweetID, count)
}

// Retweeters returns accounts that reposted a tweet.
func (c *Client) Retweeters(ctx context.Context, tweetID string, count int) (*ActorPage, error) {
	return c.tweetActors(ctx, "Retweeters", "retweeters_timeline", tweetID, count)
}

func (c *Client) tweetActors(ctx context.Context, operation, root, tweetID string, count int) (*ActorPage, error) {
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
func parseActorTimeline(body []byte, root string) (*ActorPage, error) {
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
		return &ActorPage{}, nil
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

	page := &ActorPage{}
	for _, instruction := range timeline.Timeline.Instructions {
		for _, entry := range instruction.Entries {
			if entry.Content == nil {
				return nil, &APIError{Message: "malformed activity entry: missing content"}
			}
			// A cursor lives in its own entry, exactly as in parseUserList.
			if entry.Content.CursorType != "" {
				if entry.Content.ItemContent != nil || entry.Content.Items != nil {
					return nil, &APIError{Message: "malformed activity cursor carrying data"}
				}
				switch entry.Content.CursorType {
				case "Bottom":
					if entry.Content.Value == "" {
						return nil, &APIError{Message: "malformed activity Bottom cursor"}
					}
					page.NextCursor = entry.Content.Value
				case "Top":
				default:
					return nil, &APIError{Message: fmt.Sprintf("unsupported activity cursor %q", entry.Content.CursorType)}
				}
				continue
			}
			users, err := usersFromEntry(entry)
			if err != nil {
				return nil, err
			}
			page.Users = append(page.Users, users...)
		}
	}
	return page, nil
}

// QuoteTweets returns tweets quoting a tweet.
//
// X has no dedicated operation for this; bird runs a SearchTimeline for
// "quoted_tweet_id:<id>". It is deliberately NOT birdy's Search: bird sends
// querySource "tdqt" and product "Top" here where Search sends "typed_query"
// and "Latest", and the two return partially disjoint sets — observed live on
// tweet 2085074976290505090. It is also a single page, because bird's
// non-`--all` getQuoteTweets calls fetchQuoteTweetsPage once with no loop.
func (c *Client) QuoteTweets(ctx context.Context, tweetID string, count int) (*QuotePage, error) {
	page, err := c.timelinePageFor(ctx, opSearch, map[string]any{
		"rawQuery":                               "quoted_tweet_id:" + tweetID,
		"count":                                  count,
		"querySource":                            "tdqt",
		"product":                                "Top",
		"withGrokTranslatedBio":                  true,
		"withQuickPromoteEligibilityTweetFields": false,
	}, false)
	if err != nil {
		return nil, err
	}
	return &QuotePage{Tweets: limitTweets(page.tweets, count), NextCursor: page.cursor}, nil
}
