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

var readOnlyBlockedBirdCommands = map[string]struct{}{
	"tweet":      {},
	"reply":      {},
	"follow":     {},
	"unfollow":   {},
	"unbookmark": {},
}

func runPassthrough(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	errOut := cmd.ErrOrStderr()

	if blocked, name := isReadOnlyBirdCommand(args); blocked {
		return fmt.Errorf("%q is disabled in read-only mode (BIRDY_READ_ONLY)", name)
	}

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

		account, err = rotation.Pick(st.List(), strat, rs.LastUsedName)
		if err != nil {
			return err
		}

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

func isReadOnlyBirdCommand(args []string) (bool, string) {
	if !readOnlyModeEnabled() {
		return false, ""
	}

	cmd := firstBirdCommand(args)
	if cmd == "" {
		return false, ""
	}
	_, blocked := readOnlyBlockedBirdCommands[cmd]
	return blocked, cmd
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
			return firstNonFlag
		}
		if strings.HasPrefix(a, "--") {
			if strings.Contains(a, "=") {
				continue
			}
			if i+1 < len(args) && shouldSkipFlagValue(args, i+1) {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			if len(a) == 2 && i+1 < len(args) && hasKnownBirdCommand(args[i+2:]) && shouldSkipFlagValue(args, i+1) {
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

func shouldSkipFlagValue(args []string, idx int) bool {
	if idx < 0 || idx >= len(args) {
		return false
	}
	value := strings.TrimSpace(args[idx])
	return value != "" && value != "--" && !strings.HasPrefix(value, "-")
}

func hasKnownBirdCommand(args []string) bool {
	for _, arg := range args {
		if isKnownBirdCommand(strings.TrimSpace(strings.ToLower(arg))) {
			return true
		}
	}
	return false
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
