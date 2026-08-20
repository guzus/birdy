package scrape

import (
	"fmt"
	"strings"
	"time"
)

// Filters are structured X advanced-search operators.
type Filters struct {
	Content    string
	From       string
	To         string
	Since      string
	Until      string
	Lang       string
	MinLikes   int
	MinReposts int
	Operators  []string
}

var knownFilters = map[string]string{
	"media":          "filter:media",
	"videos":         "filter:videos",
	"video":          "filter:videos",
	"images":         "filter:images",
	"image":          "filter:images",
	"links":          "filter:links",
	"replies":        "filter:replies",
	"reply":          "filter:replies",
	"quote":          "filter:quote",
	"quotes":         "filter:quote",
	"blue_verified":  "filter:blue_verified",
	"verified":       "filter:blue_verified",
	"native_video":   "filter:native_video",
	"twimg":          "filter:twimg",
	"safe":           "filter:safe",
	"hashtags":       "filter:hashtags",
	"nativeretweets": "filter:nativeretweets",
	"retweets":       "include:nativeretweets",
}

// Compile turns structured filters into one X search query.
func (f Filters) Compile() (string, error) {
	var parts []string
	if content := strings.TrimSpace(f.Content); content != "" {
		parts = append(parts, content)
	}
	if handle := strings.TrimSpace(f.From); handle != "" {
		parts = append(parts, "from:"+trimAt(handle))
	}
	if handle := strings.TrimSpace(f.To); handle != "" {
		parts = append(parts, "to:"+trimAt(handle))
	}
	since, err := normalizeDateBound(f.Since)
	if err != nil {
		return "", fmt.Errorf("since: %w", err)
	}
	if since != "" {
		parts = append(parts, "since:"+since)
	}
	until, err := normalizeDateBound(f.Until)
	if err != nil {
		return "", fmt.Errorf("until: %w", err)
	}
	if until != "" {
		parts = append(parts, "until:"+until)
	}
	if lang := strings.TrimSpace(f.Lang); lang != "" {
		parts = append(parts, "lang:"+lang)
	}
	if f.MinLikes < 0 {
		return "", fmt.Errorf("min likes must not be negative")
	}
	if f.MinLikes > 0 {
		parts = append(parts, fmt.Sprintf("min_faves:%d", f.MinLikes))
	}
	if f.MinReposts < 0 {
		return "", fmt.Errorf("min reposts must not be negative")
	}
	if f.MinReposts > 0 {
		parts = append(parts, fmt.Sprintf("min_retweets:%d", f.MinReposts))
	}
	seen := map[string]bool{}
	for _, raw := range f.Operators {
		op, err := normalizeFilter(raw)
		if err != nil {
			return "", err
		}
		if seen[op] {
			continue
		}
		seen[op] = true
		parts = append(parts, op)
	}
	return strings.Join(parts, " "), nil
}

// HasConstraints reports whether filters would change a profile fetch into search.
func (f Filters) HasConstraints() bool {
	return strings.TrimSpace(f.Content) != "" ||
		strings.TrimSpace(f.To) != "" ||
		strings.TrimSpace(f.Since) != "" ||
		strings.TrimSpace(f.Until) != "" ||
		strings.TrimSpace(f.Lang) != "" ||
		f.MinLikes > 0 || f.MinReposts > 0 || len(f.Operators) > 0
}

func normalizeFilter(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", fmt.Errorf("empty filter")
	}
	if strings.Contains(token, ":") {
		return token, nil
	}
	op, ok := knownFilters[strings.ToLower(strings.TrimPrefix(token, "-"))]
	if !ok {
		return "", fmt.Errorf("unknown filter %q", raw)
	}
	if strings.HasPrefix(token, "-") {
		return "-" + op, nil
	}
	return op, nil
}

func normalizeDateBound(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	raw = strings.TrimSuffix(raw, "_UTC")
	raw = strings.ReplaceAll(raw, "_", " ")
	layouts := []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			if layout == "2006-01-02" {
				return parsed.Format("2006-01-02"), nil
			}
			return parsed.UTC().Format("2006-01-02_15:04:05_UTC"), nil
		}
	}
	return "", fmt.Errorf("invalid date %q (want YYYY-MM-DD)", raw)
}

func trimAt(handle string) string {
	return strings.TrimPrefix(strings.TrimSpace(handle), "@")
}
