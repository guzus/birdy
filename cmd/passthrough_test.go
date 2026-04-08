package cmd

import "testing"

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
	if got := firstBirdCommand([]string{"-u", "alice", "thread", "123"}); got != "thread" {
		t.Fatalf("expected thread after short flag value, got %q", got)
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
	if got := firstBirdCommand([]string{"custom"}); got != "custom" {
		t.Fatalf("expected fallback custom command, got %q", got)
	}
}

func TestIsReadOnlyBirdCommand(t *testing.T) {
	t.Setenv("BIRDY_READ_ONLY", "1")

	blocked, name := isReadOnlyBirdCommand([]string{"tweet", "hello"})
	if !blocked || name != "tweet" {
		t.Fatalf("expected tweet blocked, got blocked=%v name=%q", blocked, name)
	}

	blocked, name = isReadOnlyBirdCommand([]string{"home"})
	if blocked {
		t.Fatalf("expected home allowed, got blocked=%v name=%q", blocked, name)
	}

	blocked, name = isReadOnlyBirdCommand([]string{"--format", "json", "tweet", "hello"})
	if !blocked || name != "tweet" {
		t.Fatalf("expected tweet blocked after flag value, got blocked=%v name=%q", blocked, name)
	}
}
