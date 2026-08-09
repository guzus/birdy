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
	tweets, _, err := c.searchFrom(ctx, query, count, "", 0, false)
	return tweets, err
}

// SearchPageAlignedFrom walks Latest search without truncating the terminal
// response page, so the cursor resumes after every returned entry.
func (c *Client) SearchPageAlignedFrom(ctx context.Context, query string, count int, cursor string, maxPages int) ([]Tweet, string, error) {
	if maxPages <= 0 {
		maxPages = timelinePageBudget(count)
	}
	return c.searchFrom(ctx, query, count, cursor, maxPages, true)
}

func (c *Client) searchFrom(ctx context.Context, query string, count int, cursor string, maxPages int, fullPages bool) ([]Tweet, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", fmt.Errorf("x api: empty search query")
	}
	return c.pagedTimeline(ctx, opSearch, pagedOptions{
		limit: count, cursor: strings.TrimSpace(cursor), maxPages: maxPages, fullPages: fullPages,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"rawQuery":    query,
				"count":       pageCount,
				"querySource": "typed_query",
				"product":     "Latest",
			}, cursor)
		},
	})
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

func timelinePageBudget(count int) int {
	pages := (max(count, 1) + timelinePageSize - 1) / timelinePageSize
	return min(max(pages, 1), userTweetsHardMaxPages)
}

// UserTweets returns a user's recent tweets. handle may include a leading @.
//
// It returns the cursor to resume from alongside the tweets, because it is the
// one command whose JSON shape depends on it: bird's user-tweets switches from
// a bare array to {tweets, nextCursor} whenever -n exceeds one page.
func (c *Client) UserTweets(ctx context.Context, handle string, count int) ([]Tweet, string, error) {
	return c.UserTweetsFrom(ctx, handle, count, "", 0)
}

// UserTweetsFrom returns recent tweets starting at cursor. maxPages bounds the
// requests made; zero retains UserTweets' ten-page safety cap.
func (c *Client) UserTweetsFrom(ctx context.Context, handle string, count int, cursor string, maxPages int) ([]Tweet, string, error) {
	return c.userTimelineFrom(ctx, opUserTweets, handle, count, cursor, maxPages, false)
}

// UserTweetsPageAlignedFrom walks the posts timeline without truncating the
// terminal response page, so its returned cursor cannot skip entries.
func (c *Client) UserTweetsPageAlignedFrom(ctx context.Context, handle string, count int, cursor string, maxPages int) ([]Tweet, string, error) {
	return c.userTimelineFrom(ctx, opUserTweets, handle, count, cursor, maxPages, true)
}

func (c *Client) userTimelineFrom(ctx context.Context, op timelineOp, handle string, count int, cursor string, maxPages int, fullPages bool) ([]Tweet, string, error) {
	user, err := c.UserByScreenName(ctx, handle)
	if err != nil {
		return nil, "", err
	}

	// bird: effectiveMaxPages = min(10, ceil(limit / 20)). Preserve that
	// behavior for the legacy, truncating surface. Page-aligned monitoring may
	// need extra explicitly-budgeted pages when X returns only duplicates or
	// tombstones, so an explicit maxPages is authoritative there.
	requestedPages := timelinePageBudget(count)
	if maxPages <= 0 || (!fullPages && maxPages > requestedPages) {
		maxPages = requestedPages
	}

	return c.pagedTimeline(ctx, op, pagedOptions{
		limit:     count,
		maxPages:  maxPages,
		cursor:    strings.TrimSpace(cursor),
		fullPages: fullPages,
		pageDelay: c.userTweetsPageDelay,
		variables: func(pageCount int, cursor string) map[string]any {
			return withCursor(map[string]any{
				"userId": user.ID,
				"count":  pageCount,
				// Profile monitoring must not admit promoted/foreign authors.
				// bird likewise sends includePromotedContent:false here.
				"includePromotedContent":                 false,
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
func (c *Client) timelinePageFor(ctx context.Context, op timelineOp, variables map[string]any, strict bool) (timelinePage, error) {
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
	if strict {
		return parseTimelinePageStrict(body, op.roots)
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
	// cursor resumes a walk after a previously returned Bottom cursor.
	cursor string
	// fullPages preserves every parsed entry from the terminal response page.
	// It may return more than limit, but its cursor then resumes losslessly.
	fullPages bool
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
		cursor     = o.cursor
		nextCursor string
		pages      int
	)
	seen := make(map[string]bool)
	seenCursors := make(map[string]struct{})
	if cursor != "" {
		seenCursors[cursor] = struct{}{}
	}

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

		page, err := c.timelinePageFor(ctx, op, o.variables(pageCount, cursor), o.fullPages)
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
			if !o.fullPages && len(tweets) >= o.limit {
				break
			}
		}

		if page.cursor == "" {
			nextCursor = ""
			break
		}
		if page.cursor == cursor {
			if o.fullPages {
				return nil, "", fmt.Errorf("x api: timeline cursor did not advance after page %d", pages)
			}
			nextCursor = ""
			break
		}
		if _, repeated := seenCursors[page.cursor]; repeated {
			if o.fullPages {
				return nil, "", fmt.Errorf("x api: timeline cursor repeated after page %d", pages)
			}
			nextCursor = ""
			break
		}
		seenCursors[page.cursor] = struct{}{}
		if o.fullPages {
			nextCursor = page.cursor
			if o.maxPages > 0 && pages >= o.maxPages {
				break
			}
			cursor = page.cursor
			continue
		}
		if len(page.tweets) == 0 || added == 0 {
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
		if !ok || string(raw) == "null" {
			continue
		}
		var instructions []instruction
		if err := json.Unmarshal(raw, &instructions); err != nil {
			continue
		}
		tweets, err := tweetsFromInstructions(instructions, false)
		if err != nil {
			return timelinePage{}, err
		}
		return timelinePage{tweets: tweets, cursor: bottomCursorFromInstructions(instructions)}, nil
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
	// A present instructions:[] above is a legitimate empty timeline. Reaching
	// here means the response no longer matches any known collection root;
	// treating schema drift as empty would make monitors consume real events.
	return timelinePage{}, &APIError{Message: "unrecognized timeline response shape: no known instructions root"}
}

// parseTimelinePageStrict is the monitoring parser. Unlike the legacy parser,
// it treats ambiguity as failure: monitors persist cursors and classify
// relations, so silently accepting a partially understood page loses events.
func parseTimelinePageStrict(body []byte, roots [][]string) (timelinePage, error) {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return timelinePage{}, &APIError{Message: "decoding response: " + err.Error()}
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, item.Message)
		}
		return timelinePage{}, &APIError{Message: joinMessages(messages)}
	}

	for _, root := range roots {
		raw, found, err := navigateStrict(envelope.Data, root)
		if err != nil {
			return timelinePage{}, &APIError{Message: "malformed timeline root: " + err.Error()}
		}
		if !found {
			continue
		}
		if string(raw) == "null" {
			return timelinePage{}, &APIError{Message: "malformed timeline root: instructions is null"}
		}
		return parseStrictTimelineInstructions(raw)
	}
	return timelinePage{}, &APIError{Message: "unrecognized timeline response shape: no known instructions root"}
}

func navigateStrict(raw json.RawMessage, path []string) (json.RawMessage, bool, error) {
	current := raw
	for index, key := range path {
		if len(current) == 0 || string(current) == "null" {
			return nil, false, fmt.Errorf("%s is null before %s", strings.Join(path[:index], "."), key)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false, fmt.Errorf("%s is not an object", strings.Join(path[:index], "."))
		}
		next, ok := object[key]
		if !ok {
			if index > 2 {
				return nil, false, fmt.Errorf("selected root is missing %s", key)
			}
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}

func parseStrictTimelineInstructions(raw json.RawMessage) (timelinePage, error) {
	var instructions []json.RawMessage
	if err := json.Unmarshal(raw, &instructions); err != nil {
		return timelinePage{}, &APIError{Message: "malformed timeline instructions: " + err.Error()}
	}
	var (
		tweets []Tweet
		cursor string
	)
	seen := make(map[string]struct{})
	for index, rawInstruction := range instructions {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(rawInstruction, &object); err != nil || object == nil {
			return timelinePage{}, &APIError{Message: fmt.Sprintf("malformed timeline instruction %d", index)}
		}
		typeName, err := rawTypeDiscriminator(object, "timeline instruction")
		if err != nil {
			return timelinePage{}, err
		}
		entriesRaw, hasEntries := object["entries"]
		entryRaw, hasEntry := object["entry"]
		if hasEntries && hasEntry {
			return timelinePage{}, &APIError{Message: fmt.Sprintf("malformed timeline instruction %d: both entries and entry", index)}
		}
		if hasEntries {
			if typeName != "" && typeName != "TimelineAddEntries" {
				return timelinePage{}, &APIError{Message: fmt.Sprintf("unsupported timeline entries instruction %q", typeName)}
			}
			if string(entriesRaw) == "null" {
				return timelinePage{}, &APIError{Message: fmt.Sprintf("malformed timeline instruction %d: entries is null", index)}
			}
			var entries []json.RawMessage
			if err := json.Unmarshal(entriesRaw, &entries); err != nil {
				return timelinePage{}, &APIError{Message: fmt.Sprintf("malformed timeline instruction %d entries", index)}
			}
			for _, rawEntry := range entries {
				entryTweets, entryCursor, err := parseStrictTimelineEntry(rawEntry)
				if err != nil {
					return timelinePage{}, err
				}
				for _, post := range entryTweets {
					if _, duplicate := seen[post.ID]; !duplicate {
						seen[post.ID] = struct{}{}
						tweets = append(tweets, post)
					}
				}
				if entryCursor != "" {
					if cursor != "" && cursor != entryCursor {
						return timelinePage{}, &APIError{Message: "multiple conflicting Bottom cursors"}
					}
					cursor = entryCursor
				}
			}
			continue
		}
		if hasEntry {
			if (typeName != "TimelineReplaceEntry" && typeName != "TimelinePinEntry") || string(entryRaw) == "null" {
				return timelinePage{}, &APIError{Message: fmt.Sprintf("unsupported singular timeline instruction %q", typeName)}
			}
			entryTweets, entryCursor, err := parseStrictTimelineEntry(entryRaw)
			if err != nil {
				return timelinePage{}, err
			}
			for _, post := range entryTweets {
				if _, duplicate := seen[post.ID]; !duplicate {
					seen[post.ID] = struct{}{}
					tweets = append(tweets, post)
				}
			}
			if entryCursor != "" {
				if cursor != "" && cursor != entryCursor {
					return timelinePage{}, &APIError{Message: "multiple conflicting Bottom cursors"}
				}
				cursor = entryCursor
			}
			continue
		}
		if !isKnownDecorativeInstruction(typeName) {
			return timelinePage{}, &APIError{Message: fmt.Sprintf("unrecognized timeline instruction %d type %q", index, typeName)}
		}
		if err := validateNonDataInstructionKeys(object, typeName); err != nil {
			return timelinePage{}, err
		}
	}
	return timelinePage{tweets: tweets, cursor: cursor}, nil
}

func parseStrictTimelineEntry(raw json.RawMessage) ([]Tweet, string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, "", &APIError{Message: "malformed timeline entry"}
	}
	contentRaw, ok := object["content"]
	if !ok || string(contentRaw) == "null" {
		return nil, "", &APIError{Message: "malformed timeline entry: missing content"}
	}
	var parsed entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", &APIError{Message: "malformed timeline entry content"}
	}
	contentType, err := consistentDiscriminator(parsed.Content.EntryType, parsed.Content.TypeName, "timeline entry content")
	if err != nil {
		return nil, "", err
	}
	typedCursor := contentType == "TimelineTimelineCursor"
	if typedCursor && parsed.Content.CursorType == "" {
		return nil, "", &APIError{Message: "malformed typed timeline cursor: missing cursorType"}
	}
	hasData := parsed.Content.ItemContent != nil || parsed.Content.Item != nil || parsed.Content.Items != nil
	if parsed.Content.CursorType != "" && hasData {
		return nil, "", &APIError{Message: "malformed timeline entry: cursor content also carries data"}
	}
	if parsed.Content.CursorType == "Bottom" {
		if parsed.Content.Value == "" {
			return nil, "", &APIError{Message: "malformed Bottom cursor: empty value"}
		}
		return nil, parsed.Content.Value, nil
	}
	if parsed.Content.CursorType == "Top" && parsed.Content.Value == "" {
		return nil, "", &APIError{Message: "malformed Top cursor: empty value"}
	}
	if parsed.Content.CursorType != "" && parsed.Content.CursorType != "Top" {
		return nil, "", &APIError{Message: fmt.Sprintf("unsupported timeline cursor type %q", parsed.Content.CursorType)}
	}
	if err := validateStrictTimelineItemTypes(parsed); err != nil {
		return nil, "", err
	}
	nodes, err := collectFromEntry(parsed, false)
	if err != nil {
		return nil, "", err
	}
	tweets := make([]Tweet, 0, len(nodes))
	for _, node := range nodes {
		if isKnownNonTweetResult(node) {
			continue
		}
		post, err := mapMonitoringTweet(node)
		if err != nil {
			return nil, "", err
		}
		tweets = append(tweets, post)
	}
	return tweets, "", nil
}

func rawTypeDiscriminator(object map[string]json.RawMessage, context string) (string, error) {
	values := make(map[string]string, 2)
	for _, key := range []string{"type", "__typename"} {
		raw, present := object[key]
		if !present {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			return "", &APIError{Message: fmt.Sprintf("malformed %s discriminator %s", context, key)}
		}
		values[key] = value
	}
	return consistentDiscriminator(values["type"], values["__typename"], context)
}

func isKnownDecorativeInstruction(typeName string) bool {
	switch typeName {
	case "TimelineClearCache", "TimelineTerminateTimeline":
		return true
	default:
		return false
	}
}

func validateNonDataInstructionKeys(object map[string]json.RawMessage, typeName string) error {
	allowed := map[string]struct{}{"type": {}, "__typename": {}}
	if typeName == "TimelineTerminateTimeline" {
		allowed["direction"] = struct{}{}
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return &APIError{Message: fmt.Sprintf("non-data instruction %q has unsupported key %q", typeName, key)}
		}
	}
	if raw, present := object["direction"]; present {
		var direction string
		if err := json.Unmarshal(raw, &direction); err != nil || (direction != "Top" && direction != "Bottom") {
			return &APIError{Message: fmt.Sprintf("non-data instruction %q has malformed direction", typeName)}
		}
	}
	return nil
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
