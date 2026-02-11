package tui

import (
	"fmt"
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
