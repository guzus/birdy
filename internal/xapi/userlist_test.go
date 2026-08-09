package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Sanitized from a live Following response on 2026-08-09. Only structural
// keys and the typename are retained; this instruction invalidates client
// cache state and carries no graph membership.
const liveFollowingNonDataInstructionsFixture = `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"type":"TimelineClearCache"},{"entries":[]},{"type":"TimelineTerminateTimeline"}]}}}}}}`

// userListBody wraps timeline entries in the envelope X returns, so each test
// writes only the part it cares about instead of counting braces.
func userListBody(entries string) []byte {
	return []byte(`{"data": {"user": {"result": {"timeline": {"timeline": {"instructions": [{"entries": ` + entries + `}]}}}}}}`)
}

func TestParseUserListReadsBothPayloadShapes(t *testing.T) {
	// legacy carries identity and counts; core-only is the newer shape.
	body := userListBody(`[
		{"content":{"itemContent":{"user_results":{"result":{
			"__typename":"User","rest_id":"1","is_blue_verified":true,
			"legacy":{"screen_name":"guzus","name":"Guzus","description":"bio",
				"followers_count":12,"friends_count":34,
				"profile_image_url_https":"https://img/a.jpg","created_at":"2020"}}}}}},
		{"content":{"itemContent":{"user_results":{"result":{
			"__typename":"User","rest_id":"2",
			"core":{"screen_name":"newer","name":"Newer","created_at":"2024"},
			"avatar":{"image_url":"https://img/b.jpg"}}}}}},
		{"content":{"cursorType":"Bottom","value":"CURSOR"}}
	]`)

	page, err := parseUserList(body)
	if err != nil {
		t.Fatalf("parseUserList: %v", err)
	}
	if len(page.Users) != 2 {
		t.Fatalf("got %d users, want 2", len(page.Users))
	}
	if page.NextCursor != "CURSOR" {
		t.Errorf("cursor = %q, want CURSOR", page.NextCursor)
	}

	legacy := page.Users[0]
	if legacy.Username != "guzus" || legacy.Name != "Guzus" || legacy.Description == nil || *legacy.Description != "bio" {
		t.Errorf("legacy identity wrong: %+v", legacy)
	}
	if legacy.FollowersCount == nil || *legacy.FollowersCount != 12 {
		t.Errorf("followers not read: %+v", legacy.FollowersCount)
	}
	if legacy.ProfileImageURL != "https://img/a.jpg" {
		t.Errorf("profile image = %q", legacy.ProfileImageURL)
	}

	// A core-only entry genuinely has no counts, so the pointers stay nil
	// rather than reporting a zero the API never sent.
	core := page.Users[1]
	if core.Username != "newer" || core.Name != "Newer" {
		t.Errorf("core identity wrong: %+v", core)
	}
	if core.FollowersCount != nil {
		t.Errorf("core-only payload should leave counts absent, got %d", *core.FollowersCount)
	}
	if core.ProfileImageURL != "https://img/b.jpg" {
		t.Errorf("avatar fallback not used: %q", core.ProfileImageURL)
	}
}

func TestParseUserListPreservesOptionalCountAndVerificationPresence(t *testing.T) {
	body := userListBody(`[
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"1","legacy":{"screen_name":"omitted","name":"Omitted"}}}}}},
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"2","is_blue_verified":false,"legacy":{"screen_name":"zero","name":"Zero","followers_count":0,"friends_count":0}}}}}}
	]`)
	page, err := parseUserList(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Users) != 2 {
		t.Fatalf("users = %+v", page.Users)
	}
	if page.Users[0].FollowersCount != nil || page.Users[0].FollowingCount != nil || page.Users[0].IsBlueVerified != nil {
		t.Fatalf("omitted fields became values: %+v", page.Users[0])
	}
	if page.Users[1].FollowersCount == nil || *page.Users[1].FollowersCount != 0 || page.Users[1].FollowingCount == nil || *page.Users[1].FollowingCount != 0 || page.Users[1].IsBlueVerified == nil || *page.Users[1].IsBlueVerified {
		t.Fatalf("explicit zero/false lost presence: %+v", page.Users[1])
	}
}

// X wraps some users in UserWithVisibilityResults; the real user is one level
// deeper and must still be read.
func TestParseUserListUnwrapsVisibilityResults(t *testing.T) {
	body := userListBody(`[
		{"content":{"itemContent":{"user_results":{"result":{
			"__typename":"UserWithVisibilityResults",
			"user":{"__typename":"User","rest_id":"9","legacy":{"screen_name":"deep","name":"Deep"}}}}}}}
	]`)

	page, err := parseUserList(body)
	if err != nil {
		t.Fatalf("parseUserList: %v", err)
	}
	if len(page.Users) != 1 || page.Users[0].Username != "deep" {
		t.Fatalf("wrapper not unwrapped: %+v", page.Users)
	}
}

// An empty list is a valid answer. Only a payload with errors and no timeline
// is a failure.
func TestParseUserListEmptyIsNotAnError(t *testing.T) {
	page, err := parseUserList(userListBody(`[]`))
	if err != nil {
		t.Fatalf("expected empty, got error: %v", err)
	}
	if len(page.Users) != 0 || page.NextCursor != "" {
		t.Errorf("expected an empty page, got %+v", page)
	}

	if _, err := parseUserList([]byte(`{"data":{}}`)); err == nil {
		t.Error("missing collection root must fail closed")
	}
	if _, err := parseUserList([]byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":null}}}}}}`)); err == nil {
		t.Error("null instructions must fail closed")
	}

	if _, err := parseUserList([]byte(`{"errors":[{"message":"nope"}]}`)); err == nil {
		t.Error("expected an error when X reports one and sends no timeline")
	}
}

// Explicit non-user entries are skipped, but malformed user entries fail
// closed so a monitor cannot reconcile them as absent accounts.
func TestParseUserListSkipsNonUsers(t *testing.T) {
	body := userListBody(`[
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"TimelineMessagePrompt"}}}}},
		{"content":{"entryType":"TimelineTimelineModule","items":[]}},
		{"content":{"entryType":"TimelineTimelineModule","items":[
			{"itemContent":{"__typename":"TimelineMessagePrompt"}}
		]}},
		{"content":{"cursorType":"Top","value":"TOP"}}
	]`)

	page, err := parseUserList(body)
	if err != nil {
		t.Fatalf("parseUserList: %v", err)
	}
	if len(page.Users) != 0 {
		t.Errorf("expected no usable users, got %+v", page.Users)
	}
}

func TestParseUserListReadsNestedModuleUsers(t *testing.T) {
	body := userListBody(`[
		{"content":{"entryType":"TimelineTimelineModule","items":[
			{"item":{"itemContent":{"user_results":{"result":{
				"__typename":"User","rest_id":"8","legacy":{"screen_name":"nested","name":"Nested"}
			}}}}}
		]}}
	]`)
	page, err := parseUserList(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Users) != 1 || page.Users[0].ID != "8" || page.Users[0].Username != "nested" {
		t.Fatalf("nested module users = %+v, want @nested", page.Users)
	}
}

func TestParseUserListRejectsMalformedUserEntries(t *testing.T) {
	cases := []string{
		`[{"content":{}}]`,
		`[{"content":{"entryType":"TimelineTimelineModule"}}]`,
		`[{"content":{"entryType":"TimelineTimelineModule","items":[{}]}}]`,
		`[{"content":{"entryType":"TimelineTimelineModule","items":[],"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"8","legacy":{"screen_name":"hidden","name":"Hidden"}}}}}}]`,
		`[{"content":{"entryType":"TimelineTimelineModule","items":[{"item":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"8"}}}}}]}}]`,
		`[{"content":{"itemContent":{"__typename":"TimelineTimelineModule"}}}]`,
		`[{"content":{"entryType":"TimelineTimelineCursor"}}]`,
		`[{"content":{"entryType":"TimelineTimelineCursor","__typename":"TimelineTweet","cursorType":"Bottom","value":"C"}}]`,
		`[{"content":{"cursorType":"Bottom","value":""}}]`,
		`[{"content":{"cursorType":"Top","value":""}}]`,
		`[{"content":{"cursorType":"Bottom","value":"C","itemContent":{"__typename":"TimelineMessagePrompt"}}}]`,
		`[{"content":{"entryType":"TimelineTimelineModule","items":[{"itemContent":{"__typename":"TimelineTimelineCursor"}}]}}]`,
		`[{"content":{"itemContent":{"__typename":"TimelineUserUnavailable"}}}]`,
		`[{"content":{"itemContent":{"__typename":"TimelineUserTombstone"}}}]`,
		`[{"content":{"entryType":"TimelineTimelineItem","itemContent":{"__typename":"TimelineUser"}}}]`,
		`[{"content":{"itemContent":{"user_results":{}}}}]`,
		`[{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":""}}}}}]`,
		`[{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"3"}}}}}]`,
		`[{"content":{"itemContent":{"user_results":{"result":{"__typename":"NewUnknownUserShape"}}}}}]`,
	}
	for _, entries := range cases {
		if _, err := parseUserList(userListBody(entries)); err == nil {
			t.Errorf("malformed entries accepted: %s", entries)
		}
	}
}

func TestParseUserListRejectsEnvelopeAndInstructionAmbiguity(t *testing.T) {
	page, err := parseUserList([]byte(liveFollowingNonDataInstructionsFixture))
	if err != nil || len(page.Users) != 0 || page.NextCursor != "" {
		t.Fatalf("live clear-cache fixture = %+v, %v", page, err)
	}

	withErrors := []byte(`{"errors":[{"message":"partial"}],"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[]}}}}}}`)
	if page, err := parseUserList(withErrors); err == nil || page != nil {
		t.Fatalf("partial GraphQL error accepted: page=%+v err=%v", page, err)
	}
	for _, instructions := range []string{
		`[{}]`,
		`[{"type":"TimelineClearCache","__typename":"UnknownInstruction","items":[{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"7","legacy":{"screen_name":"hidden","name":"Hidden"}}}}}]}]`,
		`[{"type":"TimelineClearCache","items":[{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"7","legacy":{"screen_name":"hidden","name":"Hidden"}}}}}]}]`,
		`[{"type":"TimelineTerminateTimeline","user_results":{"result":{"__typename":"User","rest_id":"7"}}}]`,
		`[{"type":null}]`,
		`[{"type":"NewInstruction","entries":[]}]`,
		`[{"type":"TimelineReplaceEntry","entry":{"content":{"itemContent":{"__typename":"TimelineMessagePrompt"}}}}]`,
	} {
		body := []byte(`{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":` + instructions + `}}}}}}`)
		if page, err := parseUserList(body); err == nil || page != nil {
			t.Fatalf("instructions=%s page=%+v err=%v, want failure", instructions, page, err)
		}
	}
}

func TestParseRESTUserListDistinguishesEmptyFromMissing(t *testing.T) {
	page, err := parseRESTUserList([]byte(`{"users":[],"next_cursor_str":"0"}`))
	if err != nil || len(page.Users) != 0 {
		t.Fatalf("present empty users = %+v, %v", page, err)
	}
	if _, err := parseRESTUserList([]byte(`{"next_cursor_str":"0"}`)); err == nil {
		t.Fatal("missing users collection must fail closed")
	}
	for _, body := range []string{`{"users":[]}`, `{"users":[],"next_cursor_str":""}`} {
		if page, err := parseRESTUserList([]byte(body)); err == nil || page != nil {
			t.Fatalf("ambiguous REST cursor accepted: page=%+v err=%v body=%s", page, err, body)
		}
	}
	for _, body := range []string{
		`{"users":[{"screen_name":"has-name"}],"next_cursor_str":"0"}`,
		`{"users":[{"id_str":"1"}],"next_cursor_str":"0"}`,
		`{"users":[{"id_str":"1","screen_name":"valid"},{"screen_name":"invalid"}],"next_cursor_str":"0"}`,
	} {
		if page, err := parseRESTUserList([]byte(body)); err == nil || page != nil {
			t.Fatalf("malformed REST users accepted: page=%+v err=%v body=%s", page, err, body)
		}
	}
}

func TestParseRESTUserListDoesNotConfuseLegacyAndBlueVerification(t *testing.T) {
	page, err := parseRESTUserList([]byte(`{"users":[
		{"id_str":"1","screen_name":"legacy","name":"Legacy","verified":true}
	],"next_cursor_str":"0"}`))
	if err != nil || len(page.Users) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Users[0].IsBlueVerified != nil {
		t.Fatalf("legacy verified became blue: %+v", page.Users[0])
	}
}

func TestParseUserListRejectsUnavailableIdentity(t *testing.T) {
	for _, typeName := range []string{"UserUnavailable", "UserTombstone"} {
		body := userListBody(`[{"content":{"itemContent":{"user_results":{"result":{"__typename":"` + typeName + `"}}}}}]`)
		if page, err := parseUserList(body); err == nil || page != nil {
			t.Fatalf("%s accepted: page=%+v err=%v", typeName, page, err)
		}
	}
}

// The GraphQL Followers operation 404s constantly, so the REST fallback is the
// path that actually serves the command. This pins that a 404 falls through
// rather than surfacing, which is how it shipped broken.
func TestFollowersFallsBackToRESTOn404(t *testing.T) {
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer graphQL.Close()

	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"users":[{"id_str":"7","screen_name":"guzus","name":"Guzus",
			"description":"bio","followers_count":57,"friends_count":9,
			"profile_image_url_https":"https://img/a.jpg"}],"next_cursor_str":"0"}`))
	}))
	defer rest.Close()

	c, err := NewClient(Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetBaseURL(graphQL.URL)
	c.SetUserListRESTPaths([]string{rest.URL})

	page, err := c.Followers(context.Background(), "1", 3, "")
	if err != nil {
		t.Fatalf("Followers: %v", err)
	}
	if len(page.Users) != 1 || page.Users[0].Username != "guzus" {
		t.Fatalf("REST fallback did not produce users: %+v", page.Users)
	}
	if page.Users[0].FollowersCount == nil || *page.Users[0].FollowersCount != 57 {
		t.Errorf("counts not mapped from the REST shape: %+v", page.Users[0])
	}
	// "0" is X's terminator, not a usable cursor.
	if page.NextCursor != "" {
		t.Errorf("next_cursor_str of 0 must not become a cursor, got %q", page.NextCursor)
	}
}

// A rate limit must not be mistaken for "GraphQL is down, try REST" — that
// would spend a second request to earn the same 429.
func TestFollowersDoesNotFallBackOnRateLimit(t *testing.T) {
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer graphQL.Close()

	var restHits int
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		restHits++
		w.Write([]byte(`{"users":[]}`))
	}))
	defer rest.Close()

	c, _ := NewClient(Credentials{AuthToken: "a", CT0: "b"})
	c.SetBaseURL(graphQL.URL)
	c.SetUserListRESTPaths([]string{rest.URL})

	if _, err := c.Followers(context.Background(), "1", 3, ""); !IsRateLimited(err) {
		t.Errorf("expected the rate limit to surface, got %v", err)
	}
	if restHits != 0 {
		t.Errorf("REST must not be tried after a 429, got %d hits", restHits)
	}
}

func TestFollowingDoesNotFallBackOnHTTP200GraphQLRateLimit(t *testing.T) {
	graphQL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"code":88,"message":"Rate limit exceeded"}]}`))
	}))
	defer graphQL.Close()

	var restHits int
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		restHits++
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	defer rest.Close()

	c, _ := NewClient(Credentials{AuthToken: "a", CT0: "b"})
	c.SetBaseURL(graphQL.URL)
	c.SetUserListRESTPaths([]string{rest.URL})
	_, err := c.Following(context.Background(), "1", 3, "")
	if !IsRateLimited(err) {
		t.Fatalf("HTTP-200 envelope = %v, want rate-limit error", err)
	}
	if restHits != 0 {
		t.Fatalf("REST fallback received %d calls after rate limit", restHits)
	}
}
