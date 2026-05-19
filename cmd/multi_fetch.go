package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

// MultiFetchOperation is one bird invocation in a multi-fetch manifest.
// `args` is passed verbatim as bird's argv (e.g.
// ["user-tweets", "@OpenAI", "-n", "20", "--json", "--plain"]).
type MultiFetchOperation struct {
	ID   string   `json:"id"`
	Args []string `json:"args"`
}

// MultiFetchManifest is the input format for `birdy multi-fetch`.
type MultiFetchManifest struct {
	Operations  []MultiFetchOperation `json:"operations"`
	Concurrency int                   `json:"concurrency,omitempty"`
}

var (
	multiFetchManifest    string
	multiFetchOutputDir   string
	multiFetchConcurrency int
	multiFetchEmptyOnFail bool
)

func newMultiFetchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "multi-fetch",
		Short:   "Run multiple bird operations in parallel from a manifest",
		GroupID: "birdy",
		Long: `multi-fetch executes a manifest of bird operations concurrently and writes
each result to <output-dir>/<id>.json.

Manifest schema (JSON):

  {
    "operations": [
      {"id": "OpenAI",    "args": ["user-tweets", "@OpenAI", "-n", "20", "--json", "--plain"]},
      {"id": "search-ai", "args": ["search", "AI announcement", "-n", "50", "--json", "--plain"]},
      {"id": "news",      "args": ["news", "--ai-only", "-n", "40", "--json", "--plain"]}
    ],
    "concurrency": 8
  }

Pass --manifest - to read the manifest from stdin. The output directory is
created if it doesn't exist. On op failure, an empty array '[]' is written
to the per-op file by default (override with --empty-on-fail=false).

Account rotation reuses the same store + strategies as the passthrough
commands (use --strategy or --account, default: round-robin). Use the
BIRDY_ACCOUNTS env var for stateless account injection in CI:

  export BIRDY_ACCOUNTS='[{"name":"ci","auth_token":"...","ct0":"..."}]'
  birdy multi-fetch --manifest m.json --output-dir /tmp/bird`,
		RunE: runMultiFetch,
	}
	cmd.Flags().StringVarP(&multiFetchManifest, "manifest", "m", "", "path to JSON manifest (use '-' for stdin)")
	cmd.Flags().StringVarP(&multiFetchOutputDir, "output-dir", "o", "", "directory to write per-op output files")
	cmd.Flags().IntVarP(&multiFetchConcurrency, "concurrency", "c", 6, "number of parallel ops (overridden by manifest.concurrency if non-zero)")
	cmd.Flags().BoolVar(&multiFetchEmptyOnFail, "empty-on-fail", true, "write '[]' on op failure instead of leaving the file absent")
	_ = cmd.MarkFlagRequired("manifest")
	_ = cmd.MarkFlagRequired("output-dir")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMultiFetchCmd())
}

func runMultiFetch(cmd *cobra.Command, _ []string) error {
	errOut := cmd.ErrOrStderr()

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
	if len(m.Operations) == 0 {
		return fmt.Errorf("manifest contains no operations")
	}

	// Validate every op has a non-empty id and args.
	idSeen := make(map[string]bool, len(m.Operations))
	for i, op := range m.Operations {
		if op.ID == "" {
			return fmt.Errorf("operation %d has empty id", i)
		}
		if idSeen[op.ID] {
			return fmt.Errorf("duplicate operation id %q", op.ID)
		}
		idSeen[op.ID] = true
		if len(op.Args) == 0 {
			return fmt.Errorf("operation %q has empty args", op.ID)
		}
	}

	conc := multiFetchConcurrency
	if m.Concurrency > 0 {
		conc = m.Concurrency
	}
	if conc < 1 {
		conc = 1
	}

	if err := os.MkdirAll(multiFetchOutputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
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

	// Pre-pick accounts (sequentially) so the rotation state is consistent.
	// Then run each op with its assigned account in parallel.
	type opTask struct {
		Op      MultiFetchOperation
		Account *store.Account
	}
	tasks := make([]opTask, 0, len(m.Operations))
	for _, op := range m.Operations {
		var acc *store.Account
		if accountFlag != "" {
			acc, err = st.Get(accountFlag)
			if err != nil {
				return err
			}
		} else {
			accounts := st.List()
			if blocked, name := isMutatingBirdCommand(op.Args); blocked {
				accounts = filterWritableAccounts(accounts)
				if len(accounts) == 0 {
					return fmt.Errorf("no writable accounts configured for op %q (%s)", op.ID, name)
				}
			}
			acc, err = rotation.Pick(accounts, strat, rs.LastUsedName)
			if err != nil {
				return err
			}
			rs.LastUsedName = acc.Name
		}
		tasks = append(tasks, opTask{Op: op, Account: acc})
	}
	// Persist rotation state once (best-effort — non-fatal if it fails).
	if err := rs.Save(); err != nil {
		fmt.Fprintf(errOut, "warning: failed to save rotation state: %v\n", err)
	}

	// Concurrent execution with bounded parallelism.
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	type opResult struct {
		ID       string
		Path     string
		Bytes    int
		Duration time.Duration
		Err      error
	}
	results := make([]opResult, len(tasks))
	start := time.Now()

	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t opTask) {
			defer wg.Done()
			defer func() { <-sem }()
			opStart := time.Now()
			outPath := filepath.Join(multiFetchOutputDir, t.Op.ID+".json")
			res, stdout, stderr, runErr := runner.RunCapture(t.Account, t.Op.Args)

			fail := runErr != nil || res.ExitCode != 0 || stdout == ""
			if fail {
				if multiFetchEmptyOnFail {
					_ = os.WriteFile(outPath, []byte("[]"), 0o644)
				}
				err := runErr
				if err == nil {
					err = fmt.Errorf("bird exit=%d stderr=%q", res.ExitCode, truncate(stderr, 200))
				}
				results[i] = opResult{
					ID:       t.Op.ID,
					Path:     outPath,
					Bytes:    0,
					Duration: time.Since(opStart),
					Err:      err,
				}
				return
			}

			if err := os.WriteFile(outPath, []byte(stdout), 0o644); err != nil {
				results[i] = opResult{
					ID:       t.Op.ID,
					Path:     outPath,
					Bytes:    0,
					Duration: time.Since(opStart),
					Err:      fmt.Errorf("writing %s: %w", outPath, err),
				}
				return
			}
			results[i] = opResult{
				ID:       t.Op.ID,
				Path:     outPath,
				Bytes:    len(stdout),
				Duration: time.Since(opStart),
			}
		}(i, t)
	}
	wg.Wait()
	totalDur := time.Since(start)

	// Summarize.
	ok := 0
	failed := 0
	for _, r := range results {
		if r.Err == nil {
			ok++
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %-30s %5d bytes  %5dms\n", r.ID, r.Bytes, r.Duration.Milliseconds())
		} else {
			failed++
			fmt.Fprintf(errOut, "✗ %-30s %s\n", r.ID, r.Err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nmulti-fetch: %d ops, %d ok, %d failed, %dms wall (concurrency=%d)\n",
		len(tasks), ok, failed, totalDur.Milliseconds(), conc)

	if failed > 0 && !multiFetchEmptyOnFail {
		return fmt.Errorf("%d/%d ops failed", failed, len(tasks))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
