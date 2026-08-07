package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Mutations differ from the read operations in three ways, which is why they do
// not reuse graphQLPost.
//
//   - Variables ride in the JSON body, not the query string.
//   - X increasingly prefers a POST to the bare /graphql root with the query id
//     in the payload, so a 404 on the per-operation URL falls back to that.
//   - They are not idempotent. Nothing here retries on transport failure: a
//     timed-out CreateTweet may well have posted, and posting twice is worse
//     than reporting an uncertain failure. bird carries a further
//     statuses/update fallback for a GraphQL-level error; it is deliberately
//     not ported, because "the mutation reported an error" is exactly the case
//     where a second attempt down a different path risks a duplicate post.

// graphQLMutate issues a mutation and returns the raw response body.
func (c *Client) graphQLMutate(ctx context.Context, operation string, variables map[string]any, features map[string]bool) ([]byte, error) {
	ids := operationQueryIDs(operation)
	if len(ids) == 0 {
		return nil, &APIError{Message: "no query id configured for " + operation}
	}

	var lastErr error
	for _, queryID := range ids {
		payload := map[string]any{"variables": variables, "queryId": queryID}
		if features != nil {
			payload["features"] = features
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("x api: encoding body: %w", err)
		}

		endpoint := fmt.Sprintf("%s/%s/%s", c.baseURL, queryID, operation)
		resp, err := c.postWithReferer(ctx, endpoint, body, mutationReferer(operation))
		if err == nil {
			return resp, nil
		}
		if !isStaleQueryID(err) {
			return nil, err
		}
		lastErr = err

		// The per-operation URL 404'd. X accepts the same payload at the bare
		// GraphQL root, where the query id in the body is what routes it.
		resp, rootErr := c.postWithReferer(ctx, c.baseURL, body, mutationReferer(operation))
		if rootErr == nil {
			return resp, nil
		}
		if !isStaleQueryID(rootErr) {
			return nil, rootErr
		}
	}
	return nil, lastErr
}

// mutationReferer mirrors the referer bird sends. X rejects a compose request
// that does not look like it came from the composer.
func mutationReferer(operation string) string {
	if operation == "CreateTweet" {
		return "https://x.com/compose/post"
	}
	return ""
}

func (c *Client) postWithReferer(ctx context.Context, endpoint string, payload []byte, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("x api: building request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("content-type", "application/json")
	if referer != "" {
		req.Header.Set("referer", referer)
	}
	return c.do(req)
}

// CreateTweet posts a tweet. When replyTo is non-empty the tweet is a reply to
// that tweet id. It returns the new tweet's id.
func (c *Client) CreateTweet(ctx context.Context, text, replyTo string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("x api: tweet text is empty")
	}

	variables := map[string]any{
		"tweet_text":   text,
		"dark_request": false,
		"media": map[string]any{
			"media_entities":     []any{},
			"possibly_sensitive": false,
		},
		"semantic_annotation_ids": []any{},
	}
	if replyTo != "" {
		variables["reply"] = map[string]any{
			"in_reply_to_tweet_id":   replyTo,
			"exclude_reply_user_ids": []any{},
		}
	}

	body, err := c.graphQLMutate(ctx, "CreateTweet", variables, tweetCreateFeatures)
	if err != nil {
		return "", err
	}
	return parseCreatedTweetID(body)
}

func parseCreatedTweetID(body []byte) (string, error) {
	var resp struct {
		Data struct {
			CreateTweet struct {
				TweetResults struct {
					Result struct {
						RestID string `json:"rest_id"`
					} `json:"result"`
				} `json:"tweet_results"`
			} `json:"create_tweet"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", &APIError{Message: "decoding create response: " + err.Error()}
	}

	if id := resp.Data.CreateTweet.TweetResults.Result.RestID; id != "" {
		return id, nil
	}
	if len(resp.Errors) > 0 {
		parts := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			if e.Code != 0 {
				parts = append(parts, fmt.Sprintf("%s (%d)", e.Message, e.Code))
			} else {
				parts = append(parts, e.Message)
			}
		}
		return "", &APIError{Message: strings.Join(parts, ", ")}
	}
	// X answered 200 with no id and no error. The tweet may or may not exist,
	// so this is reported rather than retried.
	return "", &APIError{Message: "tweet created but no ID returned"}
}

// DeleteBookmark removes a tweet from the authenticated account's bookmarks.
func (c *Client) DeleteBookmark(ctx context.Context, tweetID string) error {
	body, err := c.graphQLMutate(ctx, "DeleteBookmark", map[string]any{"tweet_id": tweetID}, nil)
	if err != nil {
		return err
	}
	return graphQLErrors(body)
}

// Follow follows a user by numeric id.
//
// The REST endpoint is tried first, matching bird: it reports "already
// following" as a distinct code, which is the difference between a no-op and a
// failure. GraphQL is the fallback.
// It returns the canonical screen name when X reports one, which is what bird
// echoes back rather than the handle the caller typed.
func (c *Client) Follow(ctx context.Context, userID string) (string, error) {
	return c.friendship(ctx, userID, "create", "CreateFriendship")
}

// Unfollow unfollows a user by numeric id.
func (c *Client) Unfollow(ctx context.Context, userID string) (string, error) {
	return c.friendship(ctx, userID, "destroy", "DestroyFriendship")
}

func (c *Client) friendship(ctx context.Context, userID, action, operation string) (string, error) {
	username, restErr := c.friendshipViaREST(ctx, userID, action)
	if restErr == nil {
		return username, nil
	}
	// A definite answer from X — blocked, not found, already in that state —
	// is the truth. Only an inconclusive failure justifies the GraphQL retry.
	var apiErr *APIError
	if asAPIError(restErr, &apiErr) && apiErr.Terminal {
		return "", restErr
	}

	variables := map[string]any{
		"user_id":                           userID,
		"include_profile_interstitial_type": 1,
		"include_blocking":                  1,
		"include_blocked_by":                1,
		"include_followed_by":               1,
		"include_want_retweets":             1,
		"include_mute_edge":                 1,
		"include_can_dm":                    1,
		"include_can_media_tag":             1,
		"skip_status":                       1,
		"withHighlightedLabel":              true,
		"withSafetyModeUserFields":          true,
		"withSuperFollowsUserFields":        true,
		"withUserResults":                   true,
	}
	body, err := c.graphQLMutate(ctx, operation, variables, nil)
	if err != nil {
		return "", err
	}
	return "", graphQLErrors(body)
}

// friendshipCodes are the v1.1 error codes with a meaning birdy acts on.
const (
	codeAlreadyInState = 160 // already following / already not following
	codeBlocked        = 162
	codeUserNotFound   = 108
)

func (c *Client) friendshipViaREST(ctx context.Context, userID, action string) (string, error) {
	form := url.Values{"user_id": {userID}, "skip_status": {"true"}}
	endpoints := c.friendshipEndpoints
	if endpoints == nil {
		endpoints = []string{
			"https://x.com/i/api/1.1/friendships/" + action + ".json",
			"https://api.twitter.com/1.1/friendships/" + action + ".json",
		}
	}

	var lastErr error
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return "", fmt.Errorf("x api: building request: %w", err)
		}
		c.setHeaders(req)
		req.Header.Set("content-type", "application/x-www-form-urlencoded")

		body, err := c.do(req)
		if err != nil {
			var apiErr *APIError
			if asAPIError(err, &apiErr) {
				if decoded := classifyFriendshipError(apiErr); decoded != nil {
					// codeAlreadyInState means the requested end state already
					// holds, which is success for an idempotent verb.
					if decoded.Code == codeAlreadyInState {
						return "", nil
					}
					return "", decoded
				}
			}
			lastErr = err
			continue
		}
		if err := restErrors(body); err != nil {
			lastErr = err
			continue
		}
		var payload struct {
			ScreenName string `json:"screen_name"`
		}
		_ = json.Unmarshal(body, &payload)
		return payload.ScreenName, nil
	}
	return "", lastErr
}

// classifyFriendshipError reads X's v1.1 error envelope out of a failed
// response body and marks the codes that are a final answer.
func classifyFriendshipError(apiErr *APIError) *APIError {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(apiErr.Message), &payload) != nil || len(payload.Errors) == 0 {
		return nil
	}

	first := payload.Errors[0]
	switch first.Code {
	case codeAlreadyInState:
		return &APIError{Code: first.Code, Message: first.Message, Terminal: true}
	case codeBlocked:
		return &APIError{Code: first.Code, Message: "You have been blocked from following this account", Terminal: true}
	case codeUserNotFound:
		return &APIError{Code: first.Code, Message: "User not found", Terminal: true}
	}
	return &APIError{
		Code:    first.Code,
		Message: fmt.Sprintf("%s (code %d)", first.Message, first.Code),
	}
}

// restErrors reports an error envelope returned alongside a 200.
func restErrors(body []byte) error {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Errors) == 0 {
		return nil
	}
	parts := make([]string, 0, len(payload.Errors))
	for _, e := range payload.Errors {
		parts = append(parts, e.Message)
	}
	return &APIError{Message: strings.Join(parts, ", ")}
}

// graphQLErrors reports an errors array returned alongside a 200.
func graphQLErrors(body []byte) error {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Errors) == 0 {
		return nil
	}
	parts := make([]string, 0, len(payload.Errors))
	for _, e := range payload.Errors {
		parts = append(parts, e.Message)
	}
	return &APIError{Message: strings.Join(parts, ", ")}
}
