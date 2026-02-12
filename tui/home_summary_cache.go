package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	homeSummaryPrompt   = "Give me a brief summary of what's on my home timeline."
	homeSummaryCacheTTL = time.Hour
)

type homeSummaryCacheRecord struct {
	Summary  string    `json:"summary"`
	CachedAt time.Time `json:"cached_at"`
}

func homeSummaryCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "birdy", "home_summary_cache.json"), nil
}

func loadHomeSummaryCache(now time.Time) (summary string, cachedAt time.Time, ok bool, err error) {
	path, err := homeSummaryCachePath()
	if err != nil {
		return "", time.Time{}, false, err
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}

	var rec homeSummaryCacheRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", time.Time{}, false, err
	}

	rec.Summary = strings.TrimSpace(rec.Summary)
	if rec.Summary == "" || rec.CachedAt.IsZero() {
		return "", time.Time{}, false, nil
	}

	age := now.Sub(rec.CachedAt)
	if age < 0 {
		age = 0
	}
	if age > homeSummaryCacheTTL {
		return "", time.Time{}, false, nil
	}

	return rec.Summary, rec.CachedAt, true, nil
}

func saveHomeSummaryCache(summary string, now time.Time) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}

	path, err := homeSummaryCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	rec := homeSummaryCacheRecord{Summary: summary, CachedAt: now}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	return nil
}

func formatHomeSummaryAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age < time.Minute {
		return "<1m"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm", int(age/time.Minute))
	}
	h := int(age / time.Hour)
	m := int((age % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}
