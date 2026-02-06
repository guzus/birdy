package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewSplashModel(t *testing.T) {
	m := NewSplashModel()
	if m.frame != 0 || m.ticks != 0 {
		t.Error("expected zero-valued splash model")
	}
}

func TestSplashInit(t *testing.T) {
	m := NewSplashModel()
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected Init to return a tick command")
	}
}

func TestSplashTickAdvancesFrame(t *testing.T) {
	m := NewSplashModel()
	m, _ = m.Update(tickMsg{})

	if m.ticks != 1 {
		t.Errorf("expected ticks=1, got %d", m.ticks)
	}
	if m.frame != 1 {
		t.Errorf("expected frame=1, got %d", m.frame)
	}
}

func TestSplashTickWrapsFrame(t *testing.T) {
	m := NewSplashModel()
	// Advance to frame 3
	for i := 0; i < 3; i++ {
		m, _ = m.Update(tickMsg{})
	}
	if m.frame != 3 {
		t.Errorf("expected frame=3, got %d", m.frame)
	}

	// Next tick wraps to frame 0
	m, _ = m.Update(tickMsg{})
	if m.frame != 0 {
		t.Errorf("expected frame=0 after wrap, got %d", m.frame)
	}
}

func TestSplashAutoAdvancesToChat(t *testing.T) {
	m := NewSplashModel()
	var cmd tea.Cmd

	// Advance through 5 ticks
	for i := 0; i < 5; i++ {
		m, cmd = m.Update(tickMsg{})
	}

	if cmd == nil {
		t.Fatal("expected command after 5 ticks")
	}
	msg := cmd()
	switchMsg, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got %T", msg)
	}
	if switchMsg.target != screenChat {
		t.Errorf("expected target=screenChat, got %d", switchMsg.target)
	}
}

func TestSplashSkipOnKeypress(t *testing.T) {
	m := NewSplashModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if cmd == nil {
		t.Fatal("expected command on keypress")
	}
	msg := cmd()
	switchMsg, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got %T", msg)
	}
	if switchMsg.target != screenChat {
		t.Errorf("expected target=screenChat, got %d", switchMsg.target)
	}
}

func TestSplashWindowSize(t *testing.T) {
	m := NewSplashModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})

	if m.width != 100 || m.height != 50 {
		t.Errorf("expected 100x50, got %dx%d", m.width, m.height)
	}
}

func TestSplashViewEmptyBeforeSize(t *testing.T) {
	m := NewSplashModel()
	if m.View() != "" {
		t.Error("expected empty view before window size is set")
	}
}

func TestSplashViewRendersAfterSize(t *testing.T) {
	m := NewSplashModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view after window size")
	}
}
