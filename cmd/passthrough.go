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

	if blocked, name := isReadOnlyBirdCommand(args); blocked {
		return fmt.Errorf("%q is disabled in read-only mode (BIRDY_READ_ONLY)", name)
	}

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("opening account store: %w", err)
	}

	if st.Len() == 0 {
		return fmt.Errorf("no accounts configured\nRun: birdy account add <name>")
	}

	var account *store.Account

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

		rs, err := state.Load()
		if err != nil {
			return fmt.Errorf("loading rotation state: %w", err)
		}

		account, err = rotation.Pick(st.List(), strat, rs.LastUsedName)
		if err != nil {
			return err
		}

		rs.LastUsedName = account.Name
		if err := rs.Save(); err != nil {
			return fmt.Errorf("saving rotation state: %w", err)
		}
	}

	if verboseFlag {
		fmt.Fprintf(os.Stderr, "[birdy] using account: %s\n", account.Name)
	}

	if err := st.RecordUsage(account.Name); err != nil {
		return err
	}
	if err := st.Save(); err != nil {
		return fmt.Errorf("saving account store: %w", err)
	}

	exitCode, err := runner.Run(account, args)
	if err != nil {
		return err
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
	for _, arg := range args {
		a := strings.TrimSpace(strings.ToLower(arg))
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}
