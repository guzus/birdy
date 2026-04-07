package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildCodexArgsUsesProvidedCommandAndModel(t *testing.T) {
	args := buildCodexArgs("continue the chat", "gpt-5.4-mini", "custom-birdy-cmd")

	joined := strings.Join(args, "\n")
	if !containsStr(joined, "exec") {
		t.Fatal("expected codex exec invocation")
	}
	if !containsStr(joined, "--json") {
		t.Fatal("expected json output mode")
	}
	if !containsStr(joined, "--skip-git-repo-check") {
		t.Fatal("expected git repo check to be disabled")
	}
	if !containsStr(joined, "custom-birdy-cmd home") {
		t.Fatal("expected birdy command path in prompt")
	}
	if !containsStr(joined, "continue the chat") {
		t.Fatal("expected turn prompt in codex prompt")
	}
}

func TestModelSelectionCyclesThroughCodex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := NewChatModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got := m.model; got != "opus" {
		t.Fatalf("expected opus, got %q", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got := m.model; got != "haiku" {
		t.Fatalf("expected haiku, got %q", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got := m.model; got != "codex" {
		t.Fatalf("expected codex, got %q", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if got := m.model; got != "sonnet" {
		t.Fatalf("expected wrap back to sonnet, got %q", got)
	}
}

func TestRunCodexProcessEmitsCommandAndSnapshots(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "codex")
	content := strings.Join([]string{
		"#!/bin/sh",
		"cat <<'EOF'",
		"{\"type\":\"thread.started\",\"thread_id\":\"t1\"}",
		"{\"type\":\"turn.started\"}",
		"{\"type\":\"item.started\",\"item\":{\"id\":\"cmd1\",\"type\":\"command_execution\",\"command\":\"birdy home\",\"status\":\"in_progress\"}}",
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"msg1\",\"type\":\"agent_message\",\"text\":\"first\"}}",
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"msg2\",\"type\":\"agent_message\",\"text\":\"second\"}}",
		"{\"type\":\"turn.completed\"}",
		"EOF",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ch := make(chan tea.Msg, 16)
	runCodexProcess(context.Background(), "test prompt", "gpt-5.4-mini", ch)

	var msgs []tea.Msg
	for msg := range ch {
		msgs = append(msgs, msg)
	}

	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	toolMsg, ok := msgs[0].(claudeToolUseMsg)
	if !ok || toolMsg.Command != "birdy home" {
		t.Fatalf("expected birdy home tool message, got %#v", msgs[0])
	}

	first, ok := msgs[1].(claudeSnapshotMsg)
	if !ok || first.Text != "first" {
		t.Fatalf("expected first snapshot, got %#v", msgs[1])
	}

	second, ok := msgs[2].(claudeSnapshotMsg)
	if !ok || second.Text != "first\n\nsecond" {
		t.Fatalf("expected cumulative snapshot, got %#v", msgs[2])
	}
}

func TestRunCodexProcessReportsErrors(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "codex")
	content := strings.Join([]string{
		"#!/bin/sh",
		"cat <<'EOF'",
		"{\"type\":\"error\",\"message\":\"boom\"}",
		"EOF",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ch := make(chan tea.Msg, 4)
	runCodexProcess(context.Background(), "test prompt", "gpt-5.4-mini", ch)

	var msgs []tea.Msg
	for msg := range ch {
		msgs = append(msgs, msg)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected one error message, got %d", len(msgs))
	}
	errMsg, ok := msgs[0].(claudeErrorMsg)
	if !ok {
		t.Fatalf("expected claudeErrorMsg, got %T", msgs[0])
	}
	if errMsg.Err == nil || errMsg.Err.Error() != "boom" {
		t.Fatalf("unexpected error payload: %#v", errMsg.Err)
	}
}
