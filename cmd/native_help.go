package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Native help for the X commands.
//
// The X subcommands run with DisableFlagParsing, so cobra never sees --help
// on them: the token used to travel with the rest of the argv into the bird
// subprocess, which printed bird's Commander banner (`-V`, `--json-full`,
// `bird tweet ...`). With bird gone that path prints `env: node: No such file`
// or `bird CLI not found`, and an agent told to "run `birdy <command> --help`,
// do not invent flags" gets nothing to work from.
//
// The flag list is not written per command. flagDocs is one ordered table of
// every flag the native parser knows, and nativeHelpFlags filters it through
// nativeAcceptsFlags — the exact predicate runPassthrough uses to decide
// whether the native engine serves an invocation. A flag therefore appears in
// help iff the engine accepts it; native_help_test.go pins that both ways.

// flagSpec documents one flag (or one alias group) for help output.
type flagSpec struct {
	// names are the spellings the parser accepts, canonical first.
	names []string
	// arg names the value for flags that take one; "" for booleans.
	arg string
	doc string
	// only restricts the entry to these commands, for a flag whose meaning
	// differs per command (--user is a numeric id on followers/following and
	// a handle on mentions). Empty means every command that accepts it.
	only []string
}

// flagDocs is the single table help is rendered from, in display order.
var flagDocs = []flagSpec{
	{names: []string{"-n", "--count", "--limit"}, arg: "N",
		doc: "number of items to return"},
	{names: []string{"--json"},
		doc: "emit JSON instead of the human-readable view"},
	{names: []string{"--plain"},
		doc: "plain output: no emoji, no color, stable for scripts"},
	{names: []string{"--no-emoji"},
		doc: "human-readable output without emoji"},
	{names: []string{"--no-color"},
		doc: "accepted for compatibility; native output is never colorized"},
	{names: []string{"--latest"},
		doc: "chronological home timeline instead of the algorithmic one"},
	{names: []string{"--user"}, arg: "ID", only: []string{"followers", "following"},
		doc: "numeric account id to list (default: the authenticated account)"},
	{names: []string{"--user", "-u"}, arg: "HANDLE", only: []string{"mentions"},
		doc: "handle whose mentions to search (default: the authenticated account)"},
	{names: []string{"--member-of"},
		doc: "lists you are a member of instead of lists you own"},
	{names: []string{"--types"}, arg: "likes,reposts,quotes",
		doc: "activity sections to fetch, comma-separated (default: all three)"},
	{names: []string{"--ai-only"},
		doc: "keep only AI-related items"},
	{names: []string{"--for-you"},
		doc: "the For You tab"},
	{names: []string{"--news-only"},
		doc: "the News tab"},
	{names: []string{"--sports"},
		doc: "the Sports tab"},
	{names: []string{"--entertainment"},
		doc: "the Entertainment tab"},
	{names: []string{"--trending-only"},
		doc: "the Trending tab"},
	{names: []string{"--json-full"},
		doc: "the --json object plus url, createdAtIso, viewCount/quoteCount/bookmarkCount, lang, isRepost/isReply/isQuote"},
	// Post-fetch filters: applied after the page is fetched, before rendering,
	// to text and JSON alike. Unset means no filtering.
	{names: []string{"--min-likes"}, arg: "N",
		doc: "drop tweets with fewer than N likes"},
	{names: []string{"--min-retweets"}, arg: "N",
		doc: "drop tweets with fewer than N reposts"},
	{names: []string{"--min-views"}, arg: "N",
		doc: "drop tweets with fewer than N views (tweets X reports no view count for are dropped)"},
	{names: []string{"--since"}, arg: "24h|7d|2w|RFC3339|YYYY-MM-DD",
		doc: "keep only tweets created at or after this time (durations are relative to now, UTC); tweets whose date cannot be parsed are dropped"},
}

// globalFlagNames are birdy's own flags, listed under every X command. Their
// usage strings come from rootCmd so the two never drift.
var globalFlagNames = []string{"account", "strategy", "verbose", "vpn", "vpn-server", "bird"}

// nativeCommandHelp is the per-command text: what the positionals are, and
// one or two invocations that work as typed.
type nativeCommandHelp struct {
	usage    string
	examples []string
	notes    string
}

var nativeCommandHelps = map[string]nativeCommandHelp{
	"read": {usage: "read <tweet-id|url>", examples: []string{
		`birdy read 1234567890123456789`,
		`birdy read https://x.com/OpenAI/status/1234567890123456789 --json`,
	}},
	"thread": {usage: "thread <tweet-id|url>", examples: []string{
		`birdy thread 1234567890123456789 --plain`,
	}},
	"replies": {usage: "replies <tweet-id|url>", examples: []string{
		`birdy replies 1234567890123456789 --json`,
	}},
	"search": {usage: "search <query>", examples: []string{
		`birdy search "golang" -n 20 --plain`,
		`birdy search "from:OpenAI since:2026-09-01" --json`,
	}, notes: "The query is X advanced-search syntax (from:, since:, min_faves:, ...)."},
	"home": {usage: "home", examples: []string{
		`birdy home -n 30 --latest`,
	}},
	"user-tweets": {usage: "user-tweets <@handle>", examples: []string{
		`birdy user-tweets @OpenAI -n 50 --json`,
	}},
	"bookmarks": {usage: "bookmarks", examples: []string{
		`birdy bookmarks -n 50 --json`,
	}},
	"list-timeline": {usage: "list-timeline <list-id>", examples: []string{
		`birdy list-timeline 1234567890123456789 -n 40 --plain`,
	}},
	"whoami": {usage: "whoami", examples: []string{
		`birdy --account main whoami`,
	}, notes: "Reports the account behind the selected credentials."},
	"about": {usage: "about <@handle>", examples: []string{
		`birdy about @OpenAI --json`,
	}},
	"likes": {usage: "likes", examples: []string{
		`birdy likes -n 20`,
	}, notes: "Lists the authenticated account's likes; there is no handle argument."},
	"followers": {usage: "followers [--user ID]", examples: []string{
		`birdy followers --user 4398626122 -n 100 --json`,
	}},
	"following": {usage: "following [--user ID]", examples: []string{
		`birdy following --user 4398626122 -n 100 --json`,
	}},
	"tweet": {usage: "tweet <text>", examples: []string{
		`birdy tweet "hello from birdy"`,
	}, notes: "Blocked under BIRDY_READ_ONLY=1 and on read-only accounts. Never retried."},
	"reply": {usage: "reply <tweet-id|url> <text>", examples: []string{
		`birdy reply 1234567890123456789 "good point"`,
	}, notes: "Blocked under BIRDY_READ_ONLY=1 and on read-only accounts. Never retried."},
	"follow": {usage: "follow <@handle|user-id>", examples: []string{
		`birdy follow @OpenAI`,
	}, notes: "Blocked under BIRDY_READ_ONLY=1 and on read-only accounts."},
	"unfollow": {usage: "unfollow <@handle|user-id>", examples: []string{
		`birdy unfollow @OpenAI`,
	}, notes: "Blocked under BIRDY_READ_ONLY=1 and on read-only accounts."},
	"unbookmark": {usage: "unbookmark <tweet-id|url>...", examples: []string{
		`birdy unbookmark 1234567890123456789 9876543210987654321`,
	}, notes: "Blocked under BIRDY_READ_ONLY=1 and on read-only accounts."},
	"check": {usage: "check", examples: []string{
		`birdy --account main check`,
	}, notes: "Inspects the selected account's stored credentials; makes no request."},
	"mentions": {usage: "mentions [--user @handle]", examples: []string{
		`birdy mentions -n 20 --plain`,
	}},
	"query-ids": {usage: "query-ids", examples: []string{
		`birdy query-ids --json`,
	}, notes: "Describes birdy's GraphQL query-id resolver (see COMPATIBILITY.md)."},
	"lists": {usage: "lists [--member-of]", examples: []string{
		`birdy lists --json`,
		`birdy lists --member-of`,
	}},
	"activity": {usage: "activity <tweet-id|url> [--types likes,reposts,quotes]", examples: []string{
		`birdy activity 1234567890123456789 --types quotes --json`,
	}},
	"news": {usage: "news [--for-you|--news-only|--sports|--entertainment|--trending-only]", examples: []string{
		`birdy news --ai-only -n 20 --plain`,
	}},
}

// helpRequested reports whether argv asks for help before the `--`
// separator. It is checked before anything else so --help is answered
// in-process and never reaches a subprocess.
func helpRequested(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--help", "-h":
			return true
		}
	}
	return false
}

// acceptsFlag asks the native parser's own predicate whether command takes
// name. The trailing token stands in for a value: consumed by a value flag,
// harmlessly positional for a boolean.
func acceptsFlag(command, name string) bool {
	return nativeAcceptsFlags(command, []string{name, "1"})
}

// nativeHelpFlags returns the flagDocs entries command accepts, in table
// order. An entry is included when the parser accepts any of its spellings,
// and every spelling shown is one the parser accepts.
func nativeHelpFlags(command string) []flagSpec {
	var out []flagSpec
	for _, spec := range flagDocs {
		if len(spec.only) > 0 && !containsString(spec.only, command) {
			continue
		}
		var names []string
		for _, name := range spec.names {
			if acceptsFlag(command, name) {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		shown := spec
		shown.names = names
		out = append(out, shown)
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// nativeHelpAvailable reports whether renderNativeHelp has text for command.
func nativeHelpAvailable(command string) bool {
	_, ok := nativeCommandHelps[command]
	return ok
}

// renderNativeHelp writes birdy-authored help for one X command.
func renderNativeHelp(w io.Writer, cmd *cobra.Command) {
	name := cmd.Name()
	help, native := nativeCommandHelps[name]

	fmt.Fprintln(w, cmd.Short)
	fmt.Fprintln(w)
	if !native {
		// Registered so the argv is routed, but no Go implementation.
		fmt.Fprintf(w, "Usage:\n  birdy --bird %s [bird flags]\n\n", name)
		fmt.Fprintf(w, "%s is not served by birdy's native engine. It runs only through\n"+
			"the bird CLI, which birdy no longer ships: install it separately\n"+
			"(npm install -g @steipete/bird) and pass --bird.\n", name)
		return
	}

	fmt.Fprintf(w, "Usage:\n  birdy %s [flags]\n\n", help.usage)
	if help.notes != "" {
		fmt.Fprintln(w, help.notes)
		fmt.Fprintln(w)
	}

	if flags := nativeHelpFlags(name); len(flags) > 0 {
		fmt.Fprintln(w, "Flags:")
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		for _, spec := range flags {
			doc := spec.doc
			if spec.names[0] == "-n" {
				doc = fmt.Sprintf("%s (default %d)", doc, defaultCountFor(name))
			}
			fmt.Fprintf(tw, "  %s\t%s\n", formatFlagNames(spec), doc)
		}
		fmt.Fprintf(tw, "  -h, --help\tshow this help\n")
		_ = tw.Flush()
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "Global Flags (before the command name):")
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	pf := rootCmd.PersistentFlags()
	for _, gname := range globalFlagNames {
		f := pf.Lookup(gname)
		if f == nil {
			continue
		}
		label := "    --" + f.Name
		if f.Shorthand != "" {
			label = "-" + f.Shorthand + ", --" + f.Name
		}
		if f.Value.Type() != "bool" {
			label += " " + strings.ToUpper(f.Name)
		}
		fmt.Fprintf(tw, "  %s\t%s\n", label, f.Usage)
	}
	_ = tw.Flush()
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Examples:")
	for _, ex := range help.examples {
		fmt.Fprintf(w, "  %s\n", ex)
	}
}

// defaultCountFor mirrors runNative's default selection.
func defaultCountFor(command string) int {
	if n, ok := defaultCounts[command]; ok {
		return n
	}
	return parseNativeArgs(nil).count
}

func formatFlagNames(spec flagSpec) string {
	names := spec.names
	// Show the short spelling first, then long ones, the way cobra does.
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.SliceStable(sorted, func(i, j int) bool {
		return !strings.HasPrefix(sorted[i], "--") && strings.HasPrefix(sorted[j], "--")
	})
	label := strings.Join(sorted, ", ")
	if strings.HasPrefix(sorted[0], "--") {
		label = "    " + label // align with entries that carry a shorthand
	}
	if spec.arg != "" {
		label += " " + spec.arg
	}
	return label
}

// nativeHelpFunc is installed as the cobra HelpFunc on every X command, so
// `birdy help <cmd>` and `birdy <cmd> --help` render the same text.
func nativeHelpFunc(cmd *cobra.Command, _ []string) {
	renderNativeHelp(cmd.OutOrStdout(), cmd)
}
