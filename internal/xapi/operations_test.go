package xapi

import (
	"encoding/json"
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

	// An empty timeline is a legitimate answer, not a failure.
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
