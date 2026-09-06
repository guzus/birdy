package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/store"
)

// setupMultiFetchTest isolates HOME, seeds a file-backed store with the named
// accounts (in order, so round-robin's first pick is deterministic), and
// resets the package-level flags multi-fetch reads. Returns the accounts.json
// path so tests can re-open the store and assert what the run persisted.
func setupMultiFetchTest(t *testing.T, accounts ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", "")
	t.Setenv("BIRDY_USE_BIRD", "")

	storePath := filepath.Join(home, ".config", "birdy", "accounts.json")
	st, err := store.OpenPath(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, name := range accounts {
		if err := st.Add(name, "tok-"+name, "ct0-"+name); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	if err := st.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}

	prevStrategy, prevAccount, prevBird, prevRun := strategyFlag, accountFlag, birdFlag, multiFetchRun
	strategyFlag, accountFlag, birdFlag = "round-robin", "", false
	t.Cleanup(func() {
		strategyFlag, accountFlag, birdFlag, multiFetchRun = prevStrategy, prevAccount, prevBird, prevRun
	})
	return storePath
}

func writeMultiFetchManifest(t *testing.T, ops ...MultiFetchOperation) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	data, err := json.Marshal(MultiFetchManifest{Operations: ops, Concurrency: 2})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// runMultiFetchCmd executes a fresh command so every flag starts at its
// default; cobra does not reset flag values between Execute calls.
func runMultiFetchCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newMultiFetchCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

type scriptedCall struct {
	Op      string // the op's positional target, e.g. "@OpenAI"
	Account string
}

// scriptedRunner records every (op, account) attempt and answers from a
// handler keyed on the op target and the per-op attempt number.
type scriptedRunner struct {
	mu       sync.Mutex
	calls    []scriptedCall
	attempts map[string]int
	handle   func(op, account string, attempt int, ctx context.Context) (runner.Result, string, string, error)
}

func newScriptedRunner(handle func(op, account string, attempt int, ctx context.Context) (runner.Result, string, string, error)) *scriptedRunner {
	return &scriptedRunner{attempts: map[string]int{}, handle: handle}
}

func (s *scriptedRunner) run(ctx context.Context, account *store.Account, args []string) (runner.Result, string, string, error) {
	op := args[0] // command name when there is no positional (news, whoami)
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			op = a
			break
		}
	}
	s.mu.Lock()
	s.attempts[op]++
	attempt := s.attempts[op]
	s.calls = append(s.calls, scriptedCall{Op: op, Account: account.Name})
	s.mu.Unlock()
	return s.handle(op, account.Name, attempt, ctx)
}

func (s *scriptedRunner) callsFor(op string) []scriptedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []scriptedCall
	for _, c := range s.calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func rateLimitedResult() (runner.Result, string, string, error) {
	return runner.Result{ExitCode: 1, RateLimited: true}, "", "x api: HTTP 429: <html>Too Many Requests</html>", nil
}

func readMultiFetchReport(t *testing.T, path string) multiFetchReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var r multiFetchReport
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("parse report: %v\n%s", err, data)
	}
	return r
}

func reportOp(t *testing.T, r multiFetchReport, id string) multiFetchOpReport {
	t.Helper()
	for _, op := range r.Ops {
		if op.ID == id {
			return op
		}
	}
	t.Fatalf("op %q missing from report: %+v", id, r.Ops)
	return multiFetchOpReport{}
}

var summaryLinePattern = regexp.MustCompile(`(?m)^multi-fetch: (\d+) ops, (\d+) ok, (\d+) failed(?: \(([^)]*)\))?, (\d+) retried, \d+ms wall \(concurrency=(\d+)\)$`)

func TestMultiFetchRetriesRateLimitedOpOnDifferentAccount(t *testing.T) {
	storePath := setupMultiFetchTest(t, "alpha", "beta")
	outDir := filepath.Join(t.TempDir(), "out")

	sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
		if attempt == 1 {
			return rateLimitedResult()
		}
		return runner.Result{}, `[{"id":"1"}]`, "", nil
	})
	multiFetchRun = sr.run

	manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "-n", "20", "--json", "--plain"}})
	stdout, stderr, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", outDir)
	if err != nil {
		t.Fatalf("multi-fetch returned error: %v\nstderr=%s", err, stderr)
	}

	calls := sr.callsFor("@OpenAI")
	if len(calls) != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d: %+v", len(calls), calls)
	}
	if calls[0].Account != "alpha" || calls[1].Account != "beta" {
		t.Fatalf("expected retry on a different account (alpha then beta), got %+v", calls)
	}

	body, err := os.ReadFile(filepath.Join(outDir, "OpenAI.json"))
	if err != nil {
		t.Fatalf("read op output: %v", err)
	}
	if string(body) != `[{"id":"1"}]` {
		t.Fatalf("expected retried output written, got %q", body)
	}
	if !strings.Contains(stdout, "✓ OpenAI") || !strings.Contains(stdout, "(attempt 2 on beta)") {
		t.Fatalf("expected ✓ line with retry annotation, got:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, "bird exit=") {
		t.Fatalf("stale 'bird exit=' label must not appear:\n%s%s", stdout, stderr)
	}

	m := summaryLinePattern.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("summary line not found in:\n%s", stdout)
	}
	if m[1] != "1" || m[2] != "1" || m[3] != "0" || m[4] != "" || m[5] != "1" || m[6] != "2" {
		t.Fatalf("unexpected summary fields %q", m[0])
	}
	if !strings.Contains(stdout, "multi-fetch: 1 ops, 1 ok, 0 failed") {
		t.Fatalf("legacy summary prefix missing:\n%s", stdout)
	}

	r := readMultiFetchReport(t, filepath.Join(outDir, "_report.json"))
	op := reportOp(t, r, "OpenAI")
	if op.Status != "ok" || !op.OK || op.Account != "beta" || op.Attempts != 2 || op.Bytes != len(`[{"id":"1"}]`) || op.Error != "" || op.Engine != "native" {
		t.Fatalf("unexpected report op: %+v", op)
	}
	if r.Summary != (multiFetchSummary{Total: 1, OK: 1, Retried: 1, Concurrency: 2, WallMS: r.Summary.WallMS}) {
		t.Fatalf("unexpected summary: %+v", r.Summary)
	}

	// The 429 was recorded against alpha and persisted, so the next run's
	// quota-aware picks (and this run's retries) see it.
	st, err := store.OpenPath(storePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	alpha, _ := st.Get("alpha")
	beta, _ := st.Get("beta")
	if alpha.LastRateLimitedAt.IsZero() || alpha.RateLimitCount != 1 {
		t.Fatalf("expected alpha's 429 persisted, got %+v", alpha)
	}
	if !beta.LastRateLimitedAt.IsZero() {
		t.Fatalf("beta must not be marked rate-limited: %+v", beta)
	}
	if alpha.UseCount != 1 || beta.UseCount != 1 {
		t.Fatalf("expected one recorded use per account, got alpha=%d beta=%d", alpha.UseCount, beta.UseCount)
	}
}

func TestMultiFetchDoesNotRetryOnCoolingOrPinnedAccount(t *testing.T) {
	t.Run("only other account is cooling", func(t *testing.T) {
		storePath := setupMultiFetchTest(t, "alpha", "beta")
		st, _ := store.OpenPath(storePath)
		if err := st.RecordRateLimit("beta"); err != nil {
			t.Fatal(err)
		}
		if err := st.Save(); err != nil {
			t.Fatal(err)
		}
		sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
			return rateLimitedResult()
		})
		multiFetchRun = sr.run
		manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}})
		outDir := t.TempDir()
		if _, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", outDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls := sr.callsFor("@OpenAI"); len(calls) != 1 || calls[0].Account != "alpha" {
			t.Fatalf("expected a single attempt on alpha, got %+v", calls)
		}
		op := reportOp(t, readMultiFetchReport(t, filepath.Join(outDir, "_report.json")), "OpenAI")
		if op.Status != "rate_limited" || op.Attempts != 1 {
			t.Fatalf("unexpected op: %+v", op)
		}
	})

	t.Run("--retry-rate-limited=0", func(t *testing.T) {
		setupMultiFetchTest(t, "alpha", "beta")
		sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
			return rateLimitedResult()
		})
		multiFetchRun = sr.run
		manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}})
		if _, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", t.TempDir(), "--retry-rate-limited", "0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls := sr.callsFor("@OpenAI"); len(calls) != 1 {
			t.Fatalf("expected no retry, got %+v", calls)
		}
	})

	t.Run("pinned --account", func(t *testing.T) {
		setupMultiFetchTest(t, "alpha", "beta")
		accountFlag = "alpha"
		sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
			return rateLimitedResult()
		})
		multiFetchRun = sr.run
		manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}})
		if _, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls := sr.callsFor("@OpenAI"); len(calls) != 1 || calls[0].Account != "alpha" {
			t.Fatalf("expected a single attempt on the pinned account, got %+v", calls)
		}
	})
}

func TestMultiFetchReportClassifiesStatusesAndSummaryLine(t *testing.T) {
	setupMultiFetchTest(t, "alpha", "beta")
	outDir := filepath.Join(t.TempDir(), "out")

	sr := newScriptedRunner(func(op, account string, attempt int, ctx context.Context) (runner.Result, string, string, error) {
		switch op {
		case "@OpenAI":
			return runner.Result{}, `[{"id":"1"}]`, "", nil
		case "@CohereAI":
			return runner.Result{ExitCode: 1, NotFound: true}, "", `x api: user "CohereAI" not found`, nil
		case "AI announcement":
			return rateLimitedResult() // on every account
		case "@broken":
			return runner.Result{ExitCode: 1}, "", "boom", nil
		case "@slow":
			<-ctx.Done()
			return runner.Result{ExitCode: 1}, "", "", fmt.Errorf("running native user-tweets: %w", ctx.Err())
		}
		return runner.Result{}, "", "", errors.New("unexpected op " + op)
	})
	multiFetchRun = sr.run

	manifest := writeMultiFetchManifest(t,
		MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}},
		MultiFetchOperation{ID: "CohereAI", Args: []string{"user-tweets", "@CohereAI", "--json"}},
		MultiFetchOperation{ID: "search-ai", Args: []string{"search", "AI announcement", "-n", "50", "--json"}},
		MultiFetchOperation{ID: "broken", Args: []string{"user-tweets", "@broken", "--json"}},
		MultiFetchOperation{ID: "slow", Args: []string{"user-tweets", "@slow", "--json"}},
	)
	stdout, stderr, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", outDir, "--op-timeout", "30ms")
	if err != nil {
		t.Fatalf("empty-on-fail (default) must not fail the command: %v", err)
	}

	r := readMultiFetchReport(t, filepath.Join(outDir, "_report.json"))
	want := map[string]struct {
		status   string
		attempts int
	}{
		"OpenAI":    {"ok", 1},
		"CohereAI":  {"not_found", 1},
		"search-ai": {"rate_limited", 2},
		"broken":    {"error", 1},
		"slow":      {"timeout", 1},
	}
	for id, w := range want {
		op := reportOp(t, r, id)
		if op.Status != w.status || op.Attempts != w.attempts || op.OK != (w.status == "ok") {
			t.Errorf("%s: got status=%s attempts=%d ok=%v, want %+v", id, op.Status, op.Attempts, op.OK, w)
		}
		if w.status != "ok" && op.Error == "" {
			t.Errorf("%s: expected an error string in the report", id)
		}
	}
	if len(r.Ops) != 5 {
		t.Fatalf("expected 5 ops in manifest order, got %d", len(r.Ops))
	}
	if r.Ops[0].ID != "OpenAI" || r.Ops[4].ID != "slow" {
		t.Fatalf("report ops must keep manifest order: %+v", r.Ops)
	}
	wantSummary := multiFetchSummary{Total: 5, OK: 1, Failed: 4, RateLimited: 1, NotFound: 1, Timeout: 1, Error: 1, Retried: 1, Concurrency: 2, WallMS: r.Summary.WallMS}
	if r.Summary != wantSummary {
		t.Fatalf("summary = %+v, want %+v", r.Summary, wantSummary)
	}

	// The retry for search-ai must have landed on the other account.
	calls := sr.callsFor("AI announcement")
	if len(calls) != 2 || calls[0].Account == calls[1].Account {
		t.Fatalf("expected one retry on a different account, got %+v", calls)
	}

	if !strings.Contains(stdout, "multi-fetch: 5 ops, 1 ok, 4 failed (1 rate_limited, 1 not_found, 1 timeout, 1 error), 1 retried, ") {
		t.Fatalf("summary line mismatch:\n%s", stdout)
	}
	for _, needle := range []string{
		"✗ CohereAI",
		"native exit=1 status=not_found account=",
		"native exit=1 status=rate_limited account=",
		"attempts=2",
		"native status=timeout account=",
	} {
		if !strings.Contains(stderr, needle) {
			t.Errorf("stderr missing %q:\n%s", needle, stderr)
		}
	}
	if strings.Contains(stderr, "bird exit=") {
		t.Fatalf("stale 'bird exit=' label in stderr:\n%s", stderr)
	}

	// Failed ops still get '[]' by default; the ok op gets its payload.
	for _, id := range []string{"CohereAI", "search-ai", "broken", "slow"} {
		body, err := os.ReadFile(filepath.Join(outDir, id+".json"))
		if err != nil || string(body) != "[]" {
			t.Errorf("%s: expected '[]' placeholder, got %q err=%v", id, body, err)
		}
	}
}

func TestMultiFetchEmptyOnFailFalseStillWritesReport(t *testing.T) {
	setupMultiFetchTest(t, "alpha")
	outDir := t.TempDir()
	reportPath := filepath.Join(t.TempDir(), "custom", "report.json")
	aggPath := filepath.Join(t.TempDir(), "agg.json")

	sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
		if op == "@OpenAI" {
			return runner.Result{}, `[{"id":"1"}]`, "", nil
		}
		return runner.Result{ExitCode: 1, NotFound: true}, "", `x api: user "Gone" not found`, nil
	})
	multiFetchRun = sr.run

	manifest := writeMultiFetchManifest(t,
		MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}},
		MultiFetchOperation{ID: "Gone", Args: []string{"user-tweets", "@Gone", "--json"}},
	)
	_, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", outDir, "--empty-on-fail=false", "--report", reportPath, "--aggregate", aggPath)
	if err == nil || !strings.Contains(err.Error(), "1/2 ops failed") {
		t.Fatalf("expected '1/2 ops failed' error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "Gone.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no placeholder file for the failed op, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "_report.json")); !os.IsNotExist(err) {
		t.Fatalf("--report must redirect the report away from the output dir, stat err=%v", err)
	}
	r := readMultiFetchReport(t, reportPath)
	if r.Summary.Failed != 1 || r.Summary.NotFound != 1 || reportOp(t, r, "Gone").Status != "not_found" {
		t.Fatalf("unexpected report: %+v", r)
	}
	if _, err := os.Stat(aggPath); err != nil {
		t.Fatalf("aggregate must be written even when ops fail: %v", err)
	}
}

func TestMultiFetchAggregateShape(t *testing.T) {
	setupMultiFetchTest(t, "alpha", "beta")
	outDir := t.TempDir()
	aggPath := filepath.Join(t.TempDir(), "agg.json")

	sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
		switch op {
		case "@OpenAI":
			return runner.Result{}, `[{"id":"1","text":"hello"}]` + "\n", "", nil
		case "AI announcement":
			return runner.Result{}, `[{"id":"2"}]`, "", nil
		case "news":
			return runner.Result{}, `[{"title":"n1"},{"title":"n2"}]`, "", nil
		case "whoami":
			return runner.Result{}, `{"username":"alpha"}`, "", nil
		case "@Limited":
			return rateLimitedResult()
		case "garbage":
			return runner.Result{}, "not json at all", "", nil
		}
		return runner.Result{}, "", "", errors.New("unexpected op " + op)
	})
	multiFetchRun = sr.run

	manifest := writeMultiFetchManifest(t,
		MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}},
		MultiFetchOperation{ID: "search-ai", Args: []string{"search", "AI announcement", "--json"}},
		MultiFetchOperation{ID: "news", Args: []string{"news", "--ai-only", "--json"}},
		MultiFetchOperation{ID: "me", Args: []string{"whoami"}},
		MultiFetchOperation{ID: "Limited", Args: []string{"user-tweets", "@Limited", "--json"}},
		MultiFetchOperation{ID: "search-garbage", Args: []string{"search", "garbage", "--json"}},
	)
	before := time.Now().UTC().Truncate(time.Second)
	if _, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", outDir, "--aggregate", aggPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var agg struct {
		Accounts map[string]json.RawMessage `json:"accounts"`
		Searches map[string]json.RawMessage `json:"searches"`
		News     []map[string]string        `json:"news"`
		Other    map[string]json.RawMessage `json:"other"`
		Meta     struct {
			GeneratedAt    string               `json:"generated_at"`
			Ops            multiFetchSummary    `json:"ops"`
			FailedOps      []multiFetchFailedOp `json:"failed_ops"`
			RateLimitedOps []string             `json:"rate_limited_ops"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(data, &agg); err != nil {
		t.Fatalf("parse aggregate: %v\n%s", err, data)
	}
	if len(agg.Accounts) != 1 || string(agg.Accounts["OpenAI"]) != `[{"id":"1","text":"hello"}]` {
		t.Fatalf("unexpected accounts bucket: %v", agg.Accounts)
	}
	if len(agg.Searches) != 1 || string(agg.Searches["search-ai"]) != `[{"id":"2"}]` {
		t.Fatalf("unexpected searches bucket: %v", agg.Searches)
	}
	if len(agg.News) != 2 || agg.News[0]["title"] != "n1" || agg.News[1]["title"] != "n2" {
		t.Fatalf("unexpected news bucket: %v", agg.News)
	}
	if len(agg.Other) != 1 || string(agg.Other["me"]) != `{"username":"alpha"}` {
		t.Fatalf("unexpected other bucket: %v", agg.Other)
	}
	wantFailed := []multiFetchFailedOp{
		{ID: "Limited", Status: "rate_limited", Error: "x api: HTTP 429: <html>Too Many Requests</html>"},
		{ID: "search-garbage", Status: "error", Error: "stdout is not valid JSON"},
	}
	if len(agg.Meta.FailedOps) != 2 || agg.Meta.FailedOps[0] != wantFailed[0] || agg.Meta.FailedOps[1] != wantFailed[1] {
		t.Fatalf("failed_ops = %+v, want %+v", agg.Meta.FailedOps, wantFailed)
	}
	if len(agg.Meta.RateLimitedOps) != 1 || agg.Meta.RateLimitedOps[0] != "Limited" {
		t.Fatalf("rate_limited_ops = %v", agg.Meta.RateLimitedOps)
	}
	// The execution report counts the garbage op as ok (it ran and produced
	// output); only the aggregate flags it as unjoinable.
	if agg.Meta.Ops.Total != 6 || agg.Meta.Ops.OK != 5 || agg.Meta.Ops.RateLimited != 1 {
		t.Fatalf("meta.ops = %+v", agg.Meta.Ops)
	}
	gen, err := time.Parse(time.RFC3339, agg.Meta.GeneratedAt)
	if err != nil || gen.Before(before) || !strings.HasSuffix(agg.Meta.GeneratedAt, "Z") {
		t.Fatalf("generated_at %q is not a fresh RFC3339 UTC stamp (err=%v)", agg.Meta.GeneratedAt, err)
	}

	// Deterministic: a second marshal of the same outcomes is byte-identical
	// apart from generated_at, which the shape sorts by key.
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(generic))
	for k := range generic {
		keys = append(keys, k)
	}
	if len(keys) != 5 {
		t.Fatalf("aggregate must always carry accounts/searches/news/other/meta, got %v", keys)
	}
}

func TestMultiFetchRateLimitDelayStillRetries(t *testing.T) {
	setupMultiFetchTest(t, "alpha", "beta")
	sr := newScriptedRunner(func(op, account string, attempt int, _ context.Context) (runner.Result, string, string, error) {
		if attempt == 1 {
			return rateLimitedResult()
		}
		return runner.Result{}, `[]`, "", nil
	})
	multiFetchRun = sr.run
	manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "--json"}})
	start := time.Now()
	if _, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", t.TempDir(), "--rate-limit-delay", "20ms"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := sr.callsFor("@OpenAI"); len(calls) != 2 {
		t.Fatalf("expected a retry after the delay, got %+v", calls)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatalf("retry happened before the delay elapsed")
	}
}

func TestMultiFetchRejectsReservedAndUnsafeIDs(t *testing.T) {
	setupMultiFetchTest(t, "alpha")
	multiFetchRun = func(context.Context, *store.Account, []string) (runner.Result, string, string, error) {
		t.Fatal("runner must not be invoked for an invalid manifest")
		return runner.Result{}, "", "", nil
	}
	cases := map[string]string{
		"_report": `reserved for multi-fetch's own files`,
		"../evil": "path separators",
		"a/b":     "path separators",
		"..":      "path separators",
	}
	for id, wantErr := range cases {
		manifest := writeMultiFetchManifest(t, MultiFetchOperation{ID: id, Args: []string{"news"}})
		_, _, err := runMultiFetchCmd(t, "--manifest", manifest, "--output-dir", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("id %q: expected error containing %q, got %v", id, wantErr, err)
		}
	}
}

func TestMultiFetchEngineSelection(t *testing.T) {
	setupMultiFetchTest(t, "alpha")
	if got := multiFetchEngine([]string{"user-tweets", "@x", "-n", "20", "--json", "--plain"}); got != "native" {
		t.Fatalf("user-tweets should be native, got %s", got)
	}
	if got := multiFetchEngine([]string{"news", "--ai-only", "-n", "40", "--json", "--plain"}); got != "native" {
		t.Fatalf("news --ai-only should be native, got %s", got)
	}
	if got := multiFetchEngine([]string{"user-tweets", "@x", "--flag-birdy-does-not-know"}); got != "bird" {
		t.Fatalf("unknown flag should fall back to bird, got %s", got)
	}
	birdFlag = true
	if got := multiFetchEngine([]string{"user-tweets", "@x", "--json"}); got != "bird" {
		t.Fatalf("--bird should force the bird engine, got %s", got)
	}
}

func TestClassifyMultiFetchOp(t *testing.T) {
	cases := []struct {
		name   string
		res    runner.Result
		stdout string
		err    error
		want   string
	}{
		{"ok", runner.Result{}, "[]", nil, "ok"},
		{"ok despite 429 marker", runner.Result{RateLimited: true}, "[]", nil, "ok"},
		{"empty stdout is a failure", runner.Result{}, "", nil, "error"},
		{"rate limited", runner.Result{ExitCode: 1, RateLimited: true}, "", nil, "rate_limited"},
		{"429 outranks not-found", runner.Result{ExitCode: 1, RateLimited: true, NotFound: true}, "", nil, "rate_limited"},
		{"not found", runner.Result{ExitCode: 1, NotFound: true}, "", nil, "not_found"},
		{"generic failure", runner.Result{ExitCode: 2}, "", nil, "error"},
		{"timeout", runner.Result{ExitCode: 1}, "", fmt.Errorf("running native: %w", context.DeadlineExceeded), "timeout"},
		{"run error", runner.Result{ExitCode: 1}, "", errors.New("bird CLI not found"), "error"},
	}
	for _, tc := range cases {
		if got := classifyMultiFetchOp(tc.res, tc.stdout, tc.err); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestFormatMultiFetchSummary(t *testing.T) {
	s := multiFetchSummary{Total: 127, OK: 113, Failed: 14, RateLimited: 12, NotFound: 1, Error: 1, Retried: 2, WallMS: 16712, Concurrency: 8}
	want := "multi-fetch: 127 ops, 113 ok, 14 failed (12 rate_limited, 1 not_found, 1 error), 2 retried, 16712ms wall (concurrency=8)"
	if got := formatMultiFetchSummary(s); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	clean := multiFetchSummary{Total: 3, OK: 3, WallMS: 5, Concurrency: 2}
	if got := formatMultiFetchSummary(clean); got != "multi-fetch: 3 ops, 3 ok, 0 failed, 0 retried, 5ms wall (concurrency=2)" {
		t.Fatalf("clean summary: %s", got)
	}
}
