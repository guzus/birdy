package xapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClampCount(t *testing.T) {
	cases := map[string]struct{ in, want int }{
		"zero defaults":      {0, 20},
		"negative defaults":  {-5, 20},
		"in range preserved": {35, 35},
		"over cap clamped":   {500, 100},
		"at cap preserved":   {100, 100},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := clampCount(tc.in); got != tc.want {
				t.Errorf("clampCount(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestNavigate(t *testing.T) {
	raw := json.RawMessage(`{"a":{"b":{"c":[1,2,3]}}}`)

	t.Run("walks a full path", func(t *testing.T) {
		got, ok := navigate(raw, []string{"a", "b", "c"})
		if !ok {
			t.Fatal("navigate returned not-ok for a valid path")
		}
		if string(got) != "[1,2,3]" {
			t.Errorf("got %s, want [1,2,3]", got)
		}
	})

	t.Run("reports a missing key", func(t *testing.T) {
		if _, ok := navigate(raw, []string{"a", "nope"}); ok {
			t.Error("navigate returned ok for a missing key")
		}
	})

	t.Run("reports a non-object mid-path", func(t *testing.T) {
		if _, ok := navigate(raw, []string{"a", "b", "c", "d"}); ok {
			t.Error("navigate walked into an array as if it were an object")
		}
	})
}

// timelineFixture builds a response with the instructions at the given path.
func timelineFixture(path []string) []byte {
	inner := `{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{` +
		`"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},` +
		`"legacy":{"full_text":"hello"}}}}}}]}`
	payload := "[" + inner + "]"
	for i := len(path) - 1; i >= 0; i-- {
		payload = `{"` + path[i] + `":` + payload + `}`
	}
	return []byte(`{"data":` + payload + `}`)
}

func TestParseTimeline(t *testing.T) {
	searchPath := []string{"search_by_raw_query", "search_timeline", "timeline", "instructions"}

	t.Run("maps tweets at a known root", func(t *testing.T) {
		tweets, err := parseTimeline(timelineFixture(searchPath), opSearch.roots)
		if err != nil {
			t.Fatalf("parseTimeline returned error: %v", err)
		}
		if len(tweets) != 1 || tweets[0].ID != "1" {
			t.Errorf("got %+v, want one tweet with id 1", tweets)
		}
	})

	// Operations whose wrapper varies declare several roots; the first that
	// matches must win rather than the whole parse failing.
	t.Run("falls through to an alternate root", func(t *testing.T) {
		altPath := []string{"user", "result", "timeline_v2", "timeline", "instructions"}
		tweets, err := parseTimeline(timelineFixture(altPath), opUserTweets.roots)
		if err != nil {
			t.Fatalf("parseTimeline returned error: %v", err)
		}
		if len(tweets) != 1 {
			t.Errorf("got %d tweets, want 1 from the alternate root", len(tweets))
		}
	})

	// A present empty collection is legitimate.
	t.Run("empty timeline is not an error", func(t *testing.T) {
		body := []byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[]}}}}}`)
		tweets, err := parseTimeline(body, opSearch.roots)
		if err != nil {
			t.Fatalf("parseTimeline returned error: %v", err)
		}
		if len(tweets) != 0 {
			t.Errorf("got %d tweets, want 0", len(tweets))
		}
	})

	t.Run("malformed tweet item fails closed", func(t *testing.T) {
		for name, result := range map[string]string{
			"missing author": `{"rest_id":"1","legacy":{"full_text":"hello"}}`,
			"missing text":   `{"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}}}`,
		} {
			t.Run(name, func(t *testing.T) {
				body := []byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":` + result + `}}}}}] }]}}}}}`)
				if tweets, err := parseTimeline(body, opSearch.roots); err == nil || len(tweets) != 0 {
					t.Fatalf("tweets=%+v err=%v, want discarded result and error", tweets, err)
				}
			})
		}
	})

	t.Run("known non-tweet result is allowed", func(t *testing.T) {
		body := []byte(`{
			"data": {"search_by_raw_query": {"search_timeline": {"timeline": {
				"instructions": [{"entries": [
					{"content": {"itemContent": {"tweet_results": {"result": {"__typename": "TweetTombstone"}}}}}
				]}]
			}}}}
		}`)
		tweets, err := parseTimeline(body, opSearch.roots)
		if err != nil || len(tweets) != 0 {
			t.Fatalf("tweets=%+v err=%v, want allowed tombstone", tweets, err)
		}
	})

	t.Run("missing tweet result is not an implicit tombstone", func(t *testing.T) {
		items := []string{
			`{"__typename":"TimelineTweet"}`,
			`{"tweet_results":{}}`,
			`{"__typename":"UnknownTimelineItem"}`,
		}
		for _, item := range items {
			body := []byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":` + item + `}}]}]}}}}}`)
			if tweets, err := parseTimeline(body, opSearch.roots); err == nil || len(tweets) != 0 {
				t.Fatalf("item=%s tweets=%+v err=%v, want fail closed", item, tweets, err)
			}
		}
	})

	t.Run("only typed empty modules are allowed", func(t *testing.T) {
		wrap := func(content string) []byte {
			return []byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[{"content":` + content + `}]}]}}}}}`)
		}
		if tweets, err := parseTimeline(wrap(`{"entryType":"TimelineTimelineModule","items":[]}`), opSearch.roots); err != nil || len(tweets) != 0 {
			t.Fatalf("typed empty module = %+v, %v; want legitimate empty", tweets, err)
		}
		for _, content := range []string{`{"items":[]}`, `{"entryType":"TimelineTimelineModule"}`} {
			if tweets, err := parseTimeline(wrap(content), opSearch.roots); err == nil || len(tweets) != 0 {
				t.Fatalf("content=%s tweets=%+v err=%v, want fail closed", content, tweets, err)
			}
		}
		mixed := `{"entryType":"TimelineTimelineModule","items":[],"itemContent":{"__typename":"TimelineTweet","tweet_results":{}}}`
		if tweets, err := parseTimeline(wrap(mixed), opSearch.roots); err == nil || len(tweets) != 0 {
			t.Fatalf("mixed module tweets=%+v err=%v, want fail closed", tweets, err)
		}
	})

	t.Run("missing collection root fails closed", func(t *testing.T) {
		if _, err := parseTimeline([]byte(`{"data":{"user":{"result":{}}}}`), opUserTweets.roots); err == nil {
			t.Fatal("missing timeline root must not be treated as an empty timeline")
		}
	})

	t.Run("null collection root fails closed", func(t *testing.T) {
		body := []byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":null}}}}}}`)
		if _, err := parseTimeline(body, opUserTweets.roots); err == nil {
			t.Fatal("null instructions must not be treated as present-empty")
		}
	})

	t.Run("surfaces the API error when no root matches", func(t *testing.T) {
		body := []byte(`{"errors":[{"message":"Rate limit exceeded"}],"data":{}}`)
		if _, err := parseTimeline(body, opSearch.roots); err == nil {
			t.Error("parseTimeline = nil error, want X's error surfaced")
		}
	})

	t.Run("rejects garbage", func(t *testing.T) {
		if _, err := parseTimeline([]byte("not json"), opSearch.roots); err == nil {
			t.Error("parseTimeline(garbage) = nil error, want error")
		}
	})
}

func TestStrictTimelineParserFailsClosed(t *testing.T) {
	validEntry := `{"content":{"itemContent":{"tweet_results":{"result":{
		"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
		"legacy":{"full_text":"hello"}
	}}}}}`

	t.Run("errors win over otherwise valid data", func(t *testing.T) {
		body := []byte(`{"errors":[{"message":"partial failure"}],"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[` + validEntry + `]}]}}}}}`)
		if _, err := parseTimelinePageStrict(body, opSearch.roots); err == nil {
			t.Fatal("GraphQL errors with data must fail monitoring")
		}
	})

	t.Run("malformed preferred root cannot fall through", func(t *testing.T) {
		for _, preferred := range []string{"null", `{}`} {
			body := []byte(`{"data":{"user":{"result":{"timeline":{"timeline":` + preferred + `},"timeline_v2":{"timeline":{"instructions":[]}}}}}}`)
			if _, err := parseTimelinePageStrict(body, opUserTweets.roots); err == nil {
				t.Fatalf("preferred root %s fell through to alternate", preferred)
			}
		}
	})

	t.Run("instruction and entry presence", func(t *testing.T) {
		bad := []string{
			`[{}]`,
			`[{"type":"NewInstruction"}]`,
			`[{"type":"NewInstruction","entries":[]}]`,
			`[{"type":"NewSingular","entry":` + validEntry + `}]`,
			`[{"entries":[{}]}]`,
			`[{"entries":[{"content":{"entryType":"TimelineTimelineCursor"}}]}]`,
			`[{"entries":[{"content":{"cursorType":"Bottom","value":""}}]}]`,
			`[{"entries":[{"content":{"cursorType":"Top","value":""}}]}]`,
			`[{"entries":[{"content":{"cursorType":"Bottom","value":"C","itemContent":{"__typename":"TimelineTweet","tweet_results":{}}}}]}]`,
		}
		for _, raw := range bad {
			if _, err := parseStrictTimelineInstructions([]byte(raw)); err == nil {
				t.Errorf("strict instructions accepted %s", raw)
			}
		}
	})

	t.Run("known singular instructions", func(t *testing.T) {
		page, err := parseStrictTimelineInstructions([]byte(`[{"type":"TimelinePinEntry","entry":` + validEntry + `}]`))
		if err != nil || len(page.tweets) != 1 || page.tweets[0].ID != "1" {
			t.Fatalf("pinned tweet = %+v, %v", page, err)
		}
		page, err = parseStrictTimelineInstructions([]byte(`[{"type":"TimelineReplaceEntry","entry":{"content":{"entryType":"TimelineTimelineCursor","cursorType":"Bottom","value":"NEXT"}}}]`))
		if err != nil || page.cursor != "NEXT" {
			t.Fatalf("replacement cursor = %+v, %v", page, err)
		}
	})

	t.Run("relations are strict and quote depth independent", func(t *testing.T) {
		t.Setenv("BIRDY_QUOTE_DEPTH", "0")
		quoted := `{"rest_id":"2","core":{"user_results":{"result":{"legacy":{"screen_name":"q","name":"Q"}}}},"legacy":{"full_text":"quoted"}}`
		outer := `{"content":{"itemContent":{"tweet_results":{"result":{
			"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},
			"legacy":{"full_text":"outer"},"quoted_status_result":{"result":` + quoted + `}
		}}}}}`
		page, err := parseStrictTimelineInstructions([]byte(`[{"entries":[` + outer + `]}]`))
		if err != nil || len(page.tweets) != 1 || page.tweets[0].QuotedTweet == nil || page.tweets[0].QuotedTweet.ID != "2" {
			t.Fatalf("strict quote = %+v, %v", page, err)
		}
		for _, relation := range []string{`"quoted_status_result":{}`, `"legacy":{"full_text":"outer","retweeted_status_result":{}}`} {
			legacy := `"legacy":{"full_text":"outer"},`
			if strings.HasPrefix(relation, `"legacy"`) {
				legacy = ""
			}
			entry := `{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"1","core":{"user_results":{"result":{"legacy":{"screen_name":"a","name":"A"}}}},` + legacy + relation + `}}}}}`
			if _, err := parseStrictTimelineInstructions([]byte(`[{"entries":[` + entry + `]}]`)); err == nil {
				t.Errorf("malformed relation accepted: %s", relation)
			}
		}
	})
}

// Every timeline operation must declare a query id and at least one root, or it
// will fail only at runtime against the live API.
func TestTimelineOpsAreWiredUp(t *testing.T) {
	ops := map[string]timelineOp{
		"search":        opSearch,
		"user-tweets":   opUserTweets,
		"home":          opHomeTimeline,
		"home-latest":   opHomeLatestTimeline,
		"likes":         opLikes,
		"bookmarks":     opBookmarks,
		"list-timeline": opListTimeline,
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			if len(queryIDs[op.name]) == 0 {
				t.Errorf("no generated query id for operation %q", op.name)
			}
			if len(op.features) == 0 {
				t.Errorf("no feature set for operation %q", op.name)
			}
			if len(op.roots) == 0 {
				t.Errorf("no response roots for operation %q", op.name)
			}
			for _, root := range op.roots {
				if root[len(root)-1] != "instructions" {
					t.Errorf("root %v does not end at instructions", root)
				}
			}
		})
	}

	// SearchTimeline 404s when sent as a GET; the flag must not regress.
	if !opSearch.post {
		t.Error("opSearch.post = false; SearchTimeline must be issued as a POST")
	}
}

func TestNormalizeHandle(t *testing.T) {
	cases := map[string]string{
		"SpaceX":                       "SpaceX",
		"@SpaceX":                      "SpaceX",
		"  @SpaceX  ":                  "SpaceX",
		"https://x.com/SpaceX":         "SpaceX",
		"https://twitter.com/SpaceX":   "SpaceX",
		"x.com/SpaceX/status/123":      "SpaceX",
		"https://x.com/SpaceX?lang=en": "SpaceX",
		"":                             "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := NormalizeHandle(in); got != want {
				t.Errorf("NormalizeHandle(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestOperationQueryIDsHonorsOverride(t *testing.T) {
	t.Setenv("BIRDY_TWEET_DETAIL_QUERY_ID", "override-hash")

	ids := operationQueryIDs("TweetDetail")
	if len(ids) == 0 || ids[0] != "override-hash" {
		t.Fatalf("operationQueryIDs = %v, want the override first", ids)
	}
	// The generated hashes must remain as fallbacks behind the override.
	if len(ids) < 2 {
		t.Errorf("operationQueryIDs = %v, want generated ids retained", ids)
	}
}

func TestCamelToSnake(t *testing.T) {
	cases := map[string]string{
		"TweetDetail":              "Tweet_Detail",
		"SearchTimeline":           "Search_Timeline",
		"ListLatestTweetsTimeline": "List_Latest_Tweets_Timeline",
	}
	for in, want := range cases {
		if got := camelToSnake(in); got != want {
			t.Errorf("camelToSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractQueryIDs(t *testing.T) {
	// The exact shape X's bundler emits.
	source := `something,e.exports={queryId:"abc123DEF456",operationName:"SearchTimeline"},more` +
		`e.exports={operationName:"HomeTimeline",queryId:"xyz789GHI012"}`

	ids := make(map[string]string)
	extractQueryIDs(source, ids)

	if ids["SearchTimeline"] != "abc123DEF456" {
		t.Errorf("SearchTimeline = %q, want abc123DEF456", ids["SearchTimeline"])
	}
	if ids["HomeTimeline"] != "xyz789GHI012" {
		t.Errorf("HomeTimeline = %q, want xyz789GHI012", ids["HomeTimeline"])
	}

	t.Run("ignores malformed hashes", func(t *testing.T) {
		ids := make(map[string]string)
		extractQueryIDs(`e.exports={queryId:"short",operationName:"Nope"}`, ids)
		if _, ok := ids["Nope"]; ok {
			t.Error("accepted a hash that is too short to be real")
		}
	})
}
