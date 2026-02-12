package cmd

import (
	"net/http/httptest"
	"testing"
)

func TestEnsureHostInviteCodeUsesFlag(t *testing.T) {
	got, err := ensureHostInviteCode("abc123")
	if err != nil {
		t.Fatalf("ensureHostInviteCode returned error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("expected provided invite code, got %q", got)
	}
}

func TestEnsureHostInviteCodeUsesEnv(t *testing.T) {
	t.Setenv("BIRDY_HOST_INVITE_CODE", "envcode")
	t.Setenv("BIRDY_HOST_TOKEN", "")
	got, err := ensureHostInviteCode("")
	if err != nil {
		t.Fatalf("ensureHostInviteCode returned error: %v", err)
	}
	if got != "envcode" {
		t.Fatalf("expected invite code from env, got %q", got)
	}
}

func TestEnsureHostInviteCodeFallsBackToLegacyTokenEnv(t *testing.T) {
	t.Setenv("BIRDY_HOST_INVITE_CODE", "")
	t.Setenv("BIRDY_HOST_TOKEN", "legacy-token")
	got, err := ensureHostInviteCode("")
	if err != nil {
		t.Fatalf("ensureHostInviteCode returned error: %v", err)
	}
	if got != "legacy-token" {
		t.Fatalf("expected fallback token env value, got %q", got)
	}
}

func TestEnsureHostInviteCodeRejectsMissing(t *testing.T) {
	t.Setenv("BIRDY_HOST_INVITE_CODE", "")
	t.Setenv("BIRDY_HOST_TOKEN", "")
	if _, err := ensureHostInviteCode(""); err == nil {
		t.Fatal("expected missing invite code to return error")
	}
}

func TestNormalizeOrigin(t *testing.T) {
	if got := normalizeOrigin(" https://Example.COM/ "); got != "https://example.com" {
		t.Fatalf("expected normalized https origin, got %q", got)
	}
	if got := normalizeOrigin("javascript:alert(1)"); got != "" {
		t.Fatalf("expected invalid scheme rejected, got %q", got)
	}
}

func TestIsHostOriginAllowedSameOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/ws", nil)
	r.Host = "birdy-host-web-production.up.railway.app"
	r.Header.Set("Origin", "https://birdy-host-web-production.up.railway.app")
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isHostOriginAllowed(r, nil) {
		t.Fatal("expected same-origin websocket request to be allowed")
	}
}

func TestIsHostOriginAllowedAllowlist(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/ws", nil)
	r.Host = "internal.railway"
	r.Header.Set("Origin", "https://birdy.guzus.xyz")
	r.Header.Set("X-Forwarded-Proto", "http")

	allowed := parseAllowedOrigins("https://birdy.guzus.xyz, https://admin.guzus.xyz")
	if !isHostOriginAllowed(r, allowed) {
		t.Fatal("expected explicit allowlist origin to be allowed")
	}
}

func TestIsHostOriginAllowedRejectsMissingOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/ws", nil)
	if isHostOriginAllowed(r, nil) {
		t.Fatal("expected missing origin to be rejected")
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

func TestHostedAccessURLWithoutTokenQuery(t *testing.T) {
	if got := hostedAccessURL("0.0.0.0:8787"); got != "http://127.0.0.1:8787" {
		t.Fatalf("expected URL without token query, got %q", got)
	}
}
