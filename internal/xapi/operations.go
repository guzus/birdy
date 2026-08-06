package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// timelineOp describes a GraphQL operation that returns a timeline of tweets.
//
// Every such operation follows the same shape — persisted query id, a variables
// blob, a feature set, and a response whose tweets live under some path ending
// in "instructions". Only those differ, so one implementation covers all of them.
type timelineOp struct {
	// name is the GraphQL operation, e.g. "SearchTimeline".
	name string
	// features is the flag set X requires for this operation.
	features map[string]bool
	// roots are candidate JSON paths from "data" to the instructions array.
	// They are tried in order, because X varies the wrapper between operations
	// and occasionally between responses for the same one.
	roots [][]string
	// post reports that X serves this operation over POST rather than GET, with
	// variables still in the query string but features and queryId in a JSON
	// body. SearchTimeline is the notable case; sending it as a GET 404s.
	post bool
}

var (
	opSearch = timelineOp{
		name:     "SearchTimeline",
		features: searchFeatures,
		roots:    [][]string{{"search_by_raw_query", "search_timeline", "timeline", "instructions"}},
		post:     true,
	}
	opUserTweets = timelineOp{
		name:     "UserTweets",
		features: userTweetsFeatures,
		roots:    [][]string{{"user", "result", "timeline", "timeline", "instructions"}, {"user", "result", "timeline_v2", "timeline", "instructions"}},
	}
	opHomeTimeline = timelineOp{
		name:     "HomeTimeline",
		features: homeTimelineFeatures,
		roots:    [][]string{{"home", "home_timeline_urt", "instructions"}},
	}
	opHomeLatestTimeline = timelineOp{
		name:     "HomeLatestTimeline",
		features: homeTimelineFeatures,
		roots:    [][]string{{"home", "home_timeline_urt", "instructions"}},
	}
	opLikes = timelineOp{
		name:     "Likes",
		features: likesFeatures,
		roots:    [][]string{{"user", "result", "timeline", "timeline", "instructions"}, {"user", "result", "timeline_v2", "timeline", "instructions"}},
	}
	opBookmarks = timelineOp{
		name:     "Bookmarks",
		features: bookmarksFeatures,
		roots:    [][]string{{"bookmark_timeline_v2", "timeline", "instructions"}, {"bookmark_collection_timeline", "timeline", "instructions"}},
	}
	opListTimeline = timelineOp{
		name:     "ListLatestTweetsTimeline",
		features: timelineFeatures,
		roots:    [][]string{{"list", "tweets_timeline", "timeline", "instructions"}},
	}
)

// Search returns tweets matching a query, most recent first.
func (c *Client) Search(ctx context.Context, query string, count int) ([]Tweet, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("x api: empty search query")
	}
	return c.timeline(ctx, opSearch, map[string]any{
		"rawQuery":    query,
		"count":       clampCount(count),
		"querySource": "typed_query",
		"product":     "Latest",
	})
}

// Home returns the authenticated account's home timeline. When latest is true it
// requests the reverse-chronological timeline instead of the ranked one.
func (c *Client) Home(ctx context.Context, count int, latest bool) ([]Tweet, error) {
	op := opHomeTimeline
	if latest {
		op = opHomeLatestTimeline
	}
	return c.timeline(ctx, op, map[string]any{
		"count":                  clampCount(count),
		"includePromotedContent": true,
		"latestControlAvailable": true,
		"requestContext":         "launch",
		"withCommunity":          true,
	})
}

// UserTweets returns a user's recent tweets. handle may include a leading @.
func (c *Client) UserTweets(ctx context.Context, handle string, count int) ([]Tweet, error) {
	user, err := c.UserByScreenName(ctx, handle)
	if err != nil {
		return nil, err
	}
	return c.timeline(ctx, opUserTweets, map[string]any{
		"userId":                                 user.ID,
		"count":                                  clampCount(count),
		"includePromotedContent":                 true,
		"withQuickPromoteEligibilityTweetFields": true,
		"withVoice":                              true,
	})
}

// Likes returns tweets a user has liked. Most accounts hide likes from others,
// so this generally only works for the authenticated account.
func (c *Client) Likes(ctx context.Context, handle string, count int) ([]Tweet, error) {
	user, err := c.UserByScreenName(ctx, handle)
	if err != nil {
		return nil, err
	}
	return c.timeline(ctx, opLikes, map[string]any{
		"userId":                 user.ID,
		"count":                  clampCount(count),
		"includePromotedContent": false,
		"withClientEventToken":   false,
		"withVoice":              true,
	})
}

// Bookmarks returns the authenticated account's bookmarked tweets.
func (c *Client) Bookmarks(ctx context.Context, count int) ([]Tweet, error) {
	return c.timeline(ctx, opBookmarks, map[string]any{
		"count":                    clampCount(count),
		"includePromotedContent":   false,
		"withDownvotePerspective":  false,
		"withReactionsMetadata":    false,
		"withReactionsPerspective": false,
	})
}

// ListTimeline returns tweets from a list by its numeric id.
func (c *Client) ListTimeline(ctx context.Context, listID string, count int) ([]Tweet, error) {
	listID = strings.TrimSpace(listID)
	if listID == "" {
		return nil, fmt.Errorf("x api: empty list id")
	}
	return c.timeline(ctx, opListTimeline, map[string]any{
		"listId":                 listID,
		"count":                  clampCount(count),
		"includePromotedContent": false,
	})
}

// Replies returns the replies to a tweet: everything in its conversation that
// is not the tweet itself or one of its ancestors.
func (c *Client) Replies(ctx context.Context, tweetID string) ([]Tweet, error) {
	conversation, err := c.Conversation(ctx, tweetID)
	if err != nil {
		return nil, err
	}

	// Ancestors sit above the focal tweet; everything else below it is a reply.
	ancestors := make(map[string]bool)
	for _, t := range ancestorChain(conversation, tweetID) {
		ancestors[t.ID] = true
	}

	replies := make([]Tweet, 0, len(conversation))
	for _, t := range conversation {
		if t.ID == tweetID || ancestors[t.ID] {
			continue
		}
		replies = append(replies, t)
	}
	return replies, nil
}

// clampCount keeps page sizes within what X accepts.
func clampCount(count int) int {
	switch {
	case count <= 0:
		return 20
	case count > 100:
		return 100
	default:
		return count
	}
}

// timeline runs a timeline operation and maps the tweets out of its response.
func (c *Client) timeline(ctx context.Context, op timelineOp, variables map[string]any) ([]Tweet, error) {
	var (
		body []byte
		err  error
	)
	if op.post {
		body, err = c.graphQLPost(ctx, op.name, variables, op.features)
	} else {
		body, err = c.graphQL(ctx, op.name, variables, op.features, nil)
	}
	if err != nil {
		return nil, err
	}
	return parseTimeline(body, op.roots)
}

// graphQLPost issues a POST-style operation: variables ride in the query string
// while features and the query id go in the JSON body.
func (c *Client) graphQLPost(ctx context.Context, operation string, variables map[string]any, features map[string]bool) ([]byte, error) {
	query, err := buildQuery(variables, nil, nil)
	if err != nil {
		return nil, err
	}

	attempt := func(ids []string) ([]byte, error) {
		var lastErr error
		for _, queryID := range ids {
			payload, err := json.Marshal(map[string]any{"features": features, "queryId": queryID})
			if err != nil {
				return nil, fmt.Errorf("x api: encoding body: %w", err)
			}

			endpoint := fmt.Sprintf("%s/%s/%s?%s", c.baseURL, queryID, operation, query)
			body, err := c.post(ctx, endpoint, payload)
			if err != nil {
				if isStaleQueryID(err) {
					lastErr = err
					continue
				}
				return nil, err
			}
			return body, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("x api: no query id configured for %s", operation)
		}
		return nil, lastErr
	}

	body, err := attempt(operationQueryIDs(operation))
	if err == nil {
		return body, nil
	}
	if !isStaleQueryID(err) {
		return nil, err
	}

	if refreshErr := resolver.refresh(ctx, c.httpClient); refreshErr != nil {
		return nil, fmt.Errorf("x api: every known %s query id was rejected and "+
			"discovery failed (%v): %w", operation, refreshErr, err)
	}
	discovered, ok := resolver.lookup(operation)
	if !ok {
		return nil, fmt.Errorf("x api: every known %s query id was rejected and "+
			"discovery did not find a current one: %w", operation, err)
	}
	return attempt([]string{discovered})
}

// graphQL performs an operation and returns the raw response body, walking the
// operation's query ids until one is accepted.
func (c *Client) graphQL(ctx context.Context, operation string, variables map[string]any, features map[string]bool, fieldToggles map[string]bool) ([]byte, error) {
	query, err := buildQuery(variables, features, fieldToggles)
	if err != nil {
		return nil, err
	}

	ids := operationQueryIDs(operation)
	if len(ids) == 0 {
		return nil, fmt.Errorf("x api: no query id configured for %s", operation)
	}

	body, err := c.tryQueryIDs(ctx, operation, query, ids)
	if err == nil {
		return body, nil
	}
	if !isStaleQueryID(err) {
		return nil, err
	}

	// Every hash we knew is stale. X publishes current ones in its web bundles,
	// so rediscover and retry once rather than failing until the next release.
	if refreshErr := resolver.refresh(ctx, c.httpClient); refreshErr != nil {
		return nil, fmt.Errorf("x api: every known %s query id was rejected and "+
			"discovery failed (%v): %w", operation, refreshErr, err)
	}
	discovered, ok := resolver.lookup(operation)
	if !ok {
		return nil, fmt.Errorf("x api: every known %s query id was rejected and "+
			"discovery did not find a current one: %w", operation, err)
	}

	return c.tryQueryIDs(ctx, operation, query, []string{discovered})
}

// tryQueryIDs issues the request against each hash until one is not a 404.
func (c *Client) tryQueryIDs(ctx context.Context, operation, query string, ids []string) ([]byte, error) {
	var lastErr error
	for _, queryID := range ids {
		endpoint := fmt.Sprintf("%s/%s/%s?%s", c.baseURL, queryID, operation, query)

		body, err := c.get(ctx, endpoint)
		if err != nil {
			var apiErr *APIError
			if ok := asAPIError(err, &apiErr); ok && apiErr.StatusCode == 404 {
				lastErr = err
				continue // rotated query id, try the next
			}
			return nil, err
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("x api: no query id configured for %s", operation)
	}
	return nil, lastErr
}

// isStaleQueryID reports whether err is the 404 X returns for a rotated hash.
func isStaleQueryID(err error) bool {
	var apiErr *APIError
	return asAPIError(err, &apiErr) && apiErr.StatusCode == 404
}

// buildQuery encodes the variables/features/fieldToggles triplet.
func buildQuery(variables map[string]any, features map[string]bool, fieldToggles map[string]bool) (string, error) {
	params := url.Values{}

	encoded, err := json.Marshal(variables)
	if err != nil {
		return "", fmt.Errorf("x api: encoding variables: %w", err)
	}
	params.Set("variables", string(encoded))

	if features != nil {
		encoded, err = json.Marshal(features)
		if err != nil {
			return "", fmt.Errorf("x api: encoding features: %w", err)
		}
		params.Set("features", string(encoded))
	}

	if fieldToggles != nil {
		encoded, err = json.Marshal(fieldToggles)
		if err != nil {
			return "", fmt.Errorf("x api: encoding fieldToggles: %w", err)
		}
		params.Set("fieldToggles", string(encoded))
	}

	return params.Encode(), nil
}

// parseTimeline maps tweets from a response whose instructions live at one of
// the given paths.
func parseTimeline(body []byte, roots [][]string) ([]Tweet, error) {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &APIError{Message: "decoding response: " + err.Error()}
	}

	for _, root := range roots {
		raw, ok := navigate(envelope.Data, root)
		if !ok {
			continue
		}
		var instructions []instruction
		if err := json.Unmarshal(raw, &instructions); err != nil {
			continue
		}
		return tweetsFromInstructions(instructions), nil
	}

	// Nothing usable at any known path: surface X's error if it gave one, since
	// that is far more actionable than "no tweets".
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			messages = append(messages, e.Message)
		}
		return nil, &APIError{Message: strings.Join(messages, ", ")}
	}
	// An empty timeline is a legitimate result, not an error.
	return nil, nil
}

// navigate walks a JSON object along a path of keys.
func navigate(raw json.RawMessage, path []string) (json.RawMessage, bool) {
	current := raw
	for _, key := range path {
		if len(current) == 0 {
			return nil, false
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false
		}
		next, ok := object[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, len(current) > 0
}
