package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
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

func TestMakeHostedWebHandlerServesIndexAndSecurityHeaders(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>ok</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	makeHostedWebHandler(webDir).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<html>ok</html>") {
		t.Fatalf("expected index body, got %q", rr.Body.String())
	}
	if got := rr.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("expected CSP header")
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", got)
	}
}

func TestMakeHostedWebHandlerFallsBackToIndexForSPARoute(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>spa</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/app/thread/123", nil)
	makeHostedWebHandler(webDir).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<html>spa</html>") {
		t.Fatalf("expected SPA index fallback, got %q", rr.Body.String())
	}
}

func TestMakeHostedWebHandlerRejectsNonGetMethods(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>ok</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/", nil)
	makeHostedWebHandler(webDir).ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestAuthenticateHostedWSSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hostUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		if !authenticateHostedWS(conn, "birdy") {
			t.Error("expected auth to succeed")
			return
		}
	}))
	defer server.Close()

	conn := dialTestWebsocket(t, server.URL, nil)
	defer conn.Close()
	if err := conn.WriteJSON(hostedWSMessage{Type: "auth", Code: "birdy"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var msg hostedWSAuthMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	if !msg.OK || msg.Error != "" {
		t.Fatalf("expected successful auth message, got %#v", msg)
	}
}

func TestAuthenticateHostedWSRejectsInvalidCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := hostUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		_ = authenticateHostedWS(conn, "birdy")
	}))
	defer server.Close()

	conn := dialTestWebsocket(t, server.URL, nil)
	defer conn.Close()
	if err := conn.WriteJSON(hostedWSMessage{Type: "auth", Code: "wrong"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var msg hostedWSAuthMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	if msg.OK || msg.Error != "invalid invite code" {
		t.Fatalf("expected invalid invite code response, got %#v", msg)
	}
}

func TestHostedWSOriginGateRejectsDisallowedOrigin(t *testing.T) {
	allowedOrigins := parseAllowedOrigins("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isHostOriginAllowed(r, allowedOrigins) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		serveHostedTTY(w, r, "birdy")
	}))
	defer server.Close()

	header := http.Header{}
	header.Set("Origin", "https://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(server.URL), header)
	if err == nil {
		t.Fatal("expected websocket dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("expected 403 response, got %d err=%v", got, err)
	}
}

func dialTestWebsocket(t *testing.T, serverURL string, header http.Header) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(serverURL), header)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func wsURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http")
}

func TestHostedWSAuthMessageJSONShape(t *testing.T) {
	data, err := json.Marshal(hostedWSAuthMessage{Type: "auth", OK: true})
	if err != nil {
		t.Fatalf("marshal auth msg: %v", err)
	}
	if !strings.Contains(string(data), `"type":"auth"`) || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("unexpected auth json: %s", data)
	}
}
