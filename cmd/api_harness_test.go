package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/opencode"
)

const harnessTestToken = "test-harness-token-not-a-real-secret-000"

func harnessTestConfig() harnessConfig {
	digest := sha256.Sum256([]byte(harnessTestToken))
	return harnessConfig{
		tokenHashes: map[string][sha256.Size]byte{"install-one": digest},
		model:       harnessModelConfig{backend: "claude-code", model: "claude-test-fixed"},
		enabled:     true,
	}
}

func harnessTestDependencies() harnessDependencies {
	return harnessDependencies{
		stream: func(_ context.Context, _ string, _ harnessModelConfig, _ string, emit func(claude.Event)) {
			emit(claude.Event{Type: claude.EventToken, Text: "answer"})
			emit(claude.Event{Type: claude.EventDone})
		},
		tokenLimiter: newHarnessRateLimiter(1_000, time.Minute, 256),
		ipLimiter:    newHarnessRateLimiter(1_000, time.Minute, 10_000),
		sem:          make(chan struct{}, 1),
		requestID:    func() string { return "req-123" },
	}
}

func harnessValidBody() string {
	return `{"version":"2","page_url":"https://x.com/home","visible_tweets":[{"id":"12345","url":"https://x.com/birdy/status/12345","author_handle":"birdy","text":"visible tweet"}],"prompt":"summarize"}`
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
	req := harnessRequest(harnessValidBody())
	req.Header.Set("X-Invite-Code", "shared-invite")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessConfig{}, harnessTestDependencies()).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"error":"harness_disabled"`) {
		t.Fatalf("unexpected disabled response: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHarnessTokenAndInviteCodeAreScopeIsolated(t *testing.T) {
	req := harnessRequest(harnessValidBody())
	req.Header.Del("Authorization")
	req.Header.Del("X-Birdy-Harness-Install-ID")
	req.Header.Set("X-Invite-Code", "shared-invite")
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("invite code reached harness endpoint: %d body=%q", rr.Code, rr.Body.String())
	}

	legacyReq := httptest.NewRequest(http.MethodPost, "http://birdy.example/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	legacyReq.Header.Set("Authorization", "Bearer "+harnessTestToken)
	legacyRR := httptest.NewRecorder()
	handleAPIChat("shared-invite").ServeHTTP(legacyRR, legacyReq)
	if legacyRR.Code != http.StatusUnauthorized {
		t.Fatalf("harness token reached legacy endpoint: %d", legacyRR.Code)
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
	topLevelFields := []string{
		`"model":"codex"`, `"cookies":"auth_token=x"`, `"raw_html":"<main>hidden</main>"`,
		`"dom":"hidden page text"`, `"visible_tweet_ids":["12345"]`, `"command":"home"`,
	}
	for _, field := range topLevelFields {
		t.Run(field, func(t *testing.T) {
			body := strings.TrimSuffix(harnessValidBody(), "}") + "," + field + "}"
			rr := httptest.NewRecorder()
			newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"invalid_json"`) {
				t.Fatalf("field %s was accepted: %d body=%q", field, rr.Code, rr.Body.String())
			}
		})
	}

	for _, field := range []string{`"cookies":"secret"`, `"html":"<b>hidden</b>"`, `"account":"private"`, `"extra":"value"`} {
		t.Run("nested "+field, func(t *testing.T) {
			body := strings.Replace(harnessValidBody(), `"text":"visible tweet"`, `"text":"visible tweet",`+field, 1)
			rr := httptest.NewRecorder()
			newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"invalid_json"`) {
				t.Fatalf("nested field %s was accepted: %d body=%q", field, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHarnessV1CutoverFailsClosed(t *testing.T) {
	tests := []struct {
		name, body, code string
	}{
		{"version one with v2 shape", strings.Replace(harnessValidBody(), `"version":"2"`, `"version":"1"`, 1), "unsupported_version"},
		{"legacy IDs", `{"version":"1","page_url":"https://x.com/home","visible_tweet_ids":["12345"],"prompt":"summarize"}`, "invalid_json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(tt.body))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"`+tt.code+`"`) {
				t.Fatalf("cutover request was not rejected: %d body=%q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHarnessPageAndTweetURLValidationIsExact(t *testing.T) {
	for _, candidate := range []string{"https://x.com/home", "https://twitter.com/user/status/12345?ref_src=test", "https://X.COM/search?q=go"} {
		if !validHarnessPageURL(candidate) {
			t.Errorf("expected valid page URL %q", candidate)
		}
	}
	for _, candidate := range []string{"http://x.com/home", "https://www.x.com/home", "https://x.com.evil.example/home", "https://user@x.com/home", "https://x.com:443/home", "https://x.com/home#hidden"} {
		if validHarnessPageURL(candidate) {
			t.Errorf("expected invalid page URL %q", candidate)
		}
	}

	tests := []struct {
		raw, id, wantURL string
		valid            bool
	}{
		{"https://x.com/birdy/status/12345", "12345", "https://x.com/birdy/status/12345", true},
		{"https://TWITTER.COM/i/status/67890", "67890", "https://twitter.com/i/status/67890", true},
		{"http://x.com/birdy/status/12345", "", "", false},
		{"https://www.x.com/birdy/status/12345", "", "", false},
		{"https://x.com/birdy/status/12345?hidden=1", "", "", false},
		{"https://x.com/birdy/status/12345/photo/1", "", "", false},
		{"https://x.com/birdy/status/12345#hidden", "", "", false},
		{"https://x.com/birdy/status/%31%32%33%34%35", "", "", false},
		{"https://x.com/birdy/status/12345@evil.example", "", "", false},
	}
	for _, tt := range tests {
		gotURL, gotID, ok := normalizeHarnessTweetURL(tt.raw)
		if ok != tt.valid || gotURL != tt.wantURL || gotID != tt.id {
			t.Errorf("normalizeHarnessTweetURL(%q) = (%q,%q,%v)", tt.raw, gotURL, gotID, ok)
		}
	}
}

func TestHarnessChatNormalizesTweetsInOrderAndUsesFixedModel(t *testing.T) {
	deps := harnessTestDependencies()
	var gotPrompt, gotSystem string
	var gotModel harnessModelConfig
	deps.stream = func(_ context.Context, prompt string, model harnessModelConfig, system string, emit func(claude.Event)) {
		gotPrompt, gotModel, gotSystem = prompt, model, system
		emit(claude.Event{Type: claude.EventDone})
	}
	body := `{"version":"2","page_url":"https://x.com/home","visible_tweets":[` +
		`{"id":"33333","url":"https://X.COM/first/status/33333","author_handle":"first","text":"first","created_at":"2026-08-09T12:00:00+09:00","reply_to_id":"11111"},` +
		`{"id":"11111","url":"https://twitter.com/second/status/11111","text":"second","quoted_tweet_id":"22222","repost_of_id":"44444"},` +
		`{"id":"33333","url":"https://x.com/first/status/33333","author_handle":"first","text":"first","created_at":"2026-08-09T03:00:00Z","reply_to_id":"11111"}` +
		`],"prompt":"compare","selected_text":"explicit selection"}`
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(body))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if gotModel != (harnessModelConfig{backend: "claude-code", model: "claude-test-fixed"}) || !strings.Contains(gotSystem, "No tools are available") || !strings.Contains(gotSystem, "untrusted client-quoted data") {
		t.Fatalf("unexpected model boundary: model=%#v system=%q", gotModel, gotSystem)
	}
	var input harnessModelInput
	if err := json.Unmarshal([]byte(gotPrompt), &input); err != nil {
		t.Fatalf("decode model input: %v prompt=%q", err, gotPrompt)
	}
	if len(input.VisibleTweets) != 2 || input.VisibleTweets[0].ID != "33333" || input.VisibleTweets[1].ID != "11111" {
		t.Fatalf("tweets not deduped in first-seen order: %#v", input.VisibleTweets)
	}
	if input.VisibleTweets[0].URL != "https://x.com/first/status/33333" || input.VisibleTweets[0].CreatedAt != "2026-08-09T03:00:00Z" {
		t.Fatalf("tweet not canonically normalized: %#v", input.VisibleTweets[0])
	}
}

func TestHarnessRejectsConflictingDuplicateTweets(t *testing.T) {
	body := `{"version":"2","page_url":"https://x.com/home","visible_tweets":[` +
		`{"id":"12345","url":"https://x.com/a/status/12345","text":"first"},` +
		`{"id":"12345","url":"https://x.com/a/status/12345","text":"changed"}` +
		`],"prompt":"compare"}`
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"conflicting_duplicate_tweet"`) {
		t.Fatalf("conflicting duplicate was accepted: %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestValidateHarnessVisibleTweetRelationsAndBounds(t *testing.T) {
	base := harnessVisibleTweet{ID: "12345", URL: "https://x.com/a/status/12345", AuthorHandle: "a", Text: "visible"}
	tests := []struct {
		name string
		edit func(*harnessVisibleTweet)
		code string
	}{
		{"URL ID mismatch", func(v *harnessVisibleTweet) { v.URL = "https://x.com/a/status/67890" }, "invalid_tweet_url"},
		{"invalid author", func(v *harnessVisibleTweet) { v.AuthorHandle = "@a" }, "invalid_author_handle"},
		{"empty text", func(v *harnessVisibleTweet) { v.Text = " \n" }, "missing_tweet_text"},
		{"large text", func(v *harnessVisibleTweet) { v.Text = strings.Repeat("x", harnessMaxTweetTextBytes+1) }, "tweet_text_too_large"},
		{"bad time", func(v *harnessVisibleTweet) { v.CreatedAt = "yesterday" }, "invalid_created_at"},
		{"bad reply", func(v *harnessVisibleTweet) { v.ReplyToID = "abc" }, "invalid_relation_id"},
		{"self quote", func(v *harnessVisibleTweet) { v.QuotedTweetID = v.ID }, "invalid_relation_id"},
		{"bad repost", func(v *harnessVisibleTweet) { v.RepostOfID = "01234" }, "invalid_relation_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := base
			tt.edit(&value)
			_, code, _ := validateHarnessVisibleTweet(value)
			if code != tt.code {
				t.Fatalf("got code %q, want %q", code, tt.code)
			}
		})
	}

	valid := base
	valid.ReplyToID = "23456"
	valid.QuotedTweetID = "34567"
	valid.RepostOfID = "45678"
	if _, code, message := validateHarnessVisibleTweet(valid); code != "" {
		t.Fatalf("independent external relationships rejected: %s %s", code, message)
	}
}

func TestValidateHarnessRequestBoundsAndContext(t *testing.T) {
	baseTweets := []harnessVisibleTweet{{ID: "12345", URL: "https://x.com/a/status/12345", Text: "visible"}}
	base := harnessChatRequest{
		Version: "2", PageURL: "https://x.com/home", Prompt: "summarize",
		VisibleTweets: &baseTweets,
	}
	tests := []struct {
		name string
		edit func(*harnessChatRequest)
		code string
	}{
		{"version", func(r *harnessChatRequest) { r.Version = "1" }, "unsupported_version"},
		{"prompt", func(r *harnessChatRequest) { r.Prompt = strings.Repeat("p", harnessMaxPromptBytes+1) }, "prompt_too_large"},
		{"selection", func(r *harnessChatRequest) { r.SelectedText = strings.Repeat("s", harnessMaxSelectionBytes+1) }, "selection_too_large"},
		{"missing array", func(r *harnessChatRequest) { r.VisibleTweets = nil }, "missing_visible_tweets"},
		{"count", func(r *harnessChatRequest) {
			values := make([]harnessVisibleTweet, harnessMaxVisibleTweets+1)
			r.VisibleTweets = &values
		}, "too_many_visible_tweets"},
		{"aggregate text", func(r *harnessChatRequest) {
			values := make([]harnessVisibleTweet, 0, 5)
			for i := 0; i < 5; i++ {
				id := fmt.Sprintf("%05d", 12345+i)
				values = append(values, harnessVisibleTweet{ID: id, URL: "https://x.com/a/status/" + id, Text: strings.Repeat("x", harnessMaxTweetTextBytes)})
			}
			r.VisibleTweets = &values
		}, "tweet_text_too_large"},
		{"context", func(r *harnessChatRequest) {
			values := []harnessVisibleTweet{}
			r.VisibleTweets = &values
		}, "missing_context"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			values := append([]harnessVisibleTweet(nil), (*base.VisibleTweets)...)
			req.VisibleTweets = &values
			tt.edit(&req)
			_, code, _ := validateHarnessRequest(req)
			if code != tt.code {
				t.Fatalf("got code %q, want %q", code, tt.code)
			}
		})
	}

	selectionOnly := base
	noTweets := []harnessVisibleTweet{}
	selectionOnly.VisibleTweets = &noTweets
	selectionOnly.SelectedText = "explicit"
	if _, code, message := validateHarnessRequest(selectionOnly); code != "" {
		t.Fatalf("selection-only request rejected: %s %s", code, message)
	}
}

func TestHarnessChatSSEIncludesRequestIDAndTerminalDone(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(_ context.Context, _ string, _ harnessModelConfig, _ string, emit func(claude.Event)) {
		emit(claude.Event{Type: claude.EventToken, Text: "answer"})
	}
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))

	if rr.Code != http.StatusOK || rr.Header().Get("X-Request-ID") != "req-123" || rr.Header().Get("X-Birdy-Harness-Version") != "2" {
		t.Fatalf("unexpected response %d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"request_id":"req-123"`) || !strings.Contains(body, "event: token\n") || strings.Count(body, "event: done\n") != 1 {
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
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost || rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected OPTIONS response: %d headers=%v body=%q", rr.Code, rr.Header(), rr.Body.String())
	}
}

func TestHarnessChatRejectsBodyOver64KiBAndInvalidUTF8(t *testing.T) {
	t.Run("body", func(t *testing.T) {
		body := strings.TrimSuffix(harnessValidBody(), "}") + `,"selected_text":"` + strings.Repeat("x", harnessMaxBodyBytes) + `"}`
		rr := httptest.NewRecorder()
		newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
		if rr.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rr.Body.String(), `"error":"body_too_large"`) {
			t.Fatalf("oversized body was not rejected: %d body=%q", rr.Code, rr.Body.String())
		}
	})
	t.Run("UTF-8", func(t *testing.T) {
		payload := append([]byte(`{"version":"2","page_url":"https://x.com/home","visible_tweets":[],"prompt":"`), 0xff)
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
	})
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

func TestHarnessChatBlocksToolEvents(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(_ context.Context, _ string, _ harnessModelConfig, _ string, emit func(claude.Event)) {
		emit(claude.Event{Type: claude.EventToolUse, Command: "birdy home"})
		emit(claude.Event{Type: claude.EventDone})
	}
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()))
	if strings.Contains(rr.Body.String(), "birdy home") || strings.Contains(rr.Body.String(), "event: tool_use") || !strings.Contains(rr.Body.String(), "disabled tool") {
		t.Fatalf("tool boundary failed: %q", rr.Body.String())
	}
}

func TestHarnessChatDeadlineEmitsErrorThenExactlyOneDone(t *testing.T) {
	deps := harnessTestDependencies()
	deps.stream = func(ctx context.Context, _ string, _ harnessModelConfig, _ string, emit func(claude.Event)) {
		<-ctx.Done()
		emit(claude.Event{Type: claude.EventDone})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	rr := httptest.NewRecorder()
	newHarnessChatHandler(harnessTestConfig(), deps).ServeHTTP(rr, harnessRequest(harnessValidBody()).WithContext(ctx))
	body := rr.Body.String()
	if !strings.Contains(body, "request timed out") || strings.Count(body, "event: done\n") != 1 {
		t.Fatalf("deadline contract failed: %q", body)
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
			t.Fatalf("token limit bypassed across IPs: %d", rr.Code)
		}
	})
	t.Run("untrusted forwarded IP", func(t *testing.T) {
		deps := harnessTestDependencies()
		deps.ipLimiter = newHarnessRateLimiter(1, time.Minute, 10_000)
		handler := newHarnessChatHandler(harnessTestConfig(), deps)
		first := harnessRequest(harnessValidBody())
		first.Header.Set("X-Forwarded-For", "198.51.100.1")
		handler.ServeHTTP(httptest.NewRecorder(), first)
		second := harnessRequest(harnessValidBody())
		second.Header.Set("X-Forwarded-For", "198.51.100.2")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, second)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("spoofed forwarded header bypassed IP limit: %d", rr.Code)
		}
	})
}

func TestLoadHarnessConfigRequiresOnlyValidHashedTokens(t *testing.T) {
	digest := sha256.Sum256([]byte(harnessTestToken))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(digest[:])+`"}`)
	t.Setenv("BIRDY_HARNESS_ACCOUNTS", `not-even-json-and-must-be-ignored`)
	t.Setenv(harnessModelEnv, "claude-fixed")
	t.Setenv(harnessTrustProxyEnv, "true")
	config, err := loadHarnessConfig("distinct-shared-invite")
	if err != nil || !config.enabled || config.model != (harnessModelConfig{backend: "claude-code", model: "claude-fixed"}) || !config.trustProxy {
		t.Fatalf("valid token-only harness config failed: config=%#v err=%v", config, err)
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

func TestLoadHarnessConfigRejectsInviteCodeReuse(t *testing.T) {
	inviteCode := "same-secret-same-secret-same-secret"
	digest := sha256.Sum256([]byte(inviteCode))
	t.Setenv(harnessTokenHashesEnv, `{"install-one":"`+hex.EncodeToString(digest[:])+`"}`)
	if _, err := loadHarnessConfig(inviteCode); err == nil || !strings.Contains(err.Error(), "must not reuse") {
		t.Fatalf("expected invite collision failure, got %v", err)
	}
}

func TestLoadHarnessModelConfigIsServerFixedAndBounded(t *testing.T) {
	t.Run("Claude default preserves compatibility", func(t *testing.T) {
		t.Setenv(harnessBackendEnv, "")
		t.Setenv(harnessModelEnv, "")
		got, err := loadHarnessModelConfig()
		if err != nil || got != (harnessModelConfig{backend: "claude-code", model: "sonnet"}) {
			t.Fatalf("default config = %#v, %v", got, err)
		}
	})

	t.Run("exact OpenCode Go route", func(t *testing.T) {
		t.Setenv(harnessBackendEnv, "opencode")
		t.Setenv(harnessModelEnv, opencode.ModelDeepSeekV4Flash)
		t.Setenv("OPENCODE_API_KEY", "test-model-provider-key")
		got, err := loadHarnessModelConfig()
		if err != nil || got != (harnessModelConfig{backend: "opencode", model: opencode.ModelDeepSeekV4Flash}) {
			t.Fatalf("OpenCode config = %#v, %v", got, err)
		}
	})

	for _, tt := range []struct {
		name, backend, model, key string
	}{
		{"wrong claimed V2 model", "opencode", "opencode-go/deepseek-v2-flash", "key"},
		{"other OpenCode model", "opencode", "opencode-go/kimi-k3", "key"},
		{"missing OpenCode key", "opencode", opencode.ModelDeepSeekV4Flash, ""},
		{"unknown provider", "openrouter", "deepseek-v4-flash", "key"},
		{"provider in Claude model", "claude-code", opencode.ModelDeepSeekV4Flash, "key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(harnessBackendEnv, tt.backend)
			t.Setenv(harnessModelEnv, tt.model)
			t.Setenv("OPENCODE_API_KEY", tt.key)
			if _, err := loadHarnessModelConfig(); err == nil {
				t.Fatalf("accepted backend=%q model=%q", tt.backend, tt.model)
			}
		})
	}
}

func TestHarnessRequestCannotSelectProviderOrModel(t *testing.T) {
	for _, field := range []string{
		`"backend":"opencode"`,
		`"provider":"opencode-go"`,
		`"model":"opencode-go/deepseek-v4-flash"`,
		`"fallback_model":"sonnet"`,
	} {
		body := strings.TrimSuffix(harnessValidBody(), "}") + "," + field + "}"
		rr := httptest.NewRecorder()
		newHarnessChatHandler(harnessTestConfig(), harnessTestDependencies()).ServeHTTP(rr, harnessRequest(body))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"error":"invalid_json"`) {
			t.Fatalf("request-controlled model field %s accepted: %d %q", field, rr.Code, rr.Body.String())
		}
	}
}
