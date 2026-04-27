package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/store"
)

// stubAccount returns a non-nil account so server code paths that pass it
// through to RunFunc don't see nil. The runner stub doesn't read it.
func stubAccount() *store.Account {
	return &store.Account{Name: "stub"}
}

// newTestServer builds a daemon.Server backed by an httptest.Server with
// the given configuration. Returns the test server and a teardown func.
func newTestServer(t *testing.T, cfg Config) (*httptest.Server, *Server, func()) {
	t.Helper()
	if cfg.PickAccount == nil {
		cfg.PickAccount = func(_ []string) (*store.Account, error) { return stubAccount(), nil }
	}
	if cfg.AccountCount == nil {
		cfg.AccountCount = func() int { return 1 }
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 4
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	hs := httptest.NewServer(srv.Handler())
	return hs, srv, hs.Close
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeRun(t *testing.T, resp *http.Response) runResponse {
	t.Helper()
	defer resp.Body.Close()
	var out runResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode runResponse: %v", err)
	}
	return out
}

func decodeError(t *testing.T, resp *http.Response) errorResponse {
	t.Helper()
	defer resp.Body.Close()
	var out errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode errorResponse: %v", err)
	}
	return out
}

// TestRunRoundTrip is the happy-path: POST /run hands `args` to the runner
// and round-trips stdout/stderr/exit_code/request_id back to the caller.
func TestRunRoundTrip(t *testing.T) {
	var gotArgs []string
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, args []string) (int, string, string, error) {
			gotArgs = args
			return 0, "hello", "world", nil
		},
	})
	defer teardown()

	resp := postJSON(t, hs.URL+"/run", `{"args":["search","ai"],"request_id":"abc"}`)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	got := decodeRun(t, resp)
	if !got.OK || got.RequestID != "abc" || got.ExitCode != 0 {
		t.Fatalf("bad response: %+v", got)
	}
	if got.Stdout != "hello" || got.Stderr != "world" {
		t.Fatalf("stdout/stderr mismatch: %+v", got)
	}
	if got.Cached {
		t.Fatalf("expected cache miss")
	}
	if len(gotArgs) != 2 || gotArgs[0] != "search" || gotArgs[1] != "ai" {
		t.Fatalf("runner saw args=%v, expected [search ai]", gotArgs)
	}
}

// TestRunRoundTripPropagatesNonZeroExit verifies the daemon returns
// HTTP 200 with the bird exit code in the JSON body, mirroring how
// `multi-fetch` and the API command surface non-zero exits.
func TestRunRoundTripPropagatesNonZeroExit(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 7, "", "boom", nil
		},
	})
	defer teardown()

	resp := postJSON(t, hs.URL+"/run", `{"args":["search","x"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got := decodeRun(t, resp)
	if got.ExitCode != 7 || got.Stderr != "boom" {
		t.Fatalf("bad response: %+v", got)
	}
}

// TestRunRunnerError surfaces an error from the runner as 500 + JSON body.
// (Distinct from a non-zero exit code, which is success at the HTTP layer.)
func TestRunRunnerError(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "", "", fmt.Errorf("bird CLI not found")
		},
	})
	defer teardown()

	resp := postJSON(t, hs.URL+"/run", `{"args":["x"]}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.OK || !strings.Contains(body.Error, "bird CLI not found") {
		t.Fatalf("bad error body: %+v", body)
	}
}

// TestRunBadJSON confirms a malformed body returns 400.
func TestRunBadJSON(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			t.Fatal("runner should not be called on bad JSON")
			return 0, "", "", nil
		},
	})
	defer teardown()

	resp := postJSON(t, hs.URL+"/run", `not-json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.OK || !strings.Contains(body.Error, "invalid json") {
		t.Fatalf("bad error body: %+v", body)
	}
}

// TestRunEmptyArgs verifies that an empty or absent args slice is rejected
// before the runner is consulted.
func TestRunEmptyArgs(t *testing.T) {
	cases := []string{
		`{"args":[]}`,
		`{"request_id":"x"}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			hs, _, teardown := newTestServer(t, Config{
				Run: func(_ *store.Account, _ []string) (int, string, string, error) {
					t.Fatal("runner should not be called on empty args")
					return 0, "", "", nil
				},
			})
			defer teardown()

			resp := postJSON(t, hs.URL+"/run", in)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d", resp.StatusCode)
			}
			body := decodeError(t, resp)
			if body.OK || !strings.Contains(body.Error, "args is required") {
				t.Fatalf("bad error body: %+v", body)
			}
		})
	}
}

// TestRunMethodNotAllowed confirms /run only accepts POST.
func TestRunMethodNotAllowed(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "", "", nil
		},
	})
	defer teardown()

	resp, err := http.Get(hs.URL + "/run")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// TestConcurrencyCap issues many parallel requests and verifies the
// in-flight count never exceeds the configured cap.
func TestConcurrencyCap(t *testing.T) {
	const (
		nReq = 16
		cap_ = 4
	)
	var (
		mu        sync.Mutex
		inFlight  int
		maxInFlight int
	)
	// release blocks each runner call until the test signals.
	release := make(chan struct{})

	hs, _, teardown := newTestServer(t, Config{
		Concurrency: cap_,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()
			<-release
			mu.Lock()
			inFlight--
			mu.Unlock()
			return 0, "", "", nil
		},
	})
	defer teardown()

	var wg sync.WaitGroup
	for i := 0; i < nReq; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := postJSON(t, hs.URL+"/run", `{"args":["x"]}`)
			resp.Body.Close()
		}()
	}

	// Give all requests time to queue up. We don't sleep arbitrarily — we
	// poll the in-flight gauge until it reaches the cap, with a generous
	// upper bound so a hang is visible.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := maxInFlight
		mu.Unlock()
		if got >= cap_ {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// At this point cap_ requests are blocked in the runner; release them
	// all at once and let the rest drain.
	close(release)
	wg.Wait()

	if maxInFlight > cap_ {
		t.Fatalf("max in-flight %d exceeded cap %d", maxInFlight, cap_)
	}
	if maxInFlight < cap_ {
		// We didn't even reach the cap, meaning either the test plumbing
		// is broken or the server is over-throttling. Either way, fail.
		t.Fatalf("max in-flight %d never reached cap %d (test plumbing)", maxInFlight, cap_)
	}
}

// TestCacheHitAndMiss verifies that with a positive TTL, identical args
// hit the cache on the second call.
func TestCacheHitAndMiss(t *testing.T) {
	var calls atomic.Int64
	hs, srv, teardown := newTestServer(t, Config{
		CacheTTL: 30 * time.Second,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			calls.Add(1)
			return 0, "first", "", nil
		},
	})
	defer teardown()

	body := `{"args":["search","ai"]}`

	// First call: miss → runner invoked.
	r1 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	if r1.Cached {
		t.Fatalf("first call should not be cached")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 runner call, got %d", calls.Load())
	}

	// Second call with same args: hit → runner not invoked.
	r2 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	if !r2.Cached {
		t.Fatalf("second call should be cached")
	}
	if r2.Stdout != "first" {
		t.Fatalf("cached stdout mismatch: %q", r2.Stdout)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected runner not re-invoked on cache hit, got %d calls", calls.Load())
	}

	// Different args: miss again.
	r3 := decodeRun(t, postJSON(t, hs.URL+"/run", `{"args":["search","ml"]}`))
	if r3.Cached {
		t.Fatalf("different args should miss")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 runner calls, got %d", calls.Load())
	}

	if got := srv.cacheHits.Load(); got != 1 {
		t.Fatalf("expected 1 cache hit reported, got %d", got)
	}
}

// TestCacheExpires uses an injected fake clock to verify TTL-based eviction.
// We don't sleep in real time — we advance the cache's clock directly.
func TestCacheExpires(t *testing.T) {
	now := time.Now()
	var calls atomic.Int64
	hs, srv, teardown := newTestServer(t, Config{
		CacheTTL: 5 * time.Second,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			calls.Add(1)
			return 0, "x", "", nil
		},
	})
	defer teardown()
	// Replace the cache clock with a controllable one.
	srv.cache.now = func() time.Time { return now }

	body := `{"args":["a"]}`
	_ = decodeRun(t, postJSON(t, hs.URL+"/run", body)) // miss, populates

	// Advance past TTL.
	now = now.Add(10 * time.Second)
	r2 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	if r2.Cached {
		t.Fatalf("entry should have expired")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 runner calls (miss after expiry), got %d", calls.Load())
	}
}

// TestCacheDisabled verifies that with TTL=0 nothing is cached.
func TestCacheDisabled(t *testing.T) {
	var calls atomic.Int64
	hs, srv, teardown := newTestServer(t, Config{
		CacheTTL: 0,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			calls.Add(1)
			return 0, "ok", "", nil
		},
	})
	defer teardown()

	if srv.cache != nil {
		t.Fatalf("cache should be nil when TTL=0")
	}
	body := `{"args":["a"]}`
	r1 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	r2 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	if r1.Cached || r2.Cached {
		t.Fatalf("no responses should be marked cached when ttl=0")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 runner calls, got %d", calls.Load())
	}
}

// TestCacheDoesNotStoreFailure verifies that non-zero exit codes are not
// cached. (Caching transient bird errors would pin the daemon to a broken
// state from the agent's perspective.)
func TestCacheDoesNotStoreFailure(t *testing.T) {
	var calls atomic.Int64
	hs, _, teardown := newTestServer(t, Config{
		CacheTTL: 30 * time.Second,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			calls.Add(1)
			return 1, "", "rate limited", nil
		},
	})
	defer teardown()

	body := `{"args":["a"]}`
	_ = decodeRun(t, postJSON(t, hs.URL+"/run", body))
	r2 := decodeRun(t, postJSON(t, hs.URL+"/run", body))
	if r2.Cached {
		t.Fatalf("failure should not be cached")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 runner calls, got %d", calls.Load())
	}
}

// TestHealthShape exercises GET /health and verifies the JSON keys exist
// and the counters update after a /run call.
func TestHealthShape(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		AccountCount: func() int { return 3 },
		CacheTTL:     30 * time.Second,
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			return 0, "ok", "", nil
		},
	})
	defer teardown()

	resp, err := http.Get(hs.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK {
		t.Fatalf("health: not ok")
	}
	if got.Accounts != 3 {
		t.Fatalf("accounts=%d", got.Accounts)
	}
	if got.UptimeSeconds < 0 {
		t.Fatalf("uptime negative: %d", got.UptimeSeconds)
	}
	if got.Served != 0 {
		t.Fatalf("expected served=0 before any /run, got %d", got.Served)
	}

	// Issue a /run and confirm served increments.
	_ = decodeRun(t, postJSON(t, hs.URL+"/run", `{"args":["x"]}`))
	resp2, err := http.Get(hs.URL + "/health")
	if err != nil {
		t.Fatalf("get health2: %v", err)
	}
	defer resp2.Body.Close()
	var got2 healthResponse
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if got2.Served != 1 {
		t.Fatalf("expected served=1 after one /run, got %d", got2.Served)
	}
}

// TestHealthMethodNotAllowed verifies /health rejects POST.
func TestHealthMethodNotAllowed(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		Run: func(_ *store.Account, _ []string) (int, string, string, error) { return 0, "", "", nil },
	})
	defer teardown()

	resp, err := http.Post(hs.URL+"/health", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// TestPickAccountError surfaces account-pick errors as 500.
func TestPickAccountError(t *testing.T) {
	hs, _, teardown := newTestServer(t, Config{
		PickAccount: func(_ []string) (*store.Account, error) {
			return nil, fmt.Errorf("no writable accounts")
		},
		Run: func(_ *store.Account, _ []string) (int, string, string, error) {
			t.Fatal("runner should not be called when pick fails")
			return 0, "", "", nil
		},
	})
	defer teardown()

	resp := postJSON(t, hs.URL+"/run", `{"args":["tweet","hi"]}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.OK || !strings.Contains(body.Error, "no writable accounts") {
		t.Fatalf("bad error body: %+v", body)
	}
}

// TestNewServerValidatesConfig verifies missing required fields are caught
// at construction time rather than at first request.
func TestNewServerValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "missing Run",
			cfg: Config{
				PickAccount: func(_ []string) (*store.Account, error) { return stubAccount(), nil },
				AccountCount: func() int { return 0 },
			},
			want: "Run",
		},
		{
			name: "missing PickAccount",
			cfg: Config{
				Run:          func(_ *store.Account, _ []string) (int, string, string, error) { return 0, "", "", nil },
				AccountCount: func() int { return 0 },
			},
			want: "PickAccount",
		},
		{
			name: "missing AccountCount",
			cfg: Config{
				Run:         func(_ *store.Account, _ []string) (int, string, string, error) { return 0, "", "", nil },
				PickAccount: func(_ []string) (*store.Account, error) { return stubAccount(), nil },
			},
			want: "AccountCount",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewServer(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

// TestKeyForArgsOrderSensitive confirms that argv order matters in cache keys.
func TestKeyForArgsOrderSensitive(t *testing.T) {
	a := keyForArgs([]string{"search", "AI", "-n", "50"})
	b := keyForArgs([]string{"search", "-n", "50", "AI"})
	if a == b {
		t.Fatalf("expected different keys for different argv orders")
	}
}
