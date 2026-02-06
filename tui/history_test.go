package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveChatHistoryEmpty(t *testing.T) {
	path, err := saveChatHistory(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Error("expected empty path for nil messages")
	}
}

func TestSaveChatHistory(t *testing.T) {
	// Override HOME to use temp dir
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	messages := []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "hi there"},
		{role: "tool", content: "birdy home"},
		{role: "error", content: "something failed"},
	}

	path, err := saveChatHistory(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	content := string(data)

	if !strings.Contains(content, "# birdy chat") {
		t.Error("expected markdown header")
	}
	if !strings.Contains(content, "## You") {
		t.Error("expected user section")
	}
	if !strings.Contains(content, "hello") {
		t.Error("expected user message content")
	}
	if !strings.Contains(content, "## birdy") {
		t.Error("expected assistant section")
	}
	if !strings.Contains(content, "hi there") {
		t.Error("expected assistant message content")
	}
	if !strings.Contains(content, "> `birdy home`") {
		t.Error("expected tool use in blockquote")
	}
	if !strings.Contains(content, "**Error:**") {
		t.Error("expected error section")
	}
}

func TestSaveChatHistoryFilePermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	messages := []chatMessage{{role: "user", content: "test"}}
	path, err := saveChatHistory(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}
}

func TestChatHistoryDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	got, err := chatHistoryDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(dir, ".config", "birdy", "chats")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
