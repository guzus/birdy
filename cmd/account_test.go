package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

func writeAccountFixtureFile(t *testing.T, home string, accounts any) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "birdy")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	data, err := json.Marshal(accounts)
	if err != nil {
		t.Fatalf("marshal accounts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "accounts.json"), data, 0600); err != nil {
		t.Fatalf("write accounts fixture: %v", err)
	}
}

func TestEnsureAccountStoreWritableRejectsEphemeralStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	st, err := store.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath returned error: %v", err)
	}
	if !st.IsEphemeral() {
		t.Fatal("expected env-only store to be ephemeral")
	}
	if err := ensureAccountStoreWritable(st); err == nil {
		t.Fatal("expected env-only store to be rejected for mutation")
	}
}

func TestEnsureAccountStoreWritableAllowsPersistedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	st, err := store.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath returned error: %v", err)
	}
	if st.IsEphemeral() {
		t.Fatal("expected file-backed store not to be ephemeral")
	}
	if err := ensureAccountStoreWritable(st); err != nil {
		t.Fatalf("expected file-backed store to be writable, got %v", err)
	}
}

func TestEnsureAccountMutationAllowedRejectsReadOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	st, err := store.OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath returned error: %v", err)
	}

	t.Setenv("BIRDY_READ_ONLY", "1")
	if err := ensureAccountMutationAllowed(st); err == nil {
		t.Fatal("expected read-only mode to reject account mutation")
	}
}

func TestAccountAddRejectsEnvOnlyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	if err := accountAddCmd.Flags().Set("auth-token", "token"); err != nil {
		t.Fatalf("set auth-token: %v", err)
	}
	if err := accountAddCmd.Flags().Set("ct0", "ct0"); err != nil {
		t.Fatalf("set ct0: %v", err)
	}
	defer func() {
		_ = accountAddCmd.Flags().Set("auth-token", "")
		_ = accountAddCmd.Flags().Set("ct0", "")
	}()

	err := accountAddCmd.RunE(accountAddCmd, []string{"new-user"})
	if err == nil {
		t.Fatal("expected add against env-only store to fail")
	}
	if !strings.Contains(err.Error(), "env-backed only") {
		t.Fatalf("expected env-backed error, got %v", err)
	}

	configPath := filepath.Join(home, ".config", "birdy", "accounts.json")
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no persisted accounts file, stat err=%v", statErr)
	}
}

func TestAccountRemoveRejectsEnvOnlyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	err := accountRemoveCmd.RunE(accountRemoveCmd, []string{"env_user"})
	if err == nil {
		t.Fatal("expected remove against env-only store to fail")
	}
	if !strings.Contains(err.Error(), "env-backed only") {
		t.Fatalf("expected env-backed error, got %v", err)
	}
}

func TestAccountUpdateRejectsEnvOnlyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	if err := accountUpdateCmd.Flags().Set("auth-token", "token"); err != nil {
		t.Fatalf("set auth-token: %v", err)
	}
	if err := accountUpdateCmd.Flags().Set("ct0", "ct0"); err != nil {
		t.Fatalf("set ct0: %v", err)
	}
	defer func() {
		_ = accountUpdateCmd.Flags().Set("auth-token", "")
		_ = accountUpdateCmd.Flags().Set("ct0", "")
	}()

	err := accountUpdateCmd.RunE(accountUpdateCmd, []string{"env_user"})
	if err == nil {
		t.Fatal("expected update against env-only store to fail")
	}
	if !strings.Contains(err.Error(), "env-backed only") {
		t.Fatalf("expected env-backed error, got %v", err)
	}
}

func TestAccountListStillWorksWithEnvOnlyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	runErr := accountListCmd.RunE(accountListCmd, nil)
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("expected list to succeed, got %v", runErr)
	}
	if !strings.Contains(string(out), "env_user") {
		t.Fatalf("expected env-backed account in list output, got %q", string(out))
	}
}

func TestAccountRemoveTrimsNameBeforeSuccessMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	runErr := accountRemoveCmd.RunE(accountRemoveCmd, []string{"  alpha  "})
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("expected remove to succeed, got %v", runErr)
	}
	if !strings.Contains(string(out), `Account "alpha" removed.`) {
		t.Fatalf("expected trimmed success message, got %q", string(out))
	}
}

func TestAccountRemoveClearsLastUsedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b"},
	})
	writeStateFixture(t, home, "alpha", "sonnet")

	if err := accountRemoveCmd.RunE(accountRemoveCmd, []string{"alpha"}); err != nil {
		t.Fatalf("expected remove to succeed, got %v", err)
	}

	loaded, err := state.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.LastUsedName != "" {
		t.Fatalf("expected last-used state to be cleared, got %q", loaded.LastUsedName)
	}
}

func TestAccountAddRejectsReadOnlyMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_READ_ONLY", "1")

	if err := accountAddCmd.Flags().Set("auth-token", "token"); err != nil {
		t.Fatalf("set auth-token: %v", err)
	}
	if err := accountAddCmd.Flags().Set("ct0", "ct0"); err != nil {
		t.Fatalf("set ct0: %v", err)
	}
	defer func() {
		_ = accountAddCmd.Flags().Set("auth-token", "")
		_ = accountAddCmd.Flags().Set("ct0", "")
	}()

	err := accountAddCmd.RunE(accountAddCmd, []string{"new-user"})
	if err == nil {
		t.Fatal("expected add in read-only mode to fail")
	}
	if !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestAccountRemoveRejectsReadOnlyMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_READ_ONLY", "1")
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	err := accountRemoveCmd.RunE(accountRemoveCmd, []string{"alpha"})
	if err == nil {
		t.Fatal("expected remove in read-only mode to fail")
	}
	if !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("expected read-only error, got %v", err)
	}
}

func TestAccountUpdateRejectsReadOnlyMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_READ_ONLY", "1")
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	if err := accountUpdateCmd.Flags().Set("auth-token", "token"); err != nil {
		t.Fatalf("set auth-token: %v", err)
	}
	if err := accountUpdateCmd.Flags().Set("ct0", "ct0"); err != nil {
		t.Fatalf("set ct0: %v", err)
	}
	defer func() {
		_ = accountUpdateCmd.Flags().Set("auth-token", "")
		_ = accountUpdateCmd.Flags().Set("ct0", "")
	}()

	err := accountUpdateCmd.RunE(accountUpdateCmd, []string{"alpha"})
	if err == nil {
		t.Fatal("expected update in read-only mode to fail")
	}
	if !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("expected read-only error, got %v", err)
	}
}
