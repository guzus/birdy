package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// makeBirdCmd creates a lightweight cobra command that forwards to bird
// via the existing passthrough logic.
//
// We set DisableFlagParsing on bird subcommands because bird has its own
// flags (--max-pages, --json, -n, etc) that cobra would otherwise try to
// validate. The side-effect is that birdy's own global flags (--account/-a,
// --strategy/-s, --verbose/-v) also bypass cobra's parsing when they appear
// before the bird subcommand name — so we extract them manually before
// forwarding the remaining args to runPassthrough.
//
// Examples that now work:
//
//	birdy --account alt4 user-tweets @handle      # use alt4 specifically
//	birdy -a alt4 user-tweets @handle              # short form
//	birdy --strategy least-used user-tweets @h     # alternate rotation strategy
//	birdy -v user-tweets @handle                   # verbose
//	birdy --account=alt4 user-tweets @handle       # equals-form
//	birdy user-tweets -- --account weird           # after --, flags pass through
func makeBirdCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		GroupID:            "bird",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cleaned, err := applyBirdyGlobalFlags(args)
			if err != nil {
				return err
			}
			// Prepend the command name back — cobra consumed it.
			return runPassthrough(cmd, append([]string{cmd.Name()}, cleaned...))
		},
	}
}

// applyBirdyGlobalFlags scans args for birdy's global flags (--account/-a,
// --strategy/-s, --verbose/-v), sets the package-level variables, and
// returns the remaining args with those flags stripped.
//
// Anything after a `--` separator is left untouched so users can pass
// `--account` (or similar) through to bird if it ever adds such a flag.
//
// Returns an error if a flag is missing its value (e.g. trailing `--account`).
func applyBirdyGlobalFlags(args []string) ([]string, error) {
	cleaned := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		arg := args[i]
		// Pass everything after `--` through unchanged.
		if arg == "--" {
			cleaned = append(cleaned, args[i:]...)
			break
		}

		// Handle --flag=value first (no need to peek next arg).
		if eq := strings.IndexByte(arg, '='); eq > 0 {
			name := arg[:eq]
			value := arg[eq+1:]
			switch name {
			case "--account", "-a":
				accountFlag = value
				i++
				continue
			case "--strategy", "-s":
				strategyFlag = value
				i++
				continue
			case "--verbose", "-v":
				// --verbose=true / --verbose=false / --verbose=1 ...
				accepted, ok := parseBoolFlag(value)
				if !ok {
					return nil, fmt.Errorf("invalid value for %s: %q", name, value)
				}
				verboseFlag = accepted
				i++
				continue
			case "--vpn":
				accepted, ok := parseBoolFlag(value)
				if !ok {
					return nil, fmt.Errorf("invalid value for %s: %q", name, value)
				}
				vpnFlag = accepted
				i++
				continue
			case "--vpn-server":
				vpnServerFlag = value
				vpnFlag = true
				i++
				continue
			}
			// Not one of ours — pass through.
			cleaned = append(cleaned, arg)
			i++
			continue
		}

		// Space-separated --flag value forms.
		switch arg {
		case "--account", "-a":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			if args[i+1] == "" {
				return nil, fmt.Errorf("%s requires a non-empty value", arg)
			}
			accountFlag = args[i+1]
			i += 2
			continue
		case "--strategy", "-s":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			if args[i+1] == "" {
				return nil, fmt.Errorf("%s requires a non-empty value", arg)
			}
			strategyFlag = args[i+1]
			i += 2
			continue
		case "--verbose", "-v":
			verboseFlag = true
			i++
			continue
		case "--vpn":
			vpnFlag = true
			i++
			continue
		case "--vpn-server":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			if args[i+1] == "" {
				return nil, fmt.Errorf("%s requires a non-empty value", arg)
			}
			vpnServerFlag = args[i+1]
			vpnFlag = true
			i += 2
			continue
		}

		cleaned = append(cleaned, arg)
		i++
	}
	return cleaned, nil
}

// parseBoolFlag mirrors cobra's bool flag value parser for the subset of
// strings we expect (true/false/1/0/yes/no/on/off, case-insensitive).
func parseBoolFlag(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
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
