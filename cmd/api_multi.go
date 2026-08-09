package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/birdtool"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

const (
	apiMaxMultiOperations       = 16
	apiMultiCommandConcurrency  = 8
	apiMultiCommandBodyMaxBytes = 256 * 1024
)

// apiMultiOperation is one bird invocation in a multi-command request.
// Either {command, args} or just {args} (with the command as args[0]) works,
// matching apiCommandRequest's flexibility.
type apiMultiOperation struct {
	ID      string   `json:"id"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// apiMultiCommandRequest is the manifest for POST /api/multi-command.
// `account` and `strategy` apply to the whole batch; per-op overrides
// were intentionally not added — keep the surface small.
type apiMultiCommandRequest struct {
	Operations  []apiMultiOperation `json:"operations"`
	Concurrency int                 `json:"concurrency,omitempty"`
	Strategy    string              `json:"strategy,omitempty"`
	Account     string              `json:"account,omitempty"`
}

// apiMultiOpResult mirrors apiCommandResponse for a single op, plus an
// `error` field for ops that failed validation or could not be picked
// to an account. OK=true means the op ran (exit_code may still be non-zero);
// OK=false means we never reached the runner.
type apiMultiOpResult struct {
	ID        string `json:"id"`
	OK        bool   `json:"ok"`
	Account   string `json:"account,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	DurationM int64  `json:"duration_ms"`
	Error     string `json:"error,omitempty"`
}

// apiMultiCommandResponse aggregates per-op results plus batch-level
// warnings (store/state recovery, rotation persistence failures).
type apiMultiCommandResponse struct {
	OK        bool               `json:"ok"`
	DurationM int64              `json:"duration_ms"`
	Results   []apiMultiOpResult `json:"results"`
	Warnings  []string           `json:"warnings,omitempty"`
}

func handleAPIMultiCommand(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, apiMultiCommandBodyMaxBytes)
		defer r.Body.Close()

		var req apiMultiCommandRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid json"})
			return
		}
		if len(req.Operations) == 0 {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "operations is required and must be non-empty"})
			return
		}
		if len(req.Operations) > apiMaxMultiOperations {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: fmt.Sprintf("too many operations (max %d)", apiMaxMultiOperations)})
			return
		}

		// Validate manifest shape (ids, args). Each op gets a normalized args
		// slice for downstream use. Manifest-level errors (duplicate ids,
		// empty args) are 400 because they suggest a buggy client; per-op
		// validation errors (unsupported command) are recorded as per-op
		// results so the rest of the batch still completes.
		idSeen := make(map[string]bool, len(req.Operations))
		opArgs := make([][]string, len(req.Operations))
		for i, op := range req.Operations {
			id := strings.TrimSpace(op.ID)
			if id == "" {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: fmt.Sprintf("operation %d: missing id", i)})
				return
			}
			if idSeen[id] {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: fmt.Sprintf("duplicate operation id %q", id)})
				return
			}
			idSeen[id] = true
			req.Operations[i].ID = id

			cmdName := strings.TrimSpace(op.Command)
			args := make([]string, 0, 1+len(op.Args))
			if cmdName != "" {
				args = append(args, cmdName)
				args = append(args, op.Args...)
			} else if len(op.Args) > 0 {
				args = append(args, op.Args...)
			}
			if len(args) == 0 {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: fmt.Sprintf("operation %q: missing command", id)})
				return
			}
			opArgs[i] = args
		}

		// Rate limit: charge one slot per op so a 16-op batch counts the same
		// as 16 sequential /api/command calls. Without this a single IP could
		// blow past the 60/min budget by 16x.
		if !commandLimiter.allowN(clientIP(r), len(req.Operations)) {
			writeJSON(w, http.StatusTooManyRequests, apiError{OK: false, Error: "rate limit exceeded, try again shortly"})
			return
		}

		// Per-batch concurrency hint (client-controlled). Capped at the
		// process-wide ceiling. Even when this is high, the goroutines also
		// acquire apiSubprocessSem so the cross-endpoint cap is enforced.
		batchConc := apiMultiCommandConcurrency
		if req.Concurrency > 0 && req.Concurrency < batchConc {
			batchConc = req.Concurrency
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

		batchWarnings := make([]string, 0, 4)
		if w := strings.TrimSpace(st.Warning); w != "" {
			batchWarnings = append(batchWarnings, w)
		}

		accountName := strings.TrimSpace(req.Account)
		var rs *state.State
		var strat rotation.Strategy
		if accountName == "" {
			strategyName := strategyFlag
			if s := strings.TrimSpace(req.Strategy); s != "" {
				strategyName = s
			}
			parsed, err := rotation.ParseStrategy(strategyName)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid strategy"})
				return
			}
			strat = parsed

			rs, err = state.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "loading rotation state"})
				return
			}
			if w := strings.TrimSpace(rs.Warning); w != "" {
				batchWarnings = append(batchWarnings, w)
			}
		}

		start := time.Now()

		// Pre-pick accounts sequentially so rotation state is consistent
		// (multi-fetch uses the same pattern). Per-op validation errors are
		// captured inline; the goroutine pool below skips ops that already
		// have an Err set.
		//
		// We work against a *local* copy of the account list. After each pick
		// we increment the picked account's UseCount/LastUsed in this copy so
		// subsequent picks under "least-used" / "least-recently-used" see the
		// updated stats and distribute the batch instead of pinning every op
		// to the same fresh account.
		type opTask struct {
			ID      string
			Args    []string
			Account *store.Account
			Err     string
		}
		tasks := make([]opTask, len(req.Operations))
		var accountsSnapshot []store.Account
		if accountName == "" {
			accountsSnapshot = st.List()
		}
		now := time.Now()
		for i, op := range req.Operations {
			args := opArgs[i]
			t := opTask{ID: op.ID, Args: args}

			first := firstBirdCommand(args)
			if first == "" {
				t.Err = "missing command"
				tasks[i] = t
				continue
			}
			if !birdtool.APIAllowed(first) {
				t.Err = "unsupported command"
				tasks[i] = t
				continue
			}
			if err := ensureBirdCommandAllowed(nil, args); err != nil {
				t.Err = err.Error()
				tasks[i] = t
				continue
			}

			var account *store.Account
			if accountName != "" {
				account, err = st.Get(accountName)
				if err != nil {
					t.Err = err.Error()
					tasks[i] = t
					continue
				}
			} else {
				eligible := accountsSnapshot
				if blocked, name := isMutatingBirdCommand(args); blocked {
					eligible = filterWritableAccounts(accountsSnapshot)
					if len(eligible) == 0 {
						t.Err = fmt.Sprintf("no writable accounts configured for %q", name)
						tasks[i] = t
						continue
					}
				}
				picked, err := rotation.Pick(eligible, strat, rs.LastUsedName)
				if err != nil {
					t.Err = err.Error()
					tasks[i] = t
					continue
				}
				rs.LastUsedName = picked.Name
				// Advance the local snapshot so the next pick under
				// least-used / least-recently-used sees this account as
				// just-used and distributes the batch.
				for j := range accountsSnapshot {
					if accountsSnapshot[j].Name == picked.Name {
						accountsSnapshot[j].UseCount++
						accountsSnapshot[j].LastUsed = now
						break
					}
				}
				account = picked
			}
			if err := ensureBirdCommandAllowed(account, args); err != nil {
				t.Err = err.Error()
				tasks[i] = t
				continue
			}
			t.Account = account
			tasks[i] = t
		}

		// Rotation state is NOT saved here. We persist after the goroutines
		// complete so a batch where every op fails before reaching the runner
		// (e.g. bad BIRDY_BIRD_PATH, missing bird binary) doesn't advance
		// rotation past accounts that never ran.

		var wg sync.WaitGroup
		results := make([]apiMultiOpResult, len(tasks))
		batchSem := make(chan struct{}, batchConc)

		for i, t := range tasks {
			results[i].ID = t.ID
			if t.Err != "" {
				results[i].OK = false
				results[i].Error = t.Err
				continue
			}
			wg.Add(1)
			go func(i int, t opTask) {
				defer wg.Done()
				// Per-batch cap (client hint). Honor client disconnect so
				// queued ops drop instead of being run after the client
				// has gone away.
				select {
				case batchSem <- struct{}{}:
					defer func() { <-batchSem }()
				case <-r.Context().Done():
					results[i] = apiMultiOpResult{ID: t.ID, OK: false, Error: "client closed request"}
					return
				}
				// Process-wide cap shared with /api/command.
				select {
				case apiSubprocessSem <- struct{}{}:
					defer func() { <-apiSubprocessSem }()
				case <-r.Context().Done():
					results[i] = apiMultiOpResult{ID: t.ID, OK: false, Error: "client closed request"}
					return
				}

				opStart := time.Now()
				res, stdout, stderr, runErr := runner.RunCapture(t.Account, t.Args)
				if runErr != nil {
					results[i] = apiMultiOpResult{
						ID:        t.ID,
						OK:        false,
						Error:     runErr.Error(),
						Account:   t.Account.Name,
						DurationM: time.Since(opStart).Milliseconds(),
					}
					return
				}
				// Store.RecordUsage and RecordRateLimit are mutex-locked
				// internally so they're safe to call from multiple
				// goroutines. Save() is deferred to a single call after
				// wg.Wait() to avoid a fsync per op.
				_ = st.RecordUsage(t.Account.Name)
				if res.RateLimited {
					_ = st.RecordRateLimit(t.Account.Name)
				}
				results[i] = apiMultiOpResult{
					ID:        t.ID,
					OK:        true,
					Account:   t.Account.Name,
					ExitCode:  res.ExitCode,
					Stdout:    stdout,
					Stderr:    stderr,
					DurationM: time.Since(opStart).Milliseconds(),
				}
			}(i, t)
		}
		wg.Wait()

		// Persist rotation state only when at least one op reached the runner
		// (mirrors /api/command semantics). Use the LAST successful op's
		// account as LastUsedName so a partially-successful batch behaves
		// like a series of sequential single-op calls would have.
		if rs != nil {
			var lastSuccessfulAccount string
			for _, res := range results {
				if res.OK {
					lastSuccessfulAccount = res.Account
				}
			}
			if lastSuccessfulAccount != "" {
				rs.LastUsedName = lastSuccessfulAccount
				if err := rs.Save(); err != nil {
					batchWarnings = append(batchWarnings, fmt.Sprintf("failed to save rotation state: %v", err))
				}
			}
		}

		if err := st.Save(); err != nil {
			batchWarnings = append(batchWarnings, fmt.Sprintf("failed to save account store: %v", err))
		}

		writeJSON(w, http.StatusOK, apiMultiCommandResponse{
			OK:        true,
			DurationM: time.Since(start).Milliseconds(),
			Results:   results,
			Warnings:  batchWarnings,
		})
	}
}
