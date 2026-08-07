package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func TestParseNativeArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got := parseNativeArgs(nil)
		if got.count != 20 {
			t.Errorf("count = %d, want 20", got.count)
		}
		if !got.emoji || got.plain || got.json {
			t.Errorf("got %+v, want emoji output by default", got)
		}
	})

	t.Run("reads the positional argument", func(t *testing.T) {
		got := parseNativeArgs([]string{"golang", "-n", "5"})
		if got.positional != "golang" {
			t.Errorf("positional = %q, want golang", got.positional)
		}
		if got.count != 5 {
			t.Errorf("count = %d, want 5", got.count)
		}
	})

	t.Run("count accepts both forms", func(t *testing.T) {
		for _, args := range [][]string{
			{"--count", "7"}, {"--count=7"}, {"-n", "7"}, {"--limit", "7"}, {"--limit=7"},
		} {
			if got := parseNativeArgs(args); got.count != 7 {
				t.Errorf("parseNativeArgs(%v).count = %d, want 7", args, got.count)
			}
		}
	})

	// --plain implies no emoji, matching bird.
	t.Run("plain disables emoji", func(t *testing.T) {
		got := parseNativeArgs([]string{"--plain"})
		if !got.plain || got.emoji {
			t.Errorf("got %+v, want plain with emoji off", got)
		}
	})

	t.Run("flags are not mistaken for the positional", func(t *testing.T) {
		got := parseNativeArgs([]string{"--json", "-n", "3", "SpaceX"})
		if got.positional != "SpaceX" {
			t.Errorf("positional = %q, want SpaceX", got.positional)
		}
		if !got.json {
			t.Error("json = false, want true")
		}
	})
}

// A command carrying a flag the native path does not implement must fall back
// to bird rather than silently ignoring the flag.
func TestNativeAcceptsFlags(t *testing.T) {
	accepted := [][]string{
		{},
		{"SpaceX"},
		{"--json"},
		{"-n", "5"},
		{"--count=5", "--plain"},
		{"golang", "--no-emoji", "--no-color"},
	}
	for _, args := range accepted {
		if !nativeAcceptsFlags("search", args) {
			t.Errorf("nativeAcceptsFlags(%v) = false, want true", args)
		}
	}

	rejected := [][]string{
		{"--max-pages", "3"},
		{"--all"},
		{"--cursor", "abc"},
		{"--json-full"},
		{"SpaceX", "--delay", "1000"},
	}
	for _, args := range rejected {
		if nativeAcceptsFlags("search", args) {
			t.Errorf("nativeAcceptsFlags(%v) = true, want false (unsupported flag)", args)
		}
	}

	// The value of -n must not itself be treated as a flag.
	if !nativeAcceptsFlags("search", []string{"-n", "5", "golang"}) {
		t.Error("nativeAcceptsFlags skipped the count value incorrectly")
	}
}

func TestNativeSupports(t *testing.T) {
	for _, command := range []string{
		"read", "thread", "search", "home", "user-tweets", "replies",
		"bookmarks", "list-timeline", "whoami", "about", "likes",
		"followers", "following", "tweet", "reply", "follow", "unfollow", "unbookmark",
	} {
		if !nativeSupports(command) {
			t.Errorf("nativeSupports(%q) = false, want true", command)
		}
	}
	// Not yet ported: these must still reach bird.
	for _, command := range []string{"lists", "news", "mentions", "check", "activity", "query-ids"} {
		if nativeSupports(command) {
			t.Errorf("nativeSupports(%q) = true, but it has no native implementation", command)
		}
	}
}

// bird's `whoami` declares no options, so `bird whoami --json` is a usage
// error. Serving it natively would answer with human-readable output instead —
// a divergence, not a fallback.
func TestCommandScopedFlagFallback(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		accept  bool
	}{
		{"whoami", nil, true},
		{"whoami", []string{"--plain"}, true},
		{"whoami", []string{"--json"}, false},
		{"whoami", []string{"-n", "5"}, false},
		{"about", []string{"guzus"}, true},
		{"about", []string{"guzus", "--json"}, true},
		{"about", []string{"guzus", "-n", "5"}, false},
		{"likes", []string{"--json"}, true},
		{"likes", []string{"-n", "5"}, true},
		{"likes", []string{"--latest"}, false},
		// Commands without a narrowing entry keep the common set.
		{"search", []string{"--json", "-n", "5"}, true},
	}

	for _, tc := range cases {
		if got := nativeAcceptsFlags(tc.command, tc.args); got != tc.accept {
			t.Errorf("nativeAcceptsFlags(%q, %v) = %v, want %v", tc.command, tc.args, got, tc.accept)
		}
	}
}

func TestUseBirdEnv(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "ON"} {
		t.Setenv("BIRDY_USE_BIRD", value)
		if !useBird() {
			t.Errorf("useBird() = false for BIRDY_USE_BIRD=%q", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no"} {
		t.Setenv("BIRDY_USE_BIRD", value)
		if useBird() {
			t.Errorf("useBird() = true for BIRDY_USE_BIRD=%q", value)
		}
	}
}

// bird's `thread` narrows to the conversation and orders oldest-first; X returns
// entries in ranking order, which is not chronological.
func TestThreadView(t *testing.T) {
	tweets := []xapi.Tweet{
		{ID: "3", ConversationID: "1", CreatedAt: "Wed Aug 05 09:00:00 +0000 2026"},
		{ID: "1", ConversationID: "1", CreatedAt: "Wed Aug 05 07:00:00 +0000 2026"},
		{ID: "9", ConversationID: "other", CreatedAt: "Wed Aug 05 08:00:00 +0000 2026"},
		{ID: "2", ConversationID: "1", CreatedAt: "Wed Aug 05 08:00:00 +0000 2026"},
	}

	got := threadView(tweets, "1")

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (the foreign conversation must be dropped)", len(got))
	}
	for i, want := range []string{"1", "2", "3"} {
		if got[i].ID != want {
			t.Errorf("position %d = %q, want %q (oldest first)", i, got[i].ID, want)
		}
	}
}

func TestThreadViewHandlesUnparseableTime(t *testing.T) {
	tweets := []xapi.Tweet{
		{ID: "2", ConversationID: "1", CreatedAt: "Wed Aug 05 08:00:00 +0000 2026"},
		{ID: "1", ConversationID: "1", CreatedAt: "not a date"},
	}
	// Must not panic, and the unparseable timestamp sorts first as zero.
	if got := threadView(tweets, "1"); len(got) != 2 || got[0].ID != "1" {
		t.Errorf("got %+v, want the zero-time tweet first", got)
	}
}

func TestMediaLabel(t *testing.T) {
	video := xapi.Media{Type: "video", URL: "u", VideoURL: "v"}
	photo := xapi.Media{Type: "photo", URL: "u"}

	emoji := nativeArgs{emoji: true}
	if got := mediaLabel(video, emoji); got != "🎬" {
		t.Errorf("video emoji label = %q, want 🎬", got)
	}
	if got := mediaLabel(photo, emoji); got != "🖼️" {
		t.Errorf("photo emoji label = %q, want 🖼️", got)
	}

	// An animated gif is neither a video nor a photo to bird.
	gif := xapi.Media{Type: "animated_gif", URL: "u", VideoURL: "v"}
	if got := mediaLabel(gif, emoji); got != "🔄" {
		t.Errorf("gif emoji label = %q, want 🔄", got)
	}

	// bird computes useEmoji as (emoji && !plain), so --plain and --no-emoji
	// share the same textual labels.
	for name, args := range map[string]nativeArgs{
		"plain":    {plain: true},
		"no-emoji": {emoji: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mediaLabel(photo, args); got != "PHOTO:" {
				t.Errorf("photo label = %q, want PHOTO:", got)
			}
			if got := mediaLabel(video, args); got != "VIDEO:" {
				t.Errorf("video label = %q, want VIDEO:", got)
			}
			if got := mediaLabel(gif, args); got != "GIF:" {
				t.Errorf("gif label = %q, want GIF:", got)
			}
		})
	}
}

// An empty result prints bird's per-command wording. Printing nothing would
// read as a silent failure.
func TestEmptyMessages(t *testing.T) {
	cases := map[string]string{
		"replies":       "No replies found.",
		"thread":        "No thread tweets found.",
		"list-timeline": "No tweets found in this list.",
		"bookmarks":     "No bookmarks found.",
		"search":        "No tweets found.",
		"home":          "No tweets found.",
		"user-tweets":   "No tweets found.",
	}
	for command, want := range cases {
		if got := emptyMessageFor(command); got != want {
			t.Errorf("emptyMessageFor(%q) = %q, want %q", command, got, want)
		}
	}

	var buf bytes.Buffer
	if err := renderTweets(&buf, nil, nativeArgs{emoji: true, command: "bookmarks"}); err != nil {
		t.Fatalf("renderTweets returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "No bookmarks found." {
		t.Errorf("empty render = %q, want the bookmarks wording", got)
	}
}

func TestStatsLine(t *testing.T) {
	tweet := xapi.Tweet{LikeCount: 10, RetweetCount: 5, ReplyCount: 2}

	if got, want := statsLine(tweet, nativeArgs{emoji: true}), "❤️ 10  🔁 5  💬 2"; got != want {
		t.Errorf("emoji stats = %q, want %q", got, want)
	}
	if got, want := statsLine(tweet, nativeArgs{plain: true}), "likes: 10  retweets: 5  replies: 2"; got != want {
		t.Errorf("plain stats = %q, want %q", got, want)
	}
	if got, want := statsLine(tweet, nativeArgs{}), "Likes 10  Retweets 5  Replies 2"; got != want {
		t.Errorf("no-emoji stats = %q, want %q", got, want)
	}
}

// Rendering is what the birdy skill and TUI actually read, so its shape is a
// contract: leading blank line, handle header, body, media, date, link.
func TestRenderTweetShape(t *testing.T) {
	tweet := xapi.Tweet{
		ID:        "123",
		Text:      "hello world",
		CreatedAt: "Wed Aug 05 07:59:09 +0000 2026",
		Author:    xapi.Author{Username: "SpaceX", Name: "SpaceX"},
		Media:     []xapi.Media{{Type: "video", URL: "https://thumb.jpg", VideoURL: "https://v.mp4"}},
		LikeCount: 7,
	}

	var buf bytes.Buffer
	if err := renderTweet(&buf, tweet, nativeArgs{emoji: true}, true); err != nil {
		t.Fatalf("renderTweet returned error: %v", err)
	}
	lines := strings.Split(buf.String(), "\n")

	if lines[0] != "" {
		t.Errorf("line 0 = %q, want a leading blank line", lines[0])
	}
	if lines[1] != "@SpaceX (SpaceX):" {
		t.Errorf("line 1 = %q, want the handle header", lines[1])
	}
	if lines[2] != "hello world" {
		t.Errorf("line 2 = %q, want the body", lines[2])
	}
	// Videos show the still thumbnail URL, not the mp4 — matching bird.
	if lines[3] != "🎬 https://thumb.jpg" {
		t.Errorf("line 3 = %q, want the thumbnail with a video icon", lines[3])
	}
	if !strings.HasPrefix(lines[4], "📅 ") {
		t.Errorf("line 4 = %q, want the date", lines[4])
	}
	if lines[5] != "🔗 https://x.com/SpaceX/status/123" {
		t.Errorf("line 5 = %q, want the permalink", lines[5])
	}
	if !strings.HasPrefix(lines[6], "❤️ 7") {
		t.Errorf("line 6 = %q, want the stats line", lines[6])
	}
}

// bird closes every list entry with a separator, including the last.
func TestRenderTweetsSeparators(t *testing.T) {
	tweets := []xapi.Tweet{
		{ID: "1", Text: "a", Author: xapi.Author{Username: "u", Name: "U"}},
		{ID: "2", Text: "b", Author: xapi.Author{Username: "u", Name: "U"}},
	}

	var buf bytes.Buffer
	if err := renderTweets(&buf, tweets, nativeArgs{emoji: true}); err != nil {
		t.Fatalf("renderTweets returned error: %v", err)
	}

	if got := strings.Count(buf.String(), listSeparator); got != 2 {
		t.Errorf("separator count = %d, want 2 (one after each entry)", got)
	}
}

func TestRenderTweetsJSONIsAlwaysAnArray(t *testing.T) {
	var buf bytes.Buffer
	// A nil slice must serialize as [] rather than null, so consumers can
	// iterate without a nil check.
	if err := renderTweets(&buf, nil, nativeArgs{json: true}); err != nil {
		t.Fatalf("renderTweets returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("empty json = %q, want []", got)
	}
}
