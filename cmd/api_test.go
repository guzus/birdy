package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestAPIChatRoutesCodexModelToCodexCLI(t *testing.T) {
	binDir := t.TempDir()

	codexScript := filepath.Join(binDir, "codex")
	codexContent := strings.Join([]string{
		"#!/bin/sh",
		"cat <<'EOF'",
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"msg1\",\"type\":\"agent_message\",\"text\":\"from codex\"}}",
		"{\"type\":\"turn.completed\"}",
		"EOF",
	}, "\n")
	if err := os.WriteFile(codexScript, []byte(codexContent), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	claudeScript := filepath.Join(binDir, "claude")
	claudeContent := strings.Join([]string{
		"#!/bin/sh",
		"echo '{\"type\":\"result\",\"result\":\"claude should not run\",\"is_error\":true}'",
	}, "\n")
	if err := os.WriteFile(claudeScript, []byte(claudeContent), 0755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello","model":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"text":"from codex"`) {
		t.Fatalf("expected codex response in SSE body, got %q", body)
	}
	if strings.Contains(body, "claude should not run") {
		t.Fatalf("expected claude path not to run, got %q", body)
	}
}
