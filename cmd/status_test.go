package cmd

import (
	"bytes"
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

	var out bytes.Buffer
	statusCmd.SetOut(&out)
	runErr := statusCmd.RunE(statusCmd, nil)
	if runErr != nil {
		t.Fatalf("expected status to succeed, got %v", runErr)
	}
	if !strings.Contains(out.String(), "Last used:  alpha (not configured)") {
		t.Fatalf("expected stale last-used annotation, got %q", out.String())
	}
}

func TestStatusShowsConfiguredLastUsedAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})
	writeStateFixture(t, home, "alpha", "sonnet")

	var out bytes.Buffer
	statusCmd.SetOut(&out)
	runErr := statusCmd.RunE(statusCmd, nil)
	if runErr != nil {
		t.Fatalf("expected status to succeed, got %v", runErr)
	}
	if !strings.Contains(out.String(), "Last used:  alpha") {
		t.Fatalf("expected last-used account in output, got %q", out.String())
	}
	if strings.Contains(out.String(), "(not configured)") {
		t.Fatalf("expected configured account not to be marked stale, got %q", out.String())
	}
}
