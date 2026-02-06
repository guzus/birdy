package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewMainModel(t *testing.T) {
	m := NewMainModel()
	if m.currentScreen != screenSplash {
		t.Errorf("expected initial screen to be splash, got %d", m.currentScreen)
	}
}

func TestMainModelInit(t *testing.T) {
	m := NewMainModel()
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected Init to return a command (tick)")
	}
}

func TestMainModelWindowSize(t *testing.T) {
	m := NewMainModel()
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	updated, cmd := m.Update(msg)
	result := updated.(MainModel)

	if result.width != 80 || result.height != 24 {
		t.Errorf("expected 80x24, got %dx%d", result.width, result.height)
	}
	if cmd != nil {
		t.Error("expected no command from WindowSizeMsg")
	}
}

func TestMainModelCtrlCQuits(t *testing.T) {
	m := NewMainModel()
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("expected Quit command from ctrl+c")
	}
	// Execute the command and check it produces a quit message
	result := cmd()
	if _, ok := result.(tea.QuitMsg); !ok {
		t.Errorf("expected QuitMsg, got %T", result)
	}
}

func TestSwitchScreenToChat(t *testing.T) {
	m := NewMainModel()
	// Set window size first so chat viewport initializes
	sizeMsg := tea.WindowSizeMsg{Width: 80, Height: 24}
	updated, _ := m.Update(sizeMsg)
	m = updated.(MainModel)

	msg := switchScreenMsg{target: screenChat}
	updated, _ = m.Update(msg)
	result := updated.(MainModel)

	if result.currentScreen != screenChat {
		t.Errorf("expected chat screen, got %d", result.currentScreen)
	}
}

func TestSwitchScreenToAccount(t *testing.T) {
	m := NewMainModel()
	msg := switchScreenMsg{target: screenAccount}
	updated, _ := m.Update(msg)
	result := updated.(MainModel)

	if result.currentScreen != screenAccount {
		t.Errorf("expected account screen, got %d", result.currentScreen)
	}
}

func TestMainModelRoutesToSplash(t *testing.T) {
	m := NewMainModel()
	// Initial screen is splash, so any key should route there
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	updated, cmd := m.Update(msg)
	result := updated.(MainModel)

	// Splash should return a switch command when a key is pressed
	if result.currentScreen != screenSplash {
		t.Errorf("expected to remain on splash, got %d", result.currentScreen)
	}
	if cmd == nil {
		t.Error("expected splash to return a command on keypress")
	}
}
