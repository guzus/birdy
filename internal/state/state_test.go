package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathMissingReturnsEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if s == nil {
		t.Fatal("expected state")
	}
	if s.LastUsedName != "" || s.Model != "" {
		t.Fatalf("expected empty state, got %#v", s)
	}
}

func TestLoadPathQuarantinesCorruptState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	s, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if s == nil {
		t.Fatal("expected state")
	}
	if s.Warning == "" {
		t.Fatal("expected recovery warning")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt original to be moved away, stat err=%v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "state.json.corrupt-") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected quarantined corrupt state file in %s", dir)
	}
}

func TestStateSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{
		path:         path,
		LastUsedName: "alice",
		Model:        "codex",
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if loaded.LastUsedName != "alice" || loaded.Model != "codex" {
		t.Fatalf("unexpected loaded state: %#v", loaded)
	}
}

func TestStateSaveNormalizesPersistedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &State{
		path:         path,
		LastUsedName: "  alice  ",
		Model:        "  GPT-5.4-mini  ",
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if loaded.LastUsedName != "alice" {
		t.Fatalf("expected trimmed LastUsedName, got %q", loaded.LastUsedName)
	}
	if loaded.Model != "codex" {
		t.Fatalf("expected normalized model codex, got %q", loaded.Model)
	}
}

func TestLoadPathNormalizesLegacyStateValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := `{"last_used_name":"  alice  ","model":"  OpUs  "}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	loaded, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if loaded.LastUsedName != "alice" {
		t.Fatalf("expected trimmed LastUsedName, got %q", loaded.LastUsedName)
	}
	if loaded.Model != "opus" {
		t.Fatalf("expected normalized model opus, got %q", loaded.Model)
	}
}

func TestLoadPathDropsUnknownModelSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := `{"last_used_name":"alice","model":"unknown-future-model"}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	loaded, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath returned error: %v", err)
	}
	if loaded.Model != "" {
		t.Fatalf("expected unknown model to be dropped, got %q", loaded.Model)
	}
}
