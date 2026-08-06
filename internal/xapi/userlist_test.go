package xapi

import "testing"

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
	if legacy.Username != "guzus" || legacy.Name != "Guzus" || legacy.Description != "bio" {
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
	page, err := parseUserList([]byte(`{"data":{}}`))
	if err != nil {
		t.Fatalf("expected empty, got error: %v", err)
	}
	if len(page.Users) != 0 || page.NextCursor != "" {
		t.Errorf("expected an empty page, got %+v", page)
	}

	if _, err := parseUserList([]byte(`{"errors":[{"message":"nope"}]}`)); err == nil {
		t.Error("expected an error when X reports one and sends no timeline")
	}
}

// Entries that are not users (ads, prompts, malformed results) are skipped
// rather than becoming blank rows.
func TestParseUserListSkipsNonUsers(t *testing.T) {
	body := userListBody(`[
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"TimelineMessagePrompt"}}}}},
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":""}}}}},
		{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"3"}}}}}
	]`)

	page, err := parseUserList(body)
	if err != nil {
		t.Fatalf("parseUserList: %v", err)
	}
	// The third entry has an id but no username, which is also unusable.
	if len(page.Users) != 0 {
		t.Errorf("expected no usable users, got %+v", page.Users)
	}
}
