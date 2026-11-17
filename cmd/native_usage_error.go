package cmd

import (
	"regexp"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

type nativeUsageError string

func (e nativeUsageError) Error() string { return string(e) }
func (nativeUsageError) ExitCode() int   { return 2 }

// Native reads have also historically accepted an X profile URL.
var profileHandleURL = regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?(?:x\.com|twitter\.com)/([A-Za-z0-9_]{1,15})/?(?:[?#].*)?$`)

func cliHandle(raw string) (string, bool) {
	handle := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if match := profileHandleURL.FindStringSubmatch(strings.TrimSpace(raw)); match != nil {
		return match[1], true
	}
	normalized, ok := xapi.ValidHandle(handle)
	return handle, ok && normalized == handle
}
