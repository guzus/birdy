package tweet

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// These are the unkeyed literals external v1 callers can already have in
// source. Keeping them in the compile graph prevents an additive-looking field
// from becoming an accidental source break.
var (
	_ = Options{"", "", ""}
	_ = Tweet{"", "", "", 0, 0, 0, "", "", Author{}, "", nil, nil, nil}
)

// pkgQualifier matches a leading package selector on a type name, so that
// "xapi.Author" and "tweet.Author" compare equal as "Author".
var pkgQualifier = regexp.MustCompile(`\b[a-z][a-z0-9_]*\.`)

type fieldSpec struct {
	Type string
	JSON string
}

func specOf(t reflect.Type) map[string]fieldSpec {
	spec := make(map[string]fieldSpec, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		spec[f.Name] = fieldSpec{
			Type: pkgQualifier.ReplaceAllString(f.Type.String(), ""),
			JSON: f.Tag.Get("json"),
		}
	}
	return spec
}

// TestPublicTypesCoverParserFields is the guard that makes the boundary in
// types.go worth having.
//
// pkg/tweet's structs used to be aliases into internal/xapi, so editing the
// parser silently changed birdy's public API. They are now separate
// declarations, which means the opposite hazard: the parser can grow a field
// and the public type can quietly fall behind. This test pins them together,
// so drift in either direction is a failure someone must resolve on purpose —
// either by mirroring the change (and treating it as an API change) or by
// documenting the divergence here.
func TestPublicTypesCoverParserFields(t *testing.T) {
	cases := []struct {
		name     string
		public   reflect.Type
		internal reflect.Type
	}{
		{"Tweet", reflect.TypeOf(Tweet{}), reflect.TypeOf(xapi.Tweet{})},
		{"Media", reflect.TypeOf(Media{}), reflect.TypeOf(xapi.Media{})},
		{"Author", reflect.TypeOf(Author{}), reflect.TypeOf(xapi.Author{})},
		{"Article", reflect.TypeOf(Article{}), reflect.TypeOf(xapi.Article{})},
		// FullTweet mirrors the parser's enriched view field for field, so
		// `--json-full` and the Go API cannot drift apart.
		{"FullTweet", reflect.TypeOf(FullTweet{}), reflect.TypeOf(xapi.FullTweet{})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub, inner := specOf(tc.public), specOf(tc.internal)

			for name, want := range inner {
				if tc.name == "Tweet" && name == "RepostedTweet" {
					continue // exposed by monitoring-only TimelineTweet, not frozen Tweet
				}
				got, ok := pub[name]
				if !ok {
					t.Errorf("xapi.%s has field %s that pkg/tweet.%s is missing; "+
						"mirror it in types.go and convertTweet (this is a public API addition)",
						tc.name, name, tc.name)
					continue
				}
				if got != want {
					t.Errorf("field %s.%s drifted: public %+v, parser %+v", tc.name, name, got, want)
				}
			}

			for name := range pub {
				if _, ok := inner[name]; !ok {
					t.Errorf("pkg/tweet.%s has field %s with no parser counterpart; "+
						"removing it from xapi.%s is a breaking public API change",
						tc.name, name, tc.name)
				}
			}
		})
	}
}

// TestPublicFieldOrderIsFrozen pins this package's declaration order as a
// constant, deliberately NOT to internal/xapi's.
//
// The previous version of this test tied the two together, reasoning that
// encoding/json emits keys in declaration order and birdy's --json is a
// byte-for-byte contract with bird, so the order was part of the public API.
// The premise was wrong: the CLI marshals xapi.Tweet, not this package's Tweet.
// cmd imports pkg/tweet for ExtractTweetID and nothing else, so nothing about
// birdy's output depends on the order here.
//
// Tying them together did real harm. Every time bird's payload shape shifted,
// internal/xapi would reorder to keep --json byte-identical, this test would
// fail, and the obvious fix was to reorder the PUBLIC struct too — silently
// breaking any caller using an unkeyed composite literal, for no benefit.
//
// So the order is frozen here instead. Reordering these fields is a breaking
// change, and this test is what makes it impossible to do by accident.
// TestPublicTypesCoverParserFields still catches a field added, renamed or
// re-tagged upstream, which is the drift that actually matters.
func TestPublicFieldOrderIsFrozen(t *testing.T) {
	frozen := map[string][]string{
		// This is the order v1.0.0 shipped. It happens to match bird's JSON key
		// order, because that is where it came from — but it is frozen on its
		// own terms now, and will not follow bird if bird's changes.
		"Tweet": {
			"ID", "Text", "CreatedAt", "ReplyCount", "RetweetCount", "LikeCount",
			"ConversationID", "InReplyToStatusID", "Author", "AuthorID",
			"QuotedTweet", "Media", "Article",
		},
		"Author":              {"Username", "Name"},
		"Media":               {"Type", "URL", "Width", "Height", "PreviewURL", "VideoURL", "DurationMs"},
		"Article":             {"Title", "PreviewText"},
		"Options":             {"Strategy", "Account", "AccountsJSON"},
		"MonitoringOptions":   {"Strategy", "Account", "AccountsJSON", "AccountPool"},
		"UserTimelineOptions": {"Limit", "Cursor", "MaxPages"},
		"TimelinePage":        {"Tweets", "NextCursor"},
		"TimelineTweet":       {"Tweet", "RepostedTweet"},
		// FullTweet's prefix is Tweet's order verbatim — that is what makes
		// --json-full a strict superset of --json — followed by the extras.
		"FullTweet": {
			"ID", "Text", "CreatedAt", "ReplyCount", "RetweetCount", "LikeCount",
			"ConversationID", "InReplyToStatusID", "Author", "AuthorID",
			"QuotedTweet", "Media", "Article", "RepostedTweet",
			"URL", "CreatedAtISO", "ViewCount", "QuoteCount", "BookmarkCount",
			"Lang", "IsRepost", "IsReply", "IsQuote",
		},
		"FullTimelinePage": {"Tweets", "NextCursor"},
		"FollowingOptions": {"PageSize", "MaxPages", "Cursor"},
		// Unavailable was appended after v1.1. Every earlier field keeps its
		// name, type, and position, so keyed literals and the JSON contract are
		// unchanged; only an unkeyed literal of this struct would break, and
		// nothing constructs one.
		"FollowingUser": {
			"ID", "Username", "Name", "Description", "FollowersCount",
			"FollowingCount", "IsBlueVerified", "ProfileImageURL", "CreatedAt",
			"Unavailable",
		},
		"FollowingSnapshot": {"Users", "NextCursor", "Complete", "Pages"},
		"UserProfile": {
			"ID", "Username", "Name", "Description", "Followers", "Following",
			"Tweets", "Verified", "CreatedAt",
		},
		"NewsItem": {
			"ID", "Headline", "Category", "TimeAgo", "Description", "URL",
		},
		"AboutProfile": {
			"AccountBasedIn", "Source", "CreatedCountryAccurate", "LocationAccurate", "LearnMoreURL",
		},
		"Viewer": {"ID", "Username", "Name"},
		"List": {
			"ID", "Name", "Description", "MemberCount", "SubscriberCount",
			"IsPrivate", "CreatedAt", "Owner",
		},
		"ListOwner": {"ID", "Username", "Name"},
	}
	types := map[string]reflect.Type{
		"Tweet":               reflect.TypeOf(Tweet{}),
		"Author":              reflect.TypeOf(Author{}),
		"Media":               reflect.TypeOf(Media{}),
		"Article":             reflect.TypeOf(Article{}),
		"Options":             reflect.TypeOf(Options{}),
		"MonitoringOptions":   reflect.TypeOf(MonitoringOptions{}),
		"UserTimelineOptions": reflect.TypeOf(UserTimelineOptions{}),
		"TimelinePage":        reflect.TypeOf(TimelinePage{}),
		"TimelineTweet":       reflect.TypeOf(TimelineTweet{}),
		"FullTweet":           reflect.TypeOf(FullTweet{}),
		"FullTimelinePage":    reflect.TypeOf(FullTimelinePage{}),
		"FollowingOptions":    reflect.TypeOf(FollowingOptions{}),
		"FollowingUser":       reflect.TypeOf(FollowingUser{}),
		"FollowingSnapshot":   reflect.TypeOf(FollowingSnapshot{}),
		"UserProfile":         reflect.TypeOf(UserProfile{}),
		"NewsItem":            reflect.TypeOf(NewsItem{}),
		"AboutProfile":        reflect.TypeOf(AboutProfile{}),
		"Viewer":              reflect.TypeOf(Viewer{}),
		"List":                reflect.TypeOf(List{}),
		"ListOwner":           reflect.TypeOf(ListOwner{}),
	}

	for name, want := range frozen {
		t.Run(name, func(t *testing.T) {
			typ := types[name]
			var got []string
			for i := range typ.NumField() {
				if f := typ.Field(i); f.IsExported() {
					got = append(got, f.Name)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("field count changed: got %v, frozen %v\n"+
					"Changing an existing exported struct field set requires a major version; prefer a new type.\n"+
					"Removing one is a breaking change.", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("field order changed at %d: got %q, frozen %q\n"+
						"Reordering exported fields breaks unkeyed composite literals in "+
						"callers' code, silently. If this is intentional it is a major "+
						"version bump; otherwise put the field back.", i, got[i], want[i])
				}
			}
		})
	}
}

// TestMonitoringStructContractsAreFrozen pins name, Go type, JSON tag, and
// order for every new semver-covered monitoring struct. The older order-only
// guard is intentionally retained because its failure message explains the
// unkeyed-literal hazard; this one covers the rest of the wire/API contract.
func TestMonitoringStructContractsAreFrozen(t *testing.T) {
	type frozenField struct{ name, typ, json string }
	cases := map[string]struct {
		typ    reflect.Type
		fields []frozenField
	}{
		"MonitoringOptions": {reflect.TypeOf(MonitoringOptions{}), []frozenField{
			{"Strategy", "string", ""}, {"Account", "string", ""},
			{"AccountsJSON", "string", ""}, {"AccountPool", "[]string", ""},
		}},
		"UserTimelineOptions": {reflect.TypeOf(UserTimelineOptions{}), []frozenField{
			{"Limit", "int", ""}, {"Cursor", "string", ""}, {"MaxPages", "int", ""},
		}},
		"TimelinePage": {reflect.TypeOf(TimelinePage{}), []frozenField{
			{"Tweets", "[]TimelineTweet", "tweets"}, {"NextCursor", "string", "nextCursor,omitempty"},
		}},
		"TimelineTweet": {reflect.TypeOf(TimelineTweet{}), []frozenField{
			{"Tweet", "Tweet", ""}, {"RepostedTweet", "*Tweet", "repostedTweet,omitempty"},
		}},
		"FollowingOptions": {reflect.TypeOf(FollowingOptions{}), []frozenField{
			{"PageSize", "int", ""}, {"MaxPages", "int", ""}, {"Cursor", "string", ""},
		}},
		"FollowingUser": {reflect.TypeOf(FollowingUser{}), []frozenField{
			{"ID", "string", "id"}, {"Username", "string", "username"}, {"Name", "string", "name"},
			{"Description", "*string", "description,omitempty"}, {"FollowersCount", "*int", "followersCount,omitempty"},
			{"FollowingCount", "*int", "followingCount,omitempty"}, {"IsBlueVerified", "*bool", "isBlueVerified,omitempty"},
			{"ProfileImageURL", "string", "profileImageUrl,omitempty"}, {"CreatedAt", "string", "createdAt,omitempty"},
			{"Unavailable", "bool", "unavailable,omitempty"},
		}},
		"FollowingSnapshot": {reflect.TypeOf(FollowingSnapshot{}), []frozenField{
			{"Users", "[]FollowingUser", "users"}, {"NextCursor", "string", "nextCursor,omitempty"},
			{"Complete", "bool", "complete"}, {"Pages", "int", "pages"},
		}},
		"UserProfile": {reflect.TypeOf(UserProfile{}), []frozenField{
			{"ID", "string", "id"}, {"Username", "string", "username"}, {"Name", "string", "name"},
			{"Description", "string", "description,omitempty"}, {"Followers", "*int", "followers,omitempty"},
			{"Following", "*int", "following,omitempty"}, {"Tweets", "*int", "tweets,omitempty"},
			{"Verified", "bool", "verified"}, {"CreatedAt", "string", "createdAt,omitempty"},
		}},
		"NewsItem": {reflect.TypeOf(NewsItem{}), []frozenField{
			{"ID", "string", "id,omitempty"}, {"Headline", "string", "headline"},
			{"Category", "string", "category,omitempty"}, {"TimeAgo", "string", "timeAgo,omitempty"},
			{"Description", "string", "description,omitempty"}, {"URL", "string", "url,omitempty"},
		}},
		"AboutProfile": {reflect.TypeOf(AboutProfile{}), []frozenField{
			{"AccountBasedIn", "string", "accountBasedIn,omitempty"}, {"Source", "string", "source,omitempty"},
			{"CreatedCountryAccurate", "*bool", "createdCountryAccurate,omitempty"},
			{"LocationAccurate", "*bool", "locationAccurate,omitempty"},
			{"LearnMoreURL", "string", "learnMoreUrl,omitempty"},
		}},
		"Viewer": {reflect.TypeOf(Viewer{}), []frozenField{
			{"ID", "string", "id"}, {"Username", "string", "username"}, {"Name", "string", "name"},
		}},
		"List": {reflect.TypeOf(List{}), []frozenField{
			{"ID", "string", "id"}, {"Name", "string", "name"},
			{"Description", "*string", "description,omitempty"}, {"MemberCount", "*int", "memberCount,omitempty"},
			{"SubscriberCount", "*int", "subscriberCount,omitempty"}, {"IsPrivate", "bool", "isPrivate"},
			{"CreatedAt", "int64", "createdAt,omitempty"}, {"Owner", "*ListOwner", "owner,omitempty"},
		}},
		"ListOwner": {reflect.TypeOf(ListOwner{}), []frozenField{
			{"ID", "string", "id"}, {"Username", "string", "username"}, {"Name", "string", "name"},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.typ.NumField() != len(tc.fields) {
				t.Fatalf("field count = %d, want %d", tc.typ.NumField(), len(tc.fields))
			}
			for i, want := range tc.fields {
				got := tc.typ.Field(i)
				gotType := pkgQualifier.ReplaceAllString(got.Type.String(), "")
				if got.Name != want.name || gotType != want.typ || got.Tag.Get("json") != want.json {
					t.Fatalf("field %d = {%s %s %q}, want {%s %s %q}", i, got.Name, gotType, got.Tag.Get("json"), want.name, want.typ, want.json)
				}
			}
		})
	}
}

// TestConvertTweetCopiesEveryField fails when a field is added to the public
// type but not wired into convertTweet, which would otherwise leave it
// permanently zero at runtime with every other test still passing.
func TestConvertTweetCopiesEveryField(t *testing.T) {
	src := xapi.Tweet{
		ID:                "1",
		Text:              "hello",
		CreatedAt:         "2026-08-07T00:00:00Z",
		ConversationID:    "2",
		InReplyToStatusID: "3",
		Author:            xapi.Author{Username: "guzus", Name: "Guzus"},
		AuthorID:          "4",
		Media: []xapi.Media{{
			Type:       "video",
			URL:        "https://example.com/thumb.jpg",
			PreviewURL: "https://example.com/preview.jpg",
			VideoURL:   "https://example.com/v.mp4",
			Width:      1920,
			Height:     1080,
			DurationMs: 4200,
		}},
		ReplyCount:   5,
		RetweetCount: 6,
		LikeCount:    7,
		QuotedTweet: &xapi.Tweet{
			ID:     "8",
			Text:   "quoted",
			Author: xapi.Author{Username: "other", Name: "Other"},
		},
		Article: &xapi.Article{Title: "An Article", PreviewText: "preview"},
	}

	got := convertTweet(src)

	// The article must cross the boundary as its own value, so mutating the
	// parser's copy afterwards cannot reach a caller's.
	if got.Article == nil {
		t.Fatal("Article dropped by convertTweet")
	}
	if got.Article == (*Article)(nil) || got.Article.Title != "An Article" || got.Article.PreviewText != "preview" {
		t.Errorf("article not converted: %+v", got.Article)
	}
	src.Article.Title = "mutated"
	if got.Article.Title != "An Article" {
		t.Error("convertArticle aliased the parser's value instead of copying it")
	}
	src.Article.Title = "An Article"

	v := reflect.ValueOf(got)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Errorf("Tweet.%s is zero after convertTweet; the field is probably "+
				"missing from the conversion", v.Type().Field(i).Name)
		}
	}

	mv := reflect.ValueOf(got.Media[0])
	for i := range mv.NumField() {
		if mv.Field(i).IsZero() {
			t.Errorf("Media.%s is zero after convertMedia", mv.Type().Field(i).Name)
		}
	}

	if got.Author.Username != "guzus" || got.Author.Name != "Guzus" {
		t.Errorf("Author not copied: %+v", got.Author)
	}

	// A quote must survive the boundary as its own converted value, not as a
	// pointer back into the parser's tree.
	if got.QuotedTweet == nil {
		t.Fatal("QuotedTweet dropped by convertTweet")
	}
	if got.QuotedTweet.ID != "8" || got.QuotedTweet.Author.Username != "other" {
		t.Errorf("quoted tweet not converted: %+v", got.QuotedTweet)
	}
}

// A quote chain must terminate rather than recursing forever, and the parser
// bounds it before this package ever sees it.
func TestConvertQuotedHandlesNesting(t *testing.T) {
	inner := &xapi.Tweet{ID: "3", Text: "inner", Author: xapi.Author{Username: "c"}}
	mid := &xapi.Tweet{ID: "2", Text: "mid", Author: xapi.Author{Username: "b"}, QuotedTweet: inner}
	got := convertTweet(xapi.Tweet{ID: "1", Text: "top", Author: xapi.Author{Username: "a"}, QuotedTweet: mid})

	if got.QuotedTweet == nil || got.QuotedTweet.QuotedTweet == nil {
		t.Fatalf("nested quote lost: %+v", got.QuotedTweet)
	}
	if got.QuotedTweet.QuotedTweet.ID != "3" {
		t.Errorf("inner quote wrong: %+v", got.QuotedTweet.QuotedTweet)
	}
	if got.QuotedTweet.QuotedTweet.QuotedTweet != nil {
		t.Error("chain should end where the parser ended it")
	}
}

// TestConvertTweetsPreservesNilness keeps `null` vs `[]` stable in JSON output
// for callers that marshal Thread results directly.
func TestConvertTweetsPreservesNilness(t *testing.T) {
	if got := convertTweets(nil); got != nil {
		t.Errorf("nil input should stay nil, got %#v", got)
	}
	got := convertTweets([]xapi.Tweet{})
	if got == nil {
		t.Error("empty input should stay non-nil so it marshals as []")
	}
	if len(got) != 0 {
		t.Errorf("empty input should stay empty, got %d", len(got))
	}
}
