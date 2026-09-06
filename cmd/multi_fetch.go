package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
	"github.com/spf13/cobra"
)

// MultiFetchOperation is one invocation in a multi-fetch manifest. `args` is
// the command argv exactly as it would follow `birdy` on the command line
// (e.g. ["user-tweets", "@OpenAI", "-n", "20", "--json", "--plain"]).
type MultiFetchOperation struct {
	ID   string   `json:"id"`
	Args []string `json:"args"`
}

// MultiFetchManifest is the input format for `birdy multi-fetch`.
type MultiFetchManifest struct {
	Operations  []MultiFetchOperation `json:"operations"`
	Concurrency int                   `json:"concurrency,omitempty"`
}

// Per-op statuses reported in _report.json and on the ✗ lines.
const (
	multiFetchStatusOK          = "ok"
	multiFetchStatusRateLimited = "rate_limited"
	multiFetchStatusNotFound    = "not_found"
	multiFetchStatusTimeout     = "timeout"
	multiFetchStatusError       = "error"
)

// multiFetchReportFile is written into the output directory next to the
// per-op files. Op ids starting with "_" are reserved so an op can never
// shadow it (or any future multi-fetch-owned file).
const multiFetchReportFile = "_report.json"

var (
	multiFetchManifest         string
	multiFetchOutputDir        string
	multiFetchConcurrency      int
	multiFetchEmptyOnFail      bool
	multiFetchRetryRateLimited int
	multiFetchRateLimitDelay   time.Duration
	multiFetchOpTimeout        time.Duration
	multiFetchReportPath       string
	multiFetchAggregatePath    string
)

// multiFetchRunFunc is the execution seam for one op: run args as account and
// return the engine's result plus captured stdout/stderr. err is reserved for
// "birdy could not run the op at all" (missing engine, cancelled context);
// an op that ran and failed comes back with err == nil and a non-zero
// ExitCode, exactly like a subprocess would. Tests script this to drive
// 429 / not-found / timeout paths without touching the network.
type multiFetchRunFunc func(ctx context.Context, account *store.Account, args []string) (runner.Result, string, string, error)

var multiFetchRun multiFetchRunFunc = runMultiFetchOp

// multiFetchEngine names the engine that will serve args: "native" when
// birdy serves the command in-process (the same rule passthrough.go applies),
// "bird" when it is handed to the bird CLI subprocess (--bird, or a
// command/flag the native engine has not implemented).
func multiFetchEngine(args []string) string {
	if len(args) == 0 {
		return "bird"
	}
	if !useBird() && nativeSupports(args[0]) && nativeAcceptsFlags(args[0], args[1:]) {
		return "native"
	}
	return "bird"
}

// runMultiFetchOp is the production multiFetchRun. Native ops run in-process
// through runNative — no subprocess, no bird binary on PATH — and their error
// is presented the way a subprocess would present it: exit 1 with the message
// on stderr, classified through the same markers the runner's scanner uses.
func runMultiFetchOp(ctx context.Context, account *store.Account, args []string) (runner.Result, string, string, error) {
	if multiFetchEngine(args) != "native" {
		return runner.RunCaptureContext(ctx, account, args, runner.Options{})
	}
	var out bytes.Buffer
	err := runNative(ctx, account, args[0], args[1:], &out)
	if err == nil {
		return runner.Result{}, out.String(), "", nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return runner.Result{ExitCode: 1}, out.String(), "", fmt.Errorf("running native %s: %w", args[0], ctxErr)
	}
	msg := err.Error()
	return runner.Result{
		ExitCode:    1,
		RateLimited: xapi.IsRateLimited(err),
		NotFound:    runner.IsNotFoundMessage([]byte(msg)),
	}, out.String(), msg, nil
}

func newMultiFetchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "multi-fetch",
		Short:   "Run multiple birdy operations in parallel from a manifest",
		GroupID: "birdy",
		Long: `multi-fetch executes a manifest of operations concurrently and writes
each result to <output-dir>/<id>.json, plus a machine-readable
<output-dir>/_report.json describing every op (status, account, attempts,
duration, bytes, error) and a summary.

Manifest schema (JSON):

  {
    "operations": [
      {"id": "OpenAI",    "args": ["user-tweets", "@OpenAI", "-n", "20", "--json", "--plain"]},
      {"id": "search-ai", "args": ["search", "AI announcement", "-n", "50", "--json", "--plain"]},
      {"id": "news",      "args": ["news", "--ai-only", "-n", "40", "--json", "--plain"]}
    ],
    "concurrency": 8
  }

Ids must be unique, contain no path separators, and must not start with "_"
(reserved for multi-fetch's own files). Pass --manifest - to read the
manifest from stdin. The output directory is created if it doesn't exist.

Ops are served by birdy's native engine in-process (the bird CLI only when
--bird is set or a flag the native engine lacks appears). A rate-limited op
(HTTP 429) records the 429 against its account and is retried once — on a
different account that is not inside its 15-minute 429 cooldown — before it
is reported as rate_limited (--retry-rate-limited, --rate-limit-delay).
Statuses: ok | rate_limited | not_found | timeout | error.

On op failure, an empty array '[]' is written to the per-op file by default
so JSON consumers keep working; _report.json is how a consumer tells a quiet
handle from a rate-limited one (override with --empty-on-fail=false to make
any failure fail the command). --aggregate joins every successful op into one
document: {"accounts": {id: [...]}, "searches": {id: [...]}, "news": [...],
"other": {id: raw}, "meta": {...}}.

Account rotation reuses the same store + strategies as the passthrough
commands (use --strategy or --account, default: round-robin). Use the
BIRDY_ACCOUNTS env var for stateless account injection in CI:

  export BIRDY_ACCOUNTS='[{"name":"ci","auth_token":"...","ct0":"..."}]'
  birdy multi-fetch --manifest m.json --output-dir /tmp/bird --aggregate /tmp/bird.json`,
		RunE: runMultiFetch,
	}
	cmd.Flags().StringVarP(&multiFetchManifest, "manifest", "m", "", "path to JSON manifest (use '-' for stdin)")
	cmd.Flags().StringVarP(&multiFetchOutputDir, "output-dir", "o", "", "directory to write per-op output files")
	cmd.Flags().IntVarP(&multiFetchConcurrency, "concurrency", "c", 6, "number of parallel ops (overridden by manifest.concurrency if non-zero)")
	cmd.Flags().BoolVar(&multiFetchEmptyOnFail, "empty-on-fail", true, "write '[]' on op failure instead of leaving the file absent")
	cmd.Flags().IntVar(&multiFetchRetryRateLimited, "retry-rate-limited", 1, "how many times a rate-limited op is retried on a different, non-cooling account (0 disables)")
	cmd.Flags().DurationVar(&multiFetchRateLimitDelay, "rate-limit-delay", 0, "pause before each rate-limit retry (holds the op's concurrency slot)")
	cmd.Flags().DurationVar(&multiFetchOpTimeout, "op-timeout", 0, "per-attempt timeout; an op that exceeds it is reported as 'timeout' (0 = none)")
	cmd.Flags().StringVar(&multiFetchReportPath, "report", "", "where to write the per-op report (default <output-dir>/_report.json)")
	cmd.Flags().StringVar(&multiFetchAggregatePath, "aggregate", "", "also write one JSON document joining every successful op's output")
	_ = cmd.MarkFlagRequired("manifest")
	_ = cmd.MarkFlagRequired("output-dir")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMultiFetchCmd())
}

// validateMultiFetchID rejects ids that could escape the output directory or
// collide with multi-fetch's own files.
func validateMultiFetchID(id string) error {
	// Only the exact basename multi-fetch writes itself is reserved. A
	// leading "_" alone is NOT: X handles may start with an underscore
	// (@_cpatonn), and callers use the handle as the op id — reserving the
	// prefix rejected a real production manifest (2026-09-06).
	if id+".json" == multiFetchReportFile {
		return fmt.Errorf("invalid id %q: reserved for multi-fetch's own files (%s)", id, multiFetchReportFile)
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid id %q: must not be \".\"/\"..\" or contain path separators", id)
	}
	return nil
}

func validateMultiFetchManifest(m *MultiFetchManifest) error {
	if len(m.Operations) == 0 {
		return fmt.Errorf("manifest contains no operations")
	}
	idSeen := make(map[string]bool, len(m.Operations))
	for i, op := range m.Operations {
		if op.ID == "" {
			return fmt.Errorf("operation %d has empty id", i)
		}
		if err := validateMultiFetchID(op.ID); err != nil {
			return fmt.Errorf("operation %d: %w", i, err)
		}
		if idSeen[op.ID] {
			return fmt.Errorf("duplicate operation id %q", op.ID)
		}
		idSeen[op.ID] = true
		if len(op.Args) == 0 {
			return fmt.Errorf("operation %q has empty args", op.ID)
		}
	}
	return nil
}

type multiFetchTask struct {
	Op       MultiFetchOperation
	Account  *store.Account
	Mutating bool // a retry must also stay on a writable account
}

// multiFetchOutcome is the final state of one op after every attempt.
type multiFetchOutcome struct {
	ID       string
	Status   string
	Engine   string
	Account  string // account of the final attempt
	Attempts int
	ExitCode int
	Duration time.Duration
	Bytes    int
	Stdout   string
	Reason   string // stderr excerpt / run error for failed ops
	RunErr   error  // non-nil when birdy could not run the op at all
}

type multiFetchOptions struct {
	RetryRateLimited int
	RateLimitDelay   time.Duration
	OpTimeout        time.Duration
	EmptyOnFail      bool
	PinnedAccount    bool // --account: there is no other account to retry on
}

// multiFetchOpReport is one entry of _report.json's "ops".
type multiFetchOpReport struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	OK         bool   `json:"ok"`
	Account    string `json:"account"`
	Engine     string `json:"engine"`
	Attempts   int    `json:"attempts"`
	DurationMS int64  `json:"duration_ms"`
	Bytes      int    `json:"bytes"`
	Error      string `json:"error,omitempty"`
}

// multiFetchSummary is _report.json's "summary" (and the aggregate's
// meta.ops). rate_limited + not_found + timeout + error == failed.
type multiFetchSummary struct {
	Total       int   `json:"total"`
	OK          int   `json:"ok"`
	Failed      int   `json:"failed"`
	RateLimited int   `json:"rate_limited"`
	NotFound    int   `json:"not_found"`
	Timeout     int   `json:"timeout"`
	Error       int   `json:"error"`
	Retried     int   `json:"retried"`
	WallMS      int64 `json:"wall_ms"`
	Concurrency int   `json:"concurrency"`
}

type multiFetchReport struct {
	Ops     []multiFetchOpReport `json:"ops"`
	Summary multiFetchSummary    `json:"summary"`
}

type multiFetchFailedOp struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type multiFetchAggregateMeta struct {
	GeneratedAt    string               `json:"generated_at"`
	Ops            multiFetchSummary    `json:"ops"`
	FailedOps      []multiFetchFailedOp `json:"failed_ops"`
	RateLimitedOps []string             `json:"rate_limited_ops"`
}

// multiFetchAggregate is the --aggregate document. Maps marshal with sorted
// keys, so the shape is deterministic for a given set of outcomes.
type multiFetchAggregate struct {
	Accounts map[string]json.RawMessage `json:"accounts"`
	Searches map[string]json.RawMessage `json:"searches"`
	News     []json.RawMessage          `json:"news"`
	Other    map[string]json.RawMessage `json:"other"`
	Meta     multiFetchAggregateMeta    `json:"meta"`
}

func runMultiFetch(cmd *cobra.Command, _ []string) error {
	errOut := cmd.ErrOrStderr()
	out := cmd.OutOrStdout()

	// Load manifest from stdin or file.
	var data []byte
	var err error
	if multiFetchManifest == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(multiFetchManifest)
	}
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}
	var m MultiFetchManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}
	if err := validateMultiFetchManifest(&m); err != nil {
		return err
	}

	conc := multiFetchConcurrency
	if m.Concurrency > 0 {
		conc = m.Concurrency
	}
	if conc < 1 {
		conc = 1
	}
	if multiFetchRetryRateLimited < 0 {
		return fmt.Errorf("--retry-rate-limited must be >= 0")
	}

	if err := os.MkdirAll(multiFetchOutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	reportPath := multiFetchReportPath
	if reportPath == "" {
		reportPath = filepath.Join(multiFetchOutputDir, multiFetchReportFile)
	}

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("opening account store: %w", err)
	}
	printStoreWarning(errOut, st)
	if st.Len() == 0 {
		return fmt.Errorf("no accounts configured\nSet BIRDY_ACCOUNTS env var or run: birdy account add <name>")
	}

	rs, err := state.Load()
	if err != nil {
		return fmt.Errorf("loading rotation state: %w", err)
	}
	printStateWarning(errOut, rs)

	strat, err := rotation.ParseStrategy(strategyFlag)
	if err != nil {
		return err
	}

	// Pre-pick the FIRST attempt's account for every op sequentially so the
	// rotation cursor stays consistent. Picks come from a local snapshot that
	// is advanced after each pick (UseCount/LastUsed), so least-used and
	// quota-aware distribute the batch instead of pinning every op to the
	// same "freshest" account (the fix api_multi.go already carries).
	//
	// Retries are picked live, per op, inside the goroutine — see
	// executeMultiFetchTask. That is where a 429 recorded by op N changes
	// the account op N+1's retry lands on.
	tasks := make([]multiFetchTask, 0, len(m.Operations))
	accountsSnapshot := st.List()
	now := time.Now()
	for _, op := range m.Operations {
		mutating, mutName := isMutatingBirdCommand(op.Args)
		var acc *store.Account
		if accountFlag != "" {
			acc, err = st.Get(accountFlag)
			if err != nil {
				return err
			}
		} else {
			eligible := accountsSnapshot
			if mutating {
				eligible = filterWritableAccounts(accountsSnapshot)
				if len(eligible) == 0 {
					return fmt.Errorf("no writable accounts configured for op %q (%s)", op.ID, mutName)
				}
			}
			picked, err := rotation.Pick(eligible, strat, rs.LastUsedName)
			if err != nil {
				return err
			}
			rs.LastUsedName = picked.Name
			for j := range accountsSnapshot {
				if accountsSnapshot[j].Name == picked.Name {
					accountsSnapshot[j].UseCount++
					accountsSnapshot[j].LastUsed = now
					break
				}
			}
			acc = picked
		}
		tasks = append(tasks, multiFetchTask{Op: op, Account: acc, Mutating: mutating})
	}

	opts := multiFetchOptions{
		RetryRateLimited: multiFetchRetryRateLimited,
		RateLimitDelay:   multiFetchRateLimitDelay,
		OpTimeout:        multiFetchOpTimeout,
		EmptyOnFail:      multiFetchEmptyOnFail,
		PinnedAccount:    accountFlag != "",
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Concurrent execution with bounded parallelism. An op holds its slot for
	// every attempt (including the optional retry delay), so total in-flight
	// work never exceeds `conc` and total attempts never exceed
	// len(ops) * (1 + retries).
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	outcomes := make([]multiFetchOutcome, len(tasks))
	start := time.Now()
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t multiFetchTask) {
			defer wg.Done()
			defer func() { <-sem }()
			outPath := filepath.Join(multiFetchOutputDir, t.Op.ID+".json")
			outcomes[i] = executeMultiFetchTask(ctx, st, t, outPath, opts)
		}(i, t)
	}
	wg.Wait()
	wall := time.Since(start)

	// Persist what the run learned. For a file-backed store this is what
	// makes the NEXT run's quota-aware picks skip an account that is still
	// inside its 429 cooldown; for an ephemeral BIRDY_ACCOUNTS store Save is
	// a no-op and the 429 state only informs this run's retries.
	if err := st.Save(); err != nil {
		fmt.Fprintf(errOut, "warning: failed to save account store: %v\n", err)
	}
	if err := rs.Save(); err != nil {
		fmt.Fprintf(errOut, "warning: failed to save rotation state: %v\n", err)
	}

	// Per-op lines. The ✓/✗ shapes are stable: downstream log greps depend
	// on them.
	summary := multiFetchSummary{Total: len(tasks), Concurrency: conc, WallMS: wall.Milliseconds()}
	for _, o := range outcomes {
		if o.Attempts > 1 {
			summary.Retried++
		}
		switch o.Status {
		case multiFetchStatusOK:
			summary.OK++
			line := fmt.Sprintf("✓ %-30s %5d bytes  %5dms", o.ID, o.Bytes, o.Duration.Milliseconds())
			if o.Attempts > 1 {
				line += fmt.Sprintf("  (attempt %d on %s)", o.Attempts, o.Account)
			}
			fmt.Fprintln(out, line)
			continue
		case multiFetchStatusRateLimited:
			summary.RateLimited++
		case multiFetchStatusNotFound:
			summary.NotFound++
		case multiFetchStatusTimeout:
			summary.Timeout++
		default:
			summary.Error++
		}
		summary.Failed++
		fmt.Fprintf(errOut, "✗ %-30s %s\n", o.ID, describeMultiFetchFailure(o))
	}
	fmt.Fprintf(out, "\n%s\n", formatMultiFetchSummary(summary))

	var errs []error
	report := buildMultiFetchReport(outcomes, summary)
	if err := writeMultiFetchJSON(reportPath, report, true); err != nil {
		errs = append(errs, fmt.Errorf("writing report: %w", err))
	} else {
		fmt.Fprintf(out, "report: %s\n", reportPath)
	}
	if multiFetchAggregatePath != "" {
		agg := buildMultiFetchAggregate(tasks, outcomes, summary, time.Now())
		if err := writeMultiFetchJSON(multiFetchAggregatePath, agg, false); err != nil {
			errs = append(errs, fmt.Errorf("writing aggregate: %w", err))
		} else {
			fmt.Fprintf(out, "aggregate: %s\n", multiFetchAggregatePath)
		}
	}
	if summary.Failed > 0 && !multiFetchEmptyOnFail {
		errs = append(errs, fmt.Errorf("%d/%d ops failed", summary.Failed, summary.Total))
	}
	return errors.Join(errs...)
}

// executeMultiFetchTask runs one op to completion: the pre-picked first
// attempt, then at most opts.RetryRateLimited retries, each on a different
// account that is not inside its 429 cooldown. It records usage and 429s
// against the store as it goes, which is what lets a later op's retry avoid
// the account this one just exhausted. Never touches the rotation cursor.
func executeMultiFetchTask(ctx context.Context, st *store.Store, t multiFetchTask, outPath string, opts multiFetchOptions) multiFetchOutcome {
	opStart := time.Now()
	o := multiFetchOutcome{ID: t.Op.ID, Engine: multiFetchEngine(t.Op.Args)}
	acc := t.Account
	tried := make(map[string]bool, 2)
	retriesLeft := opts.RetryRateLimited

	for {
		o.Attempts++
		o.Account = acc.Name
		tried[acc.Name] = true

		attemptCtx := ctx
		cancel := func() {}
		if opts.OpTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, opts.OpTimeout)
		}
		res, stdout, stderr, runErr := multiFetchRun(attemptCtx, acc, t.Op.Args)
		cancel()

		// Store bookkeeping is mutex-locked, so it is safe from the pool;
		// Save() happens once after wg.Wait().
		_ = st.RecordUsage(acc.Name)
		if res.RateLimited {
			_ = st.RecordRateLimit(acc.Name)
		}

		o.Status = classifyMultiFetchOp(res, stdout, runErr)
		o.ExitCode = res.ExitCode
		o.Stdout = stdout
		o.RunErr = runErr
		o.Reason = multiFetchReason(res, stdout, stderr, runErr)

		if o.Status != multiFetchStatusRateLimited || retriesLeft <= 0 || opts.PinnedAccount {
			break
		}
		next := pickMultiFetchRetryAccount(st.List(), tried, t.Mutating, time.Now())
		if next == nil {
			break // every other account is cooling, read-only, or already tried
		}
		retriesLeft--
		if opts.RateLimitDelay > 0 && !multiFetchSleep(ctx, opts.RateLimitDelay) {
			break
		}
		acc = next
	}
	o.Duration = time.Since(opStart)

	if o.Status == multiFetchStatusOK {
		if err := writeMultiFetchFile(outPath, []byte(o.Stdout)); err != nil {
			o.Status = multiFetchStatusError
			o.Reason = fmt.Sprintf("writing %s: %v", outPath, err)
			o.Stdout = ""
			return o
		}
		o.Bytes = len(o.Stdout)
		return o
	}
	if opts.EmptyOnFail {
		_ = writeMultiFetchFile(outPath, []byte("[]"))
	}
	return o
}

// classifyMultiFetchOp maps an engine result onto a report status. An op is
// ok only when it ran, exited 0, and produced output; a 429 marker on an
// otherwise successful op does not fail it (the account is still stamped).
func classifyMultiFetchOp(res runner.Result, stdout string, runErr error) string {
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			return multiFetchStatusTimeout
		}
		return multiFetchStatusError
	}
	if res.ExitCode == 0 && stdout != "" {
		return multiFetchStatusOK
	}
	if res.RateLimited {
		return multiFetchStatusRateLimited
	}
	if res.NotFound {
		return multiFetchStatusNotFound
	}
	return multiFetchStatusError
}

func multiFetchReason(res runner.Result, stdout, stderr string, runErr error) string {
	if runErr != nil {
		return runErr.Error()
	}
	if res.ExitCode == 0 && stdout != "" {
		return ""
	}
	if s := truncate(strings.TrimSpace(stderr), 200); s != "" {
		return s
	}
	if res.ExitCode == 0 {
		return "empty stdout"
	}
	return ""
}

// describeMultiFetchFailure renders the ✗ line's detail. The engine label is
// the engine that actually ran the op — birdy's native engine unless --bird
// forced the bird CLI — so a log never blames a binary that was not there.
func describeMultiFetchFailure(o multiFetchOutcome) string {
	if o.RunErr != nil {
		return fmt.Sprintf("%s status=%s account=%s attempts=%d err=%v", o.Engine, o.Status, o.Account, o.Attempts, o.RunErr)
	}
	return fmt.Sprintf("%s exit=%d status=%s account=%s attempts=%d stderr=%q", o.Engine, o.ExitCode, o.Status, o.Account, o.Attempts, o.Reason)
}

// formatMultiFetchSummary renders the closing line. "multi-fetch: N ops, N ok,
// N failed" stays a prefix of it so existing greps keep matching.
func formatMultiFetchSummary(s multiFetchSummary) string {
	breakdown := ""
	if s.Failed > 0 {
		parts := make([]string, 0, 4)
		for _, c := range []struct {
			n    int
			name string
		}{
			{s.RateLimited, multiFetchStatusRateLimited},
			{s.NotFound, multiFetchStatusNotFound},
			{s.Timeout, multiFetchStatusTimeout},
			{s.Error, multiFetchStatusError},
		} {
			if c.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", c.n, c.name))
			}
		}
		breakdown = " (" + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("multi-fetch: %d ops, %d ok, %d failed%s, %d retried, %dms wall (concurrency=%d)",
		s.Total, s.OK, s.Failed, breakdown, s.Retried, s.WallMS, s.Concurrency)
}

func multiFetchCooling(a store.Account, now time.Time) bool {
	return !a.LastRateLimitedAt.IsZero() && now.Sub(a.LastRateLimitedAt) < rotation.QuotaCooldown
}

// pickMultiFetchRetryAccount chooses where a rate-limited op is retried:
// quota-aware over the accounts not yet tried for this op, minus anything
// disabled, cooling, or (for a mutating op) read-only. nil means there is no
// account worth retrying on, so the op is reported rate_limited as-is.
func pickMultiFetchRetryAccount(accounts []store.Account, tried map[string]bool, mutating bool, now time.Time) *store.Account {
	eligible := make([]store.Account, 0, len(accounts))
	for _, a := range accounts {
		if tried[a.Name] || a.Disabled || multiFetchCooling(a, now) {
			continue
		}
		if mutating && a.ReadOnly {
			continue
		}
		eligible = append(eligible, a)
	}
	if len(eligible) == 0 {
		return nil
	}
	picked, err := rotation.Pick(eligible, rotation.QuotaAware, "")
	if err != nil {
		return nil
	}
	return picked
}

func multiFetchSleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func buildMultiFetchReport(outcomes []multiFetchOutcome, summary multiFetchSummary) multiFetchReport {
	ops := make([]multiFetchOpReport, 0, len(outcomes))
	for _, o := range outcomes {
		ops = append(ops, multiFetchOpReport{
			ID:         o.ID,
			Status:     o.Status,
			OK:         o.Status == multiFetchStatusOK,
			Account:    o.Account,
			Engine:     o.Engine,
			Attempts:   o.Attempts,
			DurationMS: o.Duration.Milliseconds(),
			Bytes:      o.Bytes,
			Error:      o.Reason,
		})
	}
	return multiFetchReport{Ops: ops, Summary: summary}
}

// buildMultiFetchAggregate joins successful outputs by op kind. Only ops
// whose stdout is valid JSON are joined; the rest are listed in
// meta.failed_ops so a consumer never mistakes a missing key for a quiet
// handle.
func buildMultiFetchAggregate(tasks []multiFetchTask, outcomes []multiFetchOutcome, summary multiFetchSummary, now time.Time) multiFetchAggregate {
	agg := multiFetchAggregate{
		Accounts: map[string]json.RawMessage{},
		Searches: map[string]json.RawMessage{},
		News:     []json.RawMessage{},
		Other:    map[string]json.RawMessage{},
		Meta: multiFetchAggregateMeta{
			GeneratedAt:    now.UTC().Format(time.RFC3339),
			Ops:            summary,
			FailedOps:      []multiFetchFailedOp{},
			RateLimitedOps: []string{},
		},
	}
	for i, o := range outcomes {
		if o.Status != multiFetchStatusOK {
			agg.Meta.FailedOps = append(agg.Meta.FailedOps, multiFetchFailedOp{ID: o.ID, Status: o.Status, Error: o.Reason})
			if o.Status == multiFetchStatusRateLimited {
				agg.Meta.RateLimitedOps = append(agg.Meta.RateLimitedOps, o.ID)
			}
			continue
		}
		raw := json.RawMessage(strings.TrimSpace(o.Stdout))
		if !json.Valid(raw) {
			agg.Meta.FailedOps = append(agg.Meta.FailedOps, multiFetchFailedOp{ID: o.ID, Status: multiFetchStatusError, Error: "stdout is not valid JSON"})
			continue
		}
		switch firstBirdCommand(tasks[i].Op.Args) {
		case "user-tweets":
			agg.Accounts[o.ID] = raw
		case "search":
			agg.Searches[o.ID] = raw
		case "news":
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err == nil {
				agg.News = append(agg.News, items...)
			} else {
				agg.News = append(agg.News, raw)
			}
		default:
			agg.Other[o.ID] = raw
		}
	}
	return agg
}

func writeMultiFetchJSON(path string, v any, indent bool) error {
	var data []byte
	var err error
	if indent {
		data, err = json.MarshalIndent(v, "", "  ")
	} else {
		data, err = json.Marshal(v)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeMultiFetchFile(path, append(data, '\n'))
}

// writeMultiFetchFile writes atomically (temp file + rename in the same
// directory) so a consumer watching the output dir never reads a torn file.
func writeMultiFetchFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
