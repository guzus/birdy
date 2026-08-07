package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
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
	tweets, _, err := c.pagedTimeline(ctx, opSearch, pagedOptions{
		limit: count,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"rawQuery":    query,
				"count":       pageCount,
				"querySource": "typed_query",
				"product":     "Latest",
			}, cursor)
		},
	})
	return tweets, err
}

// Home returns the authenticated account's home timeline. When latest is true it
// requests the reverse-chronological timeline instead of the ranked one.
func (c *Client) Home(ctx context.Context, count int, latest bool) ([]Tweet, error) {
	op := opHomeTimeline
	if latest {
		op = opHomeLatestTimeline
	}
	// bird's home loop reports no nextCursor at all, so neither does this.
	tweets, _, err := c.pagedTimeline(ctx, op, pagedOptions{
		limit: count,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"count":                  pageCount,
				"includePromotedContent": true,
				"latestControlAvailable": true,
				"requestContext":         "launch",
				"withCommunity":          true,
			}, cursor)
		},
	})
	return tweets, err
}

// userTweetsHardMaxPages is bird's safety cap: 10 pages of 20, i.e. 200 tweets
// in a single run. Beyond that bird refuses rather than truncating.
const userTweetsHardMaxPages = 10

// UserTweets returns a user's recent tweets. handle may include a leading @.
//
// It returns the cursor to resume from alongside the tweets, because it is the
// one command whose JSON shape depends on it: bird's user-tweets switches from
// a bare array to {tweets, nextCursor} whenever -n exceeds one page.
func (c *Client) UserTweets(ctx context.Context, handle string, count int) ([]Tweet, string, error) {
	user, err := c.UserByScreenName(ctx, handle)
	if err != nil {
		return nil, "", err
	}

	// bird: effectiveMaxPages = min(10, ceil(limit / 20)).
	maxPages := (max(count, 1) + timelinePageSize - 1) / timelinePageSize
	maxPages = min(max(maxPages, 1), userTweetsHardMaxPages)

	return c.pagedTimeline(ctx, opUserTweets, pagedOptions{
		limit:     count,
		maxPages:  maxPages,
		pageDelay: c.userTweetsPageDelay,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"userId": user.ID,
				"count":  pageCount,
				// NOTE: bird sends includePromotedContent:false here and a
				// {withArticlePlainText:false} fieldToggles blob. That is a
				// separate result-set divergence, deliberately left alone by
				// the paging change.
				"includePromotedContent":                 true,
				"withQuickPromoteEligibilityTweetFields": true,
				"withVoice":                              true,
			}, cursor)
		},
	})
}

// Likes returns tweets a user has liked. Most accounts hide likes from others,
// so this generally only works for the authenticated account.
func (c *Client) Likes(ctx context.Context, handle string, count int) ([]Tweet, error) {
	user, err := c.UserByScreenName(ctx, handle)
	if err != nil {
		return nil, err
	}
	return c.likesByUserID(ctx, user.ID, count)
}

// ViewerLikes returns the authenticated account's liked tweets.
//
// This is the shape bird's `likes` exposes: it takes no handle and reads the
// current session's likes. Resolving the viewer directly also skips the
// UserByScreenName hop that Likes needs.
func (c *Client) ViewerLikes(ctx context.Context, count int) ([]Tweet, error) {
	viewer, err := c.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	return c.likesByUserID(ctx, viewer.ID, count)
}

func (c *Client) likesByUserID(ctx context.Context, userID string, count int) ([]Tweet, error) {
	tweets, _, err := c.pagedTimeline(ctx, opLikes, pagedOptions{
		limit: count,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"userId":                 userID,
				"count":                  pageCount,
				"includePromotedContent": false,
				"withClientEventToken":   false,
				// bird sends this and birdy did not. Matching the request
				// exactly is free, and a mismatched variable set is a silent
				// way to get a differently-ranked page.
				"withBirdwatchNotes": false,
				"withVoice":          true,
			}, cursor)
		},
	})
	return tweets, err
}

// Bookmarks returns the authenticated account's bookmarked tweets.
func (c *Client) Bookmarks(ctx context.Context, count int) ([]Tweet, error) {
	tweets, _, err := c.pagedTimeline(ctx, opBookmarks, pagedOptions{
		limit: count,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"count":                    pageCount,
				"includePromotedContent":   false,
				"withDownvotePerspective":  false,
				"withReactionsMetadata":    false,
				"withReactionsPerspective": false,
			}, cursor)
		},
	})
	return tweets, err
}

// ListTimeline returns tweets from a list by its numeric id.
func (c *Client) ListTimeline(ctx context.Context, listID string, count int) ([]Tweet, error) {
	listID = strings.TrimSpace(listID)
	if listID == "" {
		return nil, fmt.Errorf("x api: empty list id")
	}
	tweets, _, err := c.pagedTimeline(ctx, opListTimeline, pagedOptions{
		limit: count,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"listId":                 listID,
				"count":                  pageCount,
				"includePromotedContent": false,
			}, cursor)
		},
	})
	return tweets, err
}

// Replies returns the direct replies to a tweet: the conversation entries whose
// in_reply_to_status_id_str is this tweet.
//
// bird selects exactly this way (lib/twitter-client-tweet-detail.js:196) and the
// depth-1 constraint is the command. "The conversation minus the focal tweet and
// its ancestors" looks equivalent but is a subtraction, and X's TweetDetail nests
// replies-to-replies inside the same conversationthread modules that carry the
// direct replies — so the subtraction silently returns the whole subtree (35 vs
// bird's 31 on one live conversation, 2 vs 1 on another).
//
// Order is X's entry order, unsorted, same as bird. Unlike `thread`, `replies`
// does not sort by createdAt.
func (c *Client) Replies(ctx context.Context, tweetID string) ([]Tweet, error) {
	tweetID = strings.TrimSpace(tweetID)
	conversation, err := c.Conversation(ctx, tweetID)
	if err != nil {
		return nil, err
	}

	// Non-nil even when empty, so --json prints [] rather than null.
	replies := make([]Tweet, 0, len(conversation))
	for _, t := range conversation {
		if t.InReplyToStatusID == tweetID {
			replies = append(replies, t)
		}
	}
	return replies, nil
}

// limitTweets truncates to the caller's requested count. X treats the count
// variable as a page-size hint and routinely returns more (pinned tweets,
// conversation modules, promoted entries), so the limit has to be applied here
// or `-n 2` silently returns everything.
func limitTweets(tweets []Tweet, count int) []Tweet {
	if count <= 0 || len(tweets) <= count {
		return tweets
	}
	return tweets[:count]
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

// timelinePageFor runs one timeline request and maps its tweets and cursor.
func (c *Client) timelinePageFor(ctx context.Context, op timelineOp, variables map[string]any) (timelinePage, error) {
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
		return timelinePage{}, err
	}
	return parseTimelinePage(body, op.roots)
}

// timelinePageSize is bird's pageSize for every timeline command.
//
// X treats `count` as a hint over ENTRIES, not a promise of parseable tweets:
// a page can contain a tombstone (`tweet_results: {}` for a deleted or withheld
// post) that both parsers drop, and several operations silently cap a page at
// 20 regardless of what was asked. Requesting `-n N` in one shot and truncating
// therefore under-returns, which is exactly what the loop below exists to fix.
const timelinePageSize = 20

// pagedOptions configures one paged walk.
type pagedOptions struct {
	// limit is the caller's -n.
	limit int
	// maxPages caps the walk; 0 is bird's unlimited.
	maxPages int
	// pageDelay waits before every page after the first; 0 is no wait. Only
	// user-tweets has one.
	pageDelay time.Duration
	// variables builds one request. pageCount is bird's min(20, remaining);
	// cursor is empty on the first page and must then be OMITTED from the
	// payload, not sent as null — X rejects an explicit null cursor.
	variables func(pageCount int, cursor string) map[string]any
}

// pagedTimeline is a transcription of bird's page loop, which is character-for-
// character the same in searchPaged, getUserTweetsPaged, fetchHomeTimeline,
// getLikesPaged, getBookmarksPaged and getListTimelinePaged.
//
// Two details are load-bearing and easy to lose:
//   - the four termination conditions are evaluated BEFORE the maxPages check,
//     so an exhausted timeline reports no cursor even when a page budget
//     remains;
//   - nextCursor is assigned at the bottom of a successful iteration, so a walk
//     that hits the limit exactly still reports a cursor to resume from. An
//     implementation that only sets it when it breaks early emits null where
//     bird emits a cursor.
func (c *Client) pagedTimeline(ctx context.Context, op timelineOp, o pagedOptions) ([]Tweet, string, error) {
	if o.limit <= 0 {
		o.limit = 20 // bird's default count
	}

	var (
		tweets     []Tweet
		cursor     string
		nextCursor string
		pages      int
	)
	seen := make(map[string]bool)

	for len(tweets) < o.limit {
		if pages > 0 && o.pageDelay > 0 {
			timer := time.NewTimer(o.pageDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, "", ctx.Err()
			case <-timer.C:
			}
		}

		pageCount := timelinePageSize
		if remaining := o.limit - len(tweets); remaining < pageCount {
			pageCount = remaining
		}

		page, err := c.timelinePageFor(ctx, op, o.variables(pageCount, cursor))
		if err != nil {
			// bird fails the whole call and discards what it accumulated.
			return nil, "", err
		}
		pages++

		added := 0
		for _, t := range page.tweets {
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			tweets = append(tweets, t)
			added++
			if len(tweets) >= o.limit {
				break
			}
		}

		if page.cursor == "" || page.cursor == cursor || len(page.tweets) == 0 || added == 0 {
			nextCursor = ""
			break
		}
		if o.maxPages > 0 && pages >= o.maxPages {
			nextCursor = page.cursor
			break
		}
		cursor = page.cursor
		nextCursor = page.cursor
	}
	return tweets, nextCursor, nil
}

// withCursor adds bird's conditionally-spread cursor variable.
func withCursor(variables map[string]any, cursor string) map[string]any {
	if cursor != "" {
		variables["cursor"] = cursor
	}
	return variables
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

// timelinePage is one response: the tweets it carried and the cursor to ask for
// the next one, empty when the timeline is exhausted.
type timelinePage struct {
	tweets []Tweet
	cursor string
}

// parseTimeline maps tweets from a response whose instructions live at one of
// the given paths, discarding the cursor.
func parseTimeline(body []byte, roots [][]string) ([]Tweet, error) {
	page, err := parseTimelinePage(body, roots)
	return page.tweets, err
}

// parseTimelinePage maps both the tweets and the Bottom cursor.
func parseTimelinePage(body []byte, roots [][]string) (timelinePage, error) {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return timelinePage{}, &APIError{Message: "decoding response: " + err.Error()}
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
		return timelinePage{
			tweets: tweetsFromInstructions(instructions),
			cursor: bottomCursorFromInstructions(instructions),
		}, nil
	}

	// Nothing usable at any known path: surface X's error if it gave one, since
	// that is far more actionable than "no tweets".
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			messages = append(messages, e.Message)
		}
		return timelinePage{}, &APIError{Message: strings.Join(messages, ", ")}
	}
	// An empty timeline is a legitimate result, not an error.
	return timelinePage{}, nil
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
