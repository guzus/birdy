package cmd

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestAPIAuthHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "http://example.com/api/command", bytes.NewBufferString(`{"command":"home"}`))
	r.Header.Set("Authorization", "Bearer birdy")
	if got := hostRequestInviteCode(r); got != "birdy" {
		t.Fatalf("expected bearer parsed, got %q", got)
	}

	r2 := httptest.NewRequest("POST", "http://example.com/api/command", nil)
	r2.Header.Set("X-Invite-Code", "x")
	if got := hostRequestInviteCode(r2); got != "x" {
		t.Fatalf("expected x-invite-code parsed, got %q", got)
	}
}
