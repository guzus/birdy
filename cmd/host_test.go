package cmd

import (
	"net/http/httptest"
	"testing"
)

func TestIsHostAuthorizedFromQueryToken(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/?token=abc123", nil)
	if !isHostAuthorized(r, "abc123") {
		t.Fatal("expected query token to authorize request")
	}
}

func TestIsHostAuthorizedFromBearerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if !isHostAuthorized(r, "abc123") {
		t.Fatal("expected bearer token to authorize request")
	}
}

func TestIsHostAuthorizedRejectsMissingToken(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	if isHostAuthorized(r, "abc123") {
		t.Fatal("expected missing token to be rejected")
	}
}

func TestEnsureHostTokenUsesFlag(t *testing.T) {
	got, generated, err := ensureHostToken("abc123")
	if err != nil {
		t.Fatalf("ensureHostToken returned error: %v", err)
	}
	if generated {
		t.Fatal("expected provided token not to be marked generated")
	}
	if got != "abc123" {
		t.Fatalf("expected provided token, got %q", got)
	}
}

func TestEnsureHostTokenGeneratesWhenMissing(t *testing.T) {
	t.Setenv("BIRDY_HOST_TOKEN", "")
	got, generated, err := ensureHostToken("")
	if err != nil {
		t.Fatalf("ensureHostToken returned error: %v", err)
	}
	if !generated {
		t.Fatal("expected missing token to trigger generation")
	}
	if len(got) < 20 {
		t.Fatalf("expected generated token to be non-trivial, got %q", got)
	}
}

func TestAdvertisedHostLocalFallbacks(t *testing.T) {
	if got := advertisedHost(":8787"); got != "127.0.0.1:8787" {
		t.Fatalf("expected local fallback host, got %q", got)
	}
	if got := advertisedHost("0.0.0.0:8787"); got != "127.0.0.1:8787" {
		t.Fatalf("expected wildcard host normalized, got %q", got)
	}
}
