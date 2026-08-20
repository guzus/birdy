package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/scrape"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
	"github.com/spf13/cobra"
)

type scrapeFlags struct {
	handles           []string
	searches          []string
	tweetIDs          []string
	listIDs           []string
	mode              string
	sort              string
	from              string
	to                string
	since             string
	until             string
	lang              string
	content           string
	minLikes          int
	minReposts        int
	filters           []string
	maxItems          int
	maxItemsPerTarget int
	output            string
	includeSearchTerm bool
}

var scrapeOpts scrapeFlags

func newScrapeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scrape [url|handle|query|id]...",
		Short:   "Scrape tweets from URLs, handles, searches, lists, and IDs",
		GroupID: "birdy",
		Long: `scrape collects tweets from mixed targets in one run. Pass tweet,
profile, search, and list URLs together with handles, tweet IDs, and
structured filters. Results are deduplicated before output.

Examples:
  birdy scrape https://x.com/nasa --max-items 50
  birdy scrape --handle elonmusk --handle nasa -n 100
  birdy scrape --search "AI lang:en" --sort both --output csv
  birdy scrape --from nasa --since 2026-01-01 --min-likes 100 --filter media
  birdy scrape --mode replies https://x.com/nasa/status/2090197889947451524
  birdy scrape --id 2090197889947451524 --id 1858743654778892784 --output flat`,
		RunE: runScrape,
	}
	cmd.Flags().StringArrayVar(&scrapeOpts.handles, "handle", nil, "profile handle to scrape (repeatable)")
	cmd.Flags().StringArrayVar(&scrapeOpts.searches, "search", nil, "search term (repeatable)")
	cmd.Flags().StringArrayVar(&scrapeOpts.tweetIDs, "id", nil, "tweet id or URL (repeatable)")
	cmd.Flags().StringArrayVar(&scrapeOpts.listIDs, "list", nil, "list id or URL (repeatable)")
	cmd.Flags().StringVar(&scrapeOpts.mode, "mode", scrape.ModeAuto, "auto, tweet, search, profile, profile-replies, profile-media, profile-likes, list, replies, quotes, thread, retweeters, favoriters")
	cmd.Flags().StringVar(&scrapeOpts.sort, "sort", "", "latest, top, or both (default latest)")
	cmd.Flags().StringVar(&scrapeOpts.from, "from", "", "only tweets by this handle")
	cmd.Flags().StringVar(&scrapeOpts.to, "to", "", "only replies to this handle")
	cmd.Flags().StringVar(&scrapeOpts.since, "since", "", "inclusive lower bound (YYYY-MM-DD)")
	cmd.Flags().StringVar(&scrapeOpts.until, "until", "", "exclusive upper bound (YYYY-MM-DD)")
	cmd.Flags().StringVar(&scrapeOpts.lang, "lang", "", "language code (en, es, ...)")
	cmd.Flags().StringVar(&scrapeOpts.content, "content", "", "free-text search content")
	cmd.Flags().IntVar(&scrapeOpts.minLikes, "min-likes", 0, "minimum like count (min_faves)")
	cmd.Flags().IntVar(&scrapeOpts.minReposts, "min-reposts", 0, "minimum repost count (min_retweets)")
	cmd.Flags().StringArrayVar(&scrapeOpts.filters, "filter", nil, "media, videos, images, links, replies, quote, blue_verified (repeatable)")
	cmd.Flags().IntVarP(&scrapeOpts.maxItems, "max-items", "n", scrape.DefaultMaxItems, "global result cap")
	cmd.Flags().IntVar(&scrapeOpts.maxItemsPerTarget, "max-items-per-target", 0, "per-target cap (defaults to --max-items)")
	cmd.Flags().StringVar(&scrapeOpts.output, "output", "json", "json, csv, or flat")
	cmd.Flags().BoolVar(&scrapeOpts.includeSearchTerm, "include-search-term", false, "attach the matching query to each tweet row")
	return cmd
}

func init() {
	rootCmd.AddCommand(newScrapeCmd())
}

func runScrape(cmd *cobra.Command, args []string) error {
	req := scrape.Request{
		Positionals: args,
		Handles:     scrapeOpts.handles,
		Searches:    scrapeOpts.searches,
		TweetIDs:    scrapeOpts.tweetIDs,
		ListIDs:     scrapeOpts.listIDs,
		Mode:        scrapeOpts.mode,
		Sort:        scrapeOpts.sort,
		Filters: scrape.Filters{
			Content:    scrapeOpts.content,
			From:       scrapeOpts.from,
			To:         scrapeOpts.to,
			Since:      scrapeOpts.since,
			Until:      scrapeOpts.until,
			Lang:       scrapeOpts.lang,
			MinLikes:   scrapeOpts.minLikes,
			MinReposts: scrapeOpts.minReposts,
			Operators:  scrapeOpts.filters,
		},
		MaxItems:          scrapeOpts.maxItems,
		MaxItemsPerTarget: scrapeOpts.maxItemsPerTarget,
		Output:            scrapeOpts.output,
		IncludeSearchTerm: scrapeOpts.includeSearchTerm,
	}
	return executeScrape(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), req)
}

func executeScrape(ctx context.Context, out, errOut io.Writer, req scrape.Request) error {
	if ctx == nil {
		ctx = context.Background()
	}
	jobs, err := req.Plan()
	if err != nil {
		return err
	}

	client, account, st, rs, err := openScrapeClient(errOut)
	if err != nil {
		return err
	}
	rows, runErr := collectScrapeRows(ctx, client, jobs, req)
	rateLimited := xapi.IsRateLimited(runErr)
	recordNativeUsage(errOut, st, rs, account, rateLimited)
	if runErr != nil && len(rows) == 0 {
		return runErr
	}
	if len(rows) == 0 {
		rows = []scrape.Row{scrape.DiagnosticRow("zero-output", "no tweets matched")}
	}
	if writeErr := writeScrapeOutput(out, rows, req.Output); writeErr != nil {
		return writeErr
	}
	return runErr
}

func openScrapeClient(errOut io.Writer) (*xapi.Client, *store.Account, *store.Store, *state.State, error) {
	st, err := store.Open()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("opening account store: %w", err)
	}
	printStoreWarning(errOut, st)
	if st.Len() == 0 {
		return nil, nil, nil, nil, fmt.Errorf("no accounts configured\nSet BIRDY_ACCOUNTS env var or run: birdy account add <name>")
	}

	var (
		account *store.Account
		rs      *state.State
	)
	if accountFlag != "" {
		account, err = st.Get(accountFlag)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	} else {
		strat, err := rotation.ParseStrategy(strategyFlag)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		rs, err = state.Load()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("loading rotation state: %w", err)
		}
		printStateWarning(errOut, rs)
		account, err = rotation.Pick(st.List(), strat, rs.LastUsedName)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if verboseFlag {
		fmt.Fprintf(errOut, "[birdy] using account: %s\n", account.Name)
		fmt.Fprintf(errOut, "[birdy] engine: native (go)\n")
	}

	client, err := xapi.NewClient(xapi.Credentials{AuthToken: account.AuthToken, CT0: account.CT0})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if vpnFlag || vpnServerFlag != "" {
		dial, err := vpnDialer(errOut)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		client.SetDialContext(dial)
	}
	return client, account, st, rs, nil
}

type scrapeFetcher interface {
	SearchWith(context.Context, string, int, xapi.SearchProduct) ([]xapi.Tweet, error)
	UserTweets(context.Context, string, int) ([]xapi.Tweet, string, error)
	Tweet(context.Context, string) (*xapi.Tweet, error)
	Conversation(context.Context, string) ([]xapi.Tweet, error)
	Replies(context.Context, string) ([]xapi.Tweet, error)
	QuoteTweets(context.Context, string, int) (*xapi.QuotePage, error)
	ListTimeline(context.Context, string, int) ([]xapi.Tweet, error)
	Likes(context.Context, string, int) ([]xapi.Tweet, error)
	Favoriters(context.Context, string, int) (*xapi.ActorPage, error)
	Retweeters(context.Context, string, int) (*xapi.ActorPage, error)
}

func collectScrapeRows(ctx context.Context, client scrapeFetcher, jobs []scrape.Job, req scrape.Request) ([]scrape.Row, error) {
	flat := strings.EqualFold(req.Output, "flat") || strings.EqualFold(req.Output, "csv")
	maxItems := req.MaxItems
	if maxItems <= 0 {
		maxItems = scrape.DefaultMaxItems
	}

	var (
		rows   []scrape.Row
		seen   = map[string]bool{}
		runErr error
	)
	addTweet := func(t xapi.Tweet, job scrape.Job) {
		if t.ID == "" || seen[t.ID] || len(rows) >= maxItems {
			return
		}
		if req.Filters.Lang != "" {
			if lang := t.Metrics().Lang; lang != "" && !strings.EqualFold(lang, req.Filters.Lang) {
				return
			}
		}
		searchTerm := ""
		if req.IncludeSearchTerm {
			searchTerm = firstNonEmpty(job.Query, job.Source)
		}
		seen[t.ID] = true
		rows = append(rows, scrape.TweetRow(t, string(job.Kind), searchTerm, flat))
	}
	addUser := func(user xapi.ListedUser, job scrape.Job, mode string) {
		key := "user:" + user.ID
		if user.ID == "" || seen[key] || len(rows) >= maxItems {
			return
		}
		seen[key] = true
		rows = append(rows, scrape.UserRow(user, job.Value, mode))
	}

	for _, job := range jobs {
		if len(rows) >= maxItems {
			break
		}
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		switch job.Kind {
		case scrape.KindTweet:
			found, err := client.Tweet(ctx, job.Value)
			if err != nil {
				runErr = err
				continue
			}
			addTweet(*found, job)
		case scrape.KindThreadJob:
			tweets, err := client.Conversation(ctx, job.Value)
			if err != nil {
				runErr = err
				continue
			}
			for _, t := range tweets {
				addTweet(t, job)
			}
		case scrape.KindRepliesJob:
			tweets, err := client.Replies(ctx, job.Value)
			if err != nil {
				runErr = err
				continue
			}
			for _, t := range tweets {
				if t.InReplyToStatusID != "" && t.InReplyToStatusID != job.Value {
					continue
				}
				addTweet(t, job)
			}
		case scrape.KindQuotesJob:
			page, err := client.QuoteTweets(ctx, job.Value, job.Limit)
			if err != nil {
				runErr = err
				continue
			}
			for _, t := range page.Tweets {
				addTweet(t, job)
			}
		case scrape.KindActorsJob:
			page, err := fetchActors(ctx, client, job)
			if err != nil {
				runErr = err
				continue
			}
			mode := job.Mode
			if mode == scrape.ModeAuto {
				mode = scrape.ModeRetweeters
			}
			for _, user := range page.Users {
				addUser(user, job, mode)
			}
		case scrape.KindList:
			tweets, err := client.ListTimeline(ctx, job.Value, job.Limit)
			if err != nil {
				runErr = err
				continue
			}
			for _, t := range tweets {
				addTweet(t, job)
			}
		case scrape.KindProfileLikes:
			tweets, err := client.Likes(ctx, job.Value, job.Limit)
			if err != nil {
				runErr = fmt.Errorf("profile likes for @%s are often hidden from other accounts: %w", job.Value, err)
				continue
			}
			for _, t := range tweets {
				addTweet(t, job)
			}
		case scrape.KindProfile:
			tweets, _, err := client.UserTweets(ctx, job.Value, job.Limit)
			if err != nil {
				runErr = err
				continue
			}
			for _, t := range tweets {
				addTweet(t, job)
			}
		case scrape.KindSearch:
			tweets, err := searchJob(ctx, client, job)
			if err != nil {
				runErr = err
			}
			for _, t := range tweets {
				addTweet(t, job)
			}
		default:
			return rows, fmt.Errorf("unsupported scrape kind %q", job.Kind)
		}
	}
	return rows, runErr
}

func fetchActors(ctx context.Context, client scrapeFetcher, job scrape.Job) (*xapi.ActorPage, error) {
	if job.Mode == scrape.ModeFavoriters {
		return client.Favoriters(ctx, job.Value, job.Limit)
	}
	return client.Retweeters(ctx, job.Value, job.Limit)
}

func searchJob(ctx context.Context, client scrapeFetcher, job scrape.Job) ([]xapi.Tweet, error) {
	switch job.Sort {
	case scrape.SortTop:
		return client.SearchWith(ctx, job.Query, job.Limit, xapi.SearchTop)
	case scrape.SortBoth:
		latest, err := client.SearchWith(ctx, job.Query, job.Limit, xapi.SearchLatest)
		if err != nil {
			return nil, err
		}
		top, err := client.SearchWith(ctx, job.Query, job.Limit, xapi.SearchTop)
		if err != nil {
			return latest, err
		}
		return mergeTweets(latest, top), nil
	default:
		return client.SearchWith(ctx, job.Query, job.Limit, xapi.SearchLatest)
	}
}

func mergeTweets(parts ...[]xapi.Tweet) []xapi.Tweet {
	seen := map[string]bool{}
	var out []xapi.Tweet
	for _, part := range parts {
		for _, t := range part {
			if t.ID == "" || seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	return out
}

func writeScrapeOutput(out io.Writer, rows []scrape.Row, format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		return scrape.WriteCSV(out, rows)
	default:
		return scrape.WriteJSON(out, rows)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// resetScrapeFlags clears package-level scrape flags between tests.
func resetScrapeFlags() {
	scrapeOpts = scrapeFlags{}
}
