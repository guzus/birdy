package xapi

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// X "Articles" are long-form posts with a rich, formatted body. They are a
// different feature from note_tweet (long posts), carried in a different node,
// and legacy.full_text for an article is not a truncation of the content — it is
// an unrelated t.co shortlink. Parsing only note_tweet therefore loses 100% of
// an article's content rather than degrading it.
//
// Everything here is a transcription of bird's extractArticleText /
// extractArticleMetadata / renderContentState
// (third_party/@steipete/bird/dist/lib/twitter-client-utils.js:65-304).

// --- Raw shapes --------------------------------------------------------------

// articleNode is the `article` key on a tweet result. bird resolves the article
// payload as `article.article_results?.result ?? article`, so the node doubles
// as an inline articleResult — hence the embedding.
type articleNode struct {
	ArticleResults *struct {
		Result *articleResult `json:"result"`
	} `json:"article_results"`
	articleResult

	// raw is kept for the last-ditch text sweep, which walks arbitrary keys.
	raw json.RawMessage
}

func (a *articleNode) UnmarshalJSON(data []byte) error {
	type alias articleNode
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*a = articleNode(v)
	a.raw = append(json.RawMessage(nil), data...)
	return nil
}

type articleResult struct {
	Title        string        `json:"title"`
	PreviewText  string        `json:"preview_text"`
	PlainText    string        `json:"plain_text"`
	ContentState *contentState `json:"content_state"`

	// The remaining shapes are the rest of bird's firstText fallback chain.
	// None has been observed live, but bird tries all of them.
	Body     *articleBody `json:"body"`
	Content  *articleBody `json:"content"`
	Text     string       `json:"text"`
	Richtext *richText    `json:"richtext"`
	RichText *richText    `json:"rich_text"`
}

type articleBody struct {
	Text     string    `json:"text"`
	Richtext *richText `json:"richtext"`
	RichText *richText `json:"rich_text"`
}

type richText struct {
	Text string `json:"text"`
}

func (r *richText) text() string {
	if r == nil {
		return ""
	}
	return r.Text
}

func (b *articleBody) candidates() []string {
	if b == nil {
		return []string{"", "", ""}
	}
	return []string{b.Text, b.Richtext.text(), b.RichText.text()}
}

// bodyCandidates is bird's per-shape fallback order:
// body.text, body.richtext.text, body.rich_text.text, content.*, text,
// richtext.text, rich_text.text.
func (a *articleResult) bodyCandidates() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, 9)
	out = append(out, a.Body.candidates()...)
	out = append(out, a.Content.candidates()...)
	return append(out, a.Text, a.Richtext.text(), a.RichText.text())
}

// contentState is a Draft.js document.
type contentState struct {
	Blocks []contentBlock `json:"blocks"`
	// entityMap arrives as a keyed array in every live payload and as an object
	// in bird's other accepted form, so it stays raw until decoded.
	EntityMap json.RawMessage `json:"entityMap"`
}

type contentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	EntityRanges []entityRange `json:"entityRanges"`
}

type entityRange struct {
	Key    int `json:"key"`
	Offset int `json:"offset"`
	Length int `json:"length"`
}

type contentEntity struct {
	Type string `json:"type"`
	Data struct {
		URL      string `json:"url"`
		TweetID  string `json:"tweetId"`
		Markdown string `json:"markdown"`
	} `json:"data"`
}

// --- Extraction --------------------------------------------------------------

// firstText mirrors bird's firstText: the first non-blank value, trimmed.
func firstText(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// articleParts resolves bird's `articleResult = article.article_results?.result
// ?? article`, returning both so the inline fallbacks stay reachable.
func articleParts(raw *tweetResult) (node *articleNode, res *articleResult) {
	if raw == nil || raw.Article == nil {
		return nil, nil
	}
	node = raw.Article
	if node.ArticleResults != nil && node.ArticleResults.Result != nil {
		return node, node.ArticleResults.Result
	}
	return node, &node.articleResult
}

// extractArticleMetadata is the `article` object in bird's JSON output. Without
// a title bird emits no article object at all, even when preview_text is there.
func extractArticleMetadata(raw *tweetResult) *Article {
	node, res := articleParts(raw)
	if node == nil {
		return nil
	}
	title := firstText(res.Title, node.Title)
	if title == "" {
		return nil
	}
	return &Article{Title: title, PreviewText: firstText(res.PreviewText, node.PreviewText)}
}

// extractArticleText reconstructs the article body. It is the first branch of
// bird's extractTweetText, ahead of note_tweet and legacy.full_text.
func extractArticleText(raw *tweetResult) string {
	node, res := articleParts(raw)
	if node == nil {
		return ""
	}
	title := firstText(res.Title, node.Title)

	// bird reads content_state only from the nested result, never inline.
	var nested *contentState
	if node.ArticleResults != nil && node.ArticleResults.Result != nil {
		nested = node.ArticleResults.Result.ContentState
	}
	if richBody := renderContentState(nested); richBody != "" {
		if title != "" && !bodyOpensWithTitle(richBody, title) {
			return title + "\n\n" + richBody
		}
		return richBody
	}

	candidates := append([]string{res.PlainText, node.PlainText}, res.bodyCandidates()...)
	candidates = append(candidates, node.bodyCandidates()...)
	body := firstText(candidates...)

	if body != "" && title != "" && body == title {
		body = ""
	}
	if body == "" {
		body = strings.Join(sweepArticleText(node, title), "\n\n")
	}

	if title != "" && body != "" && !strings.HasPrefix(body, title) {
		return title + "\n\n" + body
	}
	if body != "" {
		return body
	}
	return title
}

// bodyOpensWithTitle reports whether the rendered body already leads with the
// title, in any of the four forms bird accepts.
func bodyOpensWithTitle(body, title string) bool {
	trimmed := strings.TrimLeftFunc(body, unicode.IsSpace)
	return trimmed == title ||
		strings.HasPrefix(trimmed, title+"\n") ||
		strings.HasPrefix(trimmed, "# "+title) ||
		strings.HasPrefix(trimmed, "## "+title) ||
		strings.HasPrefix(trimmed, "### "+title)
}

// sweepArticleText is bird's collectTextFields last resort: every string found
// at a key named "text" or "title", de-duplicated in order, minus the title.
//
// One deliberate divergence: JavaScript walks object keys in insertion order,
// Go's map iteration is randomized, so keys are visited in sorted order here to
// keep the output deterministic. The two can therefore differ in ordering —
// only for a payload shape that has never been observed, and a stable wrong
// order beats an unstable one.
func sweepArticleText(node *articleNode, title string) []string {
	if node == nil || len(node.raw) == 0 {
		return nil
	}
	var tree any
	if err := json.Unmarshal(node.raw, &tree); err != nil {
		return nil
	}

	var out []string
	seen := make(map[string]bool)
	collect := func(v any) { collectTextFields(v, &out, seen) }

	// bird sweeps articleResult first, then the article node.
	if obj, ok := tree.(map[string]any); ok {
		if results, ok := obj["article_results"].(map[string]any); ok {
			collect(results["result"])
		}
	}
	collect(tree)

	if title == "" {
		return out
	}
	kept := out[:0]
	for _, v := range out {
		if v != title {
			kept = append(kept, v)
		}
	}
	return kept
}

func collectTextFields(value any, out *[]string, seen map[string]bool) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collectTextFields(item, out, seen)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nested := v[key]
			if key == "text" || key == "title" {
				if s, ok := nested.(string); ok {
					if t := strings.TrimSpace(s); t != "" && !seen[t] {
						seen[t] = true
						*out = append(*out, t)
					}
					continue
				}
			}
			collectTextFields(nested, out, seen)
		}
	}
}

// --- Draft.js rendering ------------------------------------------------------

// renderContentState turns a Draft.js document into the markdown-ish text bird
// produces. Blocks whose rendered text is empty contribute no line at all, so
// the join cannot be done before the emptiness check.
func renderContentState(cs *contentState) string {
	if cs == nil || len(cs.Blocks) == 0 {
		return ""
	}
	entities := parseEntityMap(cs.EntityMap)

	var lines []string
	orderedCounter := 0
	prevType := ""

	for _, b := range cs.Blocks {
		if b.Type != "ordered-list-item" && prevType == "ordered-list-item" {
			orderedCounter = 0
		}

		var line string
		switch b.Type {
		case "atomic":
			line = renderAtomicBlock(b, entities)
		case "ordered-list-item":
			// The counter advances even when the item's text is empty.
			orderedCounter++
			if text := renderBlockText(b, entities); text != "" {
				line = strconv.Itoa(orderedCounter) + ". " + text
			}
		default:
			if text := renderBlockText(b, entities); text != "" {
				switch b.Type {
				case "header-one":
					line = "# " + text
				case "header-two":
					line = "## " + text
				case "header-three":
					line = "### " + text
				case "unordered-list-item":
					line = "- " + text
				case "blockquote":
					line = "> " + text
				default:
					// "unstyled" and any type bird does not name.
					line = text
				}
			}
		}

		if line != "" {
			lines = append(lines, line)
		}
		prevType = b.Type
	}

	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

// parseEntityMap accepts both shapes bird does: the keyed array X actually
// sends, and a plain object.
func parseEntityMap(raw json.RawMessage) map[int]contentEntity {
	entities := make(map[int]contentEntity)
	if len(raw) == 0 {
		return entities
	}

	var asArray []struct {
		Key   json.RawMessage `json:"key"`
		Value contentEntity   `json:"value"`
	}
	if err := json.Unmarshal(raw, &asArray); err == nil {
		for _, entry := range asArray {
			if key, ok := entityKey(entry.Key); ok {
				entities[key] = entry.Value
			}
		}
		return entities
	}

	var asObject map[string]contentEntity
	if err := json.Unmarshal(raw, &asObject); err == nil {
		for key, value := range asObject {
			if n, err := strconv.Atoi(key); err == nil {
				entities[n] = value
			}
		}
	}
	return entities
}

// entityKey reads a key that X sends as a string but bird parses numerically.
func entityKey(raw json.RawMessage) (int, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		return n, err == nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	return 0, false
}

// renderBlockText applies inline LINK entities as markdown links.
//
// Draft.js offsets are UTF-16 code units, which is why this splices on encoded
// units rather than runes or bytes: live article bodies contain astral
// characters (𝕏 is a surrogate pair), and rune indexing corrupts every link
// that follows one.
func renderBlockText(b contentBlock, entities map[int]contentEntity) string {
	units := utf16.Encode([]rune(b.Text))

	type link struct {
		offset, length int
		url            string
	}
	var links []link
	for _, r := range b.EntityRanges {
		entity, ok := entities[r.Key]
		if !ok || entity.Type != "LINK" || entity.Data.URL == "" {
			continue
		}
		links = append(links, link{offset: r.Offset, length: r.Length, url: entity.Data.URL})
	}
	// Descending, so an earlier splice cannot shift a later offset.
	sort.SliceStable(links, func(i, j int) bool { return links[i].offset > links[j].offset })

	for _, l := range links {
		start, end := clampRange(l.offset, l.offset+l.length, len(units))
		text := string(utf16.Decode(units[start:end]))
		replacement := utf16.Encode([]rune("[" + text + "](" + l.url + ")"))

		next := make([]uint16, 0, len(units)-(end-start)+len(replacement))
		next = append(next, units[:start]...)
		next = append(next, replacement...)
		next = append(next, units[end:]...)
		units = next
	}
	return strings.TrimSpace(string(utf16.Decode(units)))
}

// clampRange reproduces String.prototype.slice's out-of-range behavior.
func clampRange(start, end, length int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > length {
		start = length
	}
	if end < start {
		end = start
	}
	if end > length {
		end = length
	}
	return start, end
}

// renderAtomicBlock resolves an embedded entity. Anything bird has no case for
// — MEDIA in particular, which is common in real articles — produces nothing
// and the block is dropped.
func renderAtomicBlock(b contentBlock, entities map[int]contentEntity) string {
	if len(b.EntityRanges) == 0 {
		return ""
	}
	entity, ok := entities[b.EntityRanges[0].Key]
	if !ok {
		return ""
	}

	switch entity.Type {
	case "MARKDOWN":
		return strings.TrimSpace(entity.Data.Markdown)
	case "DIVIDER":
		return "---"
	case "TWEET":
		if entity.Data.TweetID == "" {
			return ""
		}
		return "[Embedded Tweet: https://x.com/i/status/" + entity.Data.TweetID + "]"
	case "LINK":
		if entity.Data.URL == "" {
			return ""
		}
		return "[Link: " + entity.Data.URL + "]"
	case "IMAGE":
		return "[Image]"
	default:
		return ""
	}
}
