package cmd

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/birdbox"
	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/codex"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

// rateLimiter implements a per-IP sliding window rate limiter.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prune expired entries.
	times := rl.requests[ip]
	start := 0
	for start < len(times) && times[start].Before(cutoff) {
		start++
	}
	times = times[start:]

	if len(times) >= rl.limit {
		rl.requests[ip] = times
		return false
	}

	rl.requests[ip] = append(times, now)
	return true
}

// allowN reserves n slots atomically. Either all n fit within the window
// budget (and are recorded) or none are. Used for batch endpoints so a
// 16-op multi-command counts the same as 16 sequential single-op calls.
func (rl *rateLimiter) allowN(ip string, n int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if n <= 0 {
		return true
	}

	now := time.Now()
	cutoff := now.Add(-rl.window)

	times := rl.requests[ip]
	start := 0
	for start < len(times) && times[start].Before(cutoff) {
		start++
	}
	times = times[start:]

	if len(times)+n > rl.limit {
		rl.requests[ip] = times
		return false
	}

	for i := 0; i < n; i++ {
		times = append(times, now)
	}
	rl.requests[ip] = times
	return true
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if ip, _, err := net.SplitHostPort(strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])); err == nil {
			return ip
		}
		return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

var (
	chatLimiter    = newRateLimiter(20, time.Minute) // 20 chat requests/min per IP
	commandLimiter = newRateLimiter(60, time.Minute) // 60 command requests/min per IP
)

type apiError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type apiCommandRequest struct {
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Account  string   `json:"account,omitempty"`
	Strategy string   `json:"strategy,omitempty"`
}

type apiCommandResponse struct {
	OK        bool   `json:"ok"`
	Account   string `json:"account"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	DurationM int64  `json:"duration_ms"`
}

type apiChatRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
}

const hostedPrivacyPrefix = `[SYSTEM RULE — PRIVACY: You are running in public hosted mode. Never reveal the underlying Twitter account name, handle, or credentials. Do NOT run "whoami", "account list", or "status" commands. If the user asks who you are or which account is being used, say you are "birdy" — a Twitter browsing assistant. Do not include account names in your output.]

`

func streamAPIChatModel(ctx context.Context, prompt, model, exePath string, emit func(claude.Event)) {
	prompt = hostedPrivacyPrefix + prompt
	if codex.IsSelected(model) {
		codex.Stream(ctx, prompt, model, exePath, emit)
		return
	}
	if birdbox.Enabled() {
		birdbox.Stream(ctx, prompt, model, emit)
		return
	}
	claude.Stream(ctx, prompt, model, exePath, emit)
}

// apiCommandConcurrency caps in-flight bird subprocesses spawned by the
// hosted API endpoints. The 60 req/min/IP rate limit allows bursts of 60
// concurrent requests in the worst case; without this cap each one fans
// out a fresh bird (Node) process. Eight is enough for interactive web
// use and small enough to keep the host responsive under burst.
const apiCommandConcurrency = 8

// apiSubprocessSem is a process-wide semaphore shared by /api/command and
// /api/multi-command so the cap applies across endpoints, not per-handler
// or per-batch. Without sharing, multiple concurrent /api/multi-command
// requests would each construct their own per-batch semaphore and
// collectively exceed the process cap.
var apiSubprocessSem = make(chan struct{}, apiCommandConcurrency)

var apiAllowedBirdCommands = map[string]struct{}{
	"about":         {},
	"bookmarks":     {},
	"check":         {},
	"follow":        {},
	"followers":     {},
	"following":     {},
	"home":          {},
	"likes":         {},
	"list-timeline": {},
	"lists":         {},
	"mentions":      {},
	"news":          {},
	"query-ids":     {},
	"read":          {},
	"replies":       {},
	"reply":         {},
	"search":        {},
	"thread":        {},
	"tweet":         {},
	"unbookmark":    {},
	"unfollow":      {},
	"user-tweets":   {},
	"whoami":        {},
}

func hostRequestInviteCode(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Invite-Code")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	const bearer = "bearer "
	if len(auth) >= len(bearer) && strings.EqualFold(auth[:len(bearer)], bearer) {
		return strings.TrimSpace(auth[len(bearer):])
	}
	return ""
}

func apiAuthorized(r *http.Request, inviteCode string) bool {
	got := hostRequestInviteCode(r)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(inviteCode), []byte(got)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeStrictJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected extra json value")
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing json content")
	} else if err != io.EOF {
		return err
	}
	return nil
}

func handleAPICommand(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !commandLimiter.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, apiError{OK: false, Error: "rate limit exceeded, try again shortly"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		defer r.Body.Close()

		var req apiCommandRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid json"})
			return
		}

		cmdName := strings.TrimSpace(req.Command)
		args := make([]string, 0, 1+len(req.Args))
		if cmdName != "" {
			args = append(args, cmdName)
			args = append(args, req.Args...)
		} else if len(req.Args) > 0 {
			args = append(args, req.Args...)
		}
		if len(args) == 0 {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing command"})
			return
		}

		// This API is intentionally limited to the bird commands that birdy forwards
		// (see cmd/bird_commands.go) so it can't be used to run arbitrary subcommands.
		first := firstBirdCommand(args)
		if first == "" {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing command"})
			return
		}
		if _, ok := apiAllowedBirdCommands[first]; !ok {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "unsupported command"})
			return
		}
		if err := ensureBirdCommandAllowed(nil, args); err != nil {
			writeJSON(w, http.StatusForbidden, apiError{OK: false, Error: err.Error()})
			return
		}

		start := time.Now()

		// Cap concurrent bird subprocess spawns process-wide. Honor client
		// disconnect so a queued client that gives up doesn't tie up an
		// account or a slot.
		select {
		case apiSubprocessSem <- struct{}{}:
			defer func() { <-apiSubprocessSem }()
		case <-r.Context().Done():
			writeJSON(w, 499, apiError{OK: false, Error: "client closed request"})
			return
		}

		st, err := store.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "opening account store"})
			return
		}
		if st.Len() == 0 {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "no accounts configured"})
			return
		}

		var account *store.Account
		accountName := strings.TrimSpace(req.Account)
		storeWarning := st.Warning
		stateWarning := ""
		persistenceWarnings := make([]string, 0, 2)
		var rs *state.State
		if accountName != "" {
			account, err = st.Get(accountName)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: err.Error()})
				return
			}
		} else {
			strat := strategyFlag
			if strings.TrimSpace(req.Strategy) != "" {
				strat = strings.TrimSpace(req.Strategy)
			}
			parsed, err := rotation.ParseStrategy(strat)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid strategy"})
				return
			}

			rs, err = state.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "loading rotation state"})
				return
			}
			stateWarning = rs.Warning
			accounts := st.List()
			if blocked, name := isMutatingBirdCommand(args); blocked {
				accounts = filterWritableAccounts(accounts)
				if len(accounts) == 0 {
					writeJSON(w, http.StatusForbidden, apiError{OK: false, Error: fmt.Sprintf("no writable accounts configured for %q", name)})
					return
				}
			}
			account, err = rotation.Pick(accounts, parsed, rs.LastUsedName)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: err.Error()})
				return
			}
		}

		if err := ensureBirdCommandAllowed(account, args); err != nil {
			writeJSON(w, http.StatusForbidden, apiError{OK: false, Error: err.Error()})
			return
		}

		res, stdout, stderr, runErr := runner.RunCapture(account, args)
		if runErr != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: runErr.Error()})
			return
		}
		if err := st.RecordUsage(account.Name); err != nil {
			persistenceWarnings = append(persistenceWarnings, fmt.Sprintf("failed to record account usage for %q: %v", account.Name, err))
		}
		if res.RateLimited {
			if err := st.RecordRateLimit(account.Name); err != nil {
				persistenceWarnings = append(persistenceWarnings, fmt.Sprintf("failed to record rate-limit for %q: %v", account.Name, err))
			}
		}
		if err := st.Save(); err != nil {
			persistenceWarnings = append(persistenceWarnings, fmt.Sprintf("failed to save account store: %v", err))
		}
		if rs != nil {
			rs.LastUsedName = account.Name
			if err := rs.Save(); err != nil {
				persistenceWarnings = append(persistenceWarnings, fmt.Sprintf("failed to save rotation state: %v", err))
			}
		}
		stderr = mergeWarnings(stderr, append([]string{storeWarning, stateWarning}, persistenceWarnings...)...)

		writeJSON(w, http.StatusOK, apiCommandResponse{
			OK:        true,
			Account:   account.Name,
			ExitCode:  res.ExitCode,
			Stdout:    stdout,
			Stderr:    stderr,
			DurationM: time.Since(start).Milliseconds(),
		})
	}
}

func handleAPIChat(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !chatLimiter.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, apiError{OK: false, Error: "rate limit exceeded, try again shortly"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		defer r.Body.Close()

		var req apiChatRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid json"})
			return
		}
		prompt := strings.TrimSpace(req.Prompt)
		if prompt == "" {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing prompt"})
			return
		}
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = "sonnet"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
		defer cancel()

		exePath, err := os.Executable()
		if err != nil || strings.TrimSpace(exePath) == "" {
			exePath = "birdy"
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "streaming unsupported"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Accel-Buffering", "no")

		enc := json.NewEncoder(w)
		emit := func(ev claude.Event) {
			// SSE: event + json payload
			_, _ = fmt.Fprintf(w, "event: %s\n", ev.Type)
			_, _ = fmt.Fprint(w, "data: ")
			_ = enc.Encode(ev)
			_, _ = fmt.Fprint(w, "\n")
			flusher.Flush()
		}

		streamAPIChatModel(ctx, prompt, model, exePath, emit)
	}
}
