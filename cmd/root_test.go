package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// birdy's pre-subcommand flag region is the one place where bird's option table
// is closed and fully knowable, and it was the one place birdy told pflag to
// tolerate unknown flags. Both cobra's stripFlags (during subcommand
// resolution) and pflag's stripUnknownFlagValue (during parsing) resolve an
// unregistered long flag's arity by guessing "takes a value" and eating the
// next token — which before the subcommand IS the subcommand.
//
// These tests pin the three consequences: an unknown flag must be an error, a
// known bird flag must survive to the forwarded argv, and anything after the
// first operand must still pass through untouched.

// dispatched is what a routing test observed.
type dispatched struct {
	command   string   // the cobra command that handled it, "" for root
	forwarded []string // the argv handed to the bird/native path
	called    bool
}

// runCLI drives the real root command with a captured passthrough.
func runCLI(t *testing.T, args ...string) (dispatched, error) {
	t.Helper()

	var got dispatched
	restoreHook := passthroughHook
	passthroughHook = func(cmd *cobra.Command, forwarded []string) error {
		name := cmd.Name()
		if cmd == rootCmd {
			name = ""
		}
		got = dispatched{command: name, forwarded: forwarded, called: true}
		return nil
	}

	// The flag vars are package-level and cobra keeps parsed state on the
	// command, so every case starts from a clean slate.
	before := struct {
		account, strategy, vpnServer string
		verbose, bird, vpn           bool
	}{accountFlag, strategyFlag, vpnServerFlag, verboseFlag, birdFlag, vpnFlag}

	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)

	t.Cleanup(func() {
		passthroughHook = restoreHook
		accountFlag, strategyFlag, vpnServerFlag = before.account, before.strategy, before.vpnServer
		verboseFlag, birdFlag, vpnFlag = before.verbose, before.bird, before.vpn
		resetProgramFlags()
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	resetProgramFlags()
	err := execute(args)
	return got, err
}

func wantForwarded(t *testing.T, got dispatched, command string, argv ...string) {
	t.Helper()
	if !got.called {
		t.Fatalf("nothing was dispatched; wanted %s %v", command, argv)
	}
	if got.command != command {
		t.Errorf("handled by %q, want %q", got.command, command)
	}
	if strings.Join(got.forwarded, "\x00") != strings.Join(argv, "\x00") {
		t.Errorf("forwarded %v, want %v", got.forwarded, argv)
	}
}

// bird's program-level option table is closed: anything outside it, before the
// subcommand, is `error: unknown option '--x'` and exit 1.
func TestUnknownLeadingFlagIsAnError(t *testing.T) {
	cases := map[string][]string{
		"--json":              {"--json", "read", "1943513583817343406"},
		"-n":                  {"-n", "1", "search", "golang"},
		"after a known flag":  {"--account", "main", "--json", "read", "123"},
		"before a write verb": {"--json", "search", "tweet", "hello"},
	}

	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := runCLI(t, argv...)
			var unknown *unknownOptionError
			if !errors.As(err, &unknown) {
				t.Fatalf("err = %v, want an unknownOptionError", err)
			}
			if got.called {
				t.Errorf("nothing must be dispatched after a usage error, got %+v", got)
			}
		})
	}

	t.Run("message matches bird", func(t *testing.T) {
		_, err := runCLI(t, "--json", "read", "1")
		if err == nil || err.Error() != "unknown option '--json'" {
			t.Errorf("err = %v, want unknown option '--json'", err)
		}
	})
}

// The reachable-write shape: `--plain` is a documented bird program flag, and
// the left-shift promoted `tweet` — the intended command's first positional —
// into args[0], re-dispatching a `search` onto the real write command.
func TestUnknownLeadingFlagCannotRerouteToAWriteCommand(t *testing.T) {
	for _, verb := range []string{"tweet", "reply", "follow", "unfollow", "unbookmark"} {
		t.Run(verb, func(t *testing.T) {
			got, err := runCLI(t, "--json", "search", verb, "payload")
			if err == nil {
				t.Fatal("expected a usage error before any dispatch")
			}
			if got.called {
				t.Fatalf("a search invocation reached %q with %v", got.command, got.forwarded)
			}
		})

		t.Run(verb+" via a known flag", func(t *testing.T) {
			// --plain IS a valid bird program flag, so this one must dispatch —
			// to search, with --plain preserved, never to the write command.
			got, _ := runCLI(t, "--plain", "search", verb, "payload")
			wantForwarded(t, got, "search", "search", "--plain", verb, "payload")
		})
	}
}

// bird's program-level flags are legal before the subcommand and must reach the
// forwarded argv rather than being swallowed.
func TestProgramFlagsSurviveToTheForwardedArgv(t *testing.T) {
	cases := []struct {
		argv    []string
		command string
		want    []string
	}{
		{[]string{"--plain", "read", "123"}, "read", []string{"read", "--plain", "123"}},
		{[]string{"--no-emoji", "whoami"}, "whoami", []string{"whoami", "--no-emoji"}},
		{[]string{"--no-color", "read", "123"}, "read", []string{"read", "--no-color", "123"}},
		{[]string{"--auth-token", "TK", "read", "123"}, "read", []string{"read", "--auth-token", "TK", "123"}},
		{[]string{"--timeout", "5000", "read", "123"}, "read", []string{"read", "--timeout", "5000", "123"}},
		// The equals form travels verbatim: once the subcommand resolves, its
		// DisableFlagParsing means root's parser never touches these tokens.
		{[]string{"--quote-depth=0", "read", "123"}, "read", []string{"read", "--quote-depth=0", "123"}},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			got, err := runCLI(t, tc.argv...)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			wantForwarded(t, got, tc.command, tc.want...)
		})
	}
}

// bird's documented shorthand: `bird <tweet-id-or-url> [--json]`. The trailing
// flag was being eaten along with nothing else, so `--json` vanished.
func TestTweetIDShorthandKeepsTrailingFlags(t *testing.T) {
	got, err := runCLI(t, "2085459107914379659", "--json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	wantForwarded(t, got, "", "2085459107914379659", "--json")
}

// A program flag consumed by root's own parser has to be re-attached, or the
// shorthand path silently drops it.
func TestRootReinjectsAbsorbedProgramFlags(t *testing.T) {
	t.Run("boolean", func(t *testing.T) {
		got, err := runCLI(t, "--plain", "2085459107914379659")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantForwarded(t, got, "", "2085459107914379659", "--plain")
	})

	// --media and --alt are repeatable and positionally paired, so order and
	// multiplicity both matter. StringArray, not StringSlice: a path or an alt
	// text containing a comma must not be split.
	t.Run("repeatables keep order", func(t *testing.T) {
		got, err := runCLI(t, "--media", "a.png", "--media", "b,c.png", "--alt", "x,y", "123")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantForwarded(t, got, "", "123",
			"--media", "a.png", "--media", "b,c.png", "--alt", "x,y")
	})
}

// Anything after the first operand is a command birdy may not implement, so it
// must pass through byte-for-byte rather than being validated against bird's
// program table.
func TestUnknownCommandsAndFlagsStillPassThrough(t *testing.T) {
	cases := []struct {
		argv    []string
		command string
		want    []string
	}{
		// A registered bird command: DisableFlagParsing keeps its flags intact.
		{[]string{"trending", "--json"}, "trending", []string{"trending", "--json"}},
		// A command birdy has never heard of falls to root and must still
		// forward verbatim.
		{[]string{"futurecmd", "--newflag", "x"}, "", []string{"futurecmd", "--newflag", "x"}},
		{[]string{"read", "123", "--bogus"}, "read", []string{"read", "123", "--bogus"}},
		// A leading `--` ends the program-flag region without validating it.
		{[]string{"--", "--json", "read", "123"}, "", []string{"--json", "read", "123"}},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			got, err := runCLI(t, tc.argv...)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			wantForwarded(t, got, tc.command, tc.want...)
		})
	}
}

// SetInterspersed(false) stops root's parser at the first operand, so birdy's
// own globals after an unregistered command name reach RunE unparsed and have
// to be handled there.
func TestBirdyGlobalsAfterAnUnregisteredCommand(t *testing.T) {
	got, err := runCLI(t, "futurecmd", "-v", "--account", "alt2")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	wantForwarded(t, got, "", "futurecmd")
	if !verboseFlag {
		t.Error("-v after an unregistered command was not honored")
	}
	if accountFlag != "alt2" {
		t.Errorf("accountFlag = %q, want alt2", accountFlag)
	}
}

// birdy's own globals keep working in the pre-subcommand region.
func TestBirdyGlobalsBeforeTheSubcommand(t *testing.T) {
	got, err := runCLI(t, "--account=alt1", "-v", "read", "123")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	wantForwarded(t, got, "read", "read", "123")
	if accountFlag != "alt1" || !verboseFlag {
		t.Errorf("account = %q verbose = %v", accountFlag, verboseFlag)
	}
}

// --help and --version must resolve rather than becoming unknown options.
func TestHelpAndVersionResolve(t *testing.T) {
	for _, argv := range [][]string{{"--help"}, {"-h"}, {"--version"}, {"-V"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if err := checkProgramFlags(argv); err != nil {
				t.Errorf("checkProgramFlags(%v) = %v, want nil", argv, err)
			}
		})
	}
	if rootCmd.Version == "" {
		t.Error("rootCmd.Version must be set or --version hard-errors")
	}
}

// The direct unit surface, covering the shapes the end-to-end tests cannot
// reach cheaply.
func TestCheckProgramFlags(t *testing.T) {
	t.Run("accepts every bird program flag", func(t *testing.T) {
		// Transcribed from bird's dist/cli/program.js:81-95.
		value := []string{
			"--auth-token", "--ct0", "--chrome-profile", "--chrome-profile-dir",
			"--firefox-profile", "--cookie-timeout", "--cookie-source",
			"--media", "--alt", "--timeout", "--quote-depth",
		}
		for _, name := range value {
			if err := checkProgramFlags([]string{name, "v", "read", "1"}); err != nil {
				t.Errorf("%s value form rejected: %v", name, err)
			}
			if err := checkProgramFlags([]string{name + "=v", "read", "1"}); err != nil {
				t.Errorf("%s equals form rejected: %v", name, err)
			}
		}
		for _, name := range []string{"--plain", "--no-emoji", "--no-color"} {
			if err := checkProgramFlags([]string{name, "read", "1"}); err != nil {
				t.Errorf("%s rejected: %v", name, err)
			}
		}
	})

	t.Run("stops at the first operand", func(t *testing.T) {
		for _, argv := range [][]string{
			{"trending", "--json"},
			{"futurecmd", "--newflag", "x"},
			{"123", "--json"},
			{"--", "--json", "read", "123"},
			{},
			{"-"},
		} {
			if err := checkProgramFlags(argv); err != nil {
				t.Errorf("checkProgramFlags(%v) = %v, want nil", argv, err)
			}
		}
	})

	t.Run("walks shorthand clusters", func(t *testing.T) {
		// -v is boolean, -a takes a value: the cluster consumes "alt2".
		if err := checkProgramFlags([]string{"-va", "alt2", "home"}); err != nil {
			t.Errorf("clustered shorthand rejected: %v", err)
		}
		// -a's value is the rest of the cluster.
		if err := checkProgramFlags([]string{"-aalt2", "home"}); err != nil {
			t.Errorf("attached shorthand value rejected: %v", err)
		}
		if err := checkProgramFlags([]string{"-vq", "home"}); err == nil {
			t.Error("an unknown shorthand inside a cluster must be rejected")
		}
	})

	t.Run("a value-taking flag consumes its value", func(t *testing.T) {
		// "read" here is --account's VALUE, not an operand, so the scan must
		// keep going and reject --json.
		if err := checkProgramFlags([]string{"--account", "read", "--json"}); err == nil {
			t.Error("the scan stopped early and missed --json")
		}
		// A boolean flag must not consume the next token.
		if err := checkProgramFlags([]string{"--plain", "--json"}); err == nil {
			t.Error("--plain must not swallow --json")
		}
	})
}
