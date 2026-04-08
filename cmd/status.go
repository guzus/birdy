package cmd

import (
	"fmt"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show current rotation status",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		st, err := store.Open()
		if err != nil {
			return err
		}
		if st.Warning != "" {
			_, _ = fmt.Fprintf(errOut, "[birdy] warning: %s\n", st.Warning)
		}

		rs, err := state.Load()
		if err != nil {
			return err
		}
		if rs.Warning != "" {
			_, _ = fmt.Fprintf(errOut, "[birdy] warning: %s\n", rs.Warning)
		}

		accounts := st.List()
		_, _ = fmt.Fprintf(out, "Accounts:   %d\n", len(accounts))
		_, _ = fmt.Fprintf(out, "Strategy:   %s\n", strategyFlag)

		if rs.LastUsedName != "" {
			lastUsed := rs.LastUsedName
			if !accountsContainName(accounts, rs.LastUsedName) {
				lastUsed += " (not configured)"
			}
			_, _ = fmt.Fprintf(out, "Last used:  %s\n", lastUsed)
		} else {
			_, _ = fmt.Fprintln(out, "Last used:  (none)")
		}

		var totalUses int64
		for _, a := range accounts {
			totalUses += a.UseCount
		}
		_, _ = fmt.Fprintf(out, "Total uses: %d\n", totalUses)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func accountsContainName(accounts []store.Account, name string) bool {
	for _, account := range accounts {
		if account.Name == name {
			return true
		}
	}
	return false
}
