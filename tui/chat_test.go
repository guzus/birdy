package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	if m.viewport.Width != 77 {
		t.Errorf("expected viewport width 77 with scrollbar reservation, got %d", m.viewport.Width)
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

func TestChatTabQueuesDuringStreaming(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.input.SetValue("next task")

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Error("expected clear-notice timer command when queuing with tab")
	}
	if len(m.queuedPrompts) != 1 || m.queuedPrompts[0] != "next task" {
		t.Fatalf("expected one queued prompt, got %#v", m.queuedPrompts)
	}
	if m.input.Value() != "" {
		t.Errorf("expected input to reset after queueing, got %q", m.input.Value())
	}
	if !contains(m.queueNotice, "queued: next task") {
		t.Fatalf("expected queue notice to be set, got %q", m.queueNotice)
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

func TestChatSlashOpensHistoryModeWhenInputEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chatsDir := filepath.Join(home, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	historyPath := filepath.Join(chatsDir, "2026-02-11_123000.md")
	if err := os.WriteFile(historyPath, []byte("# birdy chat\n\nhello"), 0600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetValue("")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.historyMode {
		t.Fatal("expected slash to open history mode when input is empty")
	}
	if len(m.historyFiles) == 0 {
		t.Fatal("expected history files to be loaded")
	}
	if !contains(m.renderMessages(), "saved at:") {
		t.Fatal("expected history panel content")
	}
}

func TestChatSlashTypesNormallyWhenInputNotEmpty(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetValue("abc")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.historyMode {
		t.Fatal("expected slash not to open history mode when input has text")
	}
	if got := m.input.Value(); got != "abc/" {
		t.Fatalf("expected slash to be typed into input, got %q", got)
	}
}

func TestChatSlashOpensHistoryModeWhileStreaming(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chatsDir := filepath.Join(home, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	historyPath := filepath.Join(chatsDir, "2026-02-11_123000.md")
	if err := os.WriteFile(historyPath, []byte("# birdy chat\n\nhello"), 0600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.input.SetValue("")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.historyMode {
		t.Fatal("expected slash to open history mode while streaming")
	}
}

func TestChatHistoryModeReplacesFeedLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chatsDir := filepath.Join(home, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	historyPath := filepath.Join(chatsDir, "2026-02-11_123000.md")
	if err := os.WriteFile(historyPath, []byte("# birdy chat\n\nhello"), 0600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.historyMode {
		t.Fatal("expected history mode to be active")
	}

	view := m.View()
	if !contains(view, "HISTORY") {
		t.Fatal("expected history label in view")
	}
	if contains(view, "FEED") {
		t.Fatal("expected FEED label to be hidden in history mode")
	}
}

func TestChatHistoryEnterLoadsSelectedChat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chatsDir := filepath.Join(home, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	contents := strings.Join([]string{
		"# birdy chat — 2026-02-11 12:30:00",
		"",
		"## You",
		"",
		"hello",
		"",
		"## birdy",
		"",
		"hi there",
		"",
	}, "\n")
	historyPath := filepath.Join(chatsDir, "2026-02-11_123000.md")
	if err := os.WriteFile(historyPath, []byte(contents), 0600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.historyMode {
		t.Fatal("expected history mode to be active")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.historyMode {
		t.Fatal("expected enter to exit history mode after loading")
	}
	if len(m.messages) < 2 {
		t.Fatalf("expected loaded messages, got %d", len(m.messages))
	}
	if m.messages[0].role != "user" || m.messages[0].content != "hello" {
		t.Fatalf("unexpected first loaded message: %#v", m.messages[0])
	}
	if !contains(m.queueNotice, "loaded:") {
		t.Fatalf("expected loaded notice, got %q", m.queueNotice)
	}
}

func TestHistoryViewShowsGlimpseAndList(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.historyMode = true
	m.historyFiles = []string{
		"/tmp/2026-02-11_120000.md",
		"/tmp/2026-02-11_121500.md",
	}
	m.historyIndex = 1
	m.historyPreview = strings.Repeat("line\n", 80)

	out := m.renderHistoryMessages()
	if !contains(out, "GLIMPSE") {
		t.Fatalf("expected glimpse section, got %q", out)
	}
	if !contains(out, "Press Enter to open full transcript in feed.") {
		t.Fatalf("expected enter hint in glimpse, got %q", out)
	}
	if !contains(out, "  1. 2026-02-11 12:00:00") || !contains(out, ">  2. 2026-02-11 12:15:00") {
		t.Fatalf("expected timestamp list rows, got %q", out)
	}
}

func TestHistoryOpenResetsViewportToTop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chatsDir := filepath.Join(home, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir chats: %v", err)
	}
	historyPath := filepath.Join(chatsDir, "2026-02-11_123000.md")
	if err := os.WriteFile(historyPath, []byte("# birdy chat\n\nhello"), 0600); err != nil {
		t.Fatalf("write history: %v", err)
	}

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 220; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.refreshViewport()
	m.viewport.GotoBottom()
	if m.viewport.YOffset == 0 {
		t.Fatal("expected non-zero offset before opening history")
	}

	m.input.SetValue("")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected history open to reset viewport to top, got %d", m.viewport.YOffset)
	}
}

func TestRenderCommandBarContentHistoryModeShowsLoadHelp(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.historyMode = true
	m.historyFiles = []string{"/tmp/chat.md"}
	m.historyIndex = 0
	content := m.renderCommandBarContent()
	if !contains(content, "enter: open full chat") {
		t.Fatalf("expected load helper text, got %q", content)
	}
	if !contains(content, "/tmp/chat.md") {
		t.Fatalf("expected selected file path, got %q", content)
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

func TestChatSnapshotMsgReplacesAssistantContent(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	ch := make(chan tea.Msg, 10)
	m.streamCh = ch

	m.messages = append(m.messages, chatMessage{role: "assistant", content: "old"})
	m, _ = m.Update(claudeSnapshotMsg{Text: "new full content"})

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].content != "new full content" {
		t.Errorf("expected replacement content, got %q", m.messages[0].content)
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

func TestChatDoneStartsNextQueuedPrompt(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.queuedPrompts = []string{"queued prompt"}

	m, cmd := m.Update(claudeDoneMsg{})
	if cmd == nil {
		t.Fatal("expected command to start next queued prompt")
	}
	if !m.streaming {
		t.Error("expected streaming=true after starting queued prompt")
	}
	if len(m.queuedPrompts) != 0 {
		t.Errorf("expected queue to be empty, got %d items", len(m.queuedPrompts))
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].content != "queued prompt" {
		t.Errorf("expected queued prompt to be appended as user message, got %#v", m.messages)
	}
}

func TestChatEscStartsNextQueuedPrompt(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.cancelStream = func() {}
	m.queuedPrompts = []string{"queued after cancel"}

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected command to start queued prompt after cancel")
	}
	if !m.streaming {
		t.Error("expected streaming=true after queued prompt starts")
	}
	if len(m.queuedPrompts) != 0 {
		t.Errorf("expected queue empty, got %d", len(m.queuedPrompts))
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

func TestChatErrorMsgWhileHistoryModeDoesNotAppendRawError(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.historyMode = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "partial"},
	}

	m, cmd := m.Update(claudeErrorMsg{Err: fmt.Errorf("test error")})
	if cmd == nil {
		t.Fatal("expected clear-notice timer command")
	}
	if m.streaming {
		t.Error("expected streaming=false after error")
	}
	if len(m.messages) != 2 {
		t.Fatalf("expected no new error message while in history mode, got %d messages", len(m.messages))
	}
	if contains(m.renderMessages(), "test error") {
		t.Fatal("expected raw error text hidden while browsing history")
	}
	if !contains(m.queueNotice, "stream failed") {
		t.Fatalf("expected background failure notice, got %q", m.queueNotice)
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
	if !contains(view, "thinking") {
		t.Error("expected 'thinking' in header during streaming")
	}
}

func TestChatHeaderCompactsOnNarrowWidth(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m.streaming = true
	m.accountCount = 12

	view := m.View()
	if !contains(view, "thinking") {
		t.Fatal("expected compact status to still be shown on narrow width")
	}
	if contains(view, "12 accounts | sonnet | thinking") {
		t.Fatal("expected verbose header info to be compacted on narrow width")
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
	if !contains(view, "up/down: scroll") {
		t.Error("expected keyboard scroll hint in footer during streaming")
	}
}

func TestChatFooterShowsSavePathOnBottomLine(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.View()
	if !contains(view, "save: ~/.config/birdy/chats") {
		t.Fatalf("expected save path in footer, got %q", view)
	}
}

func TestChatScrollDisablesAutoFollow(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 220; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.followOutput = true
	m.refreshViewport()
	if !m.viewport.AtBottom() {
		t.Fatal("expected viewport at bottom before scroll")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.followOutput {
		t.Fatal("expected user scroll to disable auto-follow")
	}
}

func TestRefreshViewportPreservesScrollWhenAutoFollowDisabled(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 220; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.followOutput = true
	m.refreshViewport()
	m.viewport.GotoTop()
	m.followOutput = false
	prev := m.viewport.YOffset

	m.messages = append(m.messages, chatMessage{role: "assistant", content: "new line from stream"})
	m.refreshViewport()

	if got := m.viewport.YOffset; got != prev {
		t.Fatalf("expected y-offset preserved when auto-follow is disabled, got %d want %d", got, prev)
	}
}

func TestChatMouseWheelBurstCoalescing(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 260; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.refreshViewport()

	now := time.Unix(1700000000, 0)
	m.nowFn = func() time.Time { return now }
	wheelUp := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}

	before := m.viewport.YOffset
	m, _ = m.Update(wheelUp)
	afterFirst := m.viewport.YOffset
	if afterFirst >= before {
		t.Fatalf("expected first wheel event to scroll up, before=%d after=%d", before, afterFirst)
	}

	m, _ = m.Update(wheelUp)
	afterSecond := m.viewport.YOffset
	if afterSecond != afterFirst {
		t.Fatalf("expected second wheel event in same instant to be coalesced, got %d then %d", afterFirst, afterSecond)
	}

	now = now.Add(8 * time.Millisecond)
	m, _ = m.Update(wheelUp)
	afterThird := m.viewport.YOffset
	if afterThird >= afterSecond {
		t.Fatalf("expected later wheel event to scroll up again, got %d then %d", afterSecond, afterThird)
	}
}

func TestRenderStreamingTailMarkdownThrottlesRerender(t *testing.T) {
	m := NewChatModel()
	m.followOutput = true
	now := time.Unix(1700000000, 0)
	m.nowFn = func() time.Time { return now }

	_ = m.renderStreamingTailMarkdown("hello", 80)
	if got := m.streamTailContent; got != "hello" {
		t.Fatalf("expected initial streaming tail content cached, got %q", got)
	}

	now = now.Add(10 * time.Millisecond)
	_ = m.renderStreamingTailMarkdown("hello world", 80)
	if got := m.streamTailContent; got != "hello" {
		t.Fatalf("expected fast non-structural update to be throttled, got %q", got)
	}

	now = now.Add(130 * time.Millisecond)
	_ = m.renderStreamingTailMarkdown("hello world", 80)
	if got := m.streamTailContent; got != "hello world" {
		t.Fatalf("expected delayed update to rerender, got %q", got)
	}
}

func TestRenderStreamingTailMarkdownSkipsWhileNotFollowing(t *testing.T) {
	m := NewChatModel()
	m.followOutput = true
	now := time.Unix(1700000000, 0)
	m.nowFn = func() time.Time { return now }

	_ = m.renderStreamingTailMarkdown("alpha", 80)
	if got := m.streamTailContent; got != "alpha" {
		t.Fatalf("expected baseline cache, got %q", got)
	}

	m.followOutput = false
	now = now.Add(500 * time.Millisecond)
	_ = m.renderStreamingTailMarkdown("alpha beta gamma", 80)
	if got := m.streamTailContent; got != "alpha" {
		t.Fatalf("expected no rerender while not following output, got %q", got)
	}
}

func TestChatTokenMsgDoesNotRefreshViewportWhenNotFollowing(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 120; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	msgs = append(msgs, chatMessage{role: "assistant", content: "tail"})
	m.messages = msgs
	m.streaming = true
	m.followOutput = true
	m.refreshViewport()
	m.viewport.GotoTop()
	m.followOutput = false
	before := m.viewport.View()

	m, _ = m.Update(claudeTokenMsg{Text: " update"})
	after := m.viewport.View()

	if after != before {
		t.Fatal("expected viewport to stay unchanged while not following output")
	}
}

func TestChatScrollBackToBottomCatchesUpStreamedContent(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 160; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	msgs = append(msgs, chatMessage{role: "assistant", content: "tail"})
	m.messages = msgs
	m.streaming = true
	m.followOutput = true
	m.refreshViewport()
	m.viewport.GotoTop()
	m.followOutput = false

	m, _ = m.Update(claudeTokenMsg{Text: " updated"})
	if m.followOutput {
		t.Fatal("expected not following output while scrolled away")
	}

	for i := 0; i < 2000 && !m.viewport.AtBottom(); i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if !m.viewport.AtBottom() {
		t.Fatal("expected to reach bottom while scrolling down")
	}
	if !m.followOutput {
		t.Fatal("expected followOutput to re-enable at bottom")
	}
	plainView := ansiCsiPattern.ReplaceAllString(m.viewport.View(), "")
	if !contains(plainView, "tail updated") {
		t.Fatalf("expected viewport to catch up with streamed tail content, got %q", plainView)
	}
}

func TestCommandBarQueueLabelResponsive(t *testing.T) {
	m := NewChatModel()
	m.queuedPrompts = []string{"one", "two"}

	if got := m.commandBarQueueLabel(20); !contains(got, "queued: 2") {
		t.Fatalf("expected full queue label, got %q", got)
	}
	if got := m.commandBarQueueLabel(3); !contains(got, "q:2") {
		t.Fatalf("expected compact queue label, got %q", got)
	}
	if got := m.commandBarQueueLabel(2); got != "" {
		t.Fatalf("expected no label when too narrow, got %q", got)
	}
}

func TestRenderCommandBarContentShowsQueueOnTopRow(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.queuedPrompts = []string{"next"}
	m.input.SetValue("hello")

	content := m.renderCommandBarContent()
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least two lines in command bar content, got %d", len(lines))
	}
	if !contains(lines[0], "queued: 1") {
		t.Fatalf("expected queue label in top row, got %q", lines[0])
	}
	if !contains(lines[1], "hello") {
		t.Fatalf("expected input text in second row, got %q", lines[1])
	}
}

func TestRenderCommandBarContentShowsQueueNoticeBriefly(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.queuedPrompts = []string{"next"}
	m.queueNotice = "queued: check replies on that tweet"
	m.input.SetValue("hello")

	content := m.renderCommandBarContent()
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least two lines in command bar content, got %d", len(lines))
	}
	if !contains(lines[0], "queued: check replies on that tweet") {
		t.Fatalf("expected queue notice in top row, got %q", lines[0])
	}
	if !contains(lines[0], "queued: 1") {
		t.Fatalf("expected queue count in top row, got %q", lines[0])
	}
}

func TestClearQueueNoticeMsgClearsOnlyLatestNotice(t *testing.T) {
	m := NewChatModel()
	m.queueNotice = "queued: first"
	m.queueNoticeID = 2

	m, _ = m.Update(clearQueueNoticeMsg{ID: 1})
	if m.queueNotice == "" {
		t.Fatal("expected stale clear message to be ignored")
	}

	m, _ = m.Update(clearQueueNoticeMsg{ID: 2})
	if m.queueNotice != "" {
		t.Fatalf("expected matching clear message to clear notice, got %q", m.queueNotice)
	}
}

func TestRenderCommandBarContentRightAlignsQueueLabel(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.queuedPrompts = []string{"a", "b", "c"}
	m.input.SetValue("x")

	content := m.renderCommandBarContent()
	lines := strings.Split(content, "\n")
	if len(lines) < 1 {
		t.Fatal("expected command bar top row")
	}

	plainTop := ansiCsiPattern.ReplaceAllString(lines[0], "")
	expectedLabel := "queued: 3"
	if !strings.HasSuffix(plainTop, expectedLabel) {
		t.Fatalf("expected top row to end with %q, got %q", expectedLabel, plainTop)
	}

	expectedLeadingSpaces := m.input.Width - len(expectedLabel)
	if expectedLeadingSpaces < 0 {
		expectedLeadingSpaces = 0
	}
	gotLeadingSpaces := len(plainTop) - len(strings.TrimLeft(plainTop, " "))
	if gotLeadingSpaces != expectedLeadingSpaces {
		t.Fatalf("expected %d leading spaces, got %d in %q", expectedLeadingSpaces, gotLeadingSpaces, plainTop)
	}
}

func TestChatQueueCountNotShownInFooter(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.queuedPrompts = []string{"next"}

	view := m.View()
	if contains(view, "  |  queued:") {
		t.Fatal("expected queue count to be removed from footer")
	}
	if !contains(view, "queued: 1") {
		t.Fatal("expected queue count to appear in command bar")
	}
}

func TestChatIgnoresSingleLeakedMouseKey(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetValue("hello")

	leaked := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;25;12M")}
	m, _ = m.Update(leaked)

	if got := m.input.Value(); got != "hello" {
		t.Errorf("expected leaked mouse key to be ignored, got input %q", got)
	}
}

func TestChatLeakedMouseKeyDoesNotResanitizeUnchangedInput(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// Existing value may contain stale escape fragments from prior terminal noise.
	// Leaked mouse events should be swallowed quickly without re-running sanitizer.
	raw := "[43;1Rhello"
	m.input.SetValue(raw)

	leaked := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;25;12M")}
	m, _ = m.Update(leaked)

	if got := m.input.Value(); got != raw {
		t.Errorf("expected leaked mouse key to avoid resanitizing unchanged input, got %q", got)
	}
}

func TestChatIgnoresFragmentedMouseSequence(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	parts := []tea.KeyMsg{
		{Type: tea.KeyRunes, Alt: true, Runes: []rune{'['}},
		{Type: tea.KeyRunes, Runes: []rune("64;20;8")},
		{Type: tea.KeyRunes, Runes: []rune{'M'}},
	}
	for _, p := range parts {
		m, _ = m.Update(p)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if got := m.input.Value(); got != "x" {
		t.Errorf("expected normal input after mouse sequence, got %q", got)
	}
}

func TestChatMouseFragmentModeResetsOnUnexpectedKey(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Start a possible fragmented mouse sequence.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'['}})
	// Unexpected body should cancel fragment mode and be handled normally.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if got := m.input.Value(); got != "x" {
		t.Errorf("expected input to recover after false mouse fragment, got %q", got)
	}
}

func TestSanitizePromptInputRemovesMouseSequences(t *testing.T) {
	in := "hello[<65;18;19M[<65;22;26Mworld"
	got := sanitizePromptInput(in)
	if got != "helloworld" {
		t.Errorf("expected mouse sequences removed, got %q", got)
	}
}

func TestSanitizePromptInputRemovesRGBSequences(t *testing.T) {
	in := "rgb:0000/0000/0000\\11;rgb:0000/0000/0000ok"
	got := sanitizePromptInput(in)
	if got != "ok" {
		t.Errorf("expected rgb sequence removed, got %q", got)
	}
}

func TestSanitizePromptInputRemovesOSCResidueLikeScreenshot(t *testing.T) {
	in := `\;\\\0000/0000/0000\`
	got := sanitizePromptInput(in)
	if got != "" {
		t.Errorf("expected OSC residue removed, got %q", got)
	}
}

func TestSanitizePromptInputRemovesCursorPositionReports(t *testing.T) {
	in := "[43;1R[43;1Rhello"
	got := sanitizePromptInput(in)
	if got != "hello" {
		t.Errorf("expected CPR sequence removed, got %q", got)
	}
}

func TestSanitizePromptInputRemovesBareMouseTriplet(t *testing.T) {
	in := "14;26;10Mhello"
	got := sanitizePromptInput(in)
	if got != "hello" {
		t.Errorf("expected bare mouse triplet removed, got %q", got)
	}
}

func TestChatSpinnerTickRefreshesViewportIndicator(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.messages = []chatMessage{{role: "user", content: "hi"}}
	m.streaming = true
	m.refreshViewport()

	before := m.viewport.View()
	tickMsg := m.spinner.Tick()
	m, _ = m.Update(tickMsg)
	after := m.viewport.View()

	if before == after {
		t.Error("expected viewport indicator to update on spinner tick")
	}
}

func TestRenderMessagesRespectsNarrowWidthAfterResize(t *testing.T) {
	m := NewChatModel()
	m.width = 10
	m.messages = []chatMessage{
		{role: "user", content: "this is a long sentence"},
	}

	out := m.renderMessages()
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if lipgloss.Width(line) > 10 {
			t.Fatalf("line exceeds width after narrow resize: %q", line)
		}
	}
}

func TestFeedScrollbarGeometryIndicatesMiddleScroll(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 320; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.refreshViewport()

	mid := m.viewport.TotalLineCount() / 2
	m.viewport.SetYOffset(mid)
	top, h := m.feedScrollbarGeometry(m.viewport.Height)
	if h <= 0 || h >= m.viewport.Height {
		t.Fatalf("expected partial thumb height, got %d for viewport height %d", h, m.viewport.Height)
	}
	if top <= 0 || top >= m.viewport.Height-1 {
		t.Fatalf("expected thumb to indicate middle area, got top=%d height=%d viewportHeight=%d", top, h, m.viewport.Height)
	}
}

func TestRenderFeedBodyWithScrollbarShowsThumb(t *testing.T) {
	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var msgs []chatMessage
	for i := 0; i < 260; i++ {
		msgs = append(msgs, chatMessage{role: "user", content: fmt.Sprintf("line %d", i)})
	}
	m.messages = msgs
	m.refreshViewport()
	m.viewport.SetYOffset(m.viewport.TotalLineCount() / 3)

	body := ansiCsiPattern.ReplaceAllString(m.renderFeedBodyWithScrollbar(), "")
	if !contains(body, "█") {
		t.Fatalf("expected rendered feed scrollbar thumb, got %q", body)
	}
}

func TestRenderMessagesDoesNotReformatBetweenStreamingAndDone(t *testing.T) {
	m := NewChatModel()
	m.width = 80
	m.messages = []chatMessage{
		{role: "assistant", content: "**streaming** content"},
	}

	m.streaming = true
	streamingOut := m.renderMessages()

	m.streaming = false
	doneOut := m.renderMessages()

	if streamingOut != doneOut {
		t.Fatal("expected identical assistant rendering during stream and after completion")
	}
}

func TestRenderMessagesAssistantRendersMarkdown(t *testing.T) {
	m := NewChatModel()
	m.width = 80
	m.messages = []chatMessage{
		{role: "assistant", content: "Hello **world**"},
	}

	out := m.renderMessages()
	if !contains(out, "world") {
		t.Fatalf("expected rendered output to contain text, got %q", out)
	}
	if contains(out, "**world**") {
		t.Fatalf("expected markdown syntax to be formatted, got %q", out)
	}
}

func TestRenderMessagesPopulatesMarkdownCacheWhenNotStreamingTail(t *testing.T) {
	m := NewChatModel()
	m.width = 80
	m.messages = []chatMessage{
		{role: "assistant", content: "Hello **world**"},
	}

	_ = m.renderMessages()
	if len(m.markdownCache) == 0 {
		t.Fatal("expected markdown cache to be populated")
	}
}

func TestRenderMessagesSkipsMarkdownCacheForStreamingTail(t *testing.T) {
	m := NewChatModel()
	m.width = 80
	m.streaming = true
	m.messages = []chatMessage{
		{role: "assistant", content: "**streaming** tail"},
	}

	_ = m.renderMessages()
	if len(m.markdownCache) != 0 {
		t.Fatalf("expected streaming tail to bypass markdown cache, got %d entries", len(m.markdownCache))
	}
}

func TestRenderMessagesUserLineIsIndented(t *testing.T) {
	m := NewChatModel()
	m.width = 80
	m.messages = []chatMessage{
		{role: "user", content: "did elon postpone mars?"},
	}

	out := m.renderMessages()
	lines := strings.Split(out, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "You: did elon postpone mars?") {
			found = true
			if !strings.HasPrefix(line, "  ") {
				t.Fatalf("expected user line to be indented, got %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("expected to find rendered user line in %q", out)
	}
}

func TestShouldRefreshStreamThrottlesFrequentTokens(t *testing.T) {
	m := NewChatModel()
	m.lastStreamRender = time.Now()

	if m.shouldRefreshStream("a") {
		t.Fatal("expected immediate next token to be throttled")
	}
	if !m.shouldRefreshStream("\n") {
		t.Fatal("expected newline token to force refresh")
	}
}

func TestSanitizeStreamOutputRemovesAnsiAndCarriageReturn(t *testing.T) {
	in := "\x1b[31mhello\rworld\x1b[0m"
	got := sanitizeStreamOutput(in)
	if got != "helloworld" {
		t.Fatalf("expected ansi/carrriage-return removed, got %q", got)
	}
}

func TestSanitizeStreamOutputKeepsReadableWhitespace(t *testing.T) {
	in := "line1\nline2\tok"
	got := sanitizeStreamOutput(in)
	if got != in {
		t.Fatalf("expected whitespace preserved, got %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}
