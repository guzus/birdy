// Package daemon implements `birdy daemon`: a long-lived HTTP server that
// holds warm bird auth and account-rotation state and serves agent queries
// with sub-second latency on follow-up calls.
//
// Why this exists: every cold `bird` invocation costs ~200–400ms in process
// spawn alone. An agent reasoning loop that issues 5–15 follow-up bird
// queries during one inference run pays that cost over and over. The daemon
// removes the spawn cost from the agent's critical path; only the network
// round-trip to X.com remains.
//
// Scope (v1, intentional):
//   - HTTP transport only (Unix socket is a follow-up).
//   - Bounded request concurrency.
//   - Optional in-memory TTL cache, keyed on the args slice.
//   - Per-request account rotation (preserves rotation behavior across
//     processes — picking once at startup would pin the daemon to a single
//     account and defeat rotation entirely).
//   - Graceful shutdown: stop accepting new requests, drain in-flight, exit.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/store"
)

// RunFunc is the runner-execution surface used by the daemon. It mirrors
// runner.RunCapture so production code can wire that in directly while
// tests inject a stub. Tests should NEVER need to spawn a real bird.
type RunFunc func(account *store.Account, args []string) (exitCode int, stdout string, stderr string, err error)

// AccountPickFunc selects an account for a given request. The args are
// passed in so callers can apply read-only filtering for mutating bird
// commands. The returned account may be nil only if err is non-nil.
type AccountPickFunc func(args []string) (*store.Account, error)

// Config wires together the runtime dependencies for the daemon. Every
// callable is injectable so server_test.go can exercise the HTTP layer
// without touching disk, the rotation store, or a real bird binary.
type Config struct {
	// Run executes the bird CLI. Required.
	Run RunFunc
	// PickAccount selects an account for a request. Required.
	PickAccount AccountPickFunc
	// AccountCount returns the current number of configured accounts (used
	// only for /health reporting). Required.
	AccountCount func() int
	// Accounts returns the current account list so /health can report
	// per-account rate-limit state (which accounts are inside the
	// rotation.QuotaCooldown window after a 429). Optional: when nil, /health
	// reports every account as ready and an empty accounts_detail, because it
	// has no evidence either way. Only names and timestamps are ever
	// published from it — never tokens or cookies.
	Accounts func() []store.Account
	// Concurrency caps in-flight /run requests. Must be >= 1.
	Concurrency int
	// CacheTTL enables the in-memory cache when > 0. Zero disables cache.
	CacheTTL time.Duration
}

// Server is an HTTP daemon. Construct it with NewServer and call Serve.
// Server is safe for concurrent use after construction.
type Server struct {
	cfg       Config
	mux       *http.ServeMux
	sem       chan struct{}
	cache     *ttlCache // nil when CacheTTL <= 0
	startedAt time.Time
	// now is the clock /health uses for cooldown math; tests pin it.
	now func() time.Time

	// Atomic counters for /health.
	served    atomic.Int64
	cacheHits atomic.Int64
}

// NewServer builds a Server. It does not start accepting connections — call
// Serve for that. Returns an error if the config is invalid.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Run == nil {
		return nil, errors.New("daemon: Config.Run is required")
	}
	if cfg.PickAccount == nil {
		return nil, errors.New("daemon: Config.PickAccount is required")
	}
	if cfg.AccountCount == nil {
		return nil, errors.New("daemon: Config.AccountCount is required")
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}

	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		sem:       make(chan struct{}, cfg.Concurrency),
		startedAt: time.Now(),
		now:       time.Now,
	}
	if cfg.CacheTTL > 0 {
		s.cache = newTTLCache(cfg.CacheTTL)
	}

	s.mux.HandleFunc("/run", s.handleRun)
	s.mux.HandleFunc("/health", s.handleHealth)
	return s, nil
}

// Handler returns an http.Handler that the server uses. Exposed for tests
// that wire it into httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve binds to addr and serves until ctx is canceled. On cancellation it
// triggers a graceful shutdown: stops accepting new connections, drains
// in-flight requests for up to drainTimeout, then returns.
func (s *Server) Serve(ctx context.Context, addr string, drainTimeout time.Duration) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		// Graceful shutdown: http.Server.Shutdown stops accepting new conns
		// and waits for active handlers to return.
		shutCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		// Drain the goroutine's eventual error.
		<-errCh
		return nil
	}
}

// runRequest is the JSON body for POST /run.
type runRequest struct {
	Args      []string `json:"args"`
	RequestID string   `json:"request_id,omitempty"`
}

// runResponse is the JSON body returned from POST /run.
type runResponse struct {
	OK         bool   `json:"ok"`
	RequestID  string `json:"request_id,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
	Cached     bool   `json:"cached"`
}

// healthResponse is the JSON body returned from GET /health.
//
// The first six fields are the original contract and stay byte-compatible.
// The rate-limit fields were appended so a wrapper polling /health can tell
// "3 of 4 accounts cooling" apart from "all healthy" without shelling out to
// `birdy budget`. `ok` is a liveness bit — the daemon answered — and stays
// true even when every account is cooling; `degraded` carries that signal.
type healthResponse struct {
	OK            bool  `json:"ok"`
	Accounts      int   `json:"accounts"`
	UptimeSeconds int64 `json:"uptime_seconds"`
	Served        int64 `json:"served"`
	CacheHits     int64 `json:"cache_hits"`
	CacheSize     int   `json:"cache_size"`

	// AccountsCooling counts accounts whose last 429 is inside the cooldown
	// window; AccountsReady counts enabled accounts that are not cooling.
	// Disabled accounts are in neither bucket, so the two need not sum to
	// Accounts.
	AccountsCooling int `json:"accounts_cooling"`
	AccountsReady   int `json:"accounts_ready"`
	// CooldownSeconds is rotation.QuotaCooldown, the window an account is
	// avoided for after a 429.
	CooldownSeconds int `json:"cooldown_seconds"`
	// Degraded is true when no account is ready to serve: every enabled
	// account is cooling (or every account is disabled). The daemon is still
	// alive, which is why OK does not flip.
	Degraded bool `json:"degraded"`
	// AccountsDetail is never null: an empty list when Config.Accounts is
	// unset or the store is empty.
	AccountsDetail []healthAccount `json:"accounts_detail"`
}

// healthAccount is one account's rate-limit state in /health. It carries the
// name and timestamps only — credentials never leave the store.
type healthAccount struct {
	Name    string `json:"name"`
	Cooling bool   `json:"cooling"`
	// CooldownRemainingSeconds is how long until the account leaves the
	// window, rounded up; 0 when not cooling.
	CooldownRemainingSeconds int `json:"cooldown_remaining_seconds"`
	// LastRateLimitedAt is RFC 3339 in UTC, or "" if the account has never
	// been rate limited.
	LastRateLimitedAt string `json:"last_rate_limited_at"`
	// Disabled accounts are out of rotation and count as neither cooling nor
	// ready.
	Disabled bool `json:"disabled,omitempty"`
}

// errorResponse is the standard JSON error body.
type errorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleRun is POST /run. It validates the JSON body, enforces the
// concurrency cap, optionally serves from cache, otherwise picks an
// account and invokes the runner.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{OK: false, Error: "method not allowed"})
		return
	}

	// Cap the body at a generous 1 MiB — args lists are tiny in practice and
	// this keeps a buggy/malicious client from holding the daemon hostage.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "could not read request body"})
		return
	}

	var req runRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "invalid json"})
		return
	}
	if len(req.Args) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{OK: false, Error: "args is required and must be non-empty"})
		return
	}

	start := time.Now()

	// Optional cache lookup BEFORE acquiring the semaphore. Cache hits
	// don't run bird and don't compete for the concurrency budget.
	var cacheKey string
	if s.cache != nil {
		cacheKey = keyForArgs(req.Args)
		if e, ok := s.cache.get(cacheKey); ok {
			s.cacheHits.Add(1)
			s.served.Add(1)
			writeJSON(w, http.StatusOK, runResponse{
				OK:         true,
				RequestID:  req.RequestID,
				ExitCode:   e.exitCode,
				Stdout:     e.stdout,
				Stderr:     e.stderr,
				DurationMS: time.Since(start).Milliseconds(),
				Cached:     true,
			})
			return
		}
	}

	// Acquire concurrency slot. Honor request cancellation (e.g. client
	// hangs up mid-queue).
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-r.Context().Done():
		writeJSON(w, 499, errorResponse{OK: false, Error: "client closed request"})
		return
	}

	account, err := s.cfg.PickAccount(req.Args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: err.Error()})
		return
	}

	exitCode, stdout, stderr, runErr := s.cfg.Run(account, req.Args)
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{OK: false, Error: runErr.Error()})
		return
	}

	// Only cache successful results. Caching transient bird errors would
	// pin the daemon to a broken state from the agent's perspective.
	if s.cache != nil && exitCode == 0 {
		s.cache.set(cacheKey, exitCode, stdout, stderr)
	}

	s.served.Add(1)
	writeJSON(w, http.StatusOK, runResponse{
		OK:         true,
		RequestID:  req.RequestID,
		ExitCode:   exitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		DurationMS: time.Since(start).Milliseconds(),
		Cached:     false,
	})
}

// handleHealth is GET /health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{OK: false, Error: "method not allowed"})
		return
	}
	cacheSize := 0
	if s.cache != nil {
		cacheSize = s.cache.size()
	}
	resp := healthResponse{
		OK:              true,
		Accounts:        s.cfg.AccountCount(),
		UptimeSeconds:   int64(time.Since(s.startedAt).Seconds()),
		Served:          s.served.Load(),
		CacheHits:       s.cacheHits.Load(),
		CacheSize:       cacheSize,
		CooldownSeconds: int(rotation.QuotaCooldown / time.Second),
		AccountsDetail:  []healthAccount{},
	}
	if s.cfg.Accounts == nil {
		// No account list to inspect: nothing is known to be cooling.
		resp.AccountsReady = resp.Accounts
		writeJSON(w, http.StatusOK, resp)
		return
	}

	now := s.now()
	for _, a := range s.cfg.Accounts() {
		detail := healthAccount{Name: a.Name, Disabled: a.Disabled}
		if !a.LastRateLimitedAt.IsZero() {
			detail.LastRateLimitedAt = a.LastRateLimitedAt.UTC().Format(time.RFC3339)
			if remaining := rotation.QuotaCooldown - now.Sub(a.LastRateLimitedAt); remaining > 0 {
				detail.Cooling = true
				detail.CooldownRemainingSeconds = int((remaining + time.Second - 1) / time.Second)
			}
		}
		switch {
		case a.Disabled:
			// Out of rotation: neither bucket.
		case detail.Cooling:
			resp.AccountsCooling++
		default:
			resp.AccountsReady++
		}
		resp.AccountsDetail = append(resp.AccountsDetail, detail)
	}
	resp.Degraded = resp.Accounts > 0 && resp.AccountsReady == 0
	writeJSON(w, http.StatusOK, resp)
}

// DefaultRunner returns a RunFunc backed by runner.RunCapture that also
// persists per-account 429s into the given store so quota-aware
// rotation works for daemon traffic. Pass nil to skip recording (only
// useful for tests that don't care about quota state).
//
// The daemon's RunFunc shape predates runner.Result so we flatten it
// back here. Recording happens before returning so cache.set in
// handleRun observes a consistent post-call store.
func DefaultRunner(st *store.Store) RunFunc {
	return func(account *store.Account, args []string) (int, string, string, error) {
		res, stdout, stderr, err := runner.RunCapture(account, args)
		if res.RateLimited && st != nil && account != nil {
			if rlErr := st.RecordRateLimit(account.Name); rlErr == nil {
				_ = st.Save()
			}
		}
		return res.ExitCode, stdout, stderr, err
	}
}
