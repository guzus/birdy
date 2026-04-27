package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
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
