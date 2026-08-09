package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/xapi"
)

const harnessTestToken = "test-harness-token-not-a-real-secret-000"
const harnessTestAccounts = `[{"name":"harness-public","auth_token":"test-auth","ct0":"test-ct0","read_only":true}]`

func harnessTestConfig() harnessConfig {
	digest := sha256.Sum256([]byte(harnessTestToken))
	return harnessConfig{
		tokenHashes: map[string][sha256.Size]byte{"install-one": digest},
		model:       "claude-test-fixed",
		enabled:     true,
	}
}

func harnessTestDependencies() harnessDependencies {
	return harnessDependencies{
		fetch: func(_ context.Context, ids []string) ([]harnessTweetContext, error) {
			posts := make([]harnessTweetContext, 0, len(ids))
			for _, id := range ids {
				posts = append(posts, harnessTweetContext{ID: id, URL: "https://x.com/i/status/" + id, Text: "tweet " + id})
			}
			return posts, nil
		},
		stream: func(_ context.Context, _, _, _ string, emit func(claude.Event)) {
			emit(claude.Event{Type: claude.EventToken, Text: "answer"})
			emit(claude.Event{Type: claude.EventDone})
		},
		tokenLimiter: newHarnessRateLimiter(1_000, time.Minute, 256),
		ipLimiter:    newHarnessRateLimiter(1_000, time.Minute, 10_000),
		sem:          make(chan struct{}, 1),
		requestID:    func() string { return "req-123" },
		readLimiter:  newHarnessRateLimiter(1_000, time.Minute, 256),
		globalReads:  newHarnessRateLimiter(1_000, time.Minute, 1),
		fetchTimeout: time.Second,
	}
}

func harnessValidBody() string {
	return `{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["12345"],"prompt":"summarize"}`
}

func harnessRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/harness/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+harnessTestToken)
	req.Header.Set("X-Birdy-Harness-Install-ID", "install-one")
	req.RemoteAddr = "192.0.2.10:1234"
	return req
}

func TestHarnessChatIsDisabledWithoutScopedTokens(t *testing.T) {
	deps := harnessTestDependencies()
	req := harnessRequest(harnessValidBody())
	req.Header.Set("X-Invite-Code", "shared-invite")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessConfig{}, deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected disabled endpoint, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"error":"harness_disabled"`) {
		t.Fatalf("unexpected disabled response %q", rr.Body.String())
	}
}

func TestHarnessTokenAndInviteCodeAreScopeIsolated(t *testing.T) {
	deps := harnessTestDependencies()
	req := harnessRequest(harnessValidBody())
	req.Header.Del("Authorization")
	req.Header.Del("X-Birdy-Harness-Install-ID")
	req.Header.Set("X-Invite-Code", "shared-invite")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("invite code reached harness endpoint: %d body=%q", rr.Code, rr.Body.String())
	}

	legacyReq := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	legacyReq.Header.Set("Authorization", "Bearer "+harnessTestToken)
	legacyRR := httptest.NewRecorder()
	handleAPIChat("shared-invite").ServeHTTP(legacyRR, legacyReq)
	if legacyRR.Code != http.StatusUnauthorized {
		t.Fatalf("harness token reached legacy endpoint: %d body=%q", legacyRR.Code, legacyRR.Body.String())
	}
	commandReq := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/command", bytes.NewBufferString(`{"command":"home"}`))
	commandReq.Header.Set("Authorization", "Bearer "+harnessTestToken)
	commandRR := httptest.NewRecorder()
	handleAPICommand("shared-invite").ServeHTTP(commandRR, commandReq)
	if commandRR.Code != http.StatusUnauthorized {
		t.Fatalf("harness token reached command endpoint: %d", commandRR.Code)
	}
	multiReq := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/multi-command", bytes.NewBufferString(`{"operations":[{"id":"one","command":"home"}]}`))
	multiReq.Header.Set("Authorization", "Bearer "+harnessTestToken)
	multiRR := httptest.NewRecorder()
	handleAPIMultiCommand("shared-invite").ServeHTTP(multiRR, multiReq)
	if multiRR.Code != http.StatusUnauthorized {
		t.Fatalf("harness token reached multi-command endpoint: %d", multiRR.Code)
	}
}

func TestHarnessChatStrictSchemaRejectsSensitiveAndCapabilityFields(t *testing.T) {
	fields := []string{
		`"model":"codex"`,
		`"cookies":"auth_token=x"`,
		`"raw_html":"<main>hidden</main>"`,
		`"dom":"hidden page text"`,
		`"url":"https://example.com"`,
		`"command":"home"`,
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			body := strings.TrimSuffix(harnessValidBody(), "}") + "," + field + "}"
			rr := httptest.NewRecorder()
			newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"invalid_json"`) {
				t.Fatalf("field %s was accepted: %d body=%q", field, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHarnessPageURLValidationIsExact(t *testing.T) {
	valid := []string{
		"https://x.com/home",
		"https://twitter.com/user/status/12345?ref_src=test",
		"https://X.COM/search?q=go",
	}
	for _, candidate := range valid {
		if !validHarnessPageURL(candidate) {
			t.Errorf("expected valid URL %q", candidate)
		}
	}

	invalid := []string{
		"http://x.com/home",
		"https://www.x.com/home",
		"https://x.com.evil.example/home",
		"https://evil.example/x.com/home",
		"https://user@x.com/home",
		"https://x.com:443/home",
		"https://x.com/home#hidden",
		"javascript:alert(1)",
	}
	for _, candidate := range invalid {
		if validHarnessPageURL(candidate) {
			t.Errorf("expected invalid URL %q", candidate)
		}
	}
}

func TestHarnessChatNormalizesOrderedTweetIDsAndUsesFixedModel(t *testing.T) {
	deps := harnessTestDependencies()
	var fetched []string
	deps.fetch = func(_ context.Context, ids []string) ([]harnessTweetContext, error) {
		fetched = append([]string(nil), ids...)
		return []harnessTweetContext{{ID: ids[0], URL: "https://x.com/i/status/" + ids[0], Text: "visible"}}, nil
	}
	var gotPrompt, gotModel, gotSystem string
	deps.stream = func(_ context.Context, prompt, model, system string, emit func(claude.Event)) {
		gotPrompt, gotModel, gotSystem = prompt, model, system
		emit(claude.Event{Type: claude.EventDone})
	}
	body := `{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["33333","11111","33333","22222"],"prompt":"compare","selected_text":"explicit selection"}`
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(body))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if strings.Join(fetched, ",") != "33333,11111,22222" {
		t.Fatalf("IDs not deduped in order: %#v", fetched)
	}
	if gotModel != "claude-test-fixed" {
		t.Fatalf("expected fixed server model, got %q", gotModel)
	}
	if !strings.Contains(gotSystem, "No tools are available") || !strings.Contains(gotPrompt, `"user_prompt":"compare"`) {
		t.Fatalf("unexpected model boundary: system=%q prompt=%q", gotSystem, gotPrompt)
	}
}

func TestHarnessChatSSEIncludesRequestIDAndTerminalDone(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(_ context.Context, _, _, _ string, emit func(claude.Event)) {
		emit(claude.Event{Type: claude.EventToken, Text: "answer"})
		// Handler must repair a backend that exits without done.
	}
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))

	if rr.Code != http.StatusOK || rr.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("unexpected response %d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"request_id":"req-123"`) || !strings.Contains(body, "event: token\n") || !strings.Contains(body, "event: done\n") {
		t.Fatalf("incomplete SSE contract %q", body)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS header %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestHarnessChatDoesNotExposeCORSPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "http://birdy.example/api/harness/chat", nil)
	req.Header.Set("Origin", "https://x.com")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("unexpected OPTIONS response: %d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.String())
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "" || rr.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Fatalf("unexpected CORS preflight headers: %v", rr.Header())
	}
}

func TestHarnessChatRejectsBodyOver16KiB(t *testing.T) {
	body := `{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["12345"],"prompt":"` +
		strings.Repeat("x", harnessMaxBodyBytes) + `"}`
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
	if rr.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rr.Body.String(), `"error":"body_too_large"`) {
		t.Fatalf("oversized body was not rejected: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHarnessChatRejectsInvalidUTF8(t *testing.T) {
	payload := append([]byte(`{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["12345"],"prompt":"`), 0xff)
	payload = append(payload, []byte(`"}`)...)
	req := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/harness/chat", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+harnessTestToken)
	req.Header.Set("X-Birdy-Harness-Install-ID", "install-one")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"invalid_utf8"`) {
		t.Fatalf("invalid UTF-8 was not rejected: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHarnessChatFailsFastWhenCapacityIsFull(t *testing.T) {
	deps := harnessTestDependencies()
	deps.sem <- struct{}{}
	defer func() { <-deps.sem }()
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"error":"capacity_exhausted"`) {
		t.Fatalf("full capacity did not fail fast: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHarnessChatChargesTweetReadBudgetByUniqueID(t *testing.T) {
	deps := harnessTestDependencies()
	deps.readLimiter = newHarnessRateLimiter(1, time.Minute, 256)
	body := `{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["12345","67890"],"prompt":"compare"}`
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(body))
	if rr.Code != http.StatusTooManyRequests || !strings.Contains(rr.Body.String(), `"error":"tweet_read_rate_limited"`) {
		t.Fatalf("tweet read cost was not charged: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHarnessChatBlocksToolEvents(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(_ context.Context, _, _, _ string, emit func(claude.Event)) {
		emit(claude.Event{Type: claude.EventToolUse, Command: "birdy home"})
		emit(claude.Event{Type: claude.EventDone})
	}
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))
	if strings.Contains(rr.Body.String(), "birdy home") || strings.Contains(rr.Body.String(), "event: tool_use") {
		t.Fatalf("tool capability leaked into stream %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "disabled tool") || !strings.Contains(rr.Body.String(), "event: done") {
		t.Fatalf("expected sanitized error and done %q", rr.Body.String())
	}
}

func TestHarnessChatDeadlineEmitsErrorThenExactlyOneDone(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(ctx context.Context, _, _, _ string, emit func(claude.Event)) {
		<-ctx.Done()
		emit(claude.Event{Type: claude.EventDone})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req := harnessRequest(harnessValidBody()).WithContext(ctx)
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "request timed out") {
		t.Fatalf("deadline was not reported: %q", body)
	}
	if got := strings.Count(body, "event: done\n"); got != 1 {
		t.Fatalf("expected exactly one done event, got %d in %q", got, body)
	}
}

func TestHarnessChatBoundsTweetContextFetchBeforeStreaming(t *testing.T) {
	deps := harnessTestDependencies()
	deps.fetchTimeout = 5 * time.Millisecond
	var failure harnessTweetFetchFailure
	deps.reportFetchFailure = func(got harnessTweetFetchFailure) { failure = got }
	deps.fetch = func(ctx context.Context, _ []string) ([]harnessTweetContext, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))
	if rr.Code != http.StatusGatewayTimeout || !strings.Contains(rr.Body.String(), `"error":"tweet_context_timeout"`) {
		t.Fatalf("tweet fetch timeout was not bounded: %d body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "event:") {
		t.Fatalf("pre-stream timeout unexpectedly used SSE: %q", rr.Body.String())
	}
	if failure.Class != "timeout" || failure.RequestID != "req-123" || failure.TweetCount != 1 {
		t.Fatalf("timeout failure report = %#v", failure)
	}
}

func TestHarnessTweetFetchFailureClassification(t *testing.T) {
	secretDNSFailure := &url.Error{
		Op:  "Get",
		URL: "https://secret-dns.example/status/12345",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "secret resolver detail", Name: "secret-dns.example"}},
	}
	tests := []struct {
		name       string
		err        error
		wantClass  string
		wantStage  string
		wantStatus int
	}{
		{
			name: "upstream HTTP",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &xapi.APIError{
				StatusCode: http.StatusForbidden, Message: "sensitive upstream body",
			}},
			wantClass: "upstream_http", wantStage: "tweet_read", wantStatus: http.StatusForbidden,
		},
		{
			name: "upstream response",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &xapi.APIError{
				Message: "sensitive schema excerpt",
			}},
			wantClass: "upstream_response", wantStage: "tweet_read",
		},
		{
			name: "upstream rate limit",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &xapi.APIError{
				StatusCode: http.StatusTooManyRequests, RateLimited: true, Message: "sensitive upstream body",
			}},
			wantClass: "upstream_rate_limited", wantStage: "tweet_read", wantStatus: http.StatusTooManyRequests,
		},
		{
			name:      "DNS through url and net operation wrappers",
			err:       &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: secretDNSFailure},
			wantClass: "transport_dns", wantStage: "tweet_read",
		},
		{
			name: "TCP connect through wrappers",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &url.Error{
				Op: "Get", URL: "https://secret-connect.example/status/12345",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("secret dial detail")},
			}},
			wantClass: "transport_connect", wantStage: "tweet_read",
		},
		{
			name: "TLS cause wins over enclosing connect operation",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &url.Error{
				Op: "Get", URL: "https://secret-tls.example/status/12345",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: x509.UnknownAuthorityError{}},
			}},
			wantClass: "transport_tls", wantStage: "tweet_read",
		},
		{
			name: "proxy connect operation",
			err: &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &url.Error{
				Op: "Get", URL: "https://secret-target.example/status/12345",
				Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("secret proxy detail")},
			}},
			wantClass: "transport_proxy", wantStage: "tweet_read",
		},
		{
			name:      "other URL transport",
			err:       &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: &url.Error{Op: "Get", URL: "https://secret-other.example/status/12345", Err: errors.New("secret other detail")}},
			wantClass: "transport_other", wantStage: "tweet_read",
		},
		{
			name:      "configuration",
			err:       &harnessTweetFetchError{stage: harnessTweetFetchStageClientInit, cause: errors.New("secret config detail")},
			wantClass: "configuration", wantStage: "client_init",
		},
		{
			name: "timeout precedence over DNS", err: errors.Join(context.DeadlineExceeded, secretDNSFailure), wantClass: "timeout",
		},
		{
			name:      "configuration precedence over DNS",
			err:       &harnessTweetFetchError{stage: harnessTweetFetchStageClientInit, cause: secretDNSFailure},
			wantClass: "configuration", wantStage: "client_init",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyHarnessTweetFetchFailure(tt.err, "req-safe", 2, 17*time.Millisecond)
			if got.Class != tt.wantClass || got.Stage != tt.wantStage || got.UpstreamStatus != tt.wantStatus || got.ElapsedMS != 17 {
				t.Fatalf("classification = %#v", got)
			}
		})
	}
}

func TestHarnessTransportSubtypeLogsDoNotLeakWrappedDetails(t *testing.T) {
	tests := []struct {
		name      string
		wantClass string
		err       error
	}{
		{
			name: "DNS", wantClass: "transport_dns",
			err: &url.Error{Op: "Get", URL: "https://secret-dns.example/status/12345", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "secret DNS detail", Name: "secret-dns.example"},
			}},
		},
		{
			name: "connect", wantClass: "transport_connect",
			err: &url.Error{Op: "Get", URL: "https://secret-connect.example/status/12345", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: errors.New("secret connect detail 192.0.2.1")},
			},
		},
		{
			name: "TLS", wantClass: "transport_tls",
			err: &url.Error{Op: "Get", URL: "https://secret-tls.example/status/12345", Err: x509.HostnameError{
				Host: "secret-tls.example",
			}},
		},
		{
			name: "proxy", wantClass: "transport_proxy",
			err: &url.Error{Op: "Get", URL: "https://secret-target.example/status/12345", Err: &net.OpError{
				Op: "proxyconnect", Net: "tcp", Err: errors.New("secret-proxy.example 192.0.2.2")},
			},
		},
		{
			name: "other", wantClass: "transport_other",
			err: &url.Error{Op: "Get", URL: "https://secret-other.example/status/12345", Err: errors.New("secret other detail")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: fmt.Errorf("secret account wrapper: %w", tt.err)}
			failure := classifyHarnessTweetFetchFailure(wrapped, "req-safe", 1, time.Millisecond)
			if failure.Class != tt.wantClass {
				t.Fatalf("class = %q, want %q", failure.Class, tt.wantClass)
			}
			var output bytes.Buffer
			logHarnessTweetFetchFailure(slog.New(slog.NewTextHandler(&output, nil)), failure)
			logged := output.String()
			for _, forbidden := range []string{"secret", "status/12345", "192.0.2.", "account"} {
				if strings.Contains(strings.ToLower(logged), forbidden) {
					t.Fatalf("sanitized %s log leaked %q: %s", tt.name, forbidden, logged)
				}
			}
			if !strings.Contains(logged, "failure_class="+tt.wantClass) {
				t.Fatalf("sanitized log missing class: %s", logged)
			}
		})
	}
}

func TestHarnessTweetFetchFailureLogAndResponseDoNotLeak(t *testing.T) {
	const sensitive = "auth_token=secret-token ct0=secret-ct0 account=harness-public https://x.com/status/12345 prompt-secret"
	deps := harnessTestDependencies()
	deps.fetch = func(context.Context, []string) ([]harnessTweetContext, error) {
		return nil, &harnessTweetFetchError{stage: harnessTweetFetchStageRead, cause: fmt.Errorf("account read: %w", &xapi.APIError{
			StatusCode: http.StatusForbidden,
			Message:    sensitive,
		})}
	}
	var failure harnessTweetFetchFailure
	deps.reportFetchFailure = func(got harnessTweetFetchFailure) { failure = got }
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))

	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), `"error":"tweet_context_unavailable"`) {
		t.Fatalf("upstream failure status = %d body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), sensitive) || strings.Contains(rr.Body.String(), "upstream_http") || strings.Contains(rr.Body.String(), "403") {
		t.Fatalf("response leaked internal failure detail: %q", rr.Body.String())
	}
	if failure.Class != "upstream_http" || failure.UpstreamStatus != http.StatusForbidden || failure.RequestID != "req-123" {
		t.Fatalf("failure report = %#v", failure)
	}

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	logHarnessTweetFetchFailure(logger, failure)
	logged := logOutput.String()
	for _, forbidden := range []string{sensitive, "secret-token", "secret-ct0", "harness-public", "status/12345", "prompt-secret"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("sanitized log leaked %q: %s", forbidden, logged)
		}
	}
	for _, required := range []string{"request_id=req-123", "failure_class=upstream_http", "stage=tweet_read", "tweet_count=1", "upstream_status=403"} {
		if !strings.Contains(logged, required) {
			t.Fatalf("sanitized log missing %q: %s", required, logged)
		}
	}
}

func TestHarnessChatEnforcesSeparateTokenAndIPLimits(t *testing.T) {
	t.Run("token across IPs", func(t *testing.T) {
		deps := harnessTestDependencies()
		deps.tokenLimiter = newHarnessRateLimiter(1, time.Minute, 256)
		handler := newHarnessChatHandler(harnessTestConfig(), deps)
		first := harnessRequest(harnessValidBody())
		first.RemoteAddr = "192.0.2.1:1"
		handler.ServeHTTP(httptest.NewRecorder(), first)
		second := harnessRequest(harnessValidBody())
		second.RemoteAddr = "192.0.2.2:2"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, second)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("token limit bypassed across IPs: %d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("untrusted forwarded IP", func(t *testing.T) {
		deps := harnessTestDependencies()
		deps.ipLimiter = newHarnessRateLimiter(1, time.Minute, 10_000)
		config := harnessTestConfig()
		config.trustProxy = false
		handler := newHarnessChatHandler(config, deps)
		first := harnessRequest(harnessValidBody())
		first.Header.Set("X-Forwarded-For", "198.51.100.1")
		handler.ServeHTTP(httptest.NewRecorder(), first)
		second := harnessRequest(harnessValidBody())
		second.Header.Set("X-Forwarded-For", "198.51.100.2")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, second)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("spoofed X-Forwarded-For bypassed IP limit: %d body=%q", rr.Code, rr.Body.String())
		}
	})
}

func TestValidateHarnessRequestBoundsAndContext(t *testing.T) {
	base := harnessChatRequest{
		Version:         "1",
		PageURL:         "https://x.com/home",
		VisibleTweetIDs: []string{"12345"},
		Prompt:          "summarize",
	}
	tests := []struct {
		name string
		edit func(*harnessChatRequest)
		code string
	}{
		{"version", func(r *harnessChatRequest) { r.Version = "2" }, "unsupported_version"},
		{"prompt", func(r *harnessChatRequest) { r.Prompt = strings.Repeat("p", harnessMaxPromptBytes+1) }, "prompt_too_large"},
		{"selection", func(r *harnessChatRequest) { r.SelectedText = strings.Repeat("s", harnessMaxSelectionBytes+1) }, "selection_too_large"},
		{"id", func(r *harnessChatRequest) { r.VisibleTweetIDs = []string{"123abc"} }, "invalid_tweet_id"},
		{"short id", func(r *harnessChatRequest) { r.VisibleTweetIDs = []string{"1234"} }, "invalid_tweet_id"},
		{"count", func(r *harnessChatRequest) { r.VisibleTweetIDs = make([]string, harnessMaxVisibleTweetIDs+1) }, "too_many_tweet_ids"},
		{"context", func(r *harnessChatRequest) { r.VisibleTweetIDs = nil }, "missing_context"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			req.VisibleTweetIDs = append([]string(nil), base.VisibleTweetIDs...)
			tt.edit(&req)
			_, code, _ := validateHarnessRequest(req)
			if code != tt.code {
				t.Fatalf("got code %q, want %q", code, tt.code)
			}
		})
	}
}

func TestLoadHarnessConfigRequiresValidHashedTokens(t *testing.T) {
	digest := sha256.Sum256([]byte(harnessTestToken))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(digest[:])+`"}`)
	t.Setenv(harnessAccountsEnv, harnessTestAccounts)
	t.Setenv(harnessModelEnv, "claude-fixed")
	t.Setenv(harnessTrustProxyEnv, "true")
	config, err := loadHarnessConfig("distinct-shared-invite")
	if err != nil {
		t.Fatalf("load valid config: %v", err)
	}
	if !config.enabled || config.model != "claude-fixed" || !config.trustProxy {
		t.Fatalf("valid harness config was not enabled: %#v", config)
	}

	t.Setenv(harnessTokenHashesEnv, `{"install-one":"raw-secret-is-not-a-hash"}`)
	if _, err := loadHarnessConfig("distinct-shared-invite"); err == nil {
		t.Fatal("invalid raw token config did not fail")
	}

	hexDigest := hex.EncodeToString(digest[:])
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hexDigest+`","install-two":"`+hexDigest+`"}`)
	if _, err := loadHarnessConfig("distinct-shared-invite"); err == nil {
		t.Fatal("duplicate per-install tokens did not fail")
	}
}

func TestLoadHarnessConfigRejectsInviteCodeReuseAndMissingDedicatedAccounts(t *testing.T) {
	inviteCode := "same-secret-same-secret-same-secret"
	digest := sha256.Sum256([]byte(inviteCode))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(digest[:])+`"}`)
	t.Setenv(harnessAccountsEnv, harnessTestAccounts)
	if _, err := loadHarnessConfig(inviteCode); err == nil || !strings.Contains(err.Error(), "must not reuse") {
		t.Fatalf("expected invite collision failure, got %v", err)
	}

	other := sha256.Sum256([]byte(harnessTestToken))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(other[:])+`"}`)
	t.Setenv(harnessAccountsEnv, "")
	if _, err := loadHarnessConfig(inviteCode); err == nil || !strings.Contains(err.Error(), harnessAccountsEnv) {
		t.Fatalf("expected missing dedicated accounts failure, got %v", err)
	}
}

func TestLoadHarnessConfigRejectsAllDisabledDedicatedAccounts(t *testing.T) {
	digest := sha256.Sum256([]byte(harnessTestToken))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(digest[:])+`"}`)
	const disabledAccounts = `[{"name":"harness-public","auth_token":"fake-secret-auth","ct0":"fake-secret-ct0","read_only":true,"disabled":true}]`
	t.Setenv(harnessAccountsEnv, disabledAccounts)
	_, err := loadHarnessConfig("distinct-shared-invite")
	if err == nil || !strings.Contains(err.Error(), "at least one enabled read-only account") {
		t.Fatalf("all-disabled pool error = %v", err)
	}
	if strings.Contains(err.Error(), "fake-secret") || strings.Contains(err.Error(), "harness-public") {
		t.Fatalf("configuration error leaked account detail: %v", err)
	}
}

func TestHarnessErrorShapeIsDeterministic(t *testing.T) {
	deps := harnessTestDependencies()
	req := harnessRequest(harnessValidBody())
	req.Header.Set("Authorization", "Bearer wrong-wrong-wrong-wrong-wrong-wrong")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, req)

	var response harnessErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%q", err, rr.Body.String())
	}
	if response.OK || response.Error != "unauthorized" || response.RequestID != "req-123" || response.Message == "" {
		t.Fatalf("unexpected error contract %#v", response)
	}
}

func TestHarnessRequestIDIsUUIDv4(t *testing.T) {
	id := newHarnessRequestID()
	if matched, _ := regexp.MatchString(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, id); !matched {
		t.Fatalf("request ID is not UUIDv4-shaped: %q", id)
	}
}
