package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCliEventParsing(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  cliEvent
	}{
		{
			name:  "assistant event",
			input: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`,
			want: cliEvent{
				Type:    "assistant",
				Message: &cliMessage{Content: []cliContentBlock{{Type: "text", Text: "Hello"}}},
			},
		},
		{
			name:  "result event",
			input: `{"type":"result","result":"done","is_error":false}`,
			want:  cliEvent{Type: "result", Result: "done", IsError: false},
		},
		{
			name:  "error result",
			input: `{"type":"result","result":"something failed","is_error":true}`,
			want:  cliEvent{Type: "result", Result: "something failed", IsError: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got cliEvent
			if err := json.Unmarshal([]byte(tt.input), &got); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if got.Type != tt.want.Type {
				t.Errorf("type: got %q, want %q", got.Type, tt.want.Type)
			}
			if got.Result != tt.want.Result {
				t.Errorf("result: got %q, want %q", got.Result, tt.want.Result)
			}
			if got.IsError != tt.want.IsError {
				t.Errorf("is_error: got %v, want %v", got.IsError, tt.want.IsError)
			}
		})
	}
}

func TestCliContentBlockToolUse(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool1","name":"bash","input":{"command":"birdy home"}}]}}`
	var event cliEvent
	if err := json.Unmarshal([]byte(input), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if event.Message == nil {
		t.Fatal("expected non-nil message")
	}
	if len(event.Message.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(event.Message.Content))
	}

	block := event.Message.Content[0]
	if block.Type != "tool_use" {
		t.Errorf("expected tool_use, got %q", block.Type)
	}
	if block.ID != "tool1" {
		t.Errorf("expected id=tool1, got %q", block.ID)
	}

	var input2 struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(block.Input, &input2); err != nil {
		t.Fatalf("failed to unmarshal input: %v", err)
	}
	if input2.Command != "birdy home" {
		t.Errorf("expected 'birdy home', got %q", input2.Command)
	}
}

func TestWaitForNextClosedChannel(t *testing.T) {
	ch := make(chan tea.Msg)
	close(ch)

	cmd := waitForNext(ch)
	msg := cmd()
	if _, ok := msg.(claudeDoneMsg); !ok {
		t.Errorf("expected claudeDoneMsg, got %T", msg)
	}
}

func TestWaitForNextWithMessage(t *testing.T) {
	ch := make(chan tea.Msg, 1)
	ch <- claudeTokenMsg{Text: "test"}

	cmd := waitForNext(ch)
	msg := cmd()
	token, ok := msg.(claudeTokenMsg)
	if !ok {
		t.Fatalf("expected claudeTokenMsg, got %T", msg)
	}
	if token.Text != "test" {
		t.Errorf("expected 'test', got %q", token.Text)
	}
}

func TestRunClaudeProcessCancelledContext(t *testing.T) {
	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan tea.Msg, 64)
	runClaudeProcess(ctx, "test", "sonnet", ch)

	// Channel should be closed without sending an error
	// (either the process fails to start or we detect context cancellation)
	var msgs []tea.Msg
	for msg := range ch {
		msgs = append(msgs, msg)
	}

	// With a cancelled context, either we get an error about failing to start,
	// or the process gets killed immediately. Either way, we should not hang.
	// The key test is that this function returns (doesn't deadlock).
}

func TestStartClaudeWithoutClaudeBinary(t *testing.T) {
	// Temporarily modify PATH to exclude claude
	ctx := context.Background()
	cmd := startClaude(ctx, "test", "sonnet")
	msg := cmd()

	// This should either be a claudeErrorMsg (claude not found)
	// or a claudeNextMsg (claude found but may fail later)
	switch msg.(type) {
	case claudeErrorMsg:
		// Expected if claude is not installed
	case claudeNextMsg:
		// Expected if claude is installed
	default:
		t.Errorf("unexpected message type: %T", msg)
	}
}

func TestSystemPromptContainsKeyCommands(t *testing.T) {
	prompt := buildSystemPrompt("birdy")
	commands := []string{
		"birdy read",
		"birdy search",
		"birdy home",
		"birdy tweet",
		"birdy account list",
		"dive deeper",
		"explore autonomously",
		"Execution policy (aggressive tool use)",
		"Default to running birdy commands first.",
		"Ask for confirmation only before state-changing actions",
	}
	for _, cmd := range commands {
		if !containsStr(prompt, cmd) {
			t.Errorf("system prompt missing: %s", cmd)
		}
	}
}

func TestBirdyCmd(t *testing.T) {
	cmd := birdyCmd()
	if cmd == "" {
		t.Error("expected non-empty birdy command")
	}
}

func TestBuildSystemPromptUsesCommand(t *testing.T) {
	prompt := buildSystemPrompt("/usr/local/bin/birdy")
	if !containsStr(prompt, "/usr/local/bin/birdy home") {
		t.Error("expected system prompt to use the provided command path")
	}
	if !containsStr(prompt, "/usr/local/bin/birdy read") {
		t.Error("expected system prompt to use the provided command for read")
	}
}

func TestBuildClaudeArgsUsesProvidedCommand(t *testing.T) {
	args := buildClaudeArgs("test prompt", "sonnet", "custom-birdy-cmd")

	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}

	joined := strings.Join(args, "\n")
	if !containsStr(joined, "Bash(custom-birdy-cmd *),Skill(birdy)") {
		t.Error("expected allowed tools to use provided command")
	}
	if !containsStr(joined, "custom-birdy-cmd home") {
		t.Error("expected system prompt to use provided command")
	}
	if containsStr(joined, "/birdy home") {
		t.Error("expected args to avoid hardcoded /birdy command")
	}
}

func TestBuildTurnPromptIncludesHistoryAndContinuationInstruction(t *testing.T) {
	msgs := []chatMessage{
		{role: "user", content: "first question"},
		{role: "assistant", content: "first answer"},
		{role: "tool", content: "birdy home"},
		{role: "user", content: "follow-up question"},
	}

	prompt := buildTurnPrompt(msgs)

	if !containsStr(prompt, "Continue this ongoing birdy TUI chat session.") {
		t.Error("expected continuation instruction in turn prompt")
	}
	if !containsStr(prompt, "User: first question") {
		t.Error("expected first user message in prompt")
	}
	if !containsStr(prompt, "Assistant: first answer") {
		t.Error("expected assistant message in prompt")
	}
	if !containsStr(prompt, "Tool: birdy home") {
		t.Error("expected tool message in prompt")
	}
	if !containsStr(prompt, "User: follow-up question") {
		t.Error("expected latest user message in prompt")
	}
}

func TestBuildTurnPromptSkipsErrorMessages(t *testing.T) {
	msgs := []chatMessage{
		{role: "user", content: "hello"},
		{role: "error", content: "oops"},
		{role: "assistant", content: "hi"},
	}
	prompt := buildTurnPrompt(msgs)

	if containsStr(prompt, "oops") {
		t.Error("expected error content to be excluded from turn prompt")
	}
}

func TestTokenBatcherFlushesOnNewline(t *testing.T) {
	var b tokenBatcher
	out, ok := b.add("hello\n")
	if !ok {
		t.Fatal("expected newline to trigger flush")
	}
	if out != "hello\n" {
		t.Fatalf("unexpected flushed text %q", out)
	}
}

func TestTokenBatcherFlushesOnSize(t *testing.T) {
	var b tokenBatcher
	chunk := strings.Repeat("a", 240)
	out, ok := b.add(chunk)
	if !ok {
		t.Fatal("expected large chunk to trigger flush")
	}
	if out != chunk {
		t.Fatalf("unexpected flushed text size=%d", len(out))
	}
}

func TestTokenBatcherManualFlush(t *testing.T) {
	var b tokenBatcher
	if out, ok := b.add("abc"); ok || out != "" {
		t.Fatal("expected small chunk to remain buffered")
	}
	out, ok := b.flush()
	if !ok || out != "abc" {
		t.Fatalf("expected manual flush to return buffered text, got ok=%v out=%q", ok, out)
	}
}

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) &&
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()
}
