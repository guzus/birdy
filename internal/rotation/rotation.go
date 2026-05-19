package rotation

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/guzus/birdy/internal/store"
)

// Strategy defines how the next account is selected.
type Strategy string

const (
	RoundRobin        Strategy = "round-robin"
	LeastRecentlyUsed Strategy = "least-recently-used"
	LeastUsed         Strategy = "least-used"
	Random            Strategy = "random"
	QuotaAware        Strategy = "quota-aware"
)

// QuotaCooldown is the window after a 429 during which an account is
// considered "hot" and avoided by the quota-aware strategy. X's per-auth
// rate limit on most endpoints resets on a 15-minute sliding window.
const QuotaCooldown = 15 * time.Minute

// ParseStrategy converts a string to a Strategy.
func ParseStrategy(s string) (Strategy, error) {
	switch Strategy(s) {
	case RoundRobin, LeastRecentlyUsed, LeastUsed, Random, QuotaAware:
		return Strategy(s), nil
	default:
		return "", fmt.Errorf("unknown strategy %q (valid: round-robin, least-recently-used, least-used, random, quota-aware)", s)
	}
}

// Pick selects the next account from the list according to the strategy.
// lastUsedName is the name of the account used in the previous call (for round-robin).
func Pick(accounts []store.Account, strategy Strategy, lastUsedName string) (*store.Account, error) {
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts available")
	}

	switch strategy {
	case RoundRobin:
		return pickRoundRobin(accounts, lastUsedName)
	case LeastRecentlyUsed:
		return pickLeastRecentlyUsed(accounts)
	case LeastUsed:
		return pickLeastUsed(accounts)
	case Random:
		return pickRandom(accounts)
	case QuotaAware:
		return pickQuotaAware(accounts, time.Now())
	default:
		return nil, fmt.Errorf("unknown strategy %q", strategy)
	}
}

func pickRoundRobin(accounts []store.Account, lastUsedName string) (*store.Account, error) {
	if lastUsedName == "" {
		return &accounts[0], nil
	}

	for i, a := range accounts {
		if a.Name == lastUsedName {
			next := (i + 1) % len(accounts)
			return &accounts[next], nil
		}
	}
	// last used account not found, start from beginning
	return &accounts[0], nil
}

func pickLeastRecentlyUsed(accounts []store.Account) (*store.Account, error) {
	sorted := make([]store.Account, len(accounts))
	copy(sorted, accounts)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LastUsed.IsZero() && !sorted[j].LastUsed.IsZero() {
			return true // never-used accounts first
		}
		if !sorted[i].LastUsed.IsZero() && sorted[j].LastUsed.IsZero() {
			return false
		}
		return sorted[i].LastUsed.Before(sorted[j].LastUsed)
	})
	return &sorted[0], nil
}

func pickLeastUsed(accounts []store.Account) (*store.Account, error) {
	sorted := make([]store.Account, len(accounts))
	copy(sorted, accounts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UseCount < sorted[j].UseCount
	})
	return &sorted[0], nil
}

func pickRandom(accounts []store.Account) (*store.Account, error) {
	idx := rand.Intn(len(accounts))
	return &accounts[idx], nil
}

// pickQuotaAware prefers accounts that aren't currently inside a 429
// cooldown window. Among non-cooldown candidates it falls back to
// least-used (by UseCount), then least-recently-used as a tie-break.
// If every account is in cooldown, it returns the one whose cooldown
// expires soonest — there's no good option, so pick the closest to ready.
func pickQuotaAware(accounts []store.Account, now time.Time) (*store.Account, error) {
	sorted := make([]store.Account, len(accounts))
	copy(sorted, accounts)

	var available, hot []store.Account
	for _, a := range sorted {
		if !a.LastRateLimitedAt.IsZero() && now.Sub(a.LastRateLimitedAt) < QuotaCooldown {
			hot = append(hot, a)
		} else {
			available = append(available, a)
		}
	}

	if len(available) > 0 {
		sort.SliceStable(available, func(i, j int) bool {
			if available[i].UseCount != available[j].UseCount {
				return available[i].UseCount < available[j].UseCount
			}
			if available[i].LastUsed.IsZero() && !available[j].LastUsed.IsZero() {
				return true
			}
			if !available[i].LastUsed.IsZero() && available[j].LastUsed.IsZero() {
				return false
			}
			return available[i].LastUsed.Before(available[j].LastUsed)
		})
		return &available[0], nil
	}

	// All in cooldown — pick the one closest to exiting it.
	sort.SliceStable(hot, func(i, j int) bool {
		return hot[i].LastRateLimitedAt.Before(hot[j].LastRateLimitedAt)
	})
	return &hot[0], nil
}
