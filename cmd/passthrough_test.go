package cmd

import "testing"

func TestFirstBirdCommandSkipsFlags(t *testing.T) {
	if got := firstBirdCommand([]string{"--foo", "-v", "tweet"}); got != "tweet" {
		t.Fatalf("expected tweet, got %q", got)
	}
	if got := firstBirdCommand([]string{"-x"}); got != "" {
		t.Fatalf("expected empty, got %q", got)
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
}
