package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/store"
)

func getHealth(t *testing.T, url string) (healthResponse, string) {
	t.Helper()
	resp, err := http.Get(url + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	var got healthResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return got, string(raw)
}

// TestHealthRateLimitState is the reason the fields exist: a wrapper polling
// /health must be able to say "1 of 2 accounts cooling" and see when the
// cooling one comes back, without reading the store itself.
func TestHealthRateLimitState(t *testing.T) {
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	accounts := []store.Account{
		{Name: "hot", AuthToken: "secret-token", CT0: "secret-ct0",
			LastRateLimitedAt: now.Add(-5 * time.Minute)},
		{Name: "cold", AuthToken: "secret-token-2", CT0: "secret-ct0-2",
			LastRateLimitedAt: now.Add(-2 * rotation.QuotaCooldown)},
	}
	hs, srv, teardown := newTestServer(t, Config{
		AccountCount: func() int { return len(accounts) },
		Accounts:     func() []store.Account { return accounts },
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "ok", "", nil
		},
	})
	defer teardown()
	srv.now = func() time.Time { return now }

	got, raw := getHealth(t, hs.URL)

	if !got.OK {
		t.Fatalf("ok must stay true (liveness), got false:\n%s", raw)
	}
	if got.Degraded {
		t.Fatalf("degraded must be false while an account is ready:\n%s", raw)
	}
	if got.Accounts != 2 || got.AccountsCooling != 1 || got.AccountsReady != 1 {
		t.Fatalf("accounts=%d cooling=%d ready=%d, want 2/1/1:\n%s",
			got.Accounts, got.AccountsCooling, got.AccountsReady, raw)
	}
	if want := int(rotation.QuotaCooldown / time.Second); got.CooldownSeconds != want {
		t.Fatalf("cooldown_seconds=%d, want %d", got.CooldownSeconds, want)
	}
	if len(got.AccountsDetail) != 2 {
		t.Fatalf("accounts_detail=%v, want 2 entries", got.AccountsDetail)
	}

	hot, cold := got.AccountsDetail[0], got.AccountsDetail[1]
	if hot.Name != "hot" || !hot.Cooling {
		t.Fatalf("hot detail = %+v, want cooling", hot)
	}
	// 15m window, 5m elapsed: 10m = 600s remain.
	if hot.CooldownRemainingSeconds != 600 {
		t.Fatalf("hot cooldown_remaining_seconds=%d, want 600", hot.CooldownRemainingSeconds)
	}
	if hot.LastRateLimitedAt != "2026-09-06T09:55:00Z" {
		t.Fatalf("hot last_rate_limited_at=%q, want RFC3339 UTC", hot.LastRateLimitedAt)
	}
	if cold.Name != "cold" || cold.Cooling || cold.CooldownRemainingSeconds != 0 {
		t.Fatalf("cold detail = %+v, want ready with 0 remaining", cold)
	}
	if cold.LastRateLimitedAt == "" {
		t.Fatalf("cold has a historical 429 and must still report it")
	}

	// Credentials must never leave the store through /health.
	for _, secret := range []string{"secret-token", "secret-ct0", "auth_token", "ct0"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("/health leaked %q:\n%s", secret, raw)
		}
	}

	// The original six keys are still there, byte-for-byte named.
	for _, key := range []string{`"ok"`, `"accounts"`, `"uptime_seconds"`, `"served"`, `"cache_hits"`, `"cache_size"`,
		`"accounts_cooling"`, `"accounts_ready"`, `"cooldown_seconds"`, `"degraded"`, `"accounts_detail"`,
		`"cooldown_remaining_seconds"`, `"last_rate_limited_at"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("/health missing key %s:\n%s", key, raw)
		}
	}

	// Advance the clock past the window: the hot account is ready again.
	srv.now = func() time.Time { return now.Add(rotation.QuotaCooldown) }
	got, raw = getHealth(t, hs.URL)
	if got.AccountsCooling != 0 || got.AccountsReady != 2 {
		t.Fatalf("after cooldown: cooling=%d ready=%d, want 0/2:\n%s",
			got.AccountsCooling, got.AccountsReady, raw)
	}
}

// TestHealthDegradedWhenEveryAccountCooling: ok stays true (the daemon is
// alive) but degraded flips so a poller can stop sending traffic.
func TestHealthDegradedWhenEveryAccountCooling(t *testing.T) {
	now := time.Now()
	accounts := []store.Account{
		{Name: "a", LastRateLimitedAt: now.Add(-time.Minute)},
		{Name: "b", LastRateLimitedAt: now.Add(-2 * time.Minute)},
		// Disabled accounts are out of rotation: not ready, not cooling.
		{Name: "off", Disabled: true},
	}
	hs, srv, teardown := newTestServer(t, Config{
		AccountCount: func() int { return len(accounts) },
		Accounts:     func() []store.Account { return accounts },
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "ok", "", nil
		},
	})
	defer teardown()
	srv.now = func() time.Time { return now }

	got, raw := getHealth(t, hs.URL)
	if !got.OK || !got.Degraded {
		t.Fatalf("want ok=true degraded=true, got ok=%v degraded=%v:\n%s", got.OK, got.Degraded, raw)
	}
	if got.AccountsCooling != 2 || got.AccountsReady != 0 {
		t.Fatalf("cooling=%d ready=%d, want 2/0:\n%s", got.AccountsCooling, got.AccountsReady, raw)
	}
	if len(got.AccountsDetail) != 3 || !got.AccountsDetail[2].Disabled {
		t.Fatalf("disabled account must still be listed, flagged: %+v", got.AccountsDetail)
	}
}

// TestHealthWithoutAccountsFunc keeps the older Config shape working: no
// Accounts callback means nothing is known to be cooling, and the detail
// list is an empty array rather than null.
func TestHealthWithoutAccountsFunc(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		AccountCount: func() int { return 3 },
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "ok", "", nil
		},
	})
	defer teardown()

	got, raw := getHealth(t, hs.URL)
	if got.AccountsReady != 3 || got.AccountsCooling != 0 || got.Degraded {
		t.Fatalf("ready=%d cooling=%d degraded=%v, want 3/0/false:\n%s",
			got.AccountsReady, got.AccountsCooling, got.Degraded, raw)
	}
	if !strings.Contains(raw, `"accounts_detail":[]`) {
		t.Fatalf("accounts_detail must be an empty array, not null:\n%s", raw)
	}
}
