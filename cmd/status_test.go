package cmd

import (
	"bytes"
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

func TestStatusRejectsInvalidStrategy(t *testing.T) {
	prevStrategy := strategyFlag
	strategyFlag = "nonsense"
	defer func() { strategyFlag = prevStrategy }()

	var out bytes.Buffer
	var errOut bytes.Buffer
	statusCmd.SetOut(&out)
	statusCmd.SetErr(&errOut)

	err := statusCmd.RunE(statusCmd, nil)
	if err == nil {
		t.Fatal("expected invalid strategy to fail")
	}
	if !strings.Contains(err.Error(), "unknown strategy") {
		t.Fatalf("expected unknown strategy error, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no stdout on invalid strategy, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected no stderr side output on invalid strategy, got %q", errOut.String())
	}
}

func TestStatusRoutesWarningsToCommandErr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := home + "/.config/birdy"
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(configDir+"/accounts.json", []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write corrupt accounts: %v", err)
	}
	if err := os.WriteFile(configDir+"/state.json", []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	statusCmd.SetOut(&out)
	statusCmd.SetErr(&errOut)

	runErr := statusCmd.RunE(statusCmd, nil)
	if runErr != nil {
		t.Fatalf("expected status to succeed, got %v", runErr)
	}
	if !strings.Contains(errOut.String(), "[birdy] warning: recovered from corrupt account store") {
		t.Fatalf("expected account warning on stderr, got %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "[birdy] warning: recovered from corrupt state file") {
		t.Fatalf("expected state warning on stderr, got %q", errOut.String())
	}
	if strings.Contains(out.String(), "[birdy] warning:") {
		t.Fatalf("expected warnings not to leak into stdout, got %q", out.String())
	}
}
