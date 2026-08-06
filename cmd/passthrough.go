package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/vpn"
	"github.com/guzus/birdy/internal/xapi"
	"github.com/spf13/cobra"
)

func runPassthrough(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	errOut := cmd.ErrOrStderr()

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("opening account store: %w", err)
	}
	printStoreWarning(errOut, st)

	if st.Len() == 0 {
		return fmt.Errorf("no accounts configured\nRun: birdy account add <name>")
	}

	var account *store.Account
	var rs *state.State

	if accountFlag != "" {
		account, err = st.Get(accountFlag)
		if err != nil {
			return err
		}
	} else {
		strat, err := rotation.ParseStrategy(strategyFlag)
		if err != nil {
			return err
		}

		rs, err = state.Load()
		if err != nil {
			return fmt.Errorf("loading rotation state: %w", err)
		}
		printStateWarning(errOut, rs)

		accounts := st.List()
		if blocked, name := isMutatingBirdCommand(args); blocked {
			accounts = filterWritableAccounts(accounts)
			if len(accounts) == 0 {
				return fmt.Errorf("no writable accounts configured for %q", name)
			}
		}

		account, err = rotation.Pick(accounts, strat, rs.LastUsedName)
		if err != nil {
			return err
		}

	}

	if err := ensureBirdCommandAllowed(account, args); err != nil {
		return err
	}

	if verboseFlag {
		fmt.Fprintf(errOut, "[birdy] using account: %s\n", account.Name)
	}

	// Serve the command in-process when birdy implements it. Falling through to
	// bird keeps commands that have not been ported working, and --bird forces
	// the bird path for anything.
	command := args[0]
	if !useBird() && nativeSupports(command) && nativeAcceptsFlags(args[1:]) {
		if verboseFlag {
			fmt.Fprintf(errOut, "[birdy] engine: native (go)\n")
		}
		ctx := cmd.Context()
		if ctx == nil {
			// cobra leaves Context nil unless the caller used ExecuteContext.
			ctx = context.Background()
		}
		err := runNative(ctx, account, command, args[1:], cmd.OutOrStdout())
		rateLimited := xapi.IsRateLimited(err)
		if rateLimited && verboseFlag {
			fmt.Fprintf(errOut, "[birdy] %s hit a rate limit (HTTP 429)\n", account.Name)
		}
		recordNativeUsage(errOut, st, rs, account, rateLimited)
		return err
	}
	if verboseFlag {
		fmt.Fprintf(errOut, "[birdy] engine: bird (node)\n")
	}

	// Spin up a per-invocation SOCKS5 bridge when --vpn is set, so this
	// bird call goes out through a NordVPN exit IP. Bridge dies with
	// defer; per-invocation control means each birdy call can pick a
	// different exit by passing --vpn-server NAME or by random rotation
	// from the configured pool.
	runOpts := runner.Options{}
	var bridge *vpn.Bridge
	if vpnFlag || vpnServerFlag != "" {
		b, env, err := startVPN(errOut)
		if err != nil {
			return err
		}
		bridge = b
		runOpts.ExtraEnv = env
		defer bridge.Stop()
	}

	res, err := runner.RunWith(account, args, runOpts)
	if err != nil {
		return err
	}

	if err := st.RecordUsage(account.Name); err != nil {
		return err
	}
	if res.RateLimited {
		if err := st.RecordRateLimit(account.Name); err != nil {
			return err
		}
		if verboseFlag {
			fmt.Fprintf(errOut, "[birdy] %s hit a rate limit (HTTP 429)\n", account.Name)
		}
	}
	if err := st.Save(); err != nil {
		return fmt.Errorf("saving account store: %w", err)
	}
	if rs != nil {
		rs.LastUsedName = account.Name
		if err := rs.Save(); err != nil {
			return fmt.Errorf("saving rotation state: %w", err)
		}
	}

	if res.ExitCode != 0 {
		// Stop the bridge explicitly before os.Exit, since os.Exit skips
		// deferred cleanup. The OS would reclaim the listener socket on
		// process exit anyway, but this is the tidy form.
		if bridge != nil {
			bridge.Stop()
		}
		os.Exit(res.ExitCode)
	}
	return nil
}

// startVPN loads the VPN config, picks a server, starts the local
// CONNECT→SOCKS5 bridge, and returns the env vars to pass through to bird:
//
//	HTTPS_PROXY=http://127.0.0.1:<bridge-port>
//	NODE_OPTIONS=--require=<path-to-bootstrap.js>
//
// The bootstrap.js calls undici.setGlobalDispatcher so bird's fetch()
// routes through the bridge (Node fetch does NOT honor HTTPS_PROXY
// natively as of Node 26).
func startVPN(errOut io.Writer) (*vpn.Bridge, []string, error) {
	cfg, err := vpn.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("loading vpn config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	server, err := cfg.PickServer(vpnServerFlag)
	if err != nil {
		return nil, nil, err
	}
	if !vpn.UndiciInstalled() {
		return nil, nil, fmt.Errorf(
			"VPN bootstrap requires undici; run: birdy vpn install-deps",
		)
	}
	bridge, err := vpn.Start(server, cfg.Port, cfg.User, cfg.Password)
	if err != nil {
		return nil, nil, fmt.Errorf("starting vpn bridge: %w", err)
	}
	bootstrap, err := vpn.BootstrapPath()
	if err != nil {
		bridge.Stop()
		return nil, nil, fmt.Errorf("writing vpn bootstrap: %w", err)
	}
	undiciDir, err := vpn.UndiciDir()
	if err != nil {
		bridge.Stop()
		return nil, nil, fmt.Errorf("resolving undici path: %w", err)
	}
	if verboseFlag {
		fmt.Fprintf(errOut, "[birdy] vpn: routing through %s:%d\n", server, cfg.Port)
	}
	return bridge, []string{
		"HTTPS_PROXY=http://" + bridge.Addr(),
		"NODE_OPTIONS=--require=" + bootstrap,
		"BIRDY_UNDICI_PATH=" + undiciDir,
	}, nil
}

func readOnlyModeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BIRDY_READ_ONLY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstBirdCommand(args []string) string {
	firstNonFlag := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		a := strings.TrimSpace(strings.ToLower(arg))
		if a == "" {
			continue
		}
		if a == "--" {
			for j := i + 1; j < len(args); j++ {
				next := strings.TrimSpace(strings.ToLower(args[j]))
				if next != "" {
					return next
				}
			}
			return ""
		}
		if strings.HasPrefix(a, "--") {
			if strings.Contains(a, "=") {
				continue
			}
			if shouldSkipFlagValue(args, i) {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			if len(a) == 2 && shouldSkipFlagValue(args, i) {
				i++
			}
			continue
		}
		if isKnownBirdCommand(a) {
			return a
		}
		if firstNonFlag == "" {
			firstNonFlag = a
		}
	}
	return firstNonFlag
}

func shouldSkipFlagValue(args []string, flagIdx int) bool {
	idx := flagIdx + 1
	if flagIdx < 0 || idx < 0 || idx >= len(args) {
		return false
	}
	value := strings.TrimSpace(strings.ToLower(args[idx]))
	return value != "" && value != "--" && !strings.HasPrefix(value, "-") && !isKnownBirdCommand(value)
}

func isKnownBirdCommand(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") {
		return false
	}
	if _, ok := apiAllowedBirdCommands[arg]; ok {
		return true
	}
	if _, ok := readOnlyBlockedBirdCommands[arg]; ok {
		return true
	}
	return false
}
