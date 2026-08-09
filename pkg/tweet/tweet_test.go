package tweet

import (
	"testing"
)

// Response parsing and Tweet/Media semantics live in internal/xapi and are
// tested there. These tests cover this package's own concerns: reference
// parsing, client construction, account selection, and thread ancestry.

func TestExtractTweetID(t *testing.T) {
	const wantID = "1349129669258448897"

	valid := []struct {
		name string
		in   string
	}{
		{"x.com", "https://x.com/elonmusk/status/1349129669258448897"},
		{"twitter.com", "https://twitter.com/elonmusk/status/1349129669258448897"},
		{"www prefix", "https://www.x.com/elonmusk/status/1349129669258448897"},
		{"mobile prefix", "https://mobile.twitter.com/elonmusk/status/1349129669258448897"},
		{"http scheme", "http://x.com/elonmusk/status/1349129669258448897"},
		{"no scheme", "x.com/elonmusk/status/1349129669258448897"},
		{"tracking query", "https://x.com/elonmusk/status/1349129669258448897?s=20&t=abc"},
		{"trailing photo path", "https://x.com/elonmusk/status/1349129669258448897/photo/1"},
		{"statuses form", "https://twitter.com/elonmusk/statuses/1349129669258448897"},
		{"i/web/status form", "https://twitter.com/i/web/status/1349129669258448897"},
		{"i/status form", "https://x.com/i/status/1349129669258448897"},
		{"uppercase host", "https://X.com/elonmusk/status/1349129669258448897"},
		{"surrounding space", "  https://x.com/elonmusk/status/1349129669258448897  "},
		{"bare id", "1349129669258448897"},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractTweetID(tc.in)
			if err != nil {
				t.Fatalf("ExtractTweetID(%q) returned error: %v", tc.in, err)
			}
			if got != wantID {
				t.Errorf("ExtractTweetID(%q) = %q, want %q", tc.in, got, wantID)
			}
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"profile url", "https://x.com/elonmusk"},
		{"non-twitter host", "https://example.com/user/status/123"},
		{"lookalike host", "https://notx.com/user/status/123"},
		{"lookalike suffix host", "https://x.com.evil.test/user/status/123"},
		{"missing id", "https://x.com/elonmusk/status/"},
		{"non-numeric id", "https://x.com/elonmusk/status/abc"},
		{"plain text", "just some words"},
	}

	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ExtractTweetID(tc.in); err == nil {
				t.Errorf("ExtractTweetID(%q) = %q, want error", tc.in, got)
			}
		})
	}
}

// thread is returned flat, so ancestry must be reconstructed via InReplyToStatusID.
func TestAncestorChain(t *testing.T) {
	thread := []Tweet{
		{ID: "100", ConversationID: "100"},
		{ID: "101", ConversationID: "100", InReplyToStatusID: "100"},
		{ID: "102", ConversationID: "100", InReplyToStatusID: "101"},
		{ID: "999", ConversationID: "100", InReplyToStatusID: "100"}, // unrelated sibling
	}

	t.Run("returns ancestors root first", func(t *testing.T) {
		chain := AncestorChain(thread, "102")
		if len(chain) != 2 {
			t.Fatalf("len(chain) = %d, want 2", len(chain))
		}
		if chain[0].ID != "100" || chain[1].ID != "101" {
			t.Errorf("chain = [%s %s], want [100 101] (root first)", chain[0].ID, chain[1].ID)
		}
	})

	t.Run("root has no ancestors", func(t *testing.T) {
		if chain := AncestorChain(thread, "100"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty for the root", chain)
		}
	})

	t.Run("unknown target yields nothing", func(t *testing.T) {
		if chain := AncestorChain(thread, "does-not-exist"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty", chain)
		}
	})

	t.Run("stops when a parent is missing from the thread", func(t *testing.T) {
		partial := []Tweet{{ID: "202", InReplyToStatusID: "201"}}
		if chain := AncestorChain(partial, "202"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty when the parent was not returned", chain)
		}
	})

	t.Run("does not loop on cyclic data", func(t *testing.T) {
		cyclic := []Tweet{
			{ID: "300", InReplyToStatusID: "301"},
			{ID: "301", InReplyToStatusID: "300"},
		}
		if chain := AncestorChain(cyclic, "300"); len(chain) > 2 {
			t.Errorf("chain = %+v, want the cycle to be cut short", chain)
		}
	})
}

const testAccounts = `[{"name":"main","auth_token":"tok1","ct0":"ct1"},{"name":"alt","auth_token":"tok2","ct0":"ct2"}]`

func TestNewClientFromAccountsJSON(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if got := c.Accounts(); len(got) != 2 {
		t.Fatalf("Accounts() = %v, want 2 accounts", got)
	}
	// Default strategy must be quota-aware: a server should route around 429s.
	if c.strategy != "quota-aware" {
		t.Errorf("strategy = %q, want quota-aware by default", c.strategy)
	}
}

func TestNewClientValidation(t *testing.T) {
	t.Run("rejects empty account set", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: `[]`}); err == nil {
			t.Error("NewClient([]) = nil error, want error")
		}
	})

	t.Run("rejects malformed json", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: `{not json`}); err == nil {
			t.Error("NewClient(bad json) = nil error, want error")
		}
	})

	t.Run("rejects unknown strategy", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: testAccounts, Strategy: "nonsense"}); err == nil {
			t.Error("NewClient(bad strategy) = nil error, want error")
		}
	})

	t.Run("rejects pinning an unknown account", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: testAccounts, Account: "missing"}); err == nil {
			t.Error("NewClient(unknown account) = nil error, want error")
		}
	})

	t.Run("accepts pinning a known account", func(t *testing.T) {
		c, err := NewClient(Options{AccountsJSON: testAccounts, Account: "alt"})
		if err != nil {
			t.Fatalf("NewClient returned error: %v", err)
		}
		account, err := c.pick()
		if err != nil {
			t.Fatalf("pick returned error: %v", err)
		}
		// A pinned client must never rotate away from its account.
		if account.Name != "alt" {
			t.Errorf("pick() = %q, want the pinned account alt", account.Name)
		}
	})

	t.Run("accepts a validated account pool", func(t *testing.T) {
		c, err := NewMonitoringClient(MonitoringOptions{AccountsJSON: testAccounts, AccountPool: []string{" alt "}})
		if err != nil {
			t.Fatalf("NewClient returned error: %v", err)
		}
		if got := c.Accounts(); len(got) != 1 || got[0] != "alt" {
			t.Fatalf("Accounts() = %v, want [alt]", got)
		}
		for range 3 {
			account, err := c.pick()
			if err != nil || account.Name != "alt" {
				t.Fatalf("pick() = %v, %v; want alt", account, err)
			}
		}
	})

	t.Run("rejects invalid account pools", func(t *testing.T) {
		cases := []MonitoringOptions{
			{AccountsJSON: testAccounts, AccountPool: []string{"missing"}},
			{AccountsJSON: testAccounts, AccountPool: []string{"main", "main"}},
			{AccountsJSON: testAccounts, AccountPool: []string{" "}},
			{AccountsJSON: testAccounts, Account: "main", AccountPool: []string{"alt"}},
		}
		for _, opts := range cases {
			if _, err := NewMonitoringClient(opts); err == nil {
				t.Errorf("NewMonitoringClient(%+v) = nil error, want validation error", opts)
			}
		}
	})
}

func TestClientRejectsBadReference(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	// Must fail before any network call, so these are safe offline.
	if _, err := c.Read(t.Context(), "  "); err == nil {
		t.Error("Read(empty) = nil error, want error")
	}
	if _, err := c.Thread(t.Context(), "https://x.com/elonmusk"); err == nil {
		t.Error("Thread(profile url) = nil error, want error")
	}
}

// An ephemeral store must never write to disk, so an embedding service can run
// on a read-only filesystem.
func TestEphemeralStoreDoesNotPersist(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if !c.store.IsEphemeral() {
		t.Error("store.IsEphemeral() = false, want true for AccountsJSON-backed clients")
	}
	if err := c.store.Save(); err != nil {
		t.Errorf("Save() = %v, want nil no-op", err)
	}
}

func TestRotationAdvancesAcrossCalls(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts, Strategy: "round-robin"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	first, err := c.pick()
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	c.setLastUsed(first.Name)

	second, err := c.pick()
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	if second.Name == first.Name {
		t.Errorf("round-robin picked %q twice; want it to advance", first.Name)
	}
}

// Each account gets its own X client, reused across calls so it keeps a stable
// client identity.
func TestAPIClientPerAccountIsCached(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	main, err := c.store.Get("main")
	if err != nil {
		t.Fatalf("store.Get returned error: %v", err)
	}
	alt, err := c.store.Get("alt")
	if err != nil {
		t.Fatalf("store.Get returned error: %v", err)
	}

	first, err := c.apiClientFor(main)
	if err != nil {
		t.Fatalf("apiClientFor returned error: %v", err)
	}
	again, err := c.apiClientFor(main)
	if err != nil {
		t.Fatalf("apiClientFor returned error: %v", err)
	}
	if first != again {
		t.Error("apiClientFor returned a new client for the same account; want it cached")
	}

	other, err := c.apiClientFor(alt)
	if err != nil {
		t.Fatalf("apiClientFor returned error: %v", err)
	}
	if other == first {
		t.Error("apiClientFor returned the same client for different accounts")
	}
}
