package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	strategyFlag string
	accountFlag  string
	verboseFlag  bool
)

var rootCmd = &cobra.Command{
	Use:   "birdy",
	Short: "Multi-account proxy for the bird CLI",
	Long: `birdy manages multiple X/Twitter auth tokens and proxies commands
to the bird CLI, rotating between accounts to reduce rate-limit risk.

Any command not recognized by birdy is forwarded directly to bird
using the next account in the rotation.

Examples:
  birdy read 1234567890           # read a tweet, auto-rotating accounts
  birdy search "golang"           # search, auto-rotating accounts
  birdy --account main home       # use a specific account
  birdy account add main          # add a new account
  birdy account list              # list all accounts`,
	// If no subcommand matches, treat everything as bird args.
	RunE:          runPassthrough,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Allow unknown flags so they can be forwarded to bird.
	FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
	// Pass remaining args after -- or unknown args through.
	Args: cobra.ArbitraryArgs,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&strategyFlag, "strategy", "s", "round-robin",
		"rotation strategy: round-robin, least-recently-used, least-used, random")
	rootCmd.PersistentFlags().StringVarP(&accountFlag, "account", "a", "",
		"use a specific account by name (skip rotation)")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false,
		"show which account is being used")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
