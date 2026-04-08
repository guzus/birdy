package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

type noFlushRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *noFlushRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *noFlushRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *noFlushRecorder) WriteHeader(status int) {
	r.status = status
}

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

func TestAPIChatDefaultRoutesToClaudeCLI(t *testing.T) {
	binDir := t.TempDir()

	claudeScript := filepath.Join(binDir, "claude")
	claudeContent := strings.Join([]string{
		"#!/bin/sh",
		"cat <<'EOF'",
		"data: {\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"from claude\"}]}}",
		"data: {\"type\":\"result\",\"result\":\"ok\"}",
		"EOF",
	}, "\n")
	if err := os.WriteFile(claudeScript, []byte(claudeContent), 0755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, `"text":"from claude"`) {
		t.Fatalf("expected claude response in SSE body, got %q", body)
	}
}

func TestAPIChatUnauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIChatRejectsUnknownJSONFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello","bogus":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("expected invalid json error, got %q", rr.Body.String())
	}
}

func TestAPIChatRejectsTrailingJSONValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}{"prompt":"again"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("expected invalid json error, got %q", rr.Body.String())
	}
}

func TestAPIChatStreamingUnsupportedReturnsJSONError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := &noFlushRecorder{}
	handleAPIChat("birdy").ServeHTTP(rr, req)

	if rr.status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%q", rr.status, rr.body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("expected json content type, got %q", got)
	}
	if strings.Contains(rr.body.String(), "event:") {
		t.Fatalf("expected JSON error response, got SSE body %q", rr.body.String())
	}
}

func TestAPICommandRunsBirdThroughHostedMux(t *testing.T) {
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		"echo \"bird args:$*\"",
		"echo \"AUTH_TOKEN=${AUTH_TOKEN}\"",
		"echo \"CT0=${CT0}\"",
	}, "\n"))

	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	reqBody := bytes.NewBufferString(`{"command":"home","account":"alpha"}`)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer birdy")

	rr := httptest.NewRecorder()
	buildHostedMux("birdy", nil, t.TempDir()).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%q", err, rr.Body.String())
	}
	if !resp.OK || resp.Account != "alpha" || resp.ExitCode != 0 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if !strings.Contains(resp.Stdout, "bird args:home") {
		t.Fatalf("expected forwarded bird args, got %q", resp.Stdout)
	}
	if !strings.Contains(resp.Stdout, "AUTH_TOKEN=token-a") || !strings.Contains(resp.Stdout, "CT0=ct0-a") {
		t.Fatalf("expected auth env in stdout, got %q", resp.Stdout)
	}
}

func TestAPICommandFindsCommandAfterFlagValue(t *testing.T) {
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		"echo \"bird args:$*\"",
	}, "\n"))

	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]string{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})

	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	reqBody := bytes.NewBufferString(`{"args":["--format","json","home"],"account":"alpha"}`)
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%q", err, rr.Body.String())
	}
	if !strings.Contains(resp.Stdout, "bird args:--format json home") {
		t.Fatalf("expected full forwarded args, got %q", resp.Stdout)
	}
}

func TestAPICommandRejectsUnsupportedCommand(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"account","args":["list"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unsupported command") {
		t.Fatalf("expected unsupported command error, got %q", rr.Body.String())
	}
}

func TestAPICommandRejectsBareSeparatorWithoutCommand(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"args":["--format","json","--"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "missing command") {
		t.Fatalf("expected missing command error, got %q", rr.Body.String())
	}
}

func TestAPICommandRejectsUnknownJSONFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"home","bogus":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("expected invalid json error, got %q", rr.Body.String())
	}
}

func TestAPICommandRejectsTrailingJSONValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"home"}{"command":"mentions"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid json") {
		t.Fatalf("expected invalid json error, got %q", rr.Body.String())
	}
}

func TestAPICommandRejectsWriteCommandsInReadOnlyMode(t *testing.T) {
	t.Setenv("BIRDY_READ_ONLY", "1")

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"tweet","args":["hi"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "disabled in read-only mode") {
		t.Fatalf("expected read-only error, got %q", rr.Body.String())
	}
}

func TestAPICommandIncludesRecoveryWarningsInStderr(t *testing.T) {
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		"echo ok",
	}, "\n"))

	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "birdy")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "accounts.json"), []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write corrupt accounts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "state.json"), []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"env_user","auth_token":"t","ct0":"c"}]`)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"home"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp apiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%q", err, rr.Body.String())
	}
	if !strings.Contains(resp.Stderr, "recovered from corrupt account store") {
		t.Fatalf("expected account-store recovery warning, got %q", resp.Stderr)
	}
	if !strings.Contains(resp.Stderr, "recovered from corrupt state file") {
		t.Fatalf("expected state recovery warning, got %q", resp.Stderr)
	}
}

func TestAPICommandDoesNotPersistUsageOrRotationOnRunnerError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	writeStateFixture(t, home, "alpha", "sonnet")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(t.TempDir(), "missing-bird"))

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"home","strategy":"round-robin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	st, err := store.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	alpha, err := st.Get("alpha")
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	beta, err := st.Get("beta")
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if alpha.UseCount != 0 || beta.UseCount != 0 {
		t.Fatalf("expected no usage increments on runner error, alpha=%d beta=%d", alpha.UseCount, beta.UseCount)
	}

	loadedState, err := state.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loadedState.LastUsedName != "alpha" {
		t.Fatalf("expected last-used state unchanged on runner error, got %q", loadedState.LastUsedName)
	}
}

func TestAPICommandPersistsUsageAndRotationAfterSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	writeStateFixture(t, home, "alpha", "sonnet")
	t.Setenv("BIRDY_BIRD_PATH", writeFakeBirdScript(t, "#!/bin/sh\nexit 0\n"))

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/command", bytes.NewBufferString(`{"command":"home","strategy":"round-robin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")

	rr := httptest.NewRecorder()
	handleAPICommand("birdy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	st, err := store.Open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	alpha, err := st.Get("alpha")
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	beta, err := st.Get("beta")
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if alpha.UseCount != 0 {
		t.Fatalf("expected alpha unchanged, got use_count=%d", alpha.UseCount)
	}
	if beta.UseCount != 1 {
		t.Fatalf("expected beta usage incremented once, got %d", beta.UseCount)
	}
	if beta.LastUsed.IsZero() {
		t.Fatal("expected beta last_used to be set")
	}

	loadedState, err := state.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loadedState.LastUsedName != "beta" {
		t.Fatalf("expected last-used state updated after success, got %q", loadedState.LastUsedName)
	}
}

func writeFakeBirdScript(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bird")
	if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	return path
}

func writeAccountsFixture(t *testing.T, home string, accounts any) {
	t.Helper()
	configDir := filepath.Join(home, ".config", "birdy")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	data, err := json.Marshal(accounts)
	if err != nil {
		t.Fatalf("marshal accounts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "accounts.json"), data, 0600); err != nil {
		t.Fatalf("write accounts fixture: %v", err)
	}
}
