package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/guzus/birdy/internal/store"
	"github.com/spf13/cobra"
)

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage bird CLI accounts",
	Long:  "Add, remove, list, and update X/Twitter auth tokens for bird CLI.",
}

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		authToken, _ := cmd.Flags().GetString("auth-token")
		ct0, _ := cmd.Flags().GetString("ct0")

		reader := bufio.NewReader(os.Stdin)

		if authToken == "" {
			fmt.Print("auth_token: ")
			authToken, _ = reader.ReadString('\n')
			authToken = strings.TrimSpace(authToken)
		}
		if ct0 == "" {
			fmt.Print("ct0: ")
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

		if err := st.Add(name, authToken, ct0); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		fmt.Printf("Account %q added.\n", name)
		return nil
	},
}

var accountListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all accounts",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := store.Open()
		if err != nil {
			return err
		}

		accounts := st.List()
		if len(accounts) == 0 {
			fmt.Println("No accounts configured. Run: birdy account add <name>")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
		st, err := store.Open()
		if err != nil {
			return err
		}

		if err := st.Remove(args[0]); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		fmt.Printf("Account %q removed.\n", args[0])
		return nil
	},
}

var accountUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update credentials for an existing account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		authToken, _ := cmd.Flags().GetString("auth-token")
		ct0, _ := cmd.Flags().GetString("ct0")

		reader := bufio.NewReader(os.Stdin)

		if authToken == "" {
			fmt.Print("auth_token: ")
			authToken, _ = reader.ReadString('\n')
			authToken = strings.TrimSpace(authToken)
		}
		if ct0 == "" {
			fmt.Print("ct0: ")
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

		if err := st.Update(name, authToken, ct0); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}

		fmt.Printf("Account %q updated.\n", name)
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
