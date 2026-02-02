package cmd

import (
	"fmt"
	"os"

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
