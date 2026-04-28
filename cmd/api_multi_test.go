package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/state"
)

func multiCommandRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/multi-command", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", "birdy")
	return req
}

func TestAPIMultiCommandHappyPath(t *testing.T) {
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		`echo "args:$*"`,
		`echo "auth=$AUTH_TOKEN"`,
	}, "\n"))

	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b"},
	})
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	body := `{"operations":[
		{"id":"a","command":"search","args":["AI"]},
		{"id":"b","command":"user-tweets","args":["@OpenAI"]},
		{"id":"c","args":["news"]}
	]}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rr.Body.String())
	}
	if !resp.OK || len(resp.Results) != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	byID := map[string]apiMultiOpResult{}
	for _, r := range resp.Results {
		byID[r.ID] = r
	}
	for _, id := range []string{"a", "b", "c"} {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("missing result for %q", id)
		}
		if !r.OK || r.ExitCode != 0 || r.Error != "" {
			t.Fatalf("op %q failed: %+v", id, r)
		}
		if !strings.Contains(r.Stdout, "args:") {
			t.Fatalf("op %q stdout missing args echo: %q", id, r.Stdout)
		}
		if r.Account != "alpha" && r.Account != "beta" {
			t.Fatalf("op %q account not from store: %q", id, r.Account)
		}
	}
}

func TestAPIMultiCommandPerOpUnsupportedCommand(t *testing.T) {
	birdPath := writeFakeBirdScript(t, "#!/bin/sh\necho ok\n")
	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	body := `{"operations":[
		{"id":"good","command":"search","args":["AI"]},
		{"id":"bad","command":"account","args":["list"]}
	]}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]apiMultiOpResult{}
	for _, r := range resp.Results {
		byID[r.ID] = r
	}
	if !byID["good"].OK {
		t.Fatalf("good op should succeed: %+v", byID["good"])
	}
	if byID["bad"].OK || byID["bad"].Error != "unsupported command" {
		t.Fatalf("bad op should report unsupported command, got %+v", byID["bad"])
	}
}

func TestAPIMultiCommandPerOpReadOnlyMutation(t *testing.T) {
	t.Setenv("BIRDY_READ_ONLY", "1")
	birdPath := writeFakeBirdScript(t, "#!/bin/sh\necho ok\n")
	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	body := `{"operations":[
		{"id":"read","command":"search","args":["AI"]},
		{"id":"write","command":"tweet","args":["hi"]}
	]}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]apiMultiOpResult{}
	for _, r := range resp.Results {
		byID[r.ID] = r
	}
	if !byID["read"].OK {
		t.Fatalf("read op should succeed in read-only mode: %+v", byID["read"])
	}
	if byID["write"].OK {
		t.Fatalf("write op should be rejected in read-only mode")
	}
	if !strings.Contains(byID["write"].Error, "read-only") {
		t.Fatalf("expected read-only error, got %q", byID["write"].Error)
	}
}

func TestAPIMultiCommandRejectsTooManyOps(t *testing.T) {
	ops := make([]string, 0, apiMaxMultiOperations+1)
	for i := 0; i <= apiMaxMultiOperations; i++ {
		ops = append(ops, `{"id":"op`+strconv.Itoa(i)+`","command":"search","args":["x"]}`)
	}
	body := `{"operations":[` + strings.Join(ops, ",") + `]}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "too many operations") {
		t.Fatalf("expected too-many-ops error, got %q", rr.Body.String())
	}
}

func TestAPIMultiCommandRejectsEmptyOperations(t *testing.T) {
	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, `{"operations":[]}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIMultiCommandRejectsDuplicateIDs(t *testing.T) {
	body := `{"operations":[
		{"id":"x","command":"search","args":["a"]},
		{"id":"x","command":"news"}
	]}`
	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "duplicate operation id") {
		t.Fatalf("expected duplicate-id error, got %q", rr.Body.String())
	}
}

func TestAPIMultiCommandRejectsBadJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, `not-json`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIMultiCommandUnauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/multi-command",
		bytes.NewBufferString(`{"operations":[{"id":"a","command":"search","args":["x"]}]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAPIMultiCommandRunsConcurrently(t *testing.T) {
	// Each fake bird call sleeps 200ms. With 4 ops at concurrency=4 the
	// wall time should be roughly one op duration, not 4x. Generous
	// threshold (700ms) keeps the test stable on slow CI.
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		"sleep 0.2",
		`echo "done $*"`,
	}, "\n"))

	home := t.TempDir()
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
	})
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	body := `{"operations":[
		{"id":"1","command":"search","args":["a"]},
		{"id":"2","command":"search","args":["b"]},
		{"id":"3","command":"search","args":["c"]},
		{"id":"4","command":"search","args":["d"]}
	],"concurrency":4}`

	start := time.Now()
	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("expected concurrent execution to complete within 700ms, got %v", elapsed)
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(resp.Results))
	}
	for _, r := range resp.Results {
		if !r.OK {
			t.Fatalf("op %q failed: %+v", r.ID, r)
		}
	}
}

func TestAPIMultiCommandDoesNotAdvanceRotationWhenAllOpsFailBeforeRunner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a"},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b"},
	})
	writeStateFixture(t, home, "alpha", "sonnet")
	// Point bird at a path that doesn't exist so RunCapture errors before
	// the runner can spawn anything. All ops should fail with `runErr` set.
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(t.TempDir(), "missing-bird"))

	body := `{"operations":[
		{"id":"a","command":"search","args":["x"]},
		{"id":"b","command":"search","args":["y"]}
	],"strategy":"round-robin"}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, r := range resp.Results {
		if r.OK {
			t.Fatalf("expected all ops to fail at runner, %q got OK=true", r.ID)
		}
	}

	loaded, err := state.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if loaded.LastUsedName != "alpha" {
		t.Fatalf("rotation must not advance when no op reached the runner: LastUsedName=%q", loaded.LastUsedName)
	}
}

func TestAPIMultiCommandLeastUsedDistributesAcrossBatch(t *testing.T) {
	// Echo back which account ran. Two fresh accounts (use_count=0). With
	// the per-batch in-memory usage update, a 2-op least-used batch should
	// pick BOTH accounts. Without the fix, both ops pick the same account
	// (the snapshot stays unchanged across iterations).
	birdPath := writeFakeBirdScript(t, strings.Join([]string{
		"#!/bin/sh",
		`echo "$AUTH_TOKEN"`,
	}, "\n"))

	home := t.TempDir()
	t.Setenv("HOME", home)
	writeAccountsFixture(t, home, []map[string]any{
		{"name": "alpha", "auth_token": "token-a", "ct0": "ct0-a", "use_count": 0},
		{"name": "beta", "auth_token": "token-b", "ct0": "ct0-b", "use_count": 0},
	})
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	body := `{"operations":[
		{"id":"a","command":"search","args":["x"]},
		{"id":"b","command":"search","args":["y"]}
	],"strategy":"least-used"}`

	rr := httptest.NewRecorder()
	handleAPIMultiCommand("birdy").ServeHTTP(rr, multiCommandRequest(t, body))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	var resp apiMultiCommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range resp.Results {
		if !r.OK {
			t.Fatalf("op %q failed: %+v", r.ID, r)
		}
		seen[r.Account] = true
	}
	if len(seen) != 2 {
		t.Fatalf("least-used should have distributed 2 ops across 2 fresh accounts; got accounts %v", seen)
	}
}

func TestAPIMultiCommandBatchedRateLimit(t *testing.T) {
	// allowN should reject a batch that, combined with the per-IP window,
	// would push the IP past commandLimiter.limit (60/min).
	rl := newRateLimiter(5, time.Minute)
	if !rl.allowN("1.1.1.1", 4) {
		t.Fatal("expected 4-op batch to fit under limit=5")
	}
	if rl.allowN("1.1.1.1", 2) {
		t.Fatal("expected 2-op batch to be rejected (4+2 > 5)")
	}
	if !rl.allowN("1.1.1.1", 1) {
		t.Fatal("expected 1-op batch to fit (4+1 = 5)")
	}
	if rl.allowN("1.1.1.1", 1) {
		t.Fatal("expected next batch to be rejected (5+1 > 5)")
	}
}

