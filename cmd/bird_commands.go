package cmd

import "github.com/spf13/cobra"

// makeBirdCmd creates a lightweight cobra command that forwards to bird
// via the existing passthrough logic. DisableFlagParsing ensures all
// flags and args are passed through to bird untouched.
func makeBirdCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		GroupID:            "bird",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Prepend the command name back — cobra consumed it.
			return runPassthrough(cmd, append([]string{cmd.Name()}, args...))
		},
	}
}

func init() {
	rootCmd.AddGroup(
		&cobra.Group{ID: "bird", Title: "Bird Commands (forwarded to bird):"},
		&cobra.Group{ID: "birdy", Title: "Birdy Commands:"},
	)

	birdCmds := []struct{ use, short string }{
		{"about", "Get account information for a user"},
		{"bookmarks", "Get your bookmarked tweets"},
		{"check", "Check credential availability"},
		{"follow", "Follow a user"},
		{"followers", "Get followers for a user"},
		{"following", "Get following for a user"},
		{"home", "Get your home timeline"},
		{"likes", "Get likes for a user"},
		{"list-timeline", "Get tweets from a list"},
		{"lists", "Get lists for a user"},
		{"mentions", "Get your mentions"},
		{"news", "Get trending news"},
		{"query-ids", "Query tweets by IDs"},
		{"read", "Read a tweet by ID or URL"},
		{"replies", "Get replies to a tweet"},
		{"reply", "Reply to a tweet"},
		{"search", "Search for tweets"},
		{"thread", "Read a tweet thread"},
		{"tweet", "Post a new tweet"},
		{"unbookmark", "Remove a tweet from bookmarks"},
		{"unfollow", "Unfollow a user"},
		{"user-tweets", "Get tweets for a user"},
		{"whoami", "Show current authenticated user"},
	}

	for _, c := range birdCmds {
		rootCmd.AddCommand(makeBirdCmd(c.use, c.short))
	}
}
