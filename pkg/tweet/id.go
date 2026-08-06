package tweet

import (
	"fmt"
	"regexp"
	"strings"
)

// tweetURLPattern matches the status URL forms X serves. The handle segment is
// permissive because the /i/web/ and /i/status/ forms carry no handle.
//
// The leading (?:^|[/.]) enforces a host boundary. Without it the host alternation
// matches as a substring, so a lookalike domain like notx.com/user/status/123
// would be accepted as a real X link.
var tweetURLPattern = regexp.MustCompile(`(?i)(?:^|[/.])(?:twitter\.com|x\.com)/(?:[A-Za-z0-9_]{1,15}/status(?:es)?|i/web/status|i/status)/(\d+)`)

// bareTweetIDPattern matches a raw numeric tweet ID.
var bareTweetIDPattern = regexp.MustCompile(`^\d{5,25}$`)

// ExtractTweetID resolves a user-supplied reference to a tweet ID. It accepts
// status URLs (with or without scheme, with any query string or trailing
// /photo/N segment) and bare numeric IDs.
//
// Unlike a permissive parse, it rejects anything that is not recognizably a
// tweet reference rather than forwarding it to the API as-is — a profile URL
// should fail here, not produce a confusing error from X.
func ExtractTweetID(ref string) (string, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", fmt.Errorf("empty tweet reference")
	}

	if bareTweetIDPattern.MatchString(trimmed) {
		return trimmed, nil
	}

	if match := tweetURLPattern.FindStringSubmatch(trimmed); match != nil {
		return match[1], nil
	}

	return "", fmt.Errorf("%q is not a tweet URL or id", truncateRef(trimmed))
}

func truncateRef(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
