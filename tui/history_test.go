package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestChatHistoryDisplayDirUsesHomeShortcut(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	got := chatHistoryDisplayDir()
	if got != "~/.config/birdy/chats" {
		t.Fatalf("expected compact path, got %q", got)
	}
}

func TestListChatHistoryFilesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	chatsDir := filepath.Join(dir, ".config", "birdy", "chats")
	if err := os.MkdirAll(chatsDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldPath := filepath.Join(chatsDir, "old.md")
	newPath := filepath.Join(chatsDir, "new.md")
	if err := os.WriteFile(oldPath, []byte("old"), 0600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	// Ensure newer mtime ordering.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(newPath, []byte("new"), 0600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	files, err := listChatHistoryFiles(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0] != newPath {
		t.Fatalf("expected newest file first, got %q then %q", files[0], files[1])
	}
}

func TestLoadChatHistoryPreviewTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 100)), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadChatHistoryPreview(path, 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "(truncated)") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestLoadChatHistoryMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.md")
	content := strings.Join([]string{
		"# birdy chat — 2026-02-11 12:30:00",
		"",
		"## You",
		"",
		"what happened?",
		"",
		"## birdy",
		"",
		"here is a summary",
		"",
		"> `birdy home`",
		"",
		"**Error:** timeout",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgs, err := loadChatHistoryMessages(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].role != "user" || msgs[0].content != "what happened?" {
		t.Fatalf("unexpected user message: %#v", msgs[0])
	}
	if msgs[1].role != "assistant" || msgs[1].content != "here is a summary" {
		t.Fatalf("unexpected assistant message: %#v", msgs[1])
	}
	if msgs[2].role != "tool" || msgs[2].content != "birdy home" {
		t.Fatalf("unexpected tool message: %#v", msgs[2])
	}
	if msgs[3].role != "error" || msgs[3].content != "timeout" {
		t.Fatalf("unexpected error message: %#v", msgs[3])
	}
}

func TestChatHistoryFileLabelFromTimestamp(t *testing.T) {
	got := chatHistoryFileLabel("/tmp/2026-02-11_123000.md")
	if got != "2026-02-11 12:30:00" {
		t.Fatalf("unexpected label: %q", got)
	}
}
