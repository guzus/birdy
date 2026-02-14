package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
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

func TestHostOSCParserNoOSC(t *testing.T) {
	p := hostOSCParser{}
	data := []byte("hello world\x1b[31mred\x1b[0m")
	term, specs := p.process(data)
	if !reflect.DeepEqual(term, data) {
		t.Fatalf("expected passthrough, got %q", term)
	}
	if len(specs) != 0 {
		t.Fatalf("expected no specs, got %d", len(specs))
	}
}

func TestHostOSCParserSingleSpec(t *testing.T) {
	p := hostOSCParser{}
	spec := `{"root":"c1","elements":{"c1":{"type":"Card","props":{"title":"Hi"}}}}`
	data := []byte("before" + hostOSCPrefix + spec + hostOSCBEL + "after")
	term, specs := p.process(data)
	if string(term) != "beforeafter" {
		t.Fatalf("expected stripped terminal output, got %q", string(term))
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if !json.Valid(specs[0]) {
		t.Fatalf("spec is not valid JSON: %q", specs[0])
	}
	if string(specs[0]) != spec {
		t.Fatalf("spec mismatch: got %q", string(specs[0]))
	}
}

func TestHostOSCParserMultipleSpecs(t *testing.T) {
	p := hostOSCParser{}
	s1 := `{"root":"a"}`
	s2 := `{"root":"b"}`
	data := []byte(hostOSCPrefix + s1 + hostOSCBEL + "mid" + hostOSCPrefix + s2 + hostOSCBEL)
	term, specs := p.process(data)
	if string(term) != "mid" {
		t.Fatalf("expected 'mid', got %q", string(term))
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
}

func TestHostOSCParserPartialOSC(t *testing.T) {
	p := hostOSCParser{}
	spec := `{"root":"c1"}`
	full := hostOSCPrefix + spec + hostOSCBEL

	// Split in the middle of the OSC sequence.
	half := len(full) / 2
	chunk1 := []byte("before" + full[:half])
	chunk2 := []byte(full[half:] + "after")

	term1, specs1 := p.process(chunk1)
	if string(term1) != "before" {
		t.Fatalf("chunk1: expected 'before', got %q", string(term1))
	}
	if len(specs1) != 0 {
		t.Fatalf("chunk1: expected no specs yet, got %d", len(specs1))
	}

	term2, specs2 := p.process(chunk2)
	if string(term2) != "after" {
		t.Fatalf("chunk2: expected 'after', got %q", string(term2))
	}
	if len(specs2) != 1 {
		t.Fatalf("chunk2: expected 1 spec, got %d", len(specs2))
	}
	if string(specs2[0]) != spec {
		t.Fatalf("chunk2: spec mismatch: got %q", string(specs2[0]))
	}
}

func TestHostOSCParserInvalidJSON(t *testing.T) {
	p := hostOSCParser{}
	data := []byte(hostOSCPrefix + "not-json" + hostOSCBEL)
	term, specs := p.process(data)
	if len(term) != 0 {
		t.Fatalf("expected no terminal output, got %q", string(term))
	}
	if len(specs) != 0 {
		t.Fatalf("expected invalid JSON to be dropped, got %d specs", len(specs))
	}
}

func TestHostOSCParserPartialPrefix(t *testing.T) {
	p := hostOSCParser{}
	// Send data that ends with partial OSC prefix.
	prefix := hostOSCPrefix
	partial := prefix[:len(prefix)-3]
	data := []byte("text" + partial)
	term, specs := p.process(data)
	if string(term) != "text" {
		t.Fatalf("expected 'text', got %q", string(term))
	}
	if len(specs) != 0 {
		t.Fatalf("expected no specs, got %d", len(specs))
	}

	// Complete the sequence.
	rest := prefix[len(prefix)-3:] + `{"ok":true}` + hostOSCBEL + "end"
	term2, specs2 := p.process([]byte(rest))
	if string(term2) != "end" {
		t.Fatalf("expected 'end', got %q", string(term2))
	}
	if len(specs2) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs2))
	}
}
