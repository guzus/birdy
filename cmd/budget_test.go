package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/store"
)

func storeAccountFixture(rateLimitedAt time.Time) store.Account {
	return store.Account{Name: "x", LastRateLimitedAt: rateLimitedAt}
}

func TestBudgetShowsAccountsWithStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountFixtureFile(t, home, []map[string]any{
		{
			"name":       "cold-account",
			"auth_token": "token-a",
			"ct0":        "ct0-a",
			"use_count":  5,
		},
		{
			"name":                  "hot-account",
			"auth_token":            "token-b",
			"ct0":                   "ct0-b",
			"use_count":             10,
			"last_rate_limited_at":  time.Now().Add(-3 * time.Minute),
			"rate_limit_count":      2,
		},
	})

	var out bytes.Buffer
	budgetCmd.SetOut(&out)
	if err := budgetCmd.RunE(budgetCmd, nil); err != nil {
		t.Fatalf("budget RunE: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "cold-account") || !strings.Contains(s, "hot-account") {
		t.Fatalf("expected both account names in output, got %q", s)
	}
	if !strings.Contains(s, "cold") {
		t.Fatalf("expected 'cold' status in output, got %q", s)
	}
	if !strings.Contains(s, "hot") {
		t.Fatalf("expected 'hot' status in output, got %q", s)
	}
	// Cold accounts should be sorted before hot ones.
	coldIdx := strings.Index(s, "cold-account")
	hotIdx := strings.Index(s, "hot-account")
	if coldIdx < 0 || hotIdx < 0 || coldIdx > hotIdx {
		t.Fatalf("expected cold-account before hot-account in output, got cold@%d hot@%d", coldIdx, hotIdx)
	}
}

func TestBudgetErrorsOnEmptyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No accounts fixture.

	var out bytes.Buffer
	budgetCmd.SetOut(&out)
	err := budgetCmd.RunE(budgetCmd, nil)
	if err == nil {
		t.Fatal("expected error on empty store")
	}
	if !strings.Contains(err.Error(), "no accounts configured") {
		t.Fatalf("expected friendly empty-store error, got %v", err)
	}
}

func TestIsInCooldown(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		t        time.Time
		expected bool
	}{
		{"never", time.Time{}, false},
		{"just-now", now.Add(-1 * time.Second), true},
		{"5min", now.Add(-5 * time.Minute), true},
		{"14m59s", now.Add(-14*time.Minute - 59*time.Second), true},
		{"15min-boundary", now.Add(-15 * time.Minute), false},
		{"20min", now.Add(-20 * time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := storeAccountFixture(tc.t)
			got := isInCooldown(a, now)
			if got != tc.expected {
				t.Errorf("isInCooldown(%v) = %v, want %v", tc.t, got, tc.expected)
			}
		})
	}
}
