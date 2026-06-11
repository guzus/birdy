package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/vpn"
	"github.com/spf13/cobra"
)

type commonFollowingUser struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Name            string `json:"name,omitempty"`
	Description     string `json:"description,omitempty"`
	FollowersCount  int64  `json:"followersCount,omitempty"`
	FollowingCount  int64  `json:"followingCount,omitempty"`
	IsBlueVerified  bool   `json:"isBlueVerified,omitempty"`
	ProfileImageURL string `json:"profileImageUrl,omitempty"`
	CreatedAt       string `json:"createdAt,omitempty"`
}

type commonFollowingTweet struct {
	AuthorID string `json:"authorId"`
	Author   struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"author"`
}

type commonFollowingResult struct {
	commonFollowingUser
	Count      int      `json:"count"`
	FollowedBy []string `json:"followedBy"`
}

type commonFollowingBirdRunner struct {
	st      *store.Store
	rs      *state.State
	strat   rotation.Strategy
	runOpts runner.Options
}

var (
	commonFollowingMin      int
	commonFollowingPageSize int
	commonFollowingAll      bool
	commonFollowingMaxPages int
	commonFollowingJSON     bool
)

func newCommonFollowingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "common-following [handles...]",
		Aliases: []string{"common-follows"},
		Short:   "Find accounts commonly followed by at least N seed accounts",
		GroupID: "birdy",
		Long: `common-following finds high-signal accounts by fetching each seed
account's following list, counting target accounts, and returning accounts
followed by at least --min seeds.`,
		Args: cobra.MinimumNArgs(2),
		RunE: runCommonFollowing,
	}
	cmd.Flags().IntVar(&commonFollowingMin, "min", 2, "minimum number of seed accounts that must follow a target")
	cmd.Flags().IntVar(&commonFollowingPageSize, "page-size", 200, "users to request per following page")
	cmd.Flags().BoolVar(&commonFollowingAll, "all", true, "fetch all following pages for each seed")
	cmd.Flags().IntVar(&commonFollowingMaxPages, "max-pages", 0, "stop after N pages per seed when --all is true")
	cmd.Flags().BoolVar(&commonFollowingJSON, "json", false, "output JSON")
	return cmd
}

func init() {
	rootCmd.AddCommand(newCommonFollowingCmd())
}

func runCommonFollowing(cmd *cobra.Command, args []string) error {
	if commonFollowingMin < 1 {
		return fmt.Errorf("--min must be at least 1")
	}
	if commonFollowingPageSize < 1 {
		return fmt.Errorf("--page-size must be at least 1")
	}
	if commonFollowingMaxPages < 0 {
		return fmt.Errorf("--max-pages cannot be negative")
	}

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("opening account store: %w", err)
	}
	printStoreWarning(cmd.ErrOrStderr(), st)
	if st.Len() == 0 {
		return fmt.Errorf("no accounts configured\nSet BIRDY_ACCOUNTS env var or run: birdy account add <name>")
	}

	rs, err := state.Load()
	if err != nil {
		return fmt.Errorf("loading rotation state: %w", err)
	}
	printStateWarning(cmd.ErrOrStderr(), rs)

	strat, err := rotation.ParseStrategy(strategyFlag)
	if err != nil {
		return err
	}

	runOpts := runner.Options{}
	var bridge *vpn.Bridge
	if vpnFlag || vpnServerFlag != "" {
		b, env, err := startVPN(cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		bridge = b
		runOpts.ExtraEnv = env
		defer bridge.Stop()
	}

	br := &commonFollowingBirdRunner{
		st:      st,
		rs:      rs,
		strat:   strat,
		runOpts: runOpts,
	}

	seeds := normalizeCommonFollowingSeeds(args)
	if len(seeds) < 2 {
		return fmt.Errorf("at least two distinct seed accounts are required")
	}
	if commonFollowingMin > len(seeds) {
		return fmt.Errorf("--min cannot be greater than the number of distinct seed accounts")
	}

	byTarget := make(map[string]*commonFollowingResult)
	seenSeedTarget := make(map[string]map[string]bool, len(seeds))

	for _, seed := range seeds {
		userID, err := br.resolveUserID(seed)
		if err != nil {
			return fmt.Errorf("resolving @%s: %w", seed, err)
		}
		users, err := br.fetchFollowing(userID)
		if err != nil {
			return fmt.Errorf("fetching following for @%s: %w", seed, err)
		}
		if seenSeedTarget[seed] == nil {
			seenSeedTarget[seed] = make(map[string]bool, len(users))
		}
		for _, user := range users {
			if user.ID == "" || user.Username == "" || seenSeedTarget[seed][user.ID] {
				continue
			}
			seenSeedTarget[seed][user.ID] = true
			entry := byTarget[user.ID]
			if entry == nil {
				u := user
				entry = &commonFollowingResult{commonFollowingUser: u}
				byTarget[user.ID] = entry
			}
			entry.Count++
			entry.FollowedBy = append(entry.FollowedBy, seed)
		}
	}

	results := make([]commonFollowingResult, 0, len(byTarget))
	for _, entry := range byTarget {
		if entry.Count >= commonFollowingMin {
			sort.Strings(entry.FollowedBy)
			results = append(results, *entry)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Count != results[j].Count {
			return results[i].Count > results[j].Count
		}
		if results[i].FollowersCount != results[j].FollowersCount {
			return results[i].FollowersCount > results[j].FollowersCount
		}
		return strings.ToLower(results[i].Username) < strings.ToLower(results[j].Username)
	})

	if err := br.saveState(); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if commonFollowingJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	for _, result := range results {
		fmt.Fprintf(out, "%d\t@%s\t%s\t%d followers\t%s\n",
			result.Count,
			result.Username,
			result.Name,
			result.FollowersCount,
			strings.Join(result.FollowedBy, ","),
		)
	}
	return nil
}

func normalizeCommonFollowingSeeds(args []string) []string {
	seen := make(map[string]bool, len(args))
	seeds := make([]string, 0, len(args))
	for _, arg := range args {
		seed := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(arg), "@"))
		if seed == "" || seen[seed] {
			continue
		}
		seen[seed] = true
		seeds = append(seeds, seed)
	}
	return seeds
}

func (r *commonFollowingBirdRunner) resolveUserID(handle string) (string, error) {
	stdout, stderr, err := r.run([]string{"user-tweets", handle, "-n", "1", "--json", "--plain"})
	if err != nil {
		return "", errWithStderr(err, stderr)
	}
	var tweets []commonFollowingTweet
	if err := json.Unmarshal([]byte(stdout), &tweets); err != nil {
		return "", fmt.Errorf("parsing user-tweets JSON: %w", err)
	}
	for _, tweet := range tweets {
		if tweet.AuthorID != "" {
			return tweet.AuthorID, nil
		}
	}
	return "", fmt.Errorf("could not determine user ID from user-tweets output")
}

func (r *commonFollowingBirdRunner) fetchFollowing(userID string) ([]commonFollowingUser, error) {
	args := []string{"following", "--user", userID, "-n", fmt.Sprintf("%d", commonFollowingPageSize), "--json", "--plain"}
	if commonFollowingAll {
		args = append(args, "--all")
		if commonFollowingMaxPages > 0 {
			args = append(args, "--max-pages", fmt.Sprintf("%d", commonFollowingMaxPages))
		}
	}
	stdout, stderr, err := r.run(args)
	if err != nil {
		return nil, errWithStderr(err, stderr)
	}
	return parseCommonFollowingUsers([]byte(stdout))
}

func (r *commonFollowingBirdRunner) run(args []string) (string, string, error) {
	account, err := r.pickAccount()
	if err != nil {
		return "", "", err
	}
	if err := ensureBirdCommandAllowed(account, args); err != nil {
		return "", "", err
	}
	res, stdout, stderr, err := runner.RunCaptureWith(account, args, r.runOpts)
	if err != nil {
		return stdout, stderr, err
	}
	if err := r.st.RecordUsage(account.Name); err != nil {
		return stdout, stderr, err
	}
	if res.RateLimited {
		if err := r.st.RecordRateLimit(account.Name); err != nil {
			return stdout, stderr, err
		}
	}
	if res.ExitCode != 0 {
		return stdout, stderr, fmt.Errorf("bird exited with code %d", res.ExitCode)
	}
	return stdout, stderr, nil
}

func (r *commonFollowingBirdRunner) pickAccount() (*store.Account, error) {
	if accountFlag != "" {
		return r.st.Get(accountFlag)
	}
	account, err := rotation.Pick(r.st.List(), r.strat, r.rs.LastUsedName)
	if err != nil {
		return nil, err
	}
	r.rs.LastUsedName = account.Name
	return account, nil
}

func (r *commonFollowingBirdRunner) saveState() error {
	if err := r.st.Save(); err != nil {
		return fmt.Errorf("saving account store: %w", err)
	}
	if accountFlag == "" && r.rs != nil {
		if err := r.rs.Save(); err != nil {
			return fmt.Errorf("saving rotation state: %w", err)
		}
	}
	return nil
}

func parseCommonFollowingUsers(data []byte) ([]commonFollowingUser, error) {
	var users []commonFollowingUser
	if err := json.Unmarshal(data, &users); err == nil {
		return users, nil
	}
	var paged struct {
		Users []commonFollowingUser `json:"users"`
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, fmt.Errorf("parsing following JSON: %w", err)
	}
	return paged.Users, nil
}

func errWithStderr(err error, stderr string) error {
	if strings.TrimSpace(stderr) == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr))
}
