package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

var accountCmd = &cobra.Command{
	Use:     "account",
	Short:   "Manage bird CLI accounts",
	Long:    "Add, remove, list, and update X/Twitter auth tokens for bird CLI.",
	GroupID: "birdy",
}

func ensureAccountStoreWritable(st *store.Store) error {
	if st == nil || !st.IsEphemeral() {
		return nil
	}
	return fmt.Errorf("account store is env-backed only; unset BIRDY_ACCOUNTS or create ~/.config/birdy/accounts.json to add, update, or remove accounts")
}

func ensureAccountMutationAllowed(st *store.Store) error {
	if readOnlyModeEnabled() {
		return fmt.Errorf("account mutations are disabled in read-only mode (BIRDY_READ_ONLY)")
	}
	return ensureAccountStoreWritable(st)
}

func clearRemovedAccountFromState(name string, errOut io.Writer) {
	rs, err := state.Load()
	if err != nil {
		printWarning(errOut, fmt.Sprintf("removed account %q but failed to load rotation state: %v", name, err))
		return
	}
	printStateWarning(errOut, rs)
	if rs.LastUsedName != name {
		return
	}
	rs.LastUsedName = ""
	if err := rs.Save(); err != nil {
		printWarning(errOut, fmt.Sprintf("removed account %q but failed to clear last-used state: %v", name, err))
	}
}

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		authToken, _ := cmd.Flags().GetString("auth-token")
		ct0, _ := cmd.Flags().GetString("ct0")

		reader := bufio.NewReader(cmd.InOrStdin())

		if authToken == "" {
			_, _ = fmt.Fprint(out, "auth_token: ")
			authToken, _ = reader.ReadString('\n')
			authToken = strings.TrimSpace(authToken)
		}
		if ct0 == "" {
			_, _ = fmt.Fprint(out, "ct0: ")
			ct0, _ = reader.ReadString('\n')
			ct0 = strings.TrimSpace(ct0)
		}

		if authToken == "" || ct0 == "" {
			return fmt.Errorf("both auth_token and ct0 are required")
		}

		st, err := store.Open()
		if err != nil {
			return err
		}
		printStoreWarning(errOut, st)
		if err := ensureAccountMutationAllowed(st); err != nil {
			return err
		}

		if err := st.Add(name, authToken, ct0); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "Account %q added.\n", name)
		return nil
	},
}

var accountListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all accounts",
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
			_, _ = fmt.Fprintln(out, "No accounts configured. Run: birdy account add <name>")
			return nil
		}

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tUSES\tLAST USED\tADDED")
		for _, a := range accounts {
			lastUsed := "-"
			if !a.LastUsed.IsZero() {
				lastUsed = a.LastUsed.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\n",
				a.Name,
				a.UseCount,
				lastUsed,
				a.AddedAt.Format("2006-01-02 15:04"),
			)
		}
		return w.Flush()
	},
}

var accountRemoveCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Remove an account",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		st, err := store.Open()
		if err != nil {
			return err
		}
		printStoreWarning(errOut, st)
		if err := ensureAccountMutationAllowed(st); err != nil {
			return err
		}

		if err := st.Remove(name); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}
		clearRemovedAccountFromState(name, errOut)

		_, _ = fmt.Fprintf(out, "Account %q removed.\n", name)
		return nil
	},
}

var accountUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update credentials for an existing account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()

		authToken, _ := cmd.Flags().GetString("auth-token")
		ct0, _ := cmd.Flags().GetString("ct0")

		reader := bufio.NewReader(cmd.InOrStdin())

		if authToken == "" {
			_, _ = fmt.Fprint(out, "auth_token: ")
			authToken, _ = reader.ReadString('\n')
			authToken = strings.TrimSpace(authToken)
		}
		if ct0 == "" {
			_, _ = fmt.Fprint(out, "ct0: ")
			ct0, _ = reader.ReadString('\n')
			ct0 = strings.TrimSpace(ct0)
		}

		if authToken == "" || ct0 == "" {
			return fmt.Errorf("both auth_token and ct0 are required")
		}

		st, err := store.Open()
		if err != nil {
			return err
		}
		printStoreWarning(errOut, st)
		if err := ensureAccountMutationAllowed(st); err != nil {
			return err
		}

		if err := st.Update(name, authToken, ct0); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "Account %q updated.\n", name)
		return nil
	},
}

func init() {
	accountAddCmd.Flags().String("auth-token", "", "auth_token cookie value")
	accountAddCmd.Flags().String("ct0", "", "ct0 cookie value")

	accountUpdateCmd.Flags().String("auth-token", "", "auth_token cookie value")
	accountUpdateCmd.Flags().String("ct0", "", "ct0 cookie value")

	accountCmd.AddCommand(accountAddCmd)
	accountCmd.AddCommand(accountListCmd)
	accountCmd.AddCommand(accountRemoveCmd)
	accountCmd.AddCommand(accountUpdateCmd)

	rootCmd.AddCommand(accountCmd)
}
