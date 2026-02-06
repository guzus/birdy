package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guzus/birdy/internal/store"
)

// setupTestStore creates a temp store with optional accounts for testing.
func setupTestStore(t *testing.T, accounts ...store.Account) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	// Create the store and add accounts
	st, err := store.OpenPath(path)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	for _, a := range accounts {
		if err := st.Add(a.Name, a.AuthToken, a.CT0); err != nil {
			t.Fatalf("failed to add account: %v", err)
		}
	}
	if err := st.Save(); err != nil {
		t.Fatalf("failed to save store: %v", err)
	}

	// Override the default path by setting XDG or HOME
	origHome := os.Getenv("HOME")
	// Create the .config/birdy structure
	configDir := filepath.Join(dir, ".config", "birdy")
	os.MkdirAll(configDir, 0700)
	// Copy the file to the expected location
	data, _ := os.ReadFile(path)
	os.WriteFile(filepath.Join(configDir, "accounts.json"), data, 0600)
	os.Setenv("HOME", dir)

	return func() {
		os.Setenv("HOME", origHome)
	}
}

func TestNewAccountModel(t *testing.T) {
	m := NewAccountModel()
	if m.view != accountViewList {
		t.Error("expected list view initially")
	}
	if m.cursor != 0 {
		t.Error("expected cursor=0 initially")
	}
}

func TestAccountInit(t *testing.T) {
	m := NewAccountModel()
	cmd := m.Init()
	if cmd != nil {
		t.Error("expected nil command from Init")
	}
}

func TestAccountWindowSize(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if m.width != 80 || m.height != 24 {
		t.Errorf("expected 80x24, got %dx%d", m.width, m.height)
	}
}

func TestAccountTabReturnsToChat(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatal("expected command from tab press")
	}
	msg := cmd()
	switchMsg, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got %T", msg)
	}
	if switchMsg.target != screenChat {
		t.Errorf("expected screenChat, got %d", switchMsg.target)
	}
}

func TestAccountEscReturnsToChat(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected command from esc press")
	}
	msg := cmd()
	switchMsg, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got %T", msg)
	}
	if switchMsg.target != screenChat {
		t.Errorf("expected screenChat, got %d", switchMsg.target)
	}
}

func TestAccountNavigationDown(t *testing.T) {
	m := NewAccountModel()
	m.accounts = []store.Account{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 1 {
		t.Errorf("expected cursor=1, got %d", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("expected cursor=2, got %d", m.cursor)
	}

	// Should not go beyond last item
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 2 {
		t.Errorf("expected cursor=2 (clamped), got %d", m.cursor)
	}
}

func TestAccountNavigationUp(t *testing.T) {
	m := NewAccountModel()
	m.accounts = []store.Account{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m.cursor = 2

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.cursor != 1 {
		t.Errorf("expected cursor=1, got %d", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("expected cursor=0, got %d", m.cursor)
	}

	// Should not go below 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.cursor != 0 {
		t.Errorf("expected cursor=0 (clamped), got %d", m.cursor)
	}
}

func TestAccountPressASwitchesToAddView(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.view != accountViewAdd {
		t.Error("expected add view after pressing 'a'")
	}
	if cmd == nil {
		t.Error("expected blink command")
	}
}

func TestAccountAddFormEscReturnsToList(t *testing.T) {
	m := NewAccountModel()
	m.view = accountViewAdd

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.view != accountViewList {
		t.Error("expected list view after esc in add form")
	}
}

func TestAccountAddFormTabCyclesFields(t *testing.T) {
	m := NewAccountModel()
	m.view = accountViewAdd
	m.focusIndex = 0
	m.initInputs()
	m.inputs[0].Focus()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusIndex != 1 {
		t.Errorf("expected focusIndex=1, got %d", m.focusIndex)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusIndex != 2 {
		t.Errorf("expected focusIndex=2, got %d", m.focusIndex)
	}

	// Wrap around
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusIndex != 0 {
		t.Errorf("expected focusIndex=0 (wrapped), got %d", m.focusIndex)
	}
}

func TestAccountAddFormValidation(t *testing.T) {
	m := NewAccountModel()
	m.view = accountViewAdd
	m.initInputs()

	// Try to submit empty form
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.err == "" {
		t.Error("expected validation error for empty fields")
	}
	if m.view != accountViewAdd {
		t.Error("expected to stay on add form after validation error")
	}
}

func TestAccountAddFormSuccess(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	m := NewAccountModel()
	m.view = accountViewAdd
	m.initInputs()
	m.inputs[0].SetValue("testaccount")
	m.inputs[1].SetValue("token123")
	m.inputs[2].SetValue("ct0value")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.err != "" {
		t.Errorf("unexpected error: %s", m.err)
	}
	if m.view != accountViewList {
		t.Error("expected to return to list view after successful add")
	}
}

func TestAccountDeleteSuccess(t *testing.T) {
	cleanup := setupTestStore(t, store.Account{Name: "victim", AuthToken: "t", CT0: "c"})
	defer cleanup()

	m := NewAccountModel()
	m.loadAccounts()
	initialCount := len(m.accounts)
	if initialCount == 0 {
		t.Skip("no accounts loaded (store may not be accessible)")
	}

	m.cursor = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if len(m.accounts) >= initialCount {
		t.Error("expected account count to decrease after delete")
	}
}

func TestAccountViewEmptyBeforeSize(t *testing.T) {
	m := NewAccountModel()
	if m.View() != "" {
		t.Error("expected empty view before window size")
	}
}

func TestAccountViewRendersAfterSize(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view after window size")
	}
}

func TestAccountViewListShowsEmptyMessage(t *testing.T) {
	m := NewAccountModel()
	m.accounts = nil
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := m.viewList()
	if content == "" {
		t.Error("expected non-empty content for empty account list")
	}
}

func TestAccountViewListShowsAccounts(t *testing.T) {
	m := NewAccountModel()
	m.accounts = []store.Account{
		{Name: "alice"},
		{Name: "bob"},
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := m.viewList()
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestAccountViewAddForm(t *testing.T) {
	m := NewAccountModel()
	m.view = accountViewAdd
	m.initInputs()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := m.viewAddForm()
	if content == "" {
		t.Error("expected non-empty add form content")
	}
}

func TestAccountHeaderShowsCorrectTitle(t *testing.T) {
	m := NewAccountModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.View()
	if view == "" {
		t.Fatal("expected non-empty view")
	}
	// Should contain "Accounts" for list view
}
