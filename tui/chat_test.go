package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewChatModel(t *testing.T) {
	m := NewChatModel()
	if m.streaming {
		t.Error("expected streaming=false initially")
	}
	if m.autoQueried {
		t.Error("expected autoQueried=false initially")
	}
	if len(m.messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(m.messages))
	}
}

func TestChatInit(t *testing.T) {
	m := NewChatModel()
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected Init to return a batch command")
	}
}

func TestChatWindowSize(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if m.width != 80 || m.height != 24 {
		t.Errorf("expected 80x24, got %dx%d", m.width, m.height)
	}
	if !m.ready {
		t.Error("expected ready=true after WindowSizeMsg")
	}
}

func TestChatTabSwitchesToAccounts(t *testing.T) {
	m := NewChatModel()
	// Initialize with window size first
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatal("expected command from tab press")
	}
	msg := cmd()
	switchMsg, ok := msg.(switchScreenMsg)
	if !ok {
		t.Fatalf("expected switchScreenMsg, got %T", msg)
	}
	if switchMsg.target != screenAccount {
		t.Errorf("expected screenAccount, got %d", switchMsg.target)
	}
}

func TestChatTabBlockedDuringStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		// If there's a command, make sure it's NOT a screen switch
		msg := cmd()
		if _, ok := msg.(switchScreenMsg); ok {
			t.Error("should not switch screens during streaming")
		}
	}
}

func TestChatEnterWithEmptyInput(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	prevMsgCount := len(m.messages)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.messages) != prevMsgCount {
		t.Error("enter with empty input should not add messages")
	}
	if m.streaming {
		t.Error("enter with empty input should not start streaming")
	}
}

func TestChatEnterBlockedDuringStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.input.SetValue("test message")

	prevMsgCount := len(m.messages)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.messages) != prevMsgCount {
		t.Error("enter during streaming should not add messages")
	}
}

func TestChatEscCancelsStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Simulate streaming state
	cancelled := false
	m.streaming = true
	m.cancelStream = func() { cancelled = true }

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})

	if m.streaming {
		t.Error("expected streaming=false after esc")
	}
	if !cancelled {
		t.Error("expected cancel function to be called")
	}
	if m.cancelStream != nil {
		t.Error("expected cancelStream=nil after cancel")
	}
	if cmd != nil {
		t.Error("expected nil command after cancel")
	}
	// Should have added a cancelled message
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "cancelled" {
		t.Error("expected 'cancelled' message")
	}
}

func TestChatEscNoOpWhenNotStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	prevMsgCount := len(m.messages)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})

	if len(m.messages) != prevMsgCount {
		t.Error("esc when not streaming should not add messages")
	}
}

func TestChatTokenMsg(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	ch := make(chan tea.Msg, 10)
	m.streamCh = ch

	m, _ = m.Update(claudeTokenMsg{Text: "Hello"})

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].role != "assistant" {
		t.Errorf("expected assistant role, got %q", m.messages[0].role)
	}
	if m.messages[0].content != "Hello" {
		t.Errorf("expected 'Hello', got %q", m.messages[0].content)
	}
}

func TestChatTokenMsgAppendsToExisting(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	ch := make(chan tea.Msg, 10)
	m.streamCh = ch

	m.messages = append(m.messages, chatMessage{role: "assistant", content: "Hello"})
	m, _ = m.Update(claudeTokenMsg{Text: " World"})

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", m.messages[0].content)
	}
}

func TestChatToolUseMsg(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	ch := make(chan tea.Msg, 10)
	m.streamCh = ch

	m, _ = m.Update(claudeToolUseMsg{Command: "birdy home"})

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].role != "tool" {
		t.Errorf("expected tool role, got %q", m.messages[0].role)
	}
	if m.messages[0].content != "birdy home" {
		t.Errorf("expected 'birdy home', got %q", m.messages[0].content)
	}
}

func TestChatDoneMsg(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.cancelStream = func() {}

	m, cmd := m.Update(claudeDoneMsg{})

	if m.streaming {
		t.Error("expected streaming=false after done")
	}
	if m.streamCh != nil {
		t.Error("expected streamCh=nil after done")
	}
	if m.cancelStream != nil {
		t.Error("expected cancelStream=nil after done")
	}
	if cmd != nil {
		t.Error("expected nil command after done")
	}
}

func TestChatErrorMsg(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true

	m, _ = m.Update(claudeErrorMsg{Err: fmt.Errorf("test error")})

	if m.streaming {
		t.Error("expected streaming=false after error")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].role != "error" {
		t.Errorf("expected error role, got %q", m.messages[0].role)
	}
}

func TestChatRenderMessagesEmpty(t *testing.T) {
	m := NewChatModel()
	content := m.renderMessages()
	if content == "" {
		t.Error("expected non-empty placeholder text")
	}
}

func TestChatRenderMessagesWithContent(t *testing.T) {
	m := NewChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "hi there"},
		{role: "tool", content: "birdy home"},
		{role: "error", content: "something failed"},
	}
	content := m.renderMessages()
	if content == "" {
		t.Error("expected non-empty rendered messages")
	}
}

func TestChatViewEmptyBeforeReady(t *testing.T) {
	m := NewChatModel()
	if m.View() != "" {
		t.Error("expected empty view before ready")
	}
}

func TestChatViewRendersAfterSize(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view after window size")
	}
}

func TestChatHeaderShowsAccountCount(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.accountCount = 3

	view := m.View()
	if !contains(view, "3 accounts") {
		t.Error("expected '3 accounts' in header")
	}
}

func TestChatHeaderShowsSingularAccount(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.accountCount = 1

	view := m.View()
	if !contains(view, "1 account") {
		t.Error("expected '1 account' in header")
	}
}

func TestChatHeaderShowsStreamingStatus(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true

	view := m.View()
	if !contains(view, "streaming") {
		t.Error("expected 'streaming' in header during streaming")
	}
}

func TestChatFooterShowsEscDuringStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true

	view := m.View()
	if !contains(view, "esc: cancel") {
		t.Error("expected 'esc: cancel' in footer during streaming")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}
