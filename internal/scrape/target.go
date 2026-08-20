package scrape

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
	"github.com/guzus/birdy/pkg/tweet"
)

// Kind is what a scrape target resolves to.
type Kind string

const (
	KindTweet          Kind = "tweet"
	KindProfile        Kind = "profile"
	KindProfileReplies Kind = "profile-replies"
	KindProfileMedia   Kind = "profile-media"
	KindProfileLikes   Kind = "profile-likes"
	KindSearch         Kind = "search"
	KindList           Kind = "list"
)

// Target is one classified scrape input.
type Target struct {
	Kind   Kind
	Value  string
	Source string
	Sort   string
}

var (
	listURLPattern = regexp.MustCompile(`(?i)(?:^|[/.])(?:twitter\.com|x\.com)/i/lists?/(\d+)`)
)

// Classify inspects a raw input and decides how to fetch it.
func Classify(raw string) (Target, error) {
	source := strings.TrimSpace(raw)
	if source == "" {
		return Target{}, fmt.Errorf("empty scrape target")
	}

	if id, err := tweet.ExtractTweetID(source); err == nil {
		return Target{Kind: KindTweet, Value: id, Source: source}, nil
	}

	if match := listURLPattern.FindStringSubmatch(source); match != nil {
		return Target{Kind: KindList, Value: match[1], Source: source}, nil
	}

	if strings.HasPrefix(strings.ToLower(source), "list:") {
		id := strings.TrimSpace(source[len("list:"):])
		if id == "" {
			return Target{}, fmt.Errorf("empty list id")
		}
		return Target{Kind: KindList, Value: id, Source: source}, nil
	}

	if looksLikeURL(source) {
		if target, ok := classifyXURL(source); ok {
			return target, nil
		}
		return Target{}, fmt.Errorf("%q is not an X tweet, profile, search, or list URL", source)
	}

	// Bare @handles are profiles. A bare word that happens to be a legal
	// handle ("AI", "nasa") is still search — use --handle when the account
	// is the target.
	if strings.HasPrefix(source, "@") {
		if handle, ok := xapi.ValidHandle(source); ok {
			return Target{Kind: KindProfile, Value: handle, Source: source}, nil
		}
	}

	return Target{Kind: KindSearch, Value: source, Source: source}, nil
}

func looksLikeURL(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "://") || strings.HasPrefix(lower, "x.com/") ||
		strings.HasPrefix(lower, "twitter.com/") || strings.HasPrefix(lower, "www.x.com/") ||
		strings.HasPrefix(lower, "www.twitter.com/")
}

func classifyXURL(raw string) (Target, bool) {
	parsed, err := url.Parse(ensureScheme(raw))
	if err != nil {
		return Target{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "x.com" && host != "twitter.com" && host != "www.x.com" && host != "www.twitter.com" {
		return Target{}, false
	}

	path := strings.Trim(parsed.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		return Target{}, false
	}

	if parts[0] == "search" {
		query := strings.TrimSpace(parsed.Query().Get("q"))
		if query == "" {
			return Target{}, false
		}
		return Target{Kind: KindSearch, Value: query, Source: raw, Sort: searchURLSort(parsed.Query().Get("f"))}, true
	}

	if (parts[0] == "i" && len(parts) >= 3 && (parts[1] == "lists" || parts[1] == "list")) ||
		(len(parts) >= 3 && parts[1] == "lists") {
		id := parts[len(parts)-1]
		if id != "" {
			return Target{Kind: KindList, Value: id, Source: raw}, true
		}
	}

	handle, ok := xapi.ValidHandle(parts[0])
	if !ok {
		return Target{}, false
	}
	kind := KindProfile
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "with_replies":
			kind = KindProfileReplies
		case "media":
			kind = KindProfileMedia
		case "likes":
			kind = KindProfileLikes
		case "status", "statuses":
			return Target{}, false
		}
	}
	return Target{Kind: kind, Value: handle, Source: raw}, true
}

func searchURLSort(flag string) string {
	switch strings.ToLower(strings.TrimSpace(flag)) {
	case "live":
		return "latest"
	case "top":
		return "top"
	}
	return ""
}

func ensureScheme(raw string) string {
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}
