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
// Anything not listed here still forwards to bird. The --bird flag (or
// BIRDY_USE_BIRD=1) forces the bird path even for commands that are listed,
// which is the escape hatch when a native implementation misbehaves.
var nativeCommands = map[string]func(context.Context, *xapi.Client, nativeArgs, io.Writer) error{
	"read":          nativeRead,
	"thread":        nativeThread,
	"replies":       nativeReplies,
	"search":        nativeSearch,
	"home":          nativeHome,
	"user-tweets":   nativeUserTweets,
	"likes":         nativeLikes,
	"bookmarks":     nativeBookmarks,
	"list-timeline": nativeListTimeline,
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
	count      int
	json       bool
	// plain and emoji mirror bird's output switches so rendering matches.
	plain bool
	emoji bool
	// latest selects the chronological home timeline.
	latest bool
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
		case arg == "--plain":
			parsed.plain = true
			parsed.emoji = false
		case arg == "--no-emoji":
			parsed.emoji = false
		case arg == "--no-color":
			// Accepted for compatibility; native output is never colorized.
		case arg == "--latest":
			parsed.latest = true
		case arg == "-n" || arg == "--count" || arg == "--limit":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					parsed.count = n
				}
				i++
			}
		case strings.HasPrefix(arg, "--count=") || strings.HasPrefix(arg, "--limit="):
			if n, err := strconv.Atoi(arg[strings.IndexByte(arg, '=')+1:]); err == nil {
				parsed.count = n
			}
		case strings.HasPrefix(arg, "-"):
			// Ignored here; nativeAcceptsFlags rejects the command first.
		default:
			if parsed.positional == "" {
				parsed.positional = arg
			}
		}
	}
	return parsed
}

// nativeSupportedFlags are the flags the native path understands. A command
// carrying anything else is handed to bird, so a flag birdy has not implemented
// yet keeps working instead of being quietly dropped.
var nativeSupportedFlags = map[string]bool{
	"--json": true, "--plain": true, "--no-emoji": true, "--no-color": true,
	"--latest": true, "-n": true, "--count": true, "--limit": true,
}

// nativeAcceptsFlags reports whether every flag in args is one the native path
// implements.
func nativeAcceptsFlags(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name := arg
		if eq := strings.IndexByte(arg, '='); eq > 0 {
			name = arg[:eq]
		}
		if !nativeSupportedFlags[name] {
			return false
		}
		if name == "-n" || name == "--count" || name == "--limit" {
			if !strings.Contains(arg, "=") {
				i++ // skip the value
			}
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
	return handler(ctx, client, parseNativeArgs(args), out)
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

func nativeUserTweets(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("user-tweets: missing username")
	}
	tweets, err := c.UserTweets(ctx, args.positional, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

func nativeLikes(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if args.positional == "" {
		return fmt.Errorf("likes: missing username")
	}
	tweets, err := c.Likes(ctx, args.positional, args.count)
	if err != nil {
		return err
	}
	return renderTweets(out, tweets, args)
}

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

func renderTweets(out io.Writer, tweets []xapi.Tweet, args nativeArgs) error {
	if args.json {
		if tweets == nil {
			tweets = []xapi.Tweet{}
		}
		return writeNativeJSON(out, tweets)
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
	b.WriteString(tweet.Text)
	b.WriteString("\n")

	for _, media := range tweet.Media {
		fmt.Fprintf(&b, "%s %s\n", mediaLabel(media, args), media.URL)
	}

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

// mediaLabel distinguishes video from stills, matching bird's icons. In plain
// mode bird prints the uppercased media type ("PHOTO:", "VIDEO:"), not a fixed
// word, so the raw type carries through.
func mediaLabel(media xapi.Media, args nativeArgs) string {
	if args.plain {
		kind := media.Type
		if kind == "" {
			kind = "media"
		}
		return strings.ToUpper(kind) + ":"
	}
	if media.IsVideo() {
		return "🎬"
	}
	if !args.emoji {
		return "Image:"
	}
	return "🖼️"
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
	t, err := time.Parse(time.RubyDate, value)
	if err != nil {
		return time.Time{}
	}
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
