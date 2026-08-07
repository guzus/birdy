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

func accountAccessLabel(readOnly bool) string {
	if readOnly {
		return "read-only"
	}
	return "read-write"
}

func resolveReadOnlyUpdateFlags(cmd *cobra.Command) (bool, bool, error) {
	readOnlyChanged := cmd.Flags().Changed("read-only")
	readWriteChanged := cmd.Flags().Changed("read-write")
	if readOnlyChanged && readWriteChanged {
		return false, false, fmt.Errorf("choose only one of --read-only or --read-write")
	}
	if readOnlyChanged {
		return true, true, nil
	}
	if readWriteChanged {
		return false, true, nil
	}
	return false, false, nil
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
		readOnly, _ := cmd.Flags().GetBool("read-only")

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

		if err := st.AddWithReadOnly(name, authToken, ct0, readOnly); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "Account %q added (%s).\n", name, accountAccessLabel(readOnly))
		return nil
	},
}

var accountListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all accounts",
	Args:    cobra.NoArgs,
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
		fmt.Fprintln(w, "NAME\tACCESS\tROTATION\tUSES\tLAST USED\tADDED")
		for _, a := range accounts {
			lastUsed := "-"
			if !a.LastUsed.IsZero() {
				lastUsed = a.LastUsed.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
				a.Name,
				accountAccessLabel(a.ReadOnly),
				accountStateLabel(a.Disabled),
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
		authTokenChanged := cmd.Flags().Changed("auth-token")
		ct0Changed := cmd.Flags().Changed("ct0")
		readOnly, modeChanged, err := resolveReadOnlyUpdateFlags(cmd)
		if err != nil {
			return err
		}

		reader := bufio.NewReader(cmd.InOrStdin())
		updateCredentials := !modeChanged || authTokenChanged || ct0Changed
		if updateCredentials {
			if !authTokenChanged {
				_, _ = fmt.Fprint(out, "auth_token: ")
				authToken, _ = reader.ReadString('\n')
				authToken = strings.TrimSpace(authToken)
			}
			if !ct0Changed {
				_, _ = fmt.Fprint(out, "ct0: ")
				ct0, _ = reader.ReadString('\n')
				ct0 = strings.TrimSpace(ct0)
			}

			if authToken == "" || ct0 == "" {
				return fmt.Errorf("both auth_token and ct0 are required")
			}
		}

		st, err := store.Open()
		if err != nil {
			return err
		}
		printStoreWarning(errOut, st)
		if err := ensureAccountMutationAllowed(st); err != nil {
			return err
		}

		if updateCredentials {
			if err := st.Update(name, authToken, ct0); err != nil {
				return err
			}
		}
		if modeChanged {
			if err := st.SetReadOnly(name, readOnly); err != nil {
				return err
			}
		}
		if err := st.Save(); err != nil {
			return err
		}

		account, err := st.Get(name)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "Account %q updated (%s).\n", name, accountAccessLabel(account.ReadOnly))
		return nil
	},
}

func init() {
	accountAddCmd.Flags().String("auth-token", "", "auth_token cookie value")
	accountAddCmd.Flags().String("ct0", "", "ct0 cookie value")
	accountAddCmd.Flags().Bool("read-only", false, "mark the account as restricted to read-only bird commands")

	accountUpdateCmd.Flags().String("auth-token", "", "auth_token cookie value")
	accountUpdateCmd.Flags().String("ct0", "", "ct0 cookie value")
	accountUpdateCmd.Flags().Bool("read-only", false, "restrict the account to read-only bird commands")
	accountUpdateCmd.Flags().Bool("read-write", false, "allow the account to run mutating bird commands again")

	accountCmd.AddCommand(accountAddCmd)
	accountCmd.AddCommand(accountListCmd)
	accountCmd.AddCommand(accountRemoveCmd)
	accountCmd.AddCommand(accountDisableCmd)
	accountCmd.AddCommand(accountEnableCmd)
	accountCmd.AddCommand(accountUpdateCmd)

	rootCmd.AddCommand(accountCmd)
}

// setAccountEnabled flips an account's Disabled flag.
//
// Disabling is deliberately not removal: rate limits, a suspension and a stale
// login are all reasons to stop using an account, and none of them are reasons
// to throw its credentials away.
func setAccountEnabled(cmd *cobra.Command, name string, enabled bool) error {
	name = strings.TrimSpace(name)
	out := cmd.OutOrStdout()
	st, err := store.Open()
	if err != nil {
		return err
	}
	printStoreWarning(cmd.ErrOrStderr(), st)
	if err := ensureAccountMutationAllowed(st); err != nil {
		return err
	}

	account, err := st.Get(name)
	if err != nil {
		return err
	}
	if account.Disabled == !enabled {
		fmt.Fprintf(out, "%s is already %s\n", name, accountStateLabel(!enabled))
		return nil
	}

	account.Disabled = !enabled
	if err := st.Save(); err != nil {
		return err
	}

	fmt.Fprintf(out, "%s is now %s\n", name, accountStateLabel(!enabled))
	if !enabled {
		remaining := 0
		for _, a := range st.List() {
			if !a.Disabled {
				remaining++
			}
		}
		// Rotation needs somebody left, and finding that out at the next
		// command is worse than hearing it now.
		if remaining == 0 {
			fmt.Fprintf(out, "warning: no accounts remain in rotation\n")
		} else {
			fmt.Fprintf(out, "%d account(s) remain in rotation\n", remaining)
		}
		fmt.Fprintf(out, "`--account %s` still selects it explicitly\n", name)
	}
	return nil
}

func accountStateLabel(disabled bool) string {
	if disabled {
		return "disabled"
	}
	return "enabled"
}

var accountDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Take an account out of rotation without removing it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setAccountEnabled(cmd, args[0], false)
	},
}

var accountEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Return a disabled account to rotation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return setAccountEnabled(cmd, args[0], true)
	},
}
