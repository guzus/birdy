package cmd

import (
	"fmt"
	"os"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show current rotation status",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := store.Open()
		if err != nil {
			return err
		}
		if st.Warning != "" {
			_, _ = fmt.Fprintf(os.Stderr, "[birdy] warning: %s\n", st.Warning)
		}

		rs, err := state.Load()
		if err != nil {
			return err
		}
		if rs.Warning != "" {
			_, _ = fmt.Fprintf(os.Stderr, "[birdy] warning: %s\n", rs.Warning)
		}

		accounts := st.List()
		fmt.Printf("Accounts:   %d\n", len(accounts))
		fmt.Printf("Strategy:   %s\n", strategyFlag)

		if rs.LastUsedName != "" {
			fmt.Printf("Last used:  %s\n", rs.LastUsedName)
		} else {
			fmt.Printf("Last used:  (none)\n")
		}

		var totalUses int64
		for _, a := range accounts {
			totalUses += a.UseCount
		}
		fmt.Printf("Total uses: %d\n", totalUses)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
