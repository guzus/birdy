package cmd

import (
	"bytes"
	"encoding/json"
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

	var out bytes.Buffer
	accountListCmd.SetOut(&out)
	runErr := accountListCmd.RunE(accountListCmd, nil)
	if runErr != nil {
		t.Fatalf("expected list to succeed, got %v", runErr)
	}
	if !strings.Contains(out.String(), "env_user") {
		t.Fatalf("expected env-backed account in list output, got %q", out.String())
	}
}

func TestAccountListRejectsUnexpectedArgs(t *testing.T) {
	if err := accountListCmd.Args(accountListCmd, []string{"extra"}); err == nil {
		t.Fatal("expected account list to reject unexpected args")
	}
}

func TestAccountRemoveTrimsNameBeforeSuccessMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	var out bytes.Buffer
	accountRemoveCmd.SetOut(&out)
	runErr := accountRemoveCmd.RunE(accountRemoveCmd, []string{"  alpha  "})
	if runErr != nil {
		t.Fatalf("expected remove to succeed, got %v", runErr)
	}
	if !strings.Contains(out.String(), `Account "alpha" removed.`) {
		t.Fatalf("expected trimmed success message, got %q", out.String())
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

func TestAccountAddReadsPromptValuesFromCommandInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := accountAddCmd.Flags().Set("auth-token", ""); err != nil {
		t.Fatalf("reset auth-token: %v", err)
	}
	if err := accountAddCmd.Flags().Set("ct0", ""); err != nil {
		t.Fatalf("reset ct0: %v", err)
	}

	var out bytes.Buffer
	accountAddCmd.SetIn(strings.NewReader("token-from-input\nct0-from-input\n"))
	accountAddCmd.SetOut(&out)

	if err := accountAddCmd.RunE(accountAddCmd, []string{"prompt-user"}); err != nil {
		t.Fatalf("expected add to succeed, got %v", err)
	}

	if !strings.Contains(out.String(), "auth_token: ") || !strings.Contains(out.String(), "ct0: ") {
		t.Fatalf("expected prompts in output, got %q", out.String())
	}
	if !strings.Contains(out.String(), `Account "prompt-user" added.`) {
		t.Fatalf("expected success output, got %q", out.String())
	}

	st, err := store.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	acct, err := st.Get("prompt-user")
	if err != nil {
		t.Fatalf("expected prompted account to be persisted, got %v", err)
	}
	if acct.AuthToken != "token-from-input" || acct.CT0 != "ct0-from-input" {
		t.Fatalf("unexpected stored credentials: %+v", acct)
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

func TestAccountUpdateReadsPromptValuesFromCommandInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "old-token", "ct0": "old-ct0"},
	})

	if err := accountUpdateCmd.Flags().Set("auth-token", ""); err != nil {
		t.Fatalf("reset auth-token: %v", err)
	}
	if err := accountUpdateCmd.Flags().Set("ct0", ""); err != nil {
		t.Fatalf("reset ct0: %v", err)
	}

	var out bytes.Buffer
	accountUpdateCmd.SetIn(strings.NewReader("new-token\nnew-ct0\n"))
	accountUpdateCmd.SetOut(&out)

	if err := accountUpdateCmd.RunE(accountUpdateCmd, []string{"alpha"}); err != nil {
		t.Fatalf("expected update to succeed, got %v", err)
	}

	if !strings.Contains(out.String(), "auth_token: ") || !strings.Contains(out.String(), "ct0: ") {
		t.Fatalf("expected prompts in output, got %q", out.String())
	}
	if !strings.Contains(out.String(), `Account "alpha" updated.`) {
		t.Fatalf("expected success output, got %q", out.String())
	}

	st, err := store.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	acct, err := st.Get("alpha")
	if err != nil {
		t.Fatalf("expected updated account to exist, got %v", err)
	}
	if acct.AuthToken != "new-token" || acct.CT0 != "new-ct0" {
		t.Fatalf("unexpected stored credentials: %+v", acct)
	}
}
