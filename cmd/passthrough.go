package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
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

	exitCode, err := runner.Run(account, args)
	if err != nil {
		return err
	}

	if err := st.RecordUsage(account.Name); err != nil {
		return err
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

	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
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
