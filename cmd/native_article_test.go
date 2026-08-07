package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// bird prefixes an article's text with 📰 (or "Article:" without emoji) and,
// when the text is only the title, prints the preview beneath it indented by
// exactly three spaces. See cli/shared.js:237-266.

func renderOne(t *testing.T, tw xapi.Tweet, args nativeArgs) string {
	t.Helper()
	var buf bytes.Buffer
	if err := renderTweet(&buf, tw, args, false); err != nil {
		t.Fatalf("renderTweet: %v", err)
	}
	return buf.String()
}

func articleTweet() xapi.Tweet {
	return xapi.Tweet{
		ID:     "1",
		Author: xapi.Author{Username: "XCreators", Name: "Creators"},
		Text:   "The $1M Article Contest Winners\n\nToday we're announcing the winner.",
		Article: &xapi.Article{
			Title:       "The $1M Article Contest Winners",
			PreviewText: "Today we're announcing the winner.",
		},
	}
}

func TestRenderArticleFullBody(t *testing.T) {
	got := renderOne(t, articleTweet(), nativeArgs{emoji: true})

	want := "\n@XCreators (Creators):\n" +
		"📰 The $1M Article Contest Winners\n\nToday we're announcing the winner.\n" +
		"🔗 https://x.com/XCreators/status/1\n"
	if got != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Timeline responses give only the title, so the text equals the title and the
// preview goes on its own three-space-indented line.
func TestRenderArticlePreviewMode(t *testing.T) {
	tw := articleTweet()
	tw.Text = tw.Article.Title

	got := renderOne(t, tw, nativeArgs{emoji: true})
	want := "\n@XCreators (Creators):\n" +
		"📰 The $1M Article Contest Winners\n" +
		"🔗 https://x.com/XCreators/status/1\n"
	if got != want {
		t.Errorf("hasFullBody must be true when text == title\n got: %q\nwant: %q", got, want)
	}
}

// When the rendered body opens with a markdown heading rather than the bare
// title, bird takes the else branch: label + title, then the preview.
func TestRenderArticleTitleAndPreviewLine(t *testing.T) {
	tw := articleTweet()
	tw.Text = "# The $1M Article Contest Winners\n\nbody"

	got := renderOne(t, tw, nativeArgs{emoji: true})
	want := "\n@XCreators (Creators):\n" +
		"📰 The $1M Article Contest Winners\n" +
		"   Today we're announcing the winner.\n" +
		"🔗 https://x.com/XCreators/status/1\n"
	if got != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", got, want)
	}
}

// A multi-line preview is printed raw: bird indents the first line only.
func TestRenderArticlePreviewIndentsOnlyTheFirstLine(t *testing.T) {
	tw := articleTweet()
	tw.Text = "# heading that is not the title"
	tw.Article.PreviewText = "first line\nsecond line"

	got := renderOne(t, tw, nativeArgs{emoji: true})
	if !strings.Contains(got, "   first line\nsecond line\n") {
		t.Errorf("only the first preview line is indented, got: %q", got)
	}
}

// bird computes the article label from (emoji && !plain), like the media
// labels — NOT through the l()/label() helper, so --plain keeps the
// capitalized "Article:" instead of lowercasing it.
func TestRenderArticleLabelModes(t *testing.T) {
	cases := map[string]struct {
		args nativeArgs
		want string
	}{
		"emoji":    {nativeArgs{emoji: true}, "📰 "},
		"plain":    {nativeArgs{plain: true}, "Article: "},
		"no-emoji": {nativeArgs{emoji: false}, "Article: "},
		// --plain wins over --no-emoji's absence, same as bird's
		// useEmoji = emoji && !plain.
		"plain and emoji": {nativeArgs{plain: true, emoji: true}, "Article: "},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := renderOne(t, articleTweet(), tc.args)
			line := strings.Split(got, "\n")[2]
			if !strings.HasPrefix(line, tc.want) {
				t.Errorf("label line = %q, want prefix %q", line, tc.want)
			}
			if tc.args.plain && strings.Contains(got, "article:") {
				t.Error("--plain must keep the capitalized Article: label")
			}
		})
	}
}

// A quoted article shows the article title, and the 280-unit truncation budget
// includes the label prefix.
func TestRenderQuotedArticle(t *testing.T) {
	var b strings.Builder
	renderQuoted(&b, &xapi.Tweet{
		ID:      "200",
		Author:  xapi.Author{Username: "beaverd", Name: "Beaver"},
		Text:    "https://t.co/0mqTgVZtNd",
		Article: &xapi.Article{Title: "Deloitte, a $74 billion cancer"},
	}, nativeArgs{emoji: true})

	want := "┌─ QT @beaverd:\n" +
		"│ 📰 Deloitte, a $74 billion cancer\n" +
		"└─ https://x.com/beaverd/status/200\n"
	if b.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", b.String(), want)
	}
}

func TestRenderQuotedArticleTruncationIncludesTheLabel(t *testing.T) {
	title := strings.Repeat("a", 300)
	var b strings.Builder
	renderQuoted(&b, &xapi.Tweet{
		ID:      "200",
		Author:  xapi.Author{Username: "x", Name: "X"},
		Text:    "short",
		Article: &xapi.Article{Title: title},
	}, nativeArgs{emoji: true})

	// "📰 " is 3 UTF-16 units (the emoji is a surrogate pair plus the space), so
	// 277 title characters survive.
	line := strings.Split(b.String(), "\n")[1]
	if line != "│ "+truncateJS("📰 "+title, 280) {
		t.Errorf("quoted article line = %q", line)
	}
	if !strings.HasSuffix(line, "...") {
		t.Errorf("expected truncation, got %q", line)
	}
	// "📰" is a surrogate pair (2 units) plus a space, so the label costs 3 of
	// the 280 and only 277 title characters survive.
	kept := strings.TrimSuffix(strings.TrimPrefix(line, "│ 📰 "), "...")
	if len(kept) != 277 {
		t.Errorf("title kept %d chars, want 277 (the label must count against the budget)", len(kept))
	}
}

// A tweet without an article must render exactly as before.
func TestRenderNonArticleUnchanged(t *testing.T) {
	got := renderOne(t, xapi.Tweet{
		ID:     "1",
		Author: xapi.Author{Username: "a", Name: "A"},
		Text:   "plain text",
	}, nativeArgs{emoji: true})

	want := "\n@a (A):\nplain text\n🔗 https://x.com/a/status/1\n"
	if got != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", got, want)
	}
}
