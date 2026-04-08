package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestHostRejectsUnexpectedArgs(t *testing.T) {
	if err := hostCmd.Args(hostCmd, []string{"extra"}); err == nil {
		t.Fatal("expected host to reject unexpected args")
	}
}

func TestHostReturnsConfiguredWebDirError(t *testing.T) {
	t.Setenv("BIRDY_HOST_INVITE_CODE", "birdy")
	t.Setenv("BIRDY_HOST_WEB_DIR", filepath.Join(t.TempDir(), "missing-web"))

	prevAddr, prevInvite := hostAddrFlag, hostInviteCodeFlag
	hostAddrFlag, hostInviteCodeFlag = "127.0.0.1:8787", ""
	defer func() {
		hostAddrFlag, hostInviteCodeFlag = prevAddr, prevInvite
	}()

	err := hostCmd.RunE(hostCmd, nil)
	if err == nil {
		t.Fatal("expected invalid configured web dir to fail")
	}
	if !strings.Contains(err.Error(), "missing index.html") {
		t.Fatalf("expected missing index.html error, got %v", err)
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

func TestNormalizeOriginStripsDefaultPorts(t *testing.T) {
	if got := normalizeOrigin("http://Example.COM:80/"); got != "http://example.com" {
		t.Fatalf("expected normalized http default port origin, got %q", got)
	}
	if got := normalizeOrigin("https://Example.COM:443/"); got != "https://example.com" {
		t.Fatalf("expected normalized https default port origin, got %q", got)
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

func TestIsHostOriginAllowedSameOriginWithDefaultPort(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/ws", nil)
	r.Host = "birdy-host-web-production.up.railway.app:443"
	r.Header.Set("Origin", "https://birdy-host-web-production.up.railway.app")
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isHostOriginAllowed(r, nil) {
		t.Fatal("expected same-origin websocket request with default port to be allowed")
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

func TestIsHostOriginAllowedAllowlistNormalizesDefaultPort(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/ws", nil)
	r.Host = "internal.railway"
	r.Header.Set("Origin", "https://birdy.guzus.xyz")
	r.Header.Set("X-Forwarded-Proto", "http")

	allowed := parseAllowedOrigins("https://birdy.guzus.xyz:443")
	if !isHostOriginAllowed(r, allowed) {
		t.Fatal("expected allowlist origin with default port to be allowed")
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

func TestMakeHostedWebHandlerWithoutBuildRejectsNonGetMethods(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/", nil)
	makeHostedWebHandler("").ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestMakeHostedWebHandlerWithoutBuildHeadHasNoBody(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "http://example.com/", nil)
	makeHostedWebHandler("").ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD, got %q", rr.Body.String())
	}
}

func TestBuildHostedMuxServesHealthzAndStatic(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>mux</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	mux := buildHostedMux("birdy", nil, webDir)

	healthRR := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "http://example.com/healthz", nil)
	mux.ServeHTTP(healthRR, healthReq)
	if healthRR.Code != http.StatusOK || strings.TrimSpace(healthRR.Body.String()) != "ok" {
		t.Fatalf("expected healthz ok, got code=%d body=%q", healthRR.Code, healthRR.Body.String())
	}
	if got := healthRR.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected healthz security headers, got X-Frame-Options=%q", got)
	}

	indexRR := httptest.NewRecorder()
	indexReq := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	mux.ServeHTTP(indexRR, indexReq)
	if indexRR.Code != http.StatusOK || !strings.Contains(indexRR.Body.String(), "<html>mux</html>") {
		t.Fatalf("expected static index, got code=%d body=%q", indexRR.Code, indexRR.Body.String())
	}
}

func TestHostedAPICommandIncludesSecurityHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", strings.NewReader(`{"command":"home"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	buildHostedMux("birdy", nil, t.TempDir()).ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected security headers on api response, got X-Frame-Options=%q", got)
	}
	if got := rr.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("expected CSP header on api response")
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
	if strings.HasPrefix(serverURL, "ws://") || strings.HasPrefix(serverURL, "wss://") {
		return serverURL
	}
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

func TestHostedWSSmokeWithInjectedChild(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "fake-host-tui.sh")
	script := strings.Join([]string{
		"#!/bin/sh",
		"printf 'READY FROM FAKE TUI\\n'",
		"sleep 0.1",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake tui script: %v", err)
	}

	t.Setenv("BIRDY_HOST_TUI_PATH", scriptPath)
	t.Setenv("BIRDY_HOST_TUI_ARGS", "")

	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>host</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	server := httptest.NewServer(buildHostedMux("birdy", nil, webDir))
	defer server.Close()

	header := http.Header{}
	header.Set("Origin", server.URL)
	conn := dialTestWebsocket(t, server.URL+"/ws", header)
	defer conn.Close()

	if err := conn.WriteJSON(hostedWSMessage{Type: "auth", Code: "birdy"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	var auth hostedWSAuthMessage
	if err := conn.ReadJSON(&auth); err != nil {
		t.Fatalf("read auth: %v", err)
	}
	if !auth.OK {
		t.Fatalf("expected auth ok, got %#v", auth)
	}

	if err := conn.WriteJSON(hostedWSMessage{Type: "resize", Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hosted output: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected binary output, got type=%d payload=%q", msgType, string(payload))
	}
	if !strings.Contains(string(payload), "READY FROM FAKE TUI") {
		t.Fatalf("expected fake tui output, got %q", string(payload))
	}
}

func TestHostedWSReportsChildStartFailure(t *testing.T) {
	t.Setenv("BIRDY_HOST_TUI_PATH", filepath.Join(t.TempDir(), "missing-binary"))
	t.Setenv("BIRDY_HOST_TUI_ARGS", "")

	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>host</html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	server := httptest.NewServer(buildHostedMux("birdy", nil, webDir))
	defer server.Close()

	header := http.Header{}
	header.Set("Origin", server.URL)
	conn := dialTestWebsocket(t, server.URL+"/ws", header)
	defer conn.Close()

	if err := conn.WriteJSON(hostedWSMessage{Type: "auth", Code: "birdy"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	var auth hostedWSAuthMessage
	if err := conn.ReadJSON(&auth); err != nil {
		t.Fatalf("read auth: %v", err)
	}
	if !auth.OK {
		t.Fatalf("expected auth ok, got %#v", auth)
	}

	if err := conn.WriteJSON(hostedWSMessage{Type: "resize", Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hosted failure output: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected text failure message, got type=%d payload=%q", msgType, string(payload))
	}
	if !strings.Contains(string(payload), "failed to start birdy tui") {
		t.Fatalf("expected startup failure notice, got %q", string(payload))
	}
}

func TestHostedWSDisconnectBeforeResizeDoesNotStartChild(t *testing.T) {
	scriptDir := t.TempDir()
	markerPath := filepath.Join(scriptDir, "started.txt")
	scriptPath := filepath.Join(scriptDir, "fake-host-tui.sh")
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo started > " + shellQuote(markerPath),
		"sleep 0.2",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake tui script: %v", err)
	}

	t.Setenv("BIRDY_HOST_TUI_PATH", scriptPath)
	t.Setenv("BIRDY_HOST_TUI_ARGS", "")

	server := httptest.NewServer(buildHostedMux("birdy", nil, t.TempDir()))
	defer server.Close()

	header := http.Header{}
	header.Set("Origin", server.URL)
	conn := dialTestWebsocket(t, server.URL+"/ws", header)

	if err := conn.WriteJSON(hostedWSMessage{Type: "auth", Code: "birdy"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	var auth hostedWSAuthMessage
	if err := conn.ReadJSON(&auth); err != nil {
		t.Fatalf("read auth: %v", err)
	}
	if !auth.OK {
		t.Fatalf("expected auth ok, got %#v", auth)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket: %v", err)
	}

	time.Sleep(250 * time.Millisecond)

	if _, err := os.Stat(markerPath); err == nil {
		t.Fatalf("expected hosted child not to start after disconnect, marker %s exists", markerPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'"
}
