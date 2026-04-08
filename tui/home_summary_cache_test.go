package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHomeSummaryCacheSaveAndLoadFresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	if err := saveHomeSummaryCache("cached summary", now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("saveHomeSummaryCache: %v", err)
	}

	summary, cachedAt, ok, err := loadHomeSummaryCache(now)
	if err != nil {
		t.Fatalf("loadHomeSummaryCache: %v", err)
	}
	if !ok {
		t.Fatal("expected fresh cache hit")
	}
	if summary != "cached summary" {
		t.Fatalf("expected cached summary, got %q", summary)
	}
	if cachedAt.IsZero() {
		t.Fatal("expected non-zero cachedAt")
	}
}

func TestHomeSummaryCacheExpiresAfterTTL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	if err := saveHomeSummaryCache("old summary", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("saveHomeSummaryCache: %v", err)
	}

	_, _, ok, err := loadHomeSummaryCache(now)
	if err != nil {
		t.Fatalf("loadHomeSummaryCache: %v", err)
	}
	if ok {
		t.Fatal("expected stale cache miss")
	}
}

func TestHomeSummaryCacheQuarantinesCorruptFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	cachePath, err := homeSummaryCachePath()
	if err != nil {
		t.Fatalf("homeSummaryCachePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	_, _, ok, err := loadHomeSummaryCache(now)
	if err == nil {
		t.Fatal("expected corrupt cache recovery warning")
	}
	if ok {
		t.Fatal("expected corrupt cache to return miss")
	}
	if !strings.Contains(err.Error(), "recovered from corrupt home summary cache") {
		t.Fatalf("expected recovery warning, got %v", err)
	}

	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected original corrupt cache to be quarantined, stat err=%v", statErr)
	}
	matches, globErr := filepath.Glob(cachePath + ".corrupt-*")
	if globErr != nil {
		t.Fatalf("glob corrupt cache quarantine: %v", globErr)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one quarantined cache file, got %v", matches)
	}

	_, _, ok, err = loadHomeSummaryCache(now)
	if err != nil {
		t.Fatalf("expected clean miss after quarantine, got %v", err)
	}
	if ok {
		t.Fatal("expected miss after quarantine")
	}
}

func TestAutoQueryUsesCachedHomeSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	if err := saveHomeSummaryCache("cached timeline summary", now.Add(-15*time.Minute)); err != nil {
		t.Fatalf("saveHomeSummaryCache: %v", err)
	}

	m := NewChatModel()
	m.nowFn = func() time.Time { return now }
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.accountCount = 1

	m, cmd := m.Update(autoQueryMsg{})
	if cmd != nil {
		t.Fatal("expected cached auto-query to skip live Claude request")
	}
	if m.streaming {
		t.Fatal("expected streaming=false when using cached summary")
	}
	if len(m.messages) != 3 {
		t.Fatalf("expected 3 messages (user/tool/assistant), got %d", len(m.messages))
	}
	if m.messages[0].role != "user" || m.messages[0].content != homeSummaryPrompt {
		t.Fatalf("unexpected first message: %#v", m.messages[0])
	}
	if m.messages[1].role != "tool" || !strings.Contains(m.messages[1].content, "ttl 1h") {
		t.Fatalf("unexpected cache note message: %#v", m.messages[1])
	}
	if m.messages[2].role != "assistant" || m.messages[2].content != "cached timeline summary" {
		t.Fatalf("unexpected assistant cache message: %#v", m.messages[2])
	}
}

func TestAutoQueryStaleCacheStartsLivePrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	if err := saveHomeSummaryCache("stale summary", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("saveHomeSummaryCache: %v", err)
	}

	m := NewChatModel()
	m.nowFn = func() time.Time { return now }
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.accountCount = 1

	m, cmd := m.Update(autoQueryMsg{})
	if cmd == nil {
		t.Fatal("expected stale cache to trigger live Claude request")
	}
	if !m.streaming {
		t.Fatal("expected streaming=true when cache is stale")
	}
	if !m.cacheHomeSummaryOnDone {
		t.Fatal("expected home summary cache save flag to be set")
	}
	if len(m.messages) != 1 || m.messages[0].role != "user" || m.messages[0].content != homeSummaryPrompt {
		t.Fatalf("unexpected live auto-query messages: %#v", m.messages)
	}
}

func TestClaudeDoneSavesHomeSummaryCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	m := NewChatModel()
	m.nowFn = func() time.Time { return now }
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_ = m.beginPrompt(homeSummaryPrompt)
	m.messages = append(m.messages, chatMessage{role: "assistant", content: "fresh summary from live run"})

	m, _ = m.Update(claudeDoneMsg{})
	if m.cacheHomeSummaryOnDone {
		t.Fatal("expected cache-save flag to be cleared after done")
	}

	summary, _, ok, err := loadHomeSummaryCache(now)
	if err != nil {
		t.Fatalf("loadHomeSummaryCache: %v", err)
	}
	if !ok {
		t.Fatal("expected cached summary to be saved on done")
	}
	if summary != "fresh summary from live run" {
		t.Fatalf("expected saved summary, got %q", summary)
	}
}

func TestAutoQuerySurfacesHomeSummaryCacheLoadFailure(t *testing.T) {
	validHome := t.TempDir()
	t.Setenv("HOME", validHome)
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	m := NewChatModel()
	m.nowFn = func() time.Time { return now }
	m.accountCount = 1

	homeFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeFile, []byte("not-a-directory"), 0600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	t.Setenv("HOME", homeFile)

	m, cmd := m.Update(autoQueryMsg{})
	if cmd == nil {
		t.Fatal("expected live prompt command after cache load failure")
	}
	if !m.streaming {
		t.Fatal("expected streaming=true after cache load failure fallback")
	}
	if !strings.Contains(m.warning, "failed to load home summary cache") {
		t.Fatalf("expected cache load warning, got %q", m.warning)
	}
}

func TestClaudeDoneSurfacesHomeSummaryCacheSaveFailure(t *testing.T) {
	validHome := t.TempDir()
	t.Setenv("HOME", validHome)
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

	m := NewChatModel()
	m.nowFn = func() time.Time { return now }
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_ = m.beginPrompt(homeSummaryPrompt)
	m.messages = append(m.messages, chatMessage{role: "assistant", content: "fresh summary from live run"})

	homeFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeFile, []byte("not-a-directory"), 0600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	t.Setenv("HOME", homeFile)

	m, _ = m.Update(claudeDoneMsg{})
	if !strings.Contains(m.warning, "failed to save home summary cache") {
		t.Fatalf("expected cache save warning, got %q", m.warning)
	}
}
