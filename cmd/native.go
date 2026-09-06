package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
	"github.com/guzus/birdy/pkg/tweet"
)

// Commands birdy serves itself, in Go, without spawning the bird CLI.
//
// Every command birdy serves is here; nothing forwards to bird by default. The
// --bird flag (or BIRDY_USE_BIRD=1) still forces the bird path when bird is
// installed separately, which is how birdy's output is verified against the
// engine it replaced.
var nativeCommands = map[string]func(context.Context, *xapi.Client, nativeArgs, io.Writer) error{
	"read":          nativeRead,
	"thread":        nativeThread,
	"replies":       nativeReplies,
	"search":        nativeSearch,
	"home":          nativeHome,
	"user-tweets":   nativeUserTweets,
	"bookmarks":     nativeBookmarks,
	"list-timeline": nativeListTimeline,
	"whoami":        nativeWhoami,
	"about":         nativeAbout,
	"likes":         nativeLikes,
	"followers":     nativeFollowers,
	"following":     nativeFollowing,
	"tweet":         nativeTweet,
	"reply":         nativeReply,
	"follow":        nativeFollow,
	"unfollow":      nativeUnfollow,
	"unbookmark":    nativeUnbookmark,
	"check":         nativeCheck,
	"mentions":      nativeMentions,
	"query-ids":     nativeQueryIDs,
	"lists":         nativeLists,
	"activity":      nativeActivity,
	"news":          nativeNews,
}

// defaultCounts overrides the common default of 20 where bird differs.
var defaultCounts = map[string]int{
	"mentions": 10,
	"search":   10,
	"lists":    100,
	"news":     10,
}

// nativeSupports reports whether a command has a native implementation.
func nativeSupports(command string) bool {
	_, ok := nativeCommands[command]
	return ok
}

// useBird reports whether the caller asked for the bird CLI explicitly.
func useBird() bool {
	if birdFlag {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BIRDY_USE_BIRD"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// nativeArgs is the parsed argument set shared by the native commands.
type nativeArgs struct {
	// positional is the first non-flag argument: a tweet id/url, handle, query,
	// or list id depending on the command.
	positional string
	// positionals is every non-flag argument in order. reply takes two, and
	// unbookmark is variadic.
	positionals []string
	count       int
	json        bool
	// jsonFull selects the enriched `--json-full` view: the exact `--json`
	// object followed by url, createdAtIso, the extra metrics, and the
	// relation flags. It implies json.
	jsonFull bool
	// Engagement/time filters. They apply after the fetch and before
	// rendering, in text and JSON modes alike, and change nothing when unset.
	minLikes    int
	minRetweets int
	minViews    int
	since       time.Time
	sinceSet    bool
	// filterErr carries a rejected filter value so the command fails instead
	// of silently running unfiltered.
	filterErr error
	// plain and emoji mirror bird's output switches so rendering matches.
	plain bool
	emoji bool
	// latest selects the chronological home timeline.
	latest bool
	// user is what --user carried. followers/following take a numeric account
	// id here; mentions takes a handle. The commands interpret it themselves.
	user string
	// memberOf switches `lists` from owned lists to memberships.
	memberOf bool
	// types is `activity`'s --types selector.
	types string
	// aiOnly and newsTabs drive `news`.
	aiOnly   bool
	newsTabs []string
	// authToken and ct0 are the selected account's credentials, needed by
	// `check`, which reports on them rather than calling X.
	authToken string
	ct0       string
	// countErr carries a rejected --count so the command can fail the way bird
	// does instead of quietly falling back to a default.
	countErr error
	// countSet records whether the caller passed a count, so a command with a
	// different default does not silently override an explicit -n.
	countSet bool
	// command is the birdy subcommand being served, used to pick the wording
	// for an empty result.
	command string
}

// parseNativeArgs reads the flags the native commands honor. Unknown flags are
// not an error here: callers check nativeAcceptsFlags first and fall back to
// bird when something unsupported appears, rather than silently ignoring it.
func parseNativeArgs(args []string) nativeArgs {
	parsed := nativeArgs{count: 20, emoji: true}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			parsed.json = true
		case arg == "--json-full":
			parsed.json = true
			parsed.jsonFull = true
		case filterFlags[flagName(arg)]:
			name, value, inline := splitFlag(arg)
			if !inline && i+1 < len(args) {
				value = args[i+1]
				i++
			}
			if err := parsed.applyFilter(name, value); err != nil && parsed.filterErr == nil {
				parsed.filterErr = err
			}
		case arg == "--plain":
			parsed.plain = true
			parsed.emoji = false
		case arg == "--no-emoji":
			parsed.emoji = false
		case arg == "--no-color":
			// Accepted for compatibility; native output is never colorized.
		case arg == "--latest":
			parsed.latest = true
		case arg == "--member-of":
			parsed.memberOf = true
		case arg == "--ai-only":
			parsed.aiOnly = true
		case newsTabFlags[arg] != "":
			parsed.newsTabs = append(parsed.newsTabs, newsTabFlags[arg])
		case arg == "--types":
			if i+1 < len(args) {
				parsed.types = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--types="):
			parsed.types = arg[len("--types="):]
		case arg == "--user" || arg == "-u":
			if i+1 < len(args) {
				parsed.user = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--user="):
			parsed.user = arg[len("--user="):]
		case strings.HasPrefix(arg, "-u="):
			parsed.user = arg[len("-u="):]
		case arg == "-n" || arg == "--count" || arg == "--limit":
			if i+1 < len(args) {
				parsed.count, parsed.countSet, parsed.countErr = parseCount(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--count=") || strings.HasPrefix(arg, "--limit="):
			parsed.count, parsed.countSet, parsed.countErr = parseCount(arg[strings.IndexByte(arg, '=')+1:])
		case strings.HasPrefix(arg, "-"):
			// Ignored here; nativeAcceptsFlags rejects the command first.
		default:
			if parsed.positional == "" {
				parsed.positional = arg
			}
			parsed.positionals = append(parsed.positionals, arg)
		}
	}
	return parsed
}

// parseCount mirrors bird's validation: a count must parse and be positive.
// Falling back to the default on garbage would answer a different question than
// the one asked.
func parseCount(raw string) (int, bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, false, fmt.Errorf("Invalid --count. Expected a positive integer.")
	}
	return n, true, nil
}

// flagName strips an inline `=value` so `--since=24h` and `--since 24h` are
// the same flag.
func flagName(arg string) string {
	if eq := strings.IndexByte(arg, '='); eq > 0 {
		return arg[:eq]
	}
	return arg
}

// splitFlag separates `--flag=value` into its parts. inline reports whether the
// value was attached, so the caller knows whether to consume the next argument.
func splitFlag(arg string) (name, value string, inline bool) {
	if eq := strings.IndexByte(arg, '='); eq > 0 {
		return arg[:eq], arg[eq+1:], true
	}
	return arg, "", false
}

// filterFlags are the post-fetch engagement/time filters. They take a value.
var filterFlags = map[string]bool{
	"--min-likes": true, "--min-retweets": true, "--min-views": true, "--since": true,
}

// applyFilter records one filter flag. Values are validated here rather than
// at use, so a typo fails the command the way a bad --count does.
func (a *nativeArgs) applyFilter(name, value string) error {
	switch name {
	case "--since":
		since, err := parseSince(value, nativeNow())
		if err != nil {
			return err
		}
		a.since, a.sinceSet = since, true
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return fmt.Errorf("Invalid %s. Expected a non-negative integer.", name)
	}
	switch name {
	case "--min-likes":
		a.minLikes = n
	case "--min-retweets":
		a.minRetweets = n
	case "--min-views":
		a.minViews = n
	}
	return nil
}

// hasFilter reports whether any post-fetch filter was requested.
func (a nativeArgs) hasFilter() bool {
	return a.minLikes > 0 || a.minRetweets > 0 || a.minViews > 0 || a.sinceSet
}

// nativeNow is the clock --since durations are relative to. Indirected so
// tests can pin it.
var nativeNow = func() time.Time { return time.Now().UTC() }

// parseSince accepts a duration (`24h`, `90m`, plus `7d`/`2w`, which Go's
// ParseDuration lacks and which are what people actually type), an RFC 3339
// timestamp, or a `YYYY-MM-DD` date read as UTC midnight.
func parseSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("Invalid --since. Expected a duration (24h, 7d), an RFC 3339 timestamp, or YYYY-MM-DD.")
	}
	if n := len(value); n > 1 && (value[n-1] == 'd' || value[n-1] == 'w') {
		if units, err := strconv.Atoi(value[:n-1]); err == nil && units >= 0 {
			days := units
			if value[n-1] == 'w' {
				days *= 7
			}
			return now.Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("Invalid --since. A duration must not be negative.")
		}
		return now.Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse("2006-01-02", value); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("Invalid --since. Expected a duration (24h, 7d), an RFC 3339 timestamp, or YYYY-MM-DD.")
}

// filterTweets applies the engagement/time filters. With none set it returns
// the input untouched — including its nil-ness, which the JSON path turns into
// `[]` exactly as before.
//
// --since DROPS a tweet whose createdAt is missing or unparsable. A filter
// that quietly keeps what it cannot judge is not a filter, and X omits
// created_at only on shapes (tombstones, some promoted entries) a time-bounded
// query does not want anyway. Likewise a view count X did not report counts
// as 0 against --min-views.
func filterTweets(tweets []xapi.Tweet, args nativeArgs) []xapi.Tweet {
	if !args.hasFilter() {
		return tweets
	}
	kept := make([]xapi.Tweet, 0, len(tweets))
	for _, t := range tweets {
		if t.LikeCount < args.minLikes || t.RetweetCount < args.minRetweets {
			continue
		}
		if args.minViews > 0 && t.Metrics().ViewCount < args.minViews {
			continue
		}
		if args.sinceSet {
			created, ok := xapi.ParseCreatedAt(t.CreatedAt)
			if !ok || created.Before(args.since) {
				continue
			}
		}
		kept = append(kept, t)
	}
	return kept
}

// tweetListCommands render through renderTweets and therefore accept the
// post-fetch filters and --json-full. `read` returns one tweet, so it gets the
// enriched view but no filters; `activity` has its own JSON report shape and
// gets neither, rather than answering --json-full with an unenriched report.
var tweetListCommands = map[string]bool{
	"thread": true, "replies": true, "search": true, "home": true,
	"user-tweets": true, "bookmarks": true, "list-timeline": true,
	"likes": true, "mentions": true,
}

// tweetFlagAccepted widens the common set with the flags that only make sense
// where a tweet is the unit of output.
func tweetFlagAccepted(command, name string) bool {
	if name == "--json-full" {
		return command == "read" || tweetListCommands[command]
	}
	return filterFlags[name] && tweetListCommands[command]
}

// nativeSupportedFlags are the flags the native path understands. A command
// carrying anything else is handed to bird, so a flag birdy has not implemented
// yet keeps working instead of being quietly dropped.
var nativeSupportedFlags = map[string]bool{
	"--json": true, "--plain": true, "--no-emoji": true, "--no-color": true,
	"-n": true, "--count": true, "--limit": true,
}

// commandExtraFlags widens the common set for commands that accept a flag no
// other command does. --user is bird's target selector on the listing
// commands, which take a numeric id instead of a positional handle.
var commandExtraFlags = map[string]map[string]bool{
	// --latest selects the chronological home feed. It is meaningful nowhere
	// else, and bird has no such flag at all, so accepting it elsewhere meant
	// silently ignoring a flag the user passed deliberately.
	"home":      {"--latest": true},
	"followers": {"--user": true},
	"following": {"--user": true},
	// mentions also takes --user, but as a handle rather than a numeric id.
	"mentions": {"--user": true, "-u": true},
	"lists":    {"--member-of": true},
	"activity": {"--types": true},
	"news": {
		"--ai-only": true, "--for-you": true, "--news-only": true,
		"--sports": true, "--entertainment": true, "--trending-only": true,
	},
}

// flagsTakingValue consume the following argument, which must not then be
// mistaken for a flag in its own right.
var flagsTakingValue = map[string]bool{
	"-n": true, "--count": true, "--limit": true, "--user": true, "-u": true,
	"--types":     true,
	"--min-likes": true, "--min-retweets": true, "--min-views": true, "--since": true,
}

// commandUnsupportedFlags narrows nativeSupportedFlags for commands that accept
// less than the common set.
//
// bird's `whoami` declares no options at all, so `bird whoami --json` is a
// usage error. Serving it natively would turn that error into human-readable
// output — a silent divergence rather than a fallback. Routing it to bird
// reproduces bird's error, which is the behavior callers already handle.
var commandUnsupportedFlags = map[string]map[string]bool{
	"whoami": {"--json": true, "-n": true, "--count": true, "--limit": true},
	// bird's listing commands page with a cursor; --latest is a timeline flag.
	// bird's `about` takes only --json; count and latest are meaningless here.
	"about": {"-n": true, "--count": true, "--limit": true},
	// `check` reads no timeline and bird gives it no options at all.
	"check":     {"--json": true, "-n": true, "--count": true, "--limit": true},
	"query-ids": {"-n": true, "--count": true, "--limit": true},
	// The write commands take no output or paging flags. --json in particular
	// is not one bird offers here, and answering it would be a divergence.
	"tweet":      writeCommandFlags,
	"reply":      writeCommandFlags,
	"follow":     writeCommandFlags,
	"unfollow":   writeCommandFlags,
	"unbookmark": writeCommandFlags,
}

// writeCommandFlags are rejected by every mutating command, so anything beyond
// the output switches falls back to bird. Media upload is deliberately in this
// set: birdy has no upload path, and --media must not be silently dropped from
// a post.
var writeCommandFlags = map[string]bool{
	"--json": true, "-n": true, "--count": true, "--limit": true, "--latest": true,
}

// nativeAcceptsFlags reports whether every flag in args is one the native path
// implements for this command.
func nativeAcceptsFlags(command string, args []string) bool {
	unsupported := commandUnsupportedFlags[command]
	extra := commandExtraFlags[command]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name := arg
		if eq := strings.IndexByte(arg, '='); eq > 0 {
			name = arg[:eq]
		}
		if !nativeSupportedFlags[name] && !extra[name] && !tweetFlagAccepted(command, name) {
			return false
		}
		if unsupported[name] {
			return false
		}
		if flagsTakingValue[name] && !strings.Contains(arg, "=") {
			i++ // skip the value
		}
	}
	return true
}

// runNative executes a command against X directly. The account has already been
// selected by the caller, so no rotation happens here.
func runNative(ctx context.Context, account *store.Account, command string, args []string, out io.Writer) error {
	handler, ok := nativeCommands[command]
	if !ok {
		return fmt.Errorf("no native implementation for %q", command)
	}

	client, err := xapi.NewClient(xapi.Credentials{
		AuthToken: account.AuthToken,
		CT0:       account.CT0,
	})
	if err != nil {
		return err
	}

	// --vpn routes this invocation through the configured SOCKS5 exit.
	//
	// This has to happen here rather than in passthrough.go, where the bird-era
	// setup lives: that code runs after the native dispatch has already
	// returned, so once every command became native, --vpn silently stopped
	// doing anything. A routing flag that is a no-op is worse than one that
	// errors — the caller believes their egress IP changed when it did not.
	if vpnFlag || vpnServerFlag != "" {
		dial, err := vpnDialer(os.Stderr)
		if err != nil {
			return err
		}
		client.SetDialContext(dial)
	}

	parsed := parseNativeArgs(args)
	if parsed.countErr != nil {
		return parsed.countErr
	}
	if parsed.filterErr != nil {
		return parsed.filterErr
	}
	parsed.command = command
	parsed.authToken = account.AuthToken
	parsed.ct0 = account.CT0
	if n, ok := defaultCounts[command]; ok && !parsed.countSet {
		parsed.count = n
	}
	return handler(ctx, client, parsed, out)
}

// recordNativeUsage mirrors the bookkeeping the bird path performs after a run,
// so rotation state stays consistent whichever engine served the command.
// Failures here are reported but do not mask the command's own result.
func recordNativeUsage(errOut io.Writer, st *store.Store, rs *state.State, account *store.Account, rateLimited bool) {
	if err := st.RecordUsage(account.Name); err != nil {
		fmt.Fprintf(errOut, "[birdy] warning: recording account usage: %v\n", err)
	}
	if rateLimited {
		if err := st.RecordRateLimit(account.Name); err != nil {
			fmt.Fprintf(errOut, "[birdy] warning: recording rate limit: %v\n", err)
		}
	}
	if err := st.Save(); err != nil {
		fmt.Fprintf(errOut, "[birdy] warning: saving account store: %v\n", err)
	}
	if rs != nil {
		rs.LastUsedName = account.Name
		if err := rs.Save(); err != nil {
			fmt.Fprintf(errOut, "[birdy] warning: saving rotation state: %v\n", err)
		}
	}
}

// --- Command implementations -------------------------------------------------

func nativeRead(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	id, err := resolveTweetID(args.positional)
	if err != nil {
		return err
	}
	found, err := c.Tweet(ctx, id)
	if err != nil {
		return err
	}
	if args.jsonFull {
		return writeNativeJSON(out, found.Full())
	}
	if args.json {
		return writeNativeJSON(out, found)
	}
	// A single read shows engagement counts; list views do not.
	return renderTweet(out, *found, args, true)
}

func nativeThread(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	id, err := resolveTweetID(args.positional)
	if err != nil {
		return err
	}
	tweets, err := c.Conversation(ctx, id)
	if err != nil {
		return err
	}
	return renderTweets(out, threadView(tweets, id), args)
}

func nativeReplies(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	id, err := resolveTweetID(args.positional)
	if err != nil {
		return err
	}
	tweets, err := c.Replies(ctx, id)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

func nativeSearch(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("search: missing query")
	}
	tweets, err := c.Search(ctx, args.positional, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

func nativeHome(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	tweets, err := c.Home(ctx, args.count, args.latest)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

// nativeUserTweets is the one listing command whose JSON shape depends on -n.
//
// bird wraps the array in {tweets, nextCursor} whenever the count exceeds one
// page (commands/user-tweets.js:84), rejects a count above its 10-page safety
// cap before making any request, and prints a resume hint to stderr. All three
// are reachable from a plain `-n`, with no paging flag involved.
func nativeUserTweets(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("user-tweets: missing username")
	}
	// Validated before the network call, exactly where bird validates it.
	if args.count > userTweetsMaxCount {
		return fmt.Errorf("Invalid --count. Max %d tweets per run (safety cap: %d pages). Use --cursor to continue.",
			userTweetsMaxCount, userTweetsMaxCount/userTweetsPageSize)
	}

	tweets, nextCursor, err := c.UserTweets(ctx, args.positional, args.count)
	if err != nil {
		return err
	}
	// Filters narrow what was fetched; they do not fetch more. The cursor still
	// points past the last fetched page, so a resume stays exact.
	tweets = filterTweets(tweets, args)

	if args.json && args.count > userTweetsPageSize {
		if tweets == nil {
			tweets = []xapi.Tweet{}
		}
		var cursor *string
		if nextCursor != "" {
			cursor = &nextCursor
		}
		if args.jsonFull {
			return writeNativeJSON(out, fullTweetsEnvelope{Tweets: xapi.FullTweets(tweets), NextCursor: cursor})
		}
		return writeNativeJSON(out, tweetsEnvelope{Tweets: tweets, NextCursor: cursor})
	}

	if err := renderTweets(out, tweets, args); err != nil {
		return err
	}
	if !args.json && nextCursor != "" {
		fmt.Fprintf(nativeStderr, "%s More tweets available. Use --cursor %q to continue.\n",
			status("info", "ℹ️", "Info:", args), nextCursor)
	}
	return nil
}

// bird's user-tweets page budget, and the count ceiling it implies.
const (
	userTweetsPageSize = 20
	userTweetsMaxCount = 200
)

// tweetsEnvelope is bird's paginated JSON shape. Field order matters: bird
// emits tweets first and nextCursor second, and nextCursor is JSON null rather
// than absent when the timeline ran out. A map[string]any would sort the keys
// and emit nextCursor first.
type tweetsEnvelope struct {
	Tweets     []xapi.Tweet `json:"tweets"`
	NextCursor *string      `json:"nextCursor"`
}

// fullTweetsEnvelope is the same envelope under --json-full.
type fullTweetsEnvelope struct {
	Tweets     []xapi.FullTweet `json:"tweets"`
	NextCursor *string          `json:"nextCursor"`
}

// nativeStderr is where the native commands write bird's stderr-only notes.
// Indirected so tests can capture them without touching the process's stderr.
var nativeStderr io.Writer = os.Stderr

func nativeBookmarks(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	tweets, err := c.Bookmarks(ctx, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

func nativeListTimeline(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("list-timeline: missing list id")
	}
	tweets, err := c.ListTimeline(ctx, args.positional, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

// nativeWhoami reports which account the selected credentials belong to.
//
// birdy always hands bird the credentials through AUTH_TOKEN/CT0 (see
// runner.buildEnv), so bird's credential source is invariably "env AUTH_TOKEN".
// Reproducing that string keeps the output identical rather than inventing a
// birdy-specific label that would break anything parsing this.
//
// The engine line says "graphql" for the same reason. It is bird's name for
// cookie-session auth as opposed to API keys, not a claim about which endpoint
// served the request — this lookup is v1.1 REST in both engines.
func nativeWhoami(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	viewer, err := c.CurrentUser(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "%s @%s (%s)\n", label("user", "🙋", "User:", args), viewer.Username, viewer.Name)
	fmt.Fprintf(out, "%s %s\n", label("user_id", "🪪", "User ID:", args), viewer.ID)
	fmt.Fprintf(out, "%s graphql\n", label("engine", "⚙️", "Engine:", args))
	fmt.Fprintf(out, "%s %s\n", label("credentials", "🔑", "Credentials:", args), birdCredentialSource)
	return nil
}

// birdCredentialSource is what bird prints for credentials supplied through the
// environment, which is the only way birdy supplies them.
const birdCredentialSource = "env AUTH_TOKEN"

// nativeAbout prints X's "About this account" panel for a handle.
//
// Every line after the header is conditional: bird omits a field X did not
// report rather than printing an empty or default value, and the two booleans
// are tri-state for that reason.
func nativeAbout(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("about: missing username")
	}
	handle := xapi.NormalizeHandle(args.positional)
	if handle == "" {
		return fmt.Errorf("invalid username: %s", args.positional)
	}

	profile, err := c.AboutAccount(ctx, handle)
	if err != nil {
		return err
	}
	if args.json {
		return writeNativeJSON(out, profile)
	}

	fmt.Fprintf(out, "%s Account information for @%s:\n", status("info", "ℹ️", "Info:", args), handle)
	if profile.AccountBasedIn != "" {
		fmt.Fprintf(out, "  Account based in: %s\n", profile.AccountBasedIn)
	}
	if profile.CreatedCountryAccurate != nil {
		fmt.Fprintf(out, "  Creation country accurate: %s\n", yesNo(*profile.CreatedCountryAccurate))
	}
	if profile.LocationAccurate != nil {
		fmt.Fprintf(out, "  Location accurate: %s\n", yesNo(*profile.LocationAccurate))
	}
	if profile.Source != "" {
		fmt.Fprintf(out, "%s %s\n", label("source", "📍", "Source:", args), profile.Source)
	}
	if profile.LearnMoreURL != "" {
		fmt.Fprintf(out, "  Learn more: %s\n", profile.LearnMoreURL)
	}
	return nil
}

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

// nativeLikes reads the authenticated account's likes. bird's `likes` takes no
// handle, so neither does this; a positional argument would be a divergence.
func nativeLikes(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	tweets, err := c.ViewerLikes(ctx, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

// resolveTweetID accepts a status URL or a bare id.
func resolveTweetID(ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("missing tweet id or url")
	}
	return tweet.ExtractTweetID(ref)
}

// --- Rendering ---------------------------------------------------------------
//
// Output mirrors bird's, because the birdy skill and TUI drive these commands
// and read their human-readable output rather than --json.

const listSeparator = "──────────────────────────────────────────────────" // 50

// emptyMessages mirror bird's per-command wording for a result with no tweets.
// bird prints a line here; printing nothing would look like a silent failure.
var emptyMessages = map[string]string{
	"replies":       "No replies found.",
	"thread":        "No thread tweets found.",
	"list-timeline": "No tweets found in this list.",
	"bookmarks":     "No bookmarks found.",
	"likes":         "No liked tweets found.",
	"mentions":      "No mentions found.",
	"news":          "No news items found.",
}

const defaultEmptyMessage = "No tweets found."

func emptyMessageFor(command string) string {
	if message, ok := emptyMessages[command]; ok {
		return message
	}
	return defaultEmptyMessage
}

func renderTweets(out io.Writer, tweets []xapi.Tweet, args nativeArgs) error {
	tweets = filterTweets(tweets, args)

	if args.json {
		if tweets == nil {
			tweets = []xapi.Tweet{}
		}
		if args.jsonFull {
			return writeNativeJSON(out, xapi.FullTweets(tweets))
		}
		return writeNativeJSON(out, tweets)
	}

	if len(tweets) == 0 {
		_, err := fmt.Fprintln(out, emptyMessageFor(args.command))
		return err
	}

	// bird closes every entry with a separator, including the last one.
	for _, tweet := range tweets {
		if err := renderTweet(out, tweet, args, false); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, listSeparator); err != nil {
			return err
		}
	}
	return nil
}

func renderTweet(out io.Writer, tweet xapi.Tweet, args nativeArgs, withStats bool) error {
	var b strings.Builder

	b.WriteString("\n")
	fmt.Fprintf(&b, "@%s (%s):\n", tweet.Author.Username, tweet.Author.Name)

	if tweet.Article != nil {
		// bird: text that already begins with the title is the rendered body,
		// so it prints whole. Otherwise only the title is known (a timeline
		// response gives no body) and the preview goes beneath it, indented by
		// three spaces once — a multi-line preview is printed raw.
		if strings.HasPrefix(tweet.Text, tweet.Article.Title) {
			fmt.Fprintf(&b, "%s %s\n", articleLabel(args), tweet.Text)
		} else {
			fmt.Fprintf(&b, "%s %s\n", articleLabel(args), tweet.Article.Title)
			if tweet.Article.PreviewText != "" {
				fmt.Fprintf(&b, "   %s\n", tweet.Article.PreviewText)
			}
		}
	} else {
		b.WriteString(tweet.Text)
		b.WriteString("\n")
	}

	for _, media := range tweet.Media {
		fmt.Fprintf(&b, "%s %s\n", mediaLabel(media, args), media.URL)
	}

	renderQuoted(&b, tweet.QuotedTweet, args)

	if tweet.CreatedAt != "" {
		fmt.Fprintf(&b, "%s %s\n", label("date", "📅", "Date:", args), tweet.CreatedAt)
	}
	fmt.Fprintf(&b, "%s %s\n", label("url", "🔗", "URL:", args), tweet.URL())

	if withStats {
		b.WriteString(statsLine(tweet, args))
		b.WriteString("\n")
	}

	_, err := io.WriteString(out, b.String())
	return err
}

// mediaLabel mirrors bird's icons. bird computes useEmoji as (emoji && !plain),
// so --plain and --no-emoji share the same textual labels, and an animated gif
// is neither a video nor a photo.
func mediaLabel(media xapi.Media, args nativeArgs) string {
	useEmoji := args.emoji && !args.plain

	switch media.Type {
	case "video":
		if useEmoji {
			return "🎬"
		}
		return "VIDEO:"
	case "animated_gif":
		if useEmoji {
			return "🔄"
		}
		return "GIF:"
	default:
		if useEmoji {
			return "🖼️"
		}
		return "PHOTO:"
	}
}

// articleLabel mirrors bird's articleLabel (cli/shared.js:238), which is derived
// from (emoji && !plain) exactly like the media labels — NOT from the l()/label()
// helper. That distinction is load-bearing: label() lowercases in plain mode, so
// routing this through it would print "article:" where bird prints "Article:".
func articleLabel(args nativeArgs) string {
	if args.emoji && !args.plain {
		return "📰"
	}
	return "Article:"
}

// label renders a field prefix in whichever of bird's three output modes applies.
func label(plainName, emoji, text string, args nativeArgs) string {
	if args.plain {
		return plainName + ":"
	}
	if args.emoji {
		return emoji
	}
	return text
}

// status mirrors bird's status prefixes, which bracket their plain form
// (`[info]`) where labels suffix theirs with a colon (`source:`).
func status(plainName, emoji, text string, args nativeArgs) string {
	if args.plain {
		return "[" + plainName + "]"
	}
	if args.emoji {
		return emoji
	}
	return text
}

// renderQuoted prints the quoted tweet block.
//
// bird draws a box in emoji mode and a mail-style quote otherwise, truncates
// the quoted text to 280 characters, and prints at most its first four lines.
// Those limits are bird's, and reproducing them is the whole point.
func renderQuoted(b *strings.Builder, quoted *xapi.Tweet, args nativeArgs) {
	if quoted == nil {
		return
	}

	top, mid, bot := "> ", "> ", "> "
	if args.emoji && !args.plain {
		top, mid, bot = "┌─", "│ ", "└─"
	}

	fmt.Fprintf(b, "%s QT @%s:\n", top, quoted.Author.Username)

	// A quoted article shows its title, not the tweet's shortlink. The label
	// counts against bird's 280-unit budget, so it is prepended before the cut.
	qtText := quoted.Text
	if quoted.Article != nil {
		qtText = articleLabel(args) + " " + quoted.Article.Title
	}

	text := truncateJS(qtText, 280)
	for i, line := range strings.Split(text, "\n") {
		if i >= 4 {
			break
		}
		fmt.Fprintf(b, "%s%s\n", mid, line)
	}
	for _, media := range quoted.Media {
		fmt.Fprintf(b, "%s%s %s\n", mid, mediaLabel(media, args), media.URL)
	}
	fmt.Fprintf(b, "%s https://x.com/%s/status/%s\n", bot, quoted.Author.Username, quoted.ID)
}

// threadView matches bird's `thread`: the conversation narrowed to the root's
// own conversation id, oldest first. X returns entries in ranking order, which
// is not chronological.
func threadView(tweets []xapi.Tweet, rootID string) []xapi.Tweet {
	conversationID := rootID
	for _, t := range tweets {
		if t.ID == rootID && t.ConversationID != "" {
			conversationID = t.ConversationID
			break
		}
	}

	filtered := make([]xapi.Tweet, 0, len(tweets))
	for _, t := range tweets {
		if t.ConversationID == conversationID {
			filtered = append(filtered, t)
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return parseTweetTime(filtered[i].CreatedAt).Before(parseTweetTime(filtered[j].CreatedAt))
	})
	return filtered
}

// parseTweetTime reads X's created_at format. An unparseable value sorts first,
// matching bird's treatment of a missing timestamp as zero.
func parseTweetTime(value string) time.Time {
	t, _ := xapi.ParseCreatedAt(value)
	return t
}

func statsLine(tweet xapi.Tweet, args nativeArgs) string {
	if args.plain {
		return fmt.Sprintf("likes: %d  retweets: %d  replies: %d",
			tweet.LikeCount, tweet.RetweetCount, tweet.ReplyCount)
	}
	if !args.emoji {
		return fmt.Sprintf("Likes %d  Retweets %d  Replies %d",
			tweet.LikeCount, tweet.RetweetCount, tweet.ReplyCount)
	}
	return fmt.Sprintf("❤️ %d  🔁 %d  💬 %d",
		tweet.LikeCount, tweet.RetweetCount, tweet.ReplyCount)
}

func writeNativeJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}
