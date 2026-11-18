package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChubbyNativeToolPolicy(t *testing.T) {
	for _, argv := range [][]string{
		{"tweet", "x"}, {"whoami"}, {"check"}, {"bookmarks"}, {"search", "--bird", "hello"}, {"about", "--account", "other", "nasa"}, {"search", "--ct0=secret", "x"}, {"user-tweets", "nasa", "-n", "9999999"}, {"news", "--for-you"}, {"news", "--sports", "--sports"}, {"search", "--max-pages=10", "x"}, {"read", "bad"}, {"read", "123", "spurious"}, {"home", "--user", "somebody"}, {"search", "--help"},
	} {
		if _, err := chubbyNativeArgs(argv[0], argv[1:]); err == nil {
			t.Errorf("accepted %v", argv)
		}
	}
	for _, argv := range [][]string{{"about", "NASA"}, {"search", "-n", "20", "--", "--literal-search"}, {"read", "https://x.com/nasa/status/12345"}, {"news", "--sports"}, {"home", "--latest", "-n", "20"}, {"replies", "12345"}, {"user-tweets", "NASA"}, {"thread", "12345"}} {
		if _, err := chubbyNativeArgs(argv[0], argv[1:]); err != nil {
			t.Errorf("rejected %v: %v", argv, err)
		}
	}
	a, err := chubbyNativeArgs("news", nil)
	if err != nil || !slices.Contains(a, "--news-only") {
		t.Fatalf("default news scope: %v %v", a, err)
	}
}

func TestChubbyToolRunsNativelyAndSanitizesErrors(t *testing.T) {
	setupMultiFetchTest(t, "private-test-account")
	t.Setenv("BIRDY_USE_BIRD", "1")
	t.Setenv("BIRDY_BIRD_PATH", "/does-not-exist")
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	calls := 0
	http.DefaultTransport = commandRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/SearchTimeline") {
			t.Fatalf("unexpected operation %q", r.URL.Path)
		}
		if calls > 1 {
			return nil, errors.New("private-test-account provider credential sentinel")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[{"entries":[]}]}}}}}`)), Request: r}, nil
	})
	out, err := runChubbyTool(t.Context(), "search", []string{"--json", "test"})
	if err != nil || strings.TrimSpace(out) != "[]" || calls != 1 {
		t.Fatalf("native result %q, calls=%d err=%v", out, calls, err)
	}
	if out, err = runChubbyTool(t.Context(), "search", []string{"test"}); err == nil || err.Error() != "public X read failed" || out != "" {
		t.Fatalf("unsafe error %q, %v", out, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = runChubbyTool(ctx, "search", []string{"test"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read returned %v", err)
	}
}

func TestChubbyToolOutputCap(t *testing.T) {
	var out chubbyOutput
	input := bytes.Repeat([]byte("🙂"), chubbyOutputLimit)
	n, err := out.Write(input)
	result := out.String()
	if n != len(input) || err != nil || len(result) > chubbyOutputLimit || !utf8.ValidString(result) || !strings.HasSuffix(result, "[output truncated]") {
		t.Fatal("tool output cap failed")
	}
}
