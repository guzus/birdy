package cmd

import (
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/store"
)

func TestFirstBirdCommandSkipsFlags(t *testing.T) {
	if got := firstBirdCommand([]string{"--foo", "-v", "tweet"}); got != "tweet" {
		t.Fatalf("expected tweet, got %q", got)
	}
	if got := firstBirdCommand([]string{"-x"}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := firstBirdCommand([]string{"--format", "json", "home"}); got != "home" {
		t.Fatalf("expected home after long flag value, got %q", got)
	}
	if got := firstBirdCommand([]string{"--verbose", "home"}); got != "home" {
		t.Fatalf("expected home after boolean-style long flag, got %q", got)
	}
	if got := firstBirdCommand([]string{"-u", "alice", "thread", "123"}); got != "thread" {
		t.Fatalf("expected thread after short flag value, got %q", got)
	}
	if got := firstBirdCommand([]string{"-v", "home"}); got != "home" {
		t.Fatalf("expected home after boolean-style short flag, got %q", got)
	}
	if got := firstBirdCommand([]string{"tweet", "--"}); got != "tweet" {
		t.Fatalf("expected tweet to remain command before bare separator, got %q", got)
	}
	if got := firstBirdCommand([]string{"--", "home"}); got != "home" {
		t.Fatalf("expected home after separator, got %q", got)
	}
	if got := firstBirdCommand([]string{"--format", "json", "--"}); got != "" {
		t.Fatalf("expected empty command when separator ends args, got %q", got)
	}
	if got := firstBirdCommand([]string{"--user", "alice"}); got != "" {
		t.Fatalf("expected missing command after long flag value only, got %q", got)
	}
	if got := firstBirdCommand([]string{"-u", "alice"}); got != "" {
		t.Fatalf("expected missing command after short flag value only, got %q", got)
	}
	if got := firstBirdCommand([]string{"custom"}); got != "custom" {
		t.Fatalf("expected fallback custom command, got %q", got)
	}
}

func TestIsMutatingBirdCommand(t *testing.T) {
	blocked, name := isMutatingBirdCommand([]string{"tweet", "hello"})
	if !blocked || name != "tweet" {
		t.Fatalf("expected tweet blocked, got blocked=%v name=%q", blocked, name)
	}

	blocked, name = isMutatingBirdCommand([]string{"home"})
	if blocked {
		t.Fatalf("expected home allowed, got blocked=%v name=%q", blocked, name)
	}

	blocked, name = isMutatingBirdCommand([]string{"--format", "json", "tweet", "hello"})
	if !blocked || name != "tweet" {
		t.Fatalf("expected tweet blocked after flag value, got blocked=%v name=%q", blocked, name)
	}

	blocked, name = isMutatingBirdCommand([]string{"--verbose", "tweet", "hello"})
	if !blocked || name != "tweet" {
		t.Fatalf("expected tweet blocked after boolean-style flag, got blocked=%v name=%q", blocked, name)
	}
}

func TestEnsureBirdCommandAllowedHonorsGlobalReadOnlyMode(t *testing.T) {
	t.Setenv("BIRDY_READ_ONLY", "1")

	err := ensureBirdCommandAllowed(&store.Account{Name: "alpha"}, []string{"tweet", "hello"})
	if err == nil {
		t.Fatal("expected global read-only mode to block tweet")
	}
	if !strings.Contains(err.Error(), "BIRDY_READ_ONLY") {
		t.Fatalf("expected global read-only error, got %v", err)
	}
}

func TestEnsureBirdCommandAllowedHonorsAccountReadOnlyMode(t *testing.T) {
	err := ensureBirdCommandAllowed(&store.Account{Name: "alpha", ReadOnly: true}, []string{"tweet", "hello"})
	if err == nil {
		t.Fatal("expected account read-only mode to block tweet")
	}
	if !strings.Contains(err.Error(), `read-only account "alpha"`) {
		t.Fatalf("expected account read-only error, got %v", err)
	}
}

func TestFilterWritableAccounts(t *testing.T) {
	accounts := filterWritableAccounts([]store.Account{
		{Name: "alpha", ReadOnly: true},
		{Name: "beta"},
		{Name: "gamma", ReadOnly: true},
	})
	if len(accounts) != 1 || accounts[0].Name != "beta" {
		t.Fatalf("expected only writable account to remain, got %+v", accounts)
	}
}
