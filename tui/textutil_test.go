package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncatePromptTextPreservesUTF8(t *testing.T) {
	got := truncatePromptText("안녕하세요🙂 world", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid utf-8, got %q", got)
	}
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestSummarizeQueueNoticeUsesDisplayWidth(t *testing.T) {
	got := summarizeQueueNotice("안녕하세요 세상", 8)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid utf-8, got %q", got)
	}
	if lipgloss.Width(got) > 8 {
		t.Fatalf("expected rendered width <= 8, got %d for %q", lipgloss.Width(got), got)
	}
}

func TestTruncateUTF8BytesPreservesUTF8(t *testing.T) {
	original := "hello 안녕하세요🙂 world"
	got := truncateUTF8Bytes(original, len("hello 안녕"))
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid utf-8, got %q", got)
	}
	if strings.HasSuffix(got, "\ufffd") {
		t.Fatalf("expected no replacement rune, got %q", got)
	}
}
