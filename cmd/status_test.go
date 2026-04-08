package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestStatusMarksStaleLastUsedAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b"},
	})
	writeStateFixture(t, home, "alpha", "sonnet")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	runErr := statusCmd.RunE(statusCmd, nil)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("expected status to succeed, got %v", runErr)
	}
	if !strings.Contains(string(out), "Last used:  alpha (not configured)") {
		t.Fatalf("expected stale last-used annotation, got %q", string(out))
	}
}

func TestStatusShowsConfiguredLastUsedAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})
	writeStateFixture(t, home, "alpha", "sonnet")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	runErr := statusCmd.RunE(statusCmd, nil)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("expected status to succeed, got %v", runErr)
	}
	if !strings.Contains(string(out), "Last used:  alpha") {
		t.Fatalf("expected last-used account in output, got %q", string(out))
	}
	if strings.Contains(string(out), "(not configured)") {
		t.Fatalf("expected configured account not to be marked stale, got %q", string(out))
	}
}
