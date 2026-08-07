package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

// checkSeparator is 40 dashes, not the 50 the tweet list uses. bird draws them
// with different widths and the difference is visible.
const checkSeparator = "────────────────────────────────────────"

// credentialPreviewLen is how much of a token bird echoes before the ellipsis.
const credentialPreviewLen = 10

// nativeCheck reports credential availability.
//
// It makes no request: bird's `check` only inspects the credentials it
// resolved. Under birdy the account store has already supplied and validated
// both cookies by the time a command runs, so the interesting output is which
// account was selected, not whether anything was found.
func nativeCheck(_ context.Context, _ *xapi.Client, args nativeArgs, out io.Writer) error {
	fmt.Fprintf(out, "%s Credential check\n", status("info", "ℹ️", "Info:", args))
	fmt.Fprintln(out, checkSeparator)
	fmt.Fprintf(out, "%s auth_token: %s\n", status("ok", "✅", "OK:", args), preview(args.authToken))
	fmt.Fprintf(out, "%s ct0: %s\n", status("ok", "✅", "OK:", args), preview(args.ct0))
	fmt.Fprintf(out, "%s %s\n", label("source", "📍", "Source:", args), birdCredentialSource)
	fmt.Fprintf(out, "\n%s Ready to tweet!\n", status("ok", "✅", "OK:", args))
	return nil
}

// preview truncates a secret the way bird does. The count is bytes because the
// values are hex-ish ASCII; a multi-byte token is not a shape X issues.
func preview(secret string) string {
	if len(secret) <= credentialPreviewLen {
		return secret + "..."
	}
	return secret[:credentialPreviewLen] + "..."
}

// nativeMentions finds tweets mentioning a handle, defaulting to the
// authenticated account.
//
// bird implements this as a search for "@handle" rather than a notifications
// timeline, so this does too — a real mentions timeline would return a
// different set and silently diverge.
func nativeMentions(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	handle := args.user
	if handle == "" {
		viewer, err := c.CurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("could not determine current user (%w). Use --user <handle>", err)
		}
		handle = viewer.Username
	}

	normalized, ok := xapi.ValidHandle(handle)
	if !ok {
		return fmt.Errorf("Invalid --user handle. Expected something like @steipete " +
			"(letters, digits, underscore; max 15)")
	}

	tweets, err := c.Search(ctx, "@"+normalized, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

// nativeQueryIDs reports the persisted-query hashes birdy would use.
//
// This is the one command that deliberately does not match bird's output.
// bird's `query-ids` describes bird's own cache — its path, its feature
// overrides, its refresh state — and birdy has a separate resolver with a
// different cache and a different override mechanism (BIRDY_<OP>_QUERY_ID).
// Printing bird's shape would describe a file birdy never reads. The
// divergence is recorded in COMPATIBILITY.md.
func nativeQueryIDs(_ context.Context, _ *xapi.Client, args nativeArgs, out io.Writer) error {
	snapshot := xapi.QueryIDSnapshot()

	if args.json {
		return writeNativeJSON(out, snapshot)
	}

	fmt.Fprintf(out, "%s GraphQL query IDs\n", status("info", "ℹ️", "Info:", args))
	fmt.Fprintln(out, checkSeparator)
	for _, entry := range snapshot.Operations {
		fmt.Fprintf(out, "%s: %s\n", entry.Operation, strings.Join(entry.IDs, ", "))
		if entry.Source != "" {
			fmt.Fprintf(out, "  source: %s\n", entry.Source)
		}
	}
	fmt.Fprintf(out, "\ncache: %s\n", snapshot.CachePath)
	return nil
}

// nativeLists prints the authenticated account's lists.
//
// bird defaults this to 100, not the usual 20, and --member-of switches from
// owned lists to memberships with its own empty-result wording.
func nativeLists(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	fetch := c.OwnedLists
	empty := "You do not own any lists."
	if args.memberOf {
		fetch = c.ListMemberships
		empty = "You are not a member of any lists."
	}

	lists, err := fetch(ctx, args.count)
	if err != nil {
		return err
	}

	if args.json {
		if lists == nil {
			lists = []xapi.List{}
		}
		return writeNativeJSON(out, lists)
	}
	if len(lists) == 0 {
		_, err := fmt.Fprintln(out, empty)
		return err
	}

	for _, list := range lists {
		visibility := "[public]"
		if list.IsPrivate {
			visibility = "[private]"
		}
		fmt.Fprintf(out, "%s %s\n", list.Name, visibility)
		if description := deref(list.Description); description != "" {
			fmt.Fprintf(out, "  %s\n", truncateJS(description, 100))
		}
		// bird prints "0 members" when X omits the count, unlike the user
		// listing where an absent count suppresses the line entirely.
		members := 0
		if list.MemberCount != nil {
			members = *list.MemberCount
		}
		fmt.Fprintf(out, "  %s %s members\n", status("info", "ℹ️", "Info:", args), groupThousands(members))
		if list.Owner != nil {
			fmt.Fprintf(out, "  Owner: @%s\n", list.Owner.Username)
		}
		fmt.Fprintf(out, "  %s\n", list.URL())
		fmt.Fprintln(out, listSeparator)
	}
	return nil
}

// activityAliases mirror bird's --types spellings.
var activityAliases = map[string]string{
	"like": "likes", "likes": "likes", "liker": "likes", "likers": "likes",
	"repost": "reposts", "reposts": "reposts", "retweet": "reposts", "retweets": "reposts",
	"quote": "quotes", "quotes": "quotes",
}

// activityOrder is the order bird prints sections in, independent of the order
// --types listed them.
var activityOrder = []string{"likes", "reposts", "quotes"}

func parseActivityTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return activityOrder, nil
	}
	selected := map[string]bool{}
	for _, token := range strings.Split(raw, ",") {
		normalized, ok := activityAliases[strings.ToLower(strings.TrimSpace(token))]
		if !ok {
			return nil, fmt.Errorf("Invalid --types value %q. Use likes,reposts,quotes.", token)
		}
		selected[normalized] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("At least one activity type is required.")
	}
	var types []string
	for _, name := range activityOrder {
		if selected[name] {
			types = append(types, name)
		}
	}
	return types, nil
}

// nativeActivity reports who liked, reposted and quoted a tweet.
func nativeActivity(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	id, err := resolveTweetID(args.positional)
	if err != nil {
		return err
	}
	types, err := parseActivityTypes(args.types)
	if err != nil {
		return err
	}

	report := activityReport{TweetID: id}
	for _, kind := range types {
		switch kind {
		case "likes":
			page, err := c.Favoriters(ctx, id, args.count)
			if err != nil {
				return fmt.Errorf("Failed to fetch likes: %w", err)
			}
			report.Likes.Users = page.Users
			report.Likes.NextCursor = optionalString(page.NextCursor)
		case "reposts":
			page, err := c.Retweeters(ctx, id, args.count)
			if err != nil {
				return fmt.Errorf("Failed to fetch reposts: %w", err)
			}
			report.Reposts.Users = page.Users
			report.Reposts.NextCursor = optionalString(page.NextCursor)
		case "quotes":
			page, err := c.QuoteTweets(ctx, id, args.count)
			if err != nil {
				return fmt.Errorf("Failed to fetch quotes: %w", err)
			}
			report.Quotes.Tweets = page.Tweets
			report.Quotes.NextCursor = optionalString(page.NextCursor)
		}
	}

	if args.json {
		report.normalize()
		return writeNativeJSON(out, report)
	}

	for _, kind := range types {
		switch kind {
		case "likes":
			renderActivityUsers(out, "Likes", report.Likes.Users, args)
		case "reposts":
			renderActivityUsers(out, "Reposts", report.Reposts.Users, args)
		case "quotes":
			fmt.Fprintf(out, "\nQuotes (%d)\n", len(report.Quotes.Tweets))
			if len(report.Quotes.Tweets) == 0 {
				fmt.Fprintln(out, "No quote tweets found.")
				continue
			}
			if err := renderTweets(out, report.Quotes.Tweets, args); err != nil {
				return err
			}
		}
	}
	return nil
}

// activityReport is bird's --json shape for `activity`.
type activityReport struct {
	TweetID string `json:"tweetId"`
	Likes   struct {
		Users      []xapi.ListedUser `json:"users"`
		NextCursor *string           `json:"nextCursor"`
	} `json:"likes"`
	Reposts struct {
		Users      []xapi.ListedUser `json:"users"`
		NextCursor *string           `json:"nextCursor"`
	} `json:"reposts"`
	Quotes struct {
		Tweets     []xapi.Tweet `json:"tweets"`
		NextCursor *string      `json:"nextCursor"`
	} `json:"quotes"`
}

// optionalString maps an absent cursor to JSON null, which is what bird's
// `page.nextCursor ?? null` emits.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// normalize replaces nil slices with empty ones so the JSON carries [] the way
// bird's does, rather than null.
func (r *activityReport) normalize() {
	if r.Likes.Users == nil {
		r.Likes.Users = []xapi.ListedUser{}
	}
	if r.Reposts.Users == nil {
		r.Reposts.Users = []xapi.ListedUser{}
	}
	if r.Quotes.Tweets == nil {
		r.Quotes.Tweets = []xapi.Tweet{}
	}
}

// renderActivityUsers is bird's activity-specific user block: a counted header,
// a profile URL per user, and no separator — deliberately different from the
// followers/following listing.
func renderActivityUsers(out io.Writer, heading string, users []xapi.ListedUser, args nativeArgs) {
	fmt.Fprintf(out, "\n%s (%d)\n", heading, len(users))
	if len(users) == 0 {
		fmt.Fprintln(out, "No users found.")
		return
	}
	for _, user := range users {
		fmt.Fprintf(out, "@%s (%s)\n", user.Username, user.Name)
		if description := deref(user.Description); description != "" {
			fmt.Fprintf(out, "  %s\n", truncateJS(description, 100))
		}
		if user.FollowersCount != nil {
			fmt.Fprintf(out, "  %s %s followers\n", status("info", "ℹ️", "Info:", args), groupThousands(*user.FollowersCount))
		}
		fmt.Fprintf(out, "  %s https://x.com/%s\n", label("url", "🔗", "URL:", args), user.Username)
	}
}
