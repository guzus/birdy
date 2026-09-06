package cmd

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// helpOutput renders help for one registered X command through the cobra
// HelpFunc, the way both `birdy <cmd> --help` and `birdy help <cmd>` do.
func helpOutput(t *testing.T, name string) string {
	t.Helper()
	found, _, err := rootCmd.Find([]string{name})
	if err != nil || found == rootCmd {
		t.Fatalf("%q is not a registered command", name)
	}
	var buf bytes.Buffer
	renderNativeHelp(&buf, found)
	return buf.String()
}

// helpFlagTokens extracts every flag spelling shown in the per-command Flags
// block (not the global block), so a test can compare sets exactly instead
// of substring-matching, where "--json" would match "--json-full".
var flagTokenRe = regexp.MustCompile(`(?:^|[\s,])(-{1,2}[a-zA-Z][a-zA-Z0-9-]*)`)

func helpFlagTokens(help string) map[string]bool {
	tokens := map[string]bool{}
	inFlags := false
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.HasPrefix(line, "Flags:"):
			inFlags = true
			continue
		case strings.HasPrefix(line, "Global Flags"):
			inFlags = false
		}
		if !inFlags {
			continue
		}
		// Only the label column: the doc column mentions other flags in prose.
		// tabwriter pads the two columns with at least three spaces.
		label := strings.TrimSpace(line)
		if i := strings.Index(label, "   "); i >= 0 {
			label = label[:i]
		}
		for _, m := range flagTokenRe.FindAllStringSubmatch(label, -1) {
			tokens[m[1]] = true
		}
	}
	return tokens
}

// TestNativeHelpMatchesParserFlagSet is the DRY guarantee: for every native
// command, the flags shown in help are exactly the flags nativeAcceptsFlags
// accepts. A flag added to native.go without a flagDocs entry fails here, and
// so does a documented flag the parser rejects.
func TestNativeHelpMatchesParserFlagSet(t *testing.T) {
	candidates := map[string]bool{}
	for name := range nativeSupportedFlags {
		candidates[name] = true
	}
	for _, extra := range commandExtraFlags {
		for name := range extra {
			candidates[name] = true
		}
	}
	for _, spec := range flagDocs {
		for _, name := range spec.names {
			candidates[name] = true
		}
	}

	for command := range nativeCommands {
		help := helpOutput(t, command)
		shown := helpFlagTokens(help)
		delete(shown, "-h")
		delete(shown, "--help")

		var want, missing, extra []string
		for name := range candidates {
			if acceptsFlag(command, name) {
				want = append(want, name)
				if !shown[name] {
					missing = append(missing, name)
				}
			} else if shown[name] {
				extra = append(extra, name)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s: parser accepts %v but help does not list them\n%s", command, missing, help)
		}
		if len(extra) > 0 {
			t.Errorf("%s: help lists %v but the parser rejects them\n%s", command, extra, help)
		}
		if len(want) == 0 {
			t.Errorf("%s: parser accepts no flags at all; is nativeAcceptsFlags broken?", command)
		}
	}
}

// Curated spot checks, so the derivation cannot pass vacuously.
func TestNativeHelpKnownFlags(t *testing.T) {
	cases := map[string][]string{
		"search":      {"-n, --count, --limit N", "--json", "--plain", "(default 10)"},
		"user-tweets": {"-n, --count, --limit N", "(default 20)", "--json"},
		"home":        {"--latest"},
		"mentions":    {"-u, --user HANDLE"},
		"followers":   {"--user ID"},
		"lists":       {"--member-of", "(default 100)"},
		"activity":    {"--types likes,reposts,quotes"},
		"news":        {"--ai-only", "--for-you", "--news-only", "--sports", "--entertainment", "--trending-only"},
	}
	for command, wants := range cases {
		help := helpOutput(t, command)
		for _, want := range wants {
			if !strings.Contains(help, want) {
				t.Errorf("%s help missing %q\n%s", command, want, help)
			}
		}
	}

	// whoami and the write commands reject --json; help must not advertise it.
	for _, command := range []string{"whoami", "check", "tweet", "reply", "follow", "unfollow", "unbookmark"} {
		if helpFlagTokens(helpOutput(t, command))["--json"] {
			t.Errorf("%s help lists --json, which the native parser rejects for it", command)
		}
	}
}

// Every native command has usage + at least one example, and every help entry
// names a native command (or trending, which is registered but bird-only).
func TestNativeHelpCoversEveryNativeCommand(t *testing.T) {
	for command := range nativeCommands {
		help, ok := nativeCommandHelps[command]
		if !ok {
			t.Errorf("%s: no nativeCommandHelps entry", command)
			continue
		}
		if !strings.HasPrefix(help.usage, command) {
			t.Errorf("%s: usage %q must start with the command name", command, help.usage)
		}
		if len(help.examples) == 0 {
			t.Errorf("%s: no examples", command)
		}
		for _, ex := range help.examples {
			if !strings.HasPrefix(ex, "birdy ") {
				t.Errorf("%s: example %q must invoke birdy", command, ex)
			}
		}
	}
	for command := range nativeCommandHelps {
		if !nativeSupports(command) {
			t.Errorf("%s has help text but no native implementation", command)
		}
	}
}

// The text an agent reads must be birdy's, never bird's Commander banner.
func TestNativeHelpHasNoBirdBanner(t *testing.T) {
	for command := range nativeCommands {
		help := helpOutput(t, command)
		for _, bad := range []string{"fast X CLI for tweeting", "-V,", "--version", "bird tweet", "bird " + command} {
			if strings.Contains(help, bad) {
				t.Errorf("%s help carries bird text %q\n%s", command, bad, help)
			}
		}
		for _, want := range []string{"Usage:\n  birdy " + command, "Global Flags", "-a, --account", "Examples:\n  birdy "} {
			if !strings.Contains(help, want) {
				t.Errorf("%s help missing %q\n%s", command, want, help)
			}
		}
	}
}

// Registered-but-not-native commands must still answer --help in-process and
// say plainly that they need bird.
func TestBirdOnlyCommandHelp(t *testing.T) {
	help := helpOutput(t, "trending")
	for _, want := range []string{"not served by birdy's native engine", "--bird", "npm install -g @steipete/bird"} {
		if !strings.Contains(help, want) {
			t.Errorf("trending help missing %q\n%s", want, help)
		}
	}
}

// runHelpCLI drives the real root command and fails the test if anything is
// forwarded: help must be answered before an account is picked or a process
// spawned.
func runHelpCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	restoreHook := passthroughHook
	passthroughHook = func(cmd *cobra.Command, forwarded []string) error {
		t.Errorf("%v was forwarded to the passthrough as %v", args, forwarded)
		return nil
	}
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		passthroughHook = restoreHook
		resetProgramFlags()
		// cobra keeps the parsed --help value on root's FlagSet; left true,
		// every later in-process execute() would print help instead of
		// dispatching.
		if f := rootCmd.Flags().Lookup("help"); f != nil {
			_ = f.Value.Set("false")
			f.Changed = false
		}
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
	resetProgramFlags()
	err := execute(args)
	return out.String() + errOut.String(), err
}

func TestHelpIsNeverForwarded(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"search", "--help"}, "Usage:\n  birdy search <query>"},
		{[]string{"search", "-h"}, "Usage:\n  birdy search <query>"},
		{[]string{"help", "search"}, "Usage:\n  birdy search <query>"},
		{[]string{"read", "123", "--help"}, "Usage:\n  birdy read <tweet-id|url>"},
		{[]string{"-a", "main", "user-tweets", "--help"}, "Usage:\n  birdy user-tweets <@handle>"},
		{[]string{"trending", "--help"}, "not served by birdy's native engine"},
		// Root fallback path: unknown command or tweet-id shorthand.
		{[]string{"1234567890", "--help"}, "X Commands"},
		{[]string{"no-such-command", "-h"}, "X Commands"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := runHelpCLI(t, tc.args...)
			if err != nil {
				t.Fatalf("execute(%v) = %v, want nil (help exits 0)", tc.args, err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("execute(%v) output missing %q\n%s", tc.args, tc.want, out)
			}
			if strings.Contains(out, "fast X CLI") || strings.Contains(out, "node") {
				t.Fatalf("execute(%v) printed bird/node text\n%s", tc.args, out)
			}
		})
	}
}

// `--` still passes a literal --help through, so bird-side help stays
// reachable for anyone running --bird deliberately.
func TestHelpAfterDoubleDashIsForwarded(t *testing.T) {
	got, err := runCLI(t, "search", "--", "--help")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	wantForwarded(t, got, "search", "search", "--", "--help")
}

// Root help lists every native command with its one-liner, so an agent can
// discover the surface from `birdy --help` alone.
func TestRootHelpListsNativeCommands(t *testing.T) {
	out, err := runHelpCLI(t, "--help")
	if err != nil {
		t.Fatalf("execute(--help) = %v", err)
	}
	if !strings.Contains(out, "X Commands") || !strings.Contains(out, "--help` lists its flags") {
		t.Fatalf("root help lacks the X command group hint\n%s", out)
	}
	for command := range nativeCommands {
		found, _, _ := rootCmd.Find([]string{command})
		if !strings.Contains(out, "  "+command) || !strings.Contains(out, found.Short) {
			t.Errorf("root help does not list %s with its one-liner", command)
		}
	}
	if !strings.Contains(out, "bird CLI only") {
		t.Errorf("root help must flag trending as bird-only\n%s", out)
	}
}
