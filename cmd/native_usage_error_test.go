package cmd

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/store"
)

func TestCapturedNativeArgumentExitCodes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_USE_BIRD", "")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(home, "missing-bird"))
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = commandRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected request to %s", r.URL.Path)
	})
	for _, tc := range []struct {
		args    []string
		code    int
		message string
	}{
		{[]string{"user-tweets", "bird", "-n", "no"}, 2, "Invalid --count"},
		{[]string{"user-tweets", "bird", "-n", "0"}, 2, "Invalid --count"},
		{[]string{"user-tweets", "bird", "-n", "201"}, 2, "Max 200"},
		{[]string{"user-tweets", "bad/handle"}, 2, "Invalid handle"},
		{[]string{"mentions", "--user", "--bad"}, 2, "Invalid --user handle"},
		{[]string{"mentions", "--user="}, 2, "Invalid --user handle"},
		{[]string{"list-timeline", "1234"}, 2, "Invalid list ID or URL"},
		{[]string{"list-timeline", "12345", "-n", "no"}, 1, "Invalid --count"},
		{[]string{"list-timeline"}, 1, "missing list id"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			acc := &store.Account{Name: "test", AuthToken: "tok", CT0: "ct0"}
			res, stdout, stderr, err := runCommandCapture(t.Context(), acc, tc.args)
			if err != nil || res.ExitCode != tc.code || stdout != "" || !strings.Contains(stderr, tc.message) {
				t.Fatalf("exit=%d stdout=%q stderr=%q err=%v", res.ExitCode, stdout, stderr, err)
			}
		})
	}
}

func TestCapturedListTimelineUsesNumericIDFromURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_USE_BIRD", "")
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	calls := 0
	http.DefaultTransport = commandRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/ListLatestTweetsTimeline") || !strings.Contains(r.URL.Query().Get("variables"), `"listId":"123456"`) {
			return nil, fmt.Errorf("unexpected list query: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"list":{"tweets_timeline":{"timeline":{"instructions":[]}}}}}`)), Header: make(http.Header)}, nil
	})
	acc := &store.Account{Name: "test", AuthToken: "tok", CT0: "ct0"}
	res, stdout, stderr, err := runCommandCapture(t.Context(), acc, []string{"list-timeline", "https://twitter.com/i/lists/123456?s=20", "--json"})
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(stdout) != "[]" || calls != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q calls=%d err=%v", res.ExitCode, stdout, stderr, calls, err)
	}
}

func TestCLIHandleAcceptsProfileURLs(t *testing.T) {
	handle, ok := cliHandle("https://x.com/Birdy/?lang=en")
	if !ok || handle != "Birdy" {
		t.Fatalf("profile URL resolved to %q, %t", handle, ok)
	}
}
