package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

func writeStateFixture(t *testing.T, home string, lastUsedName, model string) {
	t.Helper()
	path := filepath.Join(home, ".config", "birdy", "state.json")
	loaded, err := state.LoadPath(path)
	if err != nil {
		t.Fatalf("load state path: %v", err)
	}
	loaded.LastUsedName = lastUsedName
	loaded.Model = model
	if err := loaded.Save(); err != nil {
		t.Fatalf("save state fixture: %v", err)
	}
}

func TestRunPassthroughDoesNotPersistUsageOnRunnerError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	writeStateFixture(t, home, "alpha", "sonnet")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(t.TempDir(), "missing-bird"))

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	err := runPassthrough(rootCmd, []string{"home"})
	if err == nil {
		t.Fatal("expected passthrough runner error")
	}

	st, openErr := store.Open()
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	alpha, _ := st.Get("alpha")
	beta, _ := st.Get("beta")
	if alpha.UseCount != 0 || beta.UseCount != 0 {
		t.Fatalf("expected no usage increments on runner error, alpha=%d beta=%d", alpha.UseCount, beta.UseCount)
	}

	loadedState, loadErr := state.Load()
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if loadedState.LastUsedName != "alpha" {
		t.Fatalf("expected last used account unchanged on runner error, got %q", loadedState.LastUsedName)
	}
}

func TestRunPassthroughPersistsUsageAfterSuccessfulRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	writeStateFixture(t, home, "alpha", "sonnet")

	birdPath := filepath.Join(t.TempDir(), "bird")
	if err := os.WriteFile(birdPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	if err := runPassthrough(rootCmd, []string{"home"}); err != nil {
		t.Fatalf("expected passthrough success, got %v", err)
	}

	st, openErr := store.Open()
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}
	alpha, _ := st.Get("alpha")
	beta, _ := st.Get("beta")
	if alpha.UseCount != 0 {
		t.Fatalf("expected alpha unchanged, got use_count=%d", alpha.UseCount)
	}
	if beta.UseCount != 1 {
		t.Fatalf("expected beta usage incremented once, got %d", beta.UseCount)
	}
	if beta.LastUsed.IsZero() {
		t.Fatal("expected beta last_used to be set")
	}

	loadedState, loadErr := state.Load()
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if loadedState.LastUsedName != "beta" {
		t.Fatalf("expected last used account updated after success, got %q", loadedState.LastUsedName)
	}
}

func TestRunPassthroughSpecificAccountSkipsRotationStateOnSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
	})
	writeStateFixture(t, home, "beta", "sonnet")

	birdPath := filepath.Join(t.TempDir(), "bird")
	script := strings.Join([]string{
		"#!/bin/sh",
		"exit 0",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "alpha", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	if err := runPassthrough(rootCmd, []string{"home"}); err != nil {
		t.Fatalf("expected passthrough success, got %v", err)
	}

	loadedState, loadErr := state.Load()
	if loadErr != nil {
		t.Fatalf("load state: %v", loadErr)
	}
	if loadedState.LastUsedName != "beta" {
		t.Fatalf("expected explicit account path not to rewrite rotation state unexpectedly, got %q", loadedState.LastUsedName)
	}
}

func TestRunPassthroughRejectsWriteCommandForExplicitReadOnlyAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "read_only": true},
	})

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "alpha", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	err := runPassthrough(rootCmd, []string{"tweet", "hello"})
	if err == nil {
		t.Fatal("expected explicit read-only account to block write command")
	}
	if !strings.Contains(err.Error(), `read-only account "alpha"`) {
		t.Fatalf("expected read-only account error, got %v", err)
	}
}

func TestRunPassthroughSkipsReadOnlyAccountsForWriteCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "read_only": true, "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	writeStateFixture(t, home, "beta", "sonnet")

	birdPath := filepath.Join(t.TempDir(), "bird")
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo \"$AUTH_TOKEN\" > \"$BIRDY_CAPTURE\"",
		"exit 0",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	capturePath := filepath.Join(t.TempDir(), "capture.txt")
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	t.Setenv("BIRDY_CAPTURE", capturePath)

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	if err := runPassthrough(rootCmd, []string{"tweet", "hello"}); err != nil {
		t.Fatalf("expected passthrough success, got %v", err)
	}

	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if strings.TrimSpace(string(captured)) != "token-b" {
		t.Fatalf("expected writable account token, got %q", string(captured))
	}
}

func TestRunPassthroughRejectsWriteCommandWhenAllAccountsAreReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "read_only": true},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "read_only": true},
	})

	prevStrategy, prevAccount, prevVerbose := strategyFlag, accountFlag, verboseFlag
	strategyFlag, accountFlag, verboseFlag = "round-robin", "", false
	defer func() {
		strategyFlag, accountFlag, verboseFlag = prevStrategy, prevAccount, prevVerbose
	}()

	err := runPassthrough(rootCmd, []string{"tweet", "hello"})
	if err == nil {
		t.Fatal("expected no writable accounts error")
	}
	if !strings.Contains(err.Error(), "no writable accounts configured") {
		t.Fatalf("expected no writable accounts error, got %v", err)
	}
}
