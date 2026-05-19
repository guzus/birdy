package cmd

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

var budgetCmd = &cobra.Command{
	Use:     "budget",
	Short:   "Show per-account 429 history and quota status",
	GroupID: "birdy",
	Args:    cobra.NoArgs,
	Long: `Lists each configured account with its use count, total observed
429 events, the timestamp of its most recent 429, and whether it is
currently in a quota cooldown window (15 minutes).

Use --strategy quota-aware on bird passthrough commands to route
around accounts marked "hot".`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		st, err := store.Open()
		if err != nil {
			return err
		}
		printStoreWarning(errOut, st)

		accounts := st.List()
		if len(accounts) == 0 {
			return fmt.Errorf("no accounts configured\nRun: birdy account add <name>")
		}

		// Coldest first so the next-to-use account surfaces at the top.
		sort.SliceStable(accounts, func(i, j int) bool {
			ihot := isInCooldown(accounts[i], time.Now())
			jhot := isInCooldown(accounts[j], time.Now())
			if ihot != jhot {
				return !ihot
			}
			return accounts[i].UseCount < accounts[j].UseCount
		})

		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tUSES\t429s\tLAST 429\tSTATUS")
		now := time.Now()
		for _, a := range accounts {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n",
				a.Name,
				a.UseCount,
				a.RateLimitCount,
				formatLast429(a.LastRateLimitedAt),
				formatStatus(a, now),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}

		fmt.Fprintf(out, "\nCooldown window: %s\n", rotation.QuotaCooldown)
		fmt.Fprintln(out, "Route around hot accounts with: birdy --strategy quota-aware <bird-cmd>")
		return nil
	},
}

func isInCooldown(a store.Account, now time.Time) bool {
	if a.LastRateLimitedAt.IsZero() {
		return false
	}
	return now.Sub(a.LastRateLimitedAt) < rotation.QuotaCooldown
}

func formatLast429(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatStatus(a store.Account, now time.Time) string {
	if isInCooldown(a, now) {
		remaining := rotation.QuotaCooldown - now.Sub(a.LastRateLimitedAt)
		return fmt.Sprintf("hot (cools in %s)", truncDuration(remaining))
	}
	return "cold"
}

func truncDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return "1s"
	}
	return d.String()
}

func init() {
	rootCmd.AddCommand(budgetCmd)
}
