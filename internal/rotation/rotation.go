package rotation

import (
	"fmt"
	"math/rand"
	"sort"

	"github.com/guzus/birdy/internal/store"
)

// Strategy defines how the next account is selected.
type Strategy string

const (
	RoundRobin      Strategy = "round-robin"
	LeastRecentlyUsed Strategy = "least-recently-used"
	LeastUsed       Strategy = "least-used"
	Random          Strategy = "random"
)

// ParseStrategy converts a string to a Strategy.
func ParseStrategy(s string) (Strategy, error) {
	switch Strategy(s) {
	case RoundRobin, LeastRecentlyUsed, LeastUsed, Random:
		return Strategy(s), nil
	default:
		return "", fmt.Errorf("unknown strategy %q (valid: round-robin, least-recently-used, least-used, random)", s)
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
