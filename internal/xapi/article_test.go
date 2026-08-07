package xapi

import (
	"strings"
	"testing"
)

// articleTweet wraps an article node into a minimal TweetDetail response.
func articleConversation(articleNodeJSON, extra string) string {
	return `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"100",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"XCreators","name":"Creators"}}}},
	    "legacy":{"full_text":"https://t.co/3ijc5cCIjx","conversation_id_str":"100"},
	    ` + extra + `
	    "article":` + articleNodeJSON + `
	  }}}}}
	]}]}}}`
}

func firstTweet(t *testing.T, body string) Tweet {
	t.Helper()
	tweets, err := parseConversation([]byte(body))
	if err != nil {
		t.Fatalf("parseConversation: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("got %d tweets, want 1", len(tweets))
	}
	return tweets[0]
}

// A TweetDetail article carries a Draft.js content_state. bird renders it to
// markdown and uses it as the tweet's text; legacy.full_text for an article is
// only a t.co shortlink, so falling back to it loses the entire post.
func TestArticleRichBodyBecomesTheText(t *testing.T) {
	const node = `{"article_results":{"result":{
	  "title":"The $1M Article Contest Winners",
	  "preview_text":"Today we're announcing the winner.",
	  "plain_text":"ignored when content_state is present",
	  "content_state":{
	    "blocks":[
	      {"type":"unstyled","text":"Today we're announcing the winner.","entityRanges":[]},
	      {"type":"header-two","text":"The Grand Prize","entityRanges":[]},
	      {"type":"atomic","text":" ","entityRanges":[{"key":0,"offset":0,"length":1}]},
	      {"type":"atomic","text":" ","entityRanges":[{"key":1,"offset":0,"length":1}]},
	      {"type":"unstyled","text":"Congrats to Nick Shirley.","entityRanges":[{"key":2,"offset":12,"length":12}]},
	      {"type":"unordered-list-item","text":"a bullet","entityRanges":[]},
	      {"type":"ordered-list-item","text":"first","entityRanges":[]},
	      {"type":"ordered-list-item","text":"second","entityRanges":[]},
	      {"type":"unstyled","text":"","entityRanges":[]},
	      {"type":"ordered-list-item","text":"restarted","entityRanges":[]},
	      {"type":"blockquote","text":"a quote","entityRanges":[]},
	      {"type":"atomic","text":" ","entityRanges":[{"key":3,"offset":0,"length":1}]}
	    ],
	    "entityMap":[
	      {"key":"0","value":{"type":"DIVIDER","data":{}}},
	      {"key":"1","value":{"type":"TWEET","data":{"tweetId":"2013366996180574446"}}},
	      {"key":"2","value":{"type":"LINK","data":{"url":"https://x.com/nickshirleyy/status/2016539789932302505"}}},
	      {"key":"3","value":{"type":"MEDIA","data":{}}}
	    ]
	  }
	}}}`

	tw := firstTweet(t, articleConversation(node, ""))

	if tw.Article == nil {
		t.Fatal("Article metadata missing")
	}
	if tw.Article.Title != "The $1M Article Contest Winners" {
		t.Errorf("title = %q", tw.Article.Title)
	}
	if tw.Article.PreviewText != "Today we're announcing the winner." {
		t.Errorf("previewText = %q", tw.Article.PreviewText)
	}

	want := "The $1M Article Contest Winners\n\n" +
		"Today we're announcing the winner.\n\n" +
		"## The Grand Prize\n\n" +
		"---\n\n" +
		"[Embedded Tweet: https://x.com/i/status/2013366996180574446]\n\n" +
		"Congrats to [Nick Shirley](https://x.com/nickshirleyy/status/2016539789932302505).\n\n" +
		"- a bullet\n\n" +
		"1. first\n\n" +
		"2. second\n\n" +
		"1. restarted\n\n" +
		"> a quote"
	if tw.Text != want {
		t.Errorf("text mismatch\n got: %q\nwant: %q", tw.Text, want)
	}
	// MEDIA is not a case bird handles, so its atomic block contributes nothing.
	if strings.Contains(tw.Text, "MEDIA") {
		t.Error("MEDIA atomic block should be dropped, matching bird")
	}
}

// LINK entityRanges are Draft.js offsets, i.e. UTF-16 code units. Article
// bodies contain astral characters, so rune or byte indexing silently corrupts
// the markdown links.
func TestArticleLinkOffsetsAreUTF16(t *testing.T) {
	const node = `{"article_results":{"result":{
	  "title":"T",
	  "content_state":{
	    "blocks":[{"type":"unstyled","text":"𝕏 marks the spot","entityRanges":[{"key":0,"offset":3,"length":5}]}],
	    "entityMap":[{"key":"0","value":{"type":"LINK","data":{"url":"https://x.com"}}}]
	  }
	}}}`

	tw := firstTweet(t, articleConversation(node, ""))
	want := "T\n\n𝕏 [marks](https://x.com) the spot"
	if tw.Text != want {
		t.Errorf("text = %q, want %q (rune indexing yields \"𝕏 m[arks ]...\")", tw.Text, want)
	}
}

// bird accepts entityMap as either a keyed array (the live shape) or an object.
func TestArticleEntityMapObjectForm(t *testing.T) {
	const node = `{"article_results":{"result":{
	  "title":"T",
	  "content_state":{
	    "blocks":[{"type":"atomic","text":" ","entityRanges":[{"key":7,"offset":0,"length":1}]}],
	    "entityMap":{"7":{"type":"IMAGE","data":{}}}
	  }
	}}}`

	tw := firstTweet(t, articleConversation(node, ""))
	if tw.Text != "T\n\n[Image]" {
		t.Errorf("text = %q, want %q", tw.Text, "T\n\n[Image]")
	}
}

func TestArticleAtomicEntityRenderings(t *testing.T) {
	cases := map[string]struct{ entity, want string }{
		"markdown": {"{\"type\":\"MARKDOWN\",\"data\":{\"markdown\":\"  fenced code  \"}}", "fenced code"},
		"divider":  {`{"type":"DIVIDER","data":{}}`, "---"},
		"link":     {`{"type":"LINK","data":{"url":"https://e.com"}}`, "[Link: https://e.com]"},
		"image":    {`{"type":"IMAGE","data":{}}`, "[Image]"},
		// Not a case bird has: the block is dropped and only the title remains.
		"media":            {`{"type":"MEDIA","data":{}}`, ""},
		"tweet no id":      {`{"type":"TWEET","data":{}}`, ""},
		"link no url":      {`{"type":"LINK","data":{}}`, ""},
		"unknown entity":   {`{"type":"WHATEVER","data":{}}`, ""},
		"tweet with an id": {`{"type":"TWEET","data":{"tweetId":"5"}}`, "[Embedded Tweet: https://x.com/i/status/5]"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			node := `{"article_results":{"result":{"title":"T","content_state":{` +
				`"blocks":[{"type":"atomic","text":" ","entityRanges":[{"key":0,"offset":0,"length":1}]}],` +
				`"entityMap":[{"key":"0","value":` + tc.entity + `}]}}}}`
			tw := firstTweet(t, articleConversation(node, ""))

			want := "T"
			if tc.want != "" {
				want = "T\n\n" + tc.want
			}
			if tw.Text != want {
				t.Errorf("text = %q, want %q", tw.Text, want)
			}
		})
	}
}

// Timeline responses carry only title + preview_text. bird then renders just
// the title as the text, with previewText available separately in --json.
func TestArticlePreviewOnlyShape(t *testing.T) {
	const node = `{"article_results":{"result":{
	  "title":"Live Studio: A New Home for Livestreaming on X",
	  "preview_text":"Live Studio is now available in beta.\nIt gives creators everything they need."
	}}}`

	tw := firstTweet(t, articleConversation(node, ""))
	if tw.Text != "Live Studio: A New Home for Livestreaming on X" {
		t.Errorf("text = %q, want the title", tw.Text)
	}
	if tw.Article == nil || tw.Article.PreviewText != "Live Studio is now available in beta.\nIt gives creators everything they need." {
		t.Errorf("previewText not carried: %+v", tw.Article)
	}
}

// plain_text is the fallback when there is no content_state.
func TestArticlePlainTextFallback(t *testing.T) {
	const node = `{"article_results":{"result":{"title":"T","plain_text":"the body"}}}`
	tw := firstTweet(t, articleConversation(node, ""))
	if tw.Text != "T\n\nthe body" {
		t.Errorf("text = %q", tw.Text)
	}
}

// bird's firstText chain also reads the inline article.* shapes when there is
// no article_results.result at all.
func TestArticleInlineShape(t *testing.T) {
	const node = `{"title":"Inline","body":{"richtext":{"text":"inline body"}}}`
	tw := firstTweet(t, articleConversation(node, ""))
	if tw.Text != "Inline\n\ninline body" {
		t.Errorf("text = %q", tw.Text)
	}
}

// extractArticleMetadata returns nothing without a title, even when preview_text
// is present — and extractArticleText then contributes nothing, so the text
// falls through to note_tweet / legacy.full_text.
func TestArticleWithoutTitleIsNotAnArticle(t *testing.T) {
	const node = `{"article_results":{"result":{"preview_text":"orphaned preview"}}}`
	tw := firstTweet(t, articleConversation(node, ""))
	if tw.Article != nil {
		t.Errorf("Article should be nil without a title, got %+v", tw.Article)
	}
	if tw.Text != "https://t.co/3ijc5cCIjx" {
		t.Errorf("text = %q, want the legacy full_text fallback", tw.Text)
	}
}

// bird's precedence is article, then note_tweet, then legacy.full_text.
func TestArticleTextBeatsNoteTweet(t *testing.T) {
	const node = `{"article_results":{"result":{"title":"T","plain_text":"article body"}}}`
	const noteTweet = `"note_tweet":{"note_tweet_results":{"result":{"text":"note body"}}},`
	tw := firstTweet(t, articleConversation(node, noteTweet))
	if tw.Text != "T\n\narticle body" {
		t.Errorf("text = %q, want the article body to win", tw.Text)
	}
}

// The title is prepended only when the rendered body does not already open with
// it, in any of bird's four accepted forms.
func TestArticleTitleIsNotDoubled(t *testing.T) {
	cases := map[string]string{
		"bare title line": "The Title\n\nbody",
		"h1":              "# The Title\n\nbody",
		"h2":              "## The Title\n\nbody",
		"h3":              "### The Title\n\nbody",
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			head, rest, _ := strings.Cut(first, "\n\n")
			node := `{"article_results":{"result":{"title":"The Title","content_state":{"blocks":[` +
				`{"type":"unstyled","text":"` + head + `","entityRanges":[]},` +
				`{"type":"unstyled","text":"` + rest + `","entityRanges":[]}],"entityMap":[]}}}}`
			tw := firstTweet(t, articleConversation(node, ""))
			if tw.Text != first {
				t.Errorf("text = %q, want %q (title must not be prepended again)", tw.Text, first)
			}
		})
	}
}

// A quoted article is the common case: bird maps quotes through the same
// mapper, so the quote carries its own article header and title-as-text.
func TestQuotedArticleIsMapped(t *testing.T) {
	body := `{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[
	  {"content":{"itemContent":{"tweet_results":{"result":{
	    "rest_id":"100",
	    "core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"a","name":"A"}}}},
	    "legacy":{"full_text":"look at this","conversation_id_str":"100"},
	    "quoted_status_result":{"result":{
	      "rest_id":"200",
	      "core":{"user_results":{"result":{"rest_id":"8","legacy":{"screen_name":"beaverd","name":"Beaver"}}}},
	      "legacy":{"full_text":"https://t.co/0mqTgVZtNd"},
	      "article":{"article_results":{"result":{"title":"Deloitte, a $74 billion cancer","preview_text":"Consulting fees"}}}
	    }}
	  }}}}}
	]}]}}}`

	tw := firstTweet(t, body)
	if tw.QuotedTweet == nil {
		t.Fatal("quoted tweet dropped")
	}
	if tw.QuotedTweet.Article == nil || tw.QuotedTweet.Article.Title != "Deloitte, a $74 billion cancer" {
		t.Fatalf("quoted article missing: %+v", tw.QuotedTweet.Article)
	}
	if tw.QuotedTweet.Text != "Deloitte, a $74 billion cancer" {
		t.Errorf("quoted text = %q, want the article title", tw.QuotedTweet.Text)
	}
}

// A tweet with no article at all must be untouched by any of this.
func TestNonArticleTweetUnaffected(t *testing.T) {
	tweets, err := parseConversation([]byte(conversationFixture))
	if err != nil {
		t.Fatalf("parseConversation: %v", err)
	}
	tw := tweets[0]
	if tw.Article != nil {
		t.Errorf("plain tweet grew an Article: %+v", tw.Article)
	}
	if tw.Text != "Falcon 9 has landed https://t.co/abc" {
		t.Errorf("text = %q", tw.Text)
	}
}
