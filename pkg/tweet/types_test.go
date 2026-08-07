package tweet

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub, inner := specOf(tc.public), specOf(tc.internal)

			for name, want := range inner {
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
	}

	got := convertTweet(src)

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
