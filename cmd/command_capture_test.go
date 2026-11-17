package cmd

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/store"
)

func TestNativeInvocation(t *testing.T) {
	setupMultiFetchTest(t, "alpha")
	for _, tc := range []struct{ argv, want []string }{
		{[]string{"--plain", "check"}, []string{"check", "--plain"}},
		{[]string{"--no-color", "123456789", "--json"}, []string{"read", "--no-color", "123456789", "--json"}},
		{[]string{"trending", "--sports", "--json"}, []string{"news", "--sports", "--json"}},
		{[]string{"trending", "--with-tweets"}, nil},
		{[]string{"home", "--json=false"}, nil},
		{[]string{"following", "--user"}, nil},
	} {
		before := slices.Clone(tc.argv)
		if got := nativeInvocation(tc.argv); !slices.Equal(got, tc.want) || !slices.Equal(tc.argv, before) {
			t.Errorf("selection for %q: got %q, want %q; original argv now %q", before, got, tc.want, tc.argv)
		}
	}
}

func TestCaptureRejectsSurplusWriteOperandsBeforeExecution(t *testing.T) {
	setupMultiFetchTest(t, "alpha")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(t.TempDir(), "missing"))
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = commandRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("surplus operands reached the network")
		return nil, nil
	})
	for _, argv := range [][]string{{"tweet", "", "another"}, {"follow", "-", "another"}, {"reply", "12345678", "text", "another"}} {
		res, _, stderr, err := runCommandCapture(t.Context(), &store.Account{AuthToken: "t", CT0: "c"}, argv)
		if err != nil || res.ExitCode == 0 || !strings.Contains(stderr, "too many arguments") {
			t.Fatalf("%v: exit %d, stderr %q, err %v", argv, res.ExitCode, stderr, err)
		}
	}
	parsed := parseNativeArgs([]string{"--", "--help"})
	if parsed.positional != "--help" || helpRequested([]string{"--", "--help"}) {
		t.Fatal("literal operand after -- changed")
	}
}

func TestCaptureHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, _, err := runCommandCapture(ctx, &store.Account{AuthToken: "t", CT0: "c"}, []string{"check"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("capture returned %v", err)
	}
}

type commandRoundTripFunc func(*http.Request) (*http.Response, error)

func (f commandRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
