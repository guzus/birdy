package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/store"
)

// TestDaemonCommandRegistered verifies the daemon subcommand is wired into
// rootCmd via init(). This protects against accidental re-registration churn.
func TestDaemonCommandRegistered(t *testing.T) {
	got, _, err := rootCmd.Find([]string{"daemon"})
	if err != nil {
		t.Fatalf("find daemon subcommand: %v", err)
	}
	if got == nil || got.Use != "daemon" {
		t.Fatalf("expected to find daemon subcommand, got %#v", got)
	}
}

// TestDaemonCommandHelp confirms `birdy daemon --help` runs cleanly and
// prints the documented endpoints + flags. The help text is the contract
// users rely on to discover behavior.
func TestDaemonCommandHelp(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"daemon"})
	if err != nil {
		t.Fatalf("find daemon: %v", err)
	}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Help(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := buf.String()
	mustContain := []string{
		"POST /run",
		"GET  /health",
		"--addr",
		"--concurrency",
		"--cache-ttl",
		"127.0.0.1:8080",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Fatalf("help output missing %q\nFULL:\n%s", s, out)
		}
	}
}

// TestDaemonFlagsHaveDefaults verifies the documented defaults are wired
// into the Cobra flag set. Defaults that drift silently are a common
// regression source.
func TestDaemonFlagsHaveDefaults(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"daemon"})
	if err != nil {
		t.Fatalf("find daemon: %v", err)
	}
	cases := []struct {
		flag string
		want string
	}{
		{"addr", "127.0.0.1:8080"},
		{"concurrency", "8"},
		{"cache-ttl", "0s"},
	}
	for _, tc := range cases {
		f := cmd.Flags().Lookup(tc.flag)
		if f == nil {
			t.Fatalf("flag --%s not registered", tc.flag)
		}
		if f.DefValue != tc.want {
			t.Fatalf("--%s default: want %q, got %q", tc.flag, tc.want, f.DefValue)
		}
	}
}

// TestRunDaemonRejectsUnixAddr confirms scope discipline: we deliberately
// reject the `unix:/...` form in v1 so callers don't think they're getting
// a feature we haven't shipped.
func TestRunDaemonRejectsUnixAddr(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"a","auth_token":"t","ct0":"c"}]`)

	prevAddr := daemonAddrFlag
	prevConc := daemonConcurrencyFlag
	prevTTL := daemonCacheTTLFlag
	t.Cleanup(func() {
		daemonAddrFlag = prevAddr
		daemonConcurrencyFlag = prevConc
		daemonCacheTTLFlag = prevTTL
	})
	daemonAddrFlag = "unix:/tmp/birdy.sock"
	daemonConcurrencyFlag = 4
	daemonCacheTTLFlag = 0

	err := runDaemon(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "unix socket transport is not supported") {
		t.Fatalf("expected unix-not-supported error, got %v", err)
	}
}

// TestRunDaemonRejectsBadConcurrency exercises the flag validation guard.
func TestRunDaemonRejectsBadConcurrency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"a","auth_token":"t","ct0":"c"}]`)

	prevAddr := daemonAddrFlag
	prevConc := daemonConcurrencyFlag
	prevTTL := daemonCacheTTLFlag
	t.Cleanup(func() {
		daemonAddrFlag = prevAddr
		daemonConcurrencyFlag = prevConc
		daemonCacheTTLFlag = prevTTL
	})
	daemonAddrFlag = "127.0.0.1:0"
	daemonConcurrencyFlag = 0
	daemonCacheTTLFlag = 0

	err := runDaemon(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "concurrency must be >= 1") {
		t.Fatalf("expected concurrency error, got %v", err)
	}
}

// TestRunDaemonRejectsNegativeCacheTTL guards against accidental negatives.
// Cobra accepts negative durations on Duration flags, so the command
// validation has to catch them.
func TestRunDaemonRejectsNegativeCacheTTL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"a","auth_token":"t","ct0":"c"}]`)

	prevAddr := daemonAddrFlag
	prevConc := daemonConcurrencyFlag
	prevTTL := daemonCacheTTLFlag
	t.Cleanup(func() {
		daemonAddrFlag = prevAddr
		daemonConcurrencyFlag = prevConc
		daemonCacheTTLFlag = prevTTL
	})
	daemonAddrFlag = "127.0.0.1:0"
	daemonConcurrencyFlag = 4
	daemonCacheTTLFlag = -5 * time.Second

	err := runDaemon(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cache-ttl must be >= 0") {
		t.Fatalf("expected cache-ttl error, got %v", err)
	}
}

// TestRunDaemonRejectsNoAccounts confirms the daemon refuses to start when
// no accounts are configured. This mirrors passthrough/multi-fetch.
func TestRunDaemonRejectsNoAccounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", "")

	prevAddr := daemonAddrFlag
	prevConc := daemonConcurrencyFlag
	prevTTL := daemonCacheTTLFlag
	t.Cleanup(func() {
		daemonAddrFlag = prevAddr
		daemonConcurrencyFlag = prevConc
		daemonCacheTTLFlag = prevTTL
	})
	daemonAddrFlag = "127.0.0.1:0"
	daemonConcurrencyFlag = 4
	daemonCacheTTLFlag = 0

	err := runDaemon(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "no accounts configured") {
		t.Fatalf("expected no-accounts error, got %v", err)
	}
}

func TestDaemonNativeReadRecordsGraphQLRateLimit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", "")
	t.Setenv("BIRDY_USE_BIRD", "")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(home, "missing-bird"))
	writeAccountsFixture(t, home, []map[string]string{{"name": "alpha", "auth_token": "tok", "ct0": "ct0"}})
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.Get("alpha")
	if err != nil {
		t.Fatal(err)
	}

	prev := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = prev })
	http.DefaultTransport = commandRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/TweetDetail") {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"errors":[{"code":88,"message":"Rate limit exceeded"}]}`)),
			Header:     make(http.Header),
		}, nil
	})
	exit, _, stderr, err := daemonCommandRunner(st)(context.Background(), acc, []string{"read", "1234567890123456789"})
	if err != nil || exit != 1 || !strings.Contains(stderr, "Rate limit exceeded") {
		t.Fatalf("exit=%d stderr=%q err=%v", exit, stderr, err)
	}
	persisted, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := persisted.Get("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if got.RateLimitCount != 1 || got.LastRateLimitedAt.IsZero() {
		t.Fatal("missing persisted rate limit")
	}
}
