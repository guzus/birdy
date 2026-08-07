package rotation

import (
	"testing"
	"time"

	"github.com/guzus/birdy/internal/store"
	"strings"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input string
		want  Strategy
		err   bool
	}{
		{"round-robin", RoundRobin, false},
		{"least-recently-used", LeastRecentlyUsed, false},
		{"least-used", LeastUsed, false},
		{"random", Random, false},
		{"quota-aware", QuotaAware, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseStrategy(tt.input)
			if tt.err && err == nil {
				t.Error("expected error")
			}
			if !tt.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickEmptyAccounts(t *testing.T) {
	_, err := Pick(nil, RoundRobin, "")
	if err == nil {
		t.Error("expected error for empty accounts")
	}
}

func TestPickRoundRobinFirstCall(t *testing.T) {
	accounts := []store.Account{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	a, err := Pick(accounts, RoundRobin, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name != "a" {
		t.Errorf("expected 'a', got %q", a.Name)
	}
}

func TestPickRoundRobinCycles(t *testing.T) {
	accounts := []store.Account{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	a, _ := Pick(accounts, RoundRobin, "a")
	if a.Name != "b" {
		t.Errorf("expected 'b', got %q", a.Name)
	}

	a, _ = Pick(accounts, RoundRobin, "b")
	if a.Name != "c" {
		t.Errorf("expected 'c', got %q", a.Name)
	}

	// Wraps around
	a, _ = Pick(accounts, RoundRobin, "c")
	if a.Name != "a" {
		t.Errorf("expected 'a' (wrapped), got %q", a.Name)
	}
}

func TestPickRoundRobinUnknownLast(t *testing.T) {
	accounts := []store.Account{{Name: "a"}, {Name: "b"}}
	a, _ := Pick(accounts, RoundRobin, "nonexistent")
	if a.Name != "a" {
		t.Errorf("expected 'a' (fallback), got %q", a.Name)
	}
}

func TestPickLeastRecentlyUsed(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		{Name: "recent", LastUsed: now},
		{Name: "old", LastUsed: now.Add(-time.Hour)},
		{Name: "never"},
	}

	a, _ := Pick(accounts, LeastRecentlyUsed, "")
	if a.Name != "never" {
		t.Errorf("expected 'never' (never used first), got %q", a.Name)
	}
}

func TestPickLeastRecentlyUsedAllUsed(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		{Name: "recent", LastUsed: now},
		{Name: "old", LastUsed: now.Add(-time.Hour)},
		{Name: "oldest", LastUsed: now.Add(-2 * time.Hour)},
	}

	a, _ := Pick(accounts, LeastRecentlyUsed, "")
	if a.Name != "oldest" {
		t.Errorf("expected 'oldest', got %q", a.Name)
	}
}

func TestPickLeastUsed(t *testing.T) {
	accounts := []store.Account{
		{Name: "heavy", UseCount: 100},
		{Name: "light", UseCount: 1},
		{Name: "fresh", UseCount: 0},
	}

	a, _ := Pick(accounts, LeastUsed, "")
	if a.Name != "fresh" {
		t.Errorf("expected 'fresh' (0 uses), got %q", a.Name)
	}
}

func TestPickRandom(t *testing.T) {
	accounts := []store.Account{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	// Just verify it doesn't error and returns a valid account
	a, err := Pick(accounts, Random, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, acc := range accounts {
		if acc.Name == a.Name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("random returned unexpected account: %q", a.Name)
	}
}

func TestPickSingleAccount(t *testing.T) {
	accounts := []store.Account{{Name: "only"}}

	strategies := []Strategy{RoundRobin, LeastRecentlyUsed, LeastUsed, Random, QuotaAware}
	for _, s := range strategies {
		t.Run(string(s), func(t *testing.T) {
			a, err := Pick(accounts, s, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.Name != "only" {
				t.Errorf("expected 'only', got %q", a.Name)
			}
		})
	}
}

func TestPickQuotaAwarePrefersColdAccounts(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		{Name: "hot", UseCount: 1, LastRateLimitedAt: now.Add(-2 * time.Minute)},
		{Name: "cold-heavy", UseCount: 100},
		{Name: "cold-light", UseCount: 5},
	}
	a, err := Pick(accounts, QuotaAware, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name != "cold-light" {
		t.Errorf("expected 'cold-light' (least used among cold), got %q", a.Name)
	}
}

func TestPickQuotaAwareIgnoresOldRateLimit(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		// 20 minutes ago is past the 15-min cooldown — treat as cold.
		{Name: "long-cooled", UseCount: 1, LastRateLimitedAt: now.Add(-20 * time.Minute)},
		{Name: "heavy", UseCount: 100},
	}
	a, _ := Pick(accounts, QuotaAware, "")
	if a.Name != "long-cooled" {
		t.Errorf("expected 'long-cooled' (cooldown expired), got %q", a.Name)
	}
}

func TestPickQuotaAwareAllHotPicksEarliestExpiry(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		// Every account is still in cooldown; pick the one that hit 429 longest ago.
		{Name: "newest-429", LastRateLimitedAt: now.Add(-1 * time.Minute)},
		{Name: "oldest-429", LastRateLimitedAt: now.Add(-14 * time.Minute)},
		{Name: "mid-429", LastRateLimitedAt: now.Add(-5 * time.Minute)},
	}
	a, _ := Pick(accounts, QuotaAware, "")
	if a.Name != "oldest-429" {
		t.Errorf("expected 'oldest-429' (closest to ready), got %q", a.Name)
	}
}

func TestPickQuotaAwareNeverHitFallsBackToLeastUsed(t *testing.T) {
	accounts := []store.Account{
		{Name: "heavy", UseCount: 50},
		{Name: "light", UseCount: 3},
		{Name: "untouched", UseCount: 0},
	}
	a, _ := Pick(accounts, QuotaAware, "")
	if a.Name != "untouched" {
		t.Errorf("expected 'untouched' (least used, all cold), got %q", a.Name)
	}
}

// A disabled account is out of rotation but not gone. Filtering happens in
// Pick so no strategy can reach one by accident.
func TestPickSkipsDisabledAccounts(t *testing.T) {
	accounts := []store.Account{
		{Name: "hot", Disabled: true},
		{Name: "cold"},
	}
	for _, strategy := range []Strategy{RoundRobin, LeastRecentlyUsed, LeastUsed, Random} {
		for i := 0; i < 10; i++ {
			got, err := Pick(accounts, strategy, "cold")
			if err != nil {
				t.Fatalf("%s: %v", strategy, err)
			}
			if got.Name == "hot" {
				t.Fatalf("%s picked a disabled account", strategy)
			}
		}
	}
}

func TestPickReportsWhenEveryAccountIsDisabled(t *testing.T) {
	_, err := Pick([]store.Account{{Name: "a", Disabled: true}}, RoundRobin, "")
	if err == nil {
		t.Fatal("expected an error when nothing is left in rotation")
	}
	// The message has to say how to undo it; "no accounts available" would
	// send someone looking for a missing account file.
	if !strings.Contains(err.Error(), "account enable") {
		t.Errorf("error should name the fix, got %v", err)
	}
}
