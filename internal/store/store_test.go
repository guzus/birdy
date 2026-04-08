package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "accounts.json")
}

func TestOpenPathCreatesEmptyStore(t *testing.T) {
	path := tempStorePath(t)
	st, err := OpenPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Len() != 0 {
		t.Errorf("expected 0 accounts, got %d", st.Len())
	}
}

func TestAddAndList(t *testing.T) {
	path := tempStorePath(t)
	st, err := OpenPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := st.Add("alice", "token_a", "ct0_a"); err != nil {
		t.Fatalf("failed to add: %v", err)
	}
	if err := st.Add("bob", "token_b", "ct0_b"); err != nil {
		t.Fatalf("failed to add: %v", err)
	}

	if st.Len() != 2 {
		t.Errorf("expected 2 accounts, got %d", st.Len())
	}

	accounts := st.List()
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}
	if accounts[0].Name != "alice" {
		t.Errorf("expected alice, got %q", accounts[0].Name)
	}
	if accounts[1].Name != "bob" {
		t.Errorf("expected bob, got %q", accounts[1].Name)
	}
}

func TestAddDuplicate(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")
	err := st.Add("alice", "token_b", "ct0_b")
	if err == nil {
		t.Error("expected error adding duplicate account")
	}
}

func TestAddNormalizesWhitespace(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	if err := st.Add("  alice  ", "  token_a  ", "  ct0_a  "); err != nil {
		t.Fatalf("failed to add normalized account: %v", err)
	}

	a, err := st.Get("alice")
	if err != nil {
		t.Fatalf("failed to get normalized account: %v", err)
	}
	if a.Name != "alice" || a.AuthToken != "token_a" || a.CT0 != "ct0_a" {
		t.Fatalf("expected trimmed account fields, got %#v", a)
	}
}

func TestAddRejectsBlankFields(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	if err := st.Add("   ", "token", "ct0"); err == nil {
		t.Fatal("expected blank account name to be rejected")
	}
	if err := st.Add("alice", "   ", "ct0"); err == nil {
		t.Fatal("expected blank auth token to be rejected")
	}
	if err := st.Add("alice", "token", "   "); err == nil {
		t.Fatal("expected blank ct0 to be rejected")
	}
}

func TestRemove(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")
	st.Add("bob", "token_b", "ct0_b")

	if err := st.Remove("alice"); err != nil {
		t.Fatalf("failed to remove: %v", err)
	}
	if st.Len() != 1 {
		t.Errorf("expected 1 account, got %d", st.Len())
	}

	accounts := st.List()
	if accounts[0].Name != "bob" {
		t.Errorf("expected bob, got %q", accounts[0].Name)
	}
}

func TestRemoveTrimsName(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.Remove("  alice  "); err != nil {
		t.Fatalf("failed to remove with padded name: %v", err)
	}
	if st.Len() != 0 {
		t.Fatalf("expected store to be empty after remove, got %d", st.Len())
	}
}

func TestRemoveNotFound(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	err := st.Remove("nonexistent")
	if err == nil {
		t.Error("expected error removing nonexistent account")
	}
}

func TestGet(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	a, err := st.Get("alice")
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}
	if a.Name != "alice" {
		t.Errorf("expected alice, got %q", a.Name)
	}
	if a.AuthToken != "token_a" {
		t.Errorf("expected token_a, got %q", a.AuthToken)
	}
}

func TestGetTrimsName(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	a, err := st.Get("  alice  ")
	if err != nil {
		t.Fatalf("failed to get with padded name: %v", err)
	}
	if a.Name != "alice" {
		t.Fatalf("expected alice, got %q", a.Name)
	}
}

func TestGetNotFound(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	_, err := st.Get("nonexistent")
	if err == nil {
		t.Error("expected error getting nonexistent account")
	}
}

func TestRecordUsage(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.RecordUsage("alice"); err != nil {
		t.Fatalf("failed to record usage: %v", err)
	}

	a, _ := st.Get("alice")
	if a.UseCount != 1 {
		t.Errorf("expected use_count=1, got %d", a.UseCount)
	}
	if a.LastUsed.IsZero() {
		t.Error("expected last_used to be set")
	}
}

func TestRecordUsageNotFound(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	err := st.RecordUsage("nonexistent")
	if err == nil {
		t.Error("expected error recording usage for nonexistent account")
	}
}

func TestRecordUsageTrimsName(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.RecordUsage("  alice  "); err != nil {
		t.Fatalf("failed to record usage with padded name: %v", err)
	}

	a, _ := st.Get("alice")
	if a.UseCount != 1 {
		t.Fatalf("expected use_count=1, got %d", a.UseCount)
	}
}

func TestUpdate(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.Update("alice", "new_token", "new_ct0"); err != nil {
		t.Fatalf("failed to update: %v", err)
	}

	a, _ := st.Get("alice")
	if a.AuthToken != "new_token" {
		t.Errorf("expected new_token, got %q", a.AuthToken)
	}
	if a.CT0 != "new_ct0" {
		t.Errorf("expected new_ct0, got %q", a.CT0)
	}
}

func TestUpdateNormalizesWhitespace(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.Update("  alice  ", "  new_token  ", "  new_ct0  "); err != nil {
		t.Fatalf("failed to update with padded fields: %v", err)
	}

	a, _ := st.Get("alice")
	if a.AuthToken != "new_token" || a.CT0 != "new_ct0" {
		t.Fatalf("expected trimmed credentials, got %#v", a)
	}
}

func TestUpdateRejectsBlankFields(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.Update("   ", "new_token", "new_ct0"); err == nil {
		t.Fatal("expected blank account name to be rejected")
	}
	if err := st.Update("alice", "   ", "new_ct0"); err == nil {
		t.Fatal("expected blank auth token to be rejected")
	}
	if err := st.Update("alice", "new_token", "   "); err == nil {
		t.Fatal("expected blank ct0 to be rejected")
	}
}

func TestUpdateNotFound(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	err := st.Update("nonexistent", "t", "c")
	if err == nil {
		t.Error("expected error updating nonexistent account")
	}
}

func TestSaveAndReload(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")
	st.Add("bob", "token_b", "ct0_b")

	if err := st.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Reload from same path
	st2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("failed to reopen: %v", err)
	}
	if st2.Len() != 2 {
		t.Errorf("expected 2 accounts after reload, got %d", st2.Len())
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "accounts.json")
	st, _ := OpenPath(path)

	st.Add("alice", "token_a", "ct0_a")

	if err := st.Save(); err != nil {
		t.Fatalf("failed to save with nested dir: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected file to be created")
	}
}

func TestEphemeralSaveIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	// Set BIRDY_ACCOUNTS env var without a pre-existing file
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	st, err := OpenPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if st.Len() != 1 {
		t.Fatalf("expected 1 account from env, got %d", st.Len())
	}

	// Save should be a no-op in ephemeral mode
	if err := st.Save(); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// File should not exist
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected no file to be written in ephemeral mode")
	}
}

func TestEnvAccountsMerge(t *testing.T) {
	path := tempStorePath(t)

	// Create a file store first
	st, _ := OpenPath(path)
	st.Add("file_user", "file_token", "file_ct0")
	st.Save()

	// Set env var with overlapping and new accounts
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"file_user","auth_token":"env_token","ct0":"env_ct0"},{"name":"env_only","auth_token":"t","ct0":"c"}]`)

	st2, err := OpenPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if st2.Len() != 2 {
		t.Errorf("expected 2 accounts (merged), got %d", st2.Len())
	}

	// file_user should be overridden by env
	a, _ := st2.Get("file_user")
	if a.AuthToken != "env_token" {
		t.Errorf("expected env_token (overridden), got %q", a.AuthToken)
	}
}

func TestEnvAccountsNormalizeWhitespace(t *testing.T) {
	path := tempStorePath(t)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"  env_user  ","auth_token":"  t  ","ct0":"  c  "}]`)

	st, err := OpenPath(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	a, err := st.Get("env_user")
	if err != nil {
		t.Fatalf("failed to get normalized env account: %v", err)
	}
	if a.Name != "env_user" || a.AuthToken != "t" || a.CT0 != "c" {
		t.Fatalf("expected trimmed env account, got %#v", a)
	}
}

func TestEnvAccountsRejectBlankFields(t *testing.T) {
	path := tempStorePath(t)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"   ","auth_token":"t","ct0":"c"}]`)

	if _, err := OpenPath(path); err == nil {
		t.Fatal("expected invalid env account payload to fail")
	}
}

func TestListReturnsCopy(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)
	st.Add("alice", "t", "c")

	list := st.List()
	list[0].Name = "modified"

	// Original should be unchanged
	a, _ := st.Get("alice")
	if a.Name != "alice" {
		t.Error("List should return a copy, not a reference to internal data")
	}
}

func TestFilePermissions(t *testing.T) {
	path := tempStorePath(t)
	st, _ := OpenPath(path)
	st.Add("alice", "secret_token", "secret_ct0")
	st.Save()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

func TestOpenPathQuarantinesCorruptStore(t *testing.T) {
	path := tempStorePath(t)
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}

	st, err := OpenPath(path)
	if err != nil {
		t.Fatalf("OpenPath returned error: %v", err)
	}
	if st.Len() != 0 {
		t.Fatalf("expected empty recovered store, got %d accounts", st.Len())
	}
	if st.Warning == "" {
		t.Fatal("expected recovery warning")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt original moved away, stat err=%v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "accounts.json.corrupt-") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected quarantined corrupt store file in %s", filepath.Dir(path))
	}
}
