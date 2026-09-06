package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/guzus/birdy/internal/daemon"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

// daemon-specific flags. The persistent --account and --strategy flags
// from rootCmd are reused for account selection.
var (
	daemonAddrFlag        string
	daemonConcurrencyFlag int
	daemonCacheTTLFlag    time.Duration
)

// daemonShutdownTimeout is how long the daemon waits for in-flight requests
// to drain after SIGTERM/SIGINT before forcing exit.
const daemonShutdownTimeout = 10 * time.Second

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		Short:   "Run birdy as a long-lived HTTP server with warm bird auth state",
		GroupID: "birdy",
		Long: `daemon runs birdy as a foreground HTTP server. It holds warm bird auth
and account-rotation state so agent reasoning loops that issue many
follow-up bird queries get sub-second latency on each call instead of
paying the ~200–400ms bird CLI startup cost every time.

Endpoints:

  POST /run     run a single bird command. Body:
                  {"args": ["search","AI","-n","50","--json","--plain"],
                   "request_id": "optional-uuid"}
                Response:
                  {"ok": true, "request_id": "...", "exit_code": 0,
                   "stdout": "...", "stderr": "...", "duration_ms": 142,
                   "cached": false}

  GET  /health  daemon liveness, counters, and per-account rate-limit
                state. Response:
                  {"ok": true, "accounts": N, "uptime_seconds": ...,
                   "served": ..., "cache_hits": ..., "cache_size": ...,
                   "accounts_cooling": 1, "accounts_ready": 3,
                   "cooldown_seconds": 900, "degraded": false,
                   "accounts_detail": [
                     {"name": "main", "cooling": true,
                      "cooldown_remaining_seconds": 412,
                      "last_rate_limited_at": "2026-09-06T09:41:07Z"},
                     ...]}
                "ok" is liveness and stays true; "degraded" flips when no
                account is ready (every enabled account is inside the
                429 cooldown window — the same view as "birdy budget").
                Names and timestamps only; credentials are never exposed.

Account rotation reuses the same store, strategies, and read-only filters
as the passthrough commands. Pass --strategy or --account to override the
defaults; rotation state is persisted across requests so the daemon
respects the same per-account distribution as one-shot CLI invocations.

Examples:

  birdy daemon --addr 127.0.0.1:8080
  birdy daemon --addr 127.0.0.1:8080 --concurrency 16 --cache-ttl 30s
  curl -X POST http://127.0.0.1:8080/run \
    -H 'Content-Type: application/json' \
    -d '{"args":["search","AI","-n","50","--json","--plain"]}'

The daemon shuts down gracefully on SIGINT or SIGTERM: it stops accepting
new requests, drains in-flight requests for up to ` + daemonShutdownTimeout.String() + `,
then exits.`,
		Args: cobra.NoArgs,
		RunE: runDaemon,
	}
	cmd.Flags().StringVar(&daemonAddrFlag, "addr", "127.0.0.1:8080",
		"HTTP listen address (host:port)")
	cmd.Flags().IntVar(&daemonConcurrencyFlag, "concurrency", 8,
		"max in-flight bird invocations (>= 1)")
	cmd.Flags().DurationVar(&daemonCacheTTLFlag, "cache-ttl", 0,
		"in-memory result cache TTL (e.g. 30s); 0 disables caching")
	return cmd
}

func init() {
	rootCmd.AddCommand(newDaemonCmd())
}

func runDaemon(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	addr := strings.TrimSpace(daemonAddrFlag)
	if addr == "" {
		return fmt.Errorf("--addr is required")
	}
	// Defensive: reject the unix:/... shorthand the issue mentioned —
	// scope discipline says HTTP only for v1.
	if strings.HasPrefix(strings.ToLower(addr), "unix:") {
		return fmt.Errorf("unix socket transport is not supported in v1; use a host:port address")
	}
	if daemonConcurrencyFlag < 1 {
		return fmt.Errorf("--concurrency must be >= 1")
	}
	if daemonCacheTTLFlag < 0 {
		return fmt.Errorf("--cache-ttl must be >= 0")
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

	parsedStrategy, err := rotation.ParseStrategy(strategyFlag)
	if err != nil {
		return err
	}

	// pickAccount mirrors the multi-fetch / passthrough rotation logic, but
	// runs once per request so cross-request rotation behavior is preserved.
	// Reasoning: the daemon's whole reason to exist is sharing warm state.
	// If we picked once at startup, we'd pin the daemon to one account and
	// defeat the rotation system. Per-request picking keeps the rotation
	// state file authoritative across all birdy invocations (CLI, multi-
	// fetch, daemon).
	pickAccount := func(args []string) (*store.Account, error) {
		if accountFlag != "" {
			acc, err := st.Get(accountFlag)
			if err != nil {
				return nil, err
			}
			if err := ensureBirdCommandAllowed(acc, args); err != nil {
				return nil, err
			}
			return acc, nil
		}
		accounts := st.List()
		if blocked, name := isMutatingBirdCommand(args); blocked {
			accounts = filterWritableAccounts(accounts)
			if len(accounts) == 0 {
				return nil, fmt.Errorf("no writable accounts configured for %q", name)
			}
		}
		acc, err := rotation.Pick(accounts, parsedStrategy, rs.LastUsedName)
		if err != nil {
			return nil, err
		}
		if err := ensureBirdCommandAllowed(acc, args); err != nil {
			return nil, err
		}
		// Best-effort: update rotation state so the next request rotates
		// to the next account. We deliberately do not block the response
		// on a failed save; the warning is logged to stderr.
		rs.LastUsedName = acc.Name
		if err := rs.Save(); err != nil {
			fmt.Fprintf(errOut, "[birdy] warning: failed to save rotation state: %v\n", err)
		}
		// Record usage on the store too, mirroring passthrough.
		if err := st.RecordUsage(acc.Name); err != nil {
			fmt.Fprintf(errOut, "[birdy] warning: failed to record usage for %q: %v\n", acc.Name, err)
		} else if err := st.Save(); err != nil {
			fmt.Fprintf(errOut, "[birdy] warning: failed to save account store: %v\n", err)
		}
		return acc, nil
	}

	srv, err := daemon.NewServer(daemon.Config{
		Run:          daemon.DefaultRunner(st),
		PickAccount:  pickAccount,
		AccountCount: st.Len,
		Accounts:     st.List,
		Concurrency:  daemonConcurrencyFlag,
		CacheTTL:     daemonCacheTTLFlag,
	})
	if err != nil {
		return fmt.Errorf("constructing daemon: %w", err)
	}

	// Wire SIGINT / SIGTERM into a context cancellation that drives the
	// daemon's graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintf(out, "birdy daemon: listening on http://%s (concurrency=%d, cache-ttl=%s, accounts=%d)\n",
		addr, daemonConcurrencyFlag, daemonCacheTTLFlag, st.Len())
	if daemonCacheTTLFlag == 0 {
		fmt.Fprintln(out, "birdy daemon: cache disabled (set --cache-ttl=30s to enable)")
	}
	fmt.Fprintln(out, "birdy daemon: SIGINT/SIGTERM triggers graceful shutdown")

	if err := srv.Serve(ctx, addr, daemonShutdownTimeout); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	fmt.Fprintln(out, "birdy daemon: stopped")
	return nil
}
