package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	strategyFlag  string
	birdFlag      bool
	accountFlag   string
	verboseFlag   bool
	vpnFlag       bool
	vpnServerFlag string
)

// bird's program-level options (dist/cli/program.js:81-95). Registering them is
// not cosmetic. Both cobra's stripFlags, during subcommand resolution, and
// pflag's stripUnknownFlagValue, during parsing, resolve an unregistered long
// flag's arity by assuming it takes a value and consuming the next token — and
// before the subcommand, that token IS the subcommand. Declaring the real table
// removes the guess.
var (
	plainFlag   bool
	noEmojiFlag bool
	noColorFlag bool

	authTokenFlag        string
	ct0Flag              string
	chromeProfileFlag    string
	chromeProfileDirFlag string
	firefoxProfileFlag   string
	cookieTimeoutFlag    string
	timeoutFlag          string
	quoteDepthFlag       string

	cookieSourceFlag []string
	mediaFlag        []string
	altFlag          []string
)

var rootCmd = &cobra.Command{
	Use:   "birdy",
	Short: "Multi-account proxy for the bird CLI",
	Long: `birdy manages multiple X/Twitter auth tokens and proxies commands
to the bird CLI, rotating between accounts to reduce rate-limit risk.

Any command not recognized by birdy is forwarded directly to bird
using the next account in the rotation.

Examples:
  birdy read 1234567890           # read a tweet, auto-rotating accounts
  birdy search "golang"           # search, auto-rotating accounts
  birdy --account main home       # use a specific account
  birdy account add main          # add a new account
  birdy account list              # list all accounts`,
	// RunE is assigned in init: referencing runRootPassthrough here would be an
	// initialization cycle, since it reads rootCmd's own flag state.
	SilenceUsage:  true,
	SilenceErrors: true,
	// Pass remaining args after -- or unknown args through.
	Args: cobra.ArbitraryArgs,
}

func init() {
	// If no subcommand matches, treat everything as bird args.
	rootCmd.RunE = runRootPassthrough

	rootCmd.PersistentFlags().StringVarP(&strategyFlag, "strategy", "s", "round-robin",
		"rotation strategy: round-robin, least-recently-used, least-used, random")
	rootCmd.PersistentFlags().BoolVar(&birdFlag, "bird", false,
		"force the bird CLI (Node) instead of birdy's native Go implementation")
	rootCmd.PersistentFlags().StringVarP(&accountFlag, "account", "a", "",
		"use a specific account by name (skip rotation)")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false,
		"show which account is being used")
	rootCmd.PersistentFlags().BoolVar(&vpnFlag, "vpn", false,
		"route bird through the configured SOCKS5 VPN (see: birdy vpn)")
	rootCmd.PersistentFlags().StringVar(&vpnServerFlag, "vpn-server", "",
		"pin to a specific SOCKS5 server hostname (implies --vpn)")

	pf := rootCmd.PersistentFlags()
	pf.BoolVar(&plainFlag, "plain", false, "plain output (stable, no emoji, no color)")
	pf.BoolVar(&noEmojiFlag, "no-emoji", false, "disable emoji output")
	pf.BoolVar(&noColorFlag, "no-color", false, "disable ANSI colors (or set NO_COLOR)")
	pf.StringVar(&authTokenFlag, "auth-token", "", "Twitter auth_token cookie")
	pf.StringVar(&ct0Flag, "ct0", "", "Twitter ct0 cookie")
	pf.StringVar(&chromeProfileFlag, "chrome-profile", "", "Chrome profile name for cookie extraction")
	pf.StringVar(&chromeProfileDirFlag, "chrome-profile-dir", "", "Chrome/Chromium profile directory or cookie DB path")
	pf.StringVar(&firefoxProfileFlag, "firefox-profile", "", "Firefox profile name for cookie extraction")
	pf.StringVar(&cookieTimeoutFlag, "cookie-timeout", "", "cookie extraction timeout in milliseconds")
	pf.StringVar(&timeoutFlag, "timeout", "", "request timeout in milliseconds")
	pf.StringVar(&quoteDepthFlag, "quote-depth", "", "max quoted tweet depth (default 1; 0 disables)")
	// StringArray, not StringSlice: StringSlice splits on commas, which would
	// corrupt a media path or an alt text containing one. bird pairs the Nth
	// --alt with the Nth --media, so order and multiplicity are both meaningful.
	pf.StringArrayVar(&cookieSourceFlag, "cookie-source", nil, "cookie source for browser cookie extraction (repeatable)")
	pf.StringArrayVar(&mediaFlag, "media", nil, "attach media file (repeatable)")
	pf.StringArrayVar(&altFlag, "alt", nil, "alt text for the corresponding --media (repeatable)")

	// Stop root's own parser at the first operand, so tokens that follow a
	// command birdy does not register (`birdy trending --json`, or whatever
	// bird adds next) are forwarded verbatim instead of parsed. This applies
	// only to rootCmd.Flags(); children build their own FlagSets.
	rootCmd.Flags().SetInterspersed(false)

	// bird answers --version and -V. Without a version flag the pre-scan below
	// would turn today's silent help-with-exit-0 into a hard error.
	//
	// The flag is registered here rather than left to cobra's
	// InitDefaultVersionFlag because cobra would bind it to -v, which
	// --verbose already owns. It reports birdy's version, not bird's: this is
	// birdy, and `birdy version` already prints exactly this string.
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(fmt.Sprintf("birdy %s (commit: %s, built: %s)\n", version, commit, date))
	rootCmd.Flags().BoolP("version", "V", false, "print birdy's version")
}

// unknownOptionError is a usage error in bird's wording. bird writes
// `error: unknown option '--json'` to stderr and exits 1; anything grepping
// that output diverges if the casing changes.
type unknownOptionError struct{ opt string }

func (e *unknownOptionError) Error() string { return "unknown option '" + e.opt + "'" }

// rootFlagSet returns root's complete flag table.
//
// LocalFlags(), not Flags(): Flags() does not merge persistent flags, so every
// lookup here would fail and even --account would be rejected.
func rootFlagSet() *pflag.FlagSet {
	rootCmd.InitDefaultHelpFlag()
	rootCmd.InitDefaultVersionFlag()
	return rootCmd.LocalFlags()
}

// checkProgramFlags validates the flag region that precedes the subcommand.
//
// It runs before cobra's Find/stripFlags, which is what makes mis-dispatch
// impossible rather than merely unlikely. bird's program-level option set is
// closed, so anything else here is an error reported exactly as bird reports
// it. The scan stops at the first operand, so flags belonging to a command
// birdy does not implement are never inspected and still forward untouched.
func checkProgramFlags(args []string) error {
	fs := rootFlagSet()

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return nil
		case !strings.HasPrefix(arg, "-") || arg == "-":
			return nil // first operand: the region ends here
		case strings.HasPrefix(arg, "--"):
			name := arg[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				if fs.Lookup(name[:eq]) == nil {
					return &unknownOptionError{"--" + name[:eq]}
				}
				continue
			}
			f := fs.Lookup(name)
			if f == nil {
				return &unknownOptionError{arg}
			}
			if f.NoOptDefVal == "" {
				i++ // consumes the next token as its value
			}
		default:
			if err := checkShorthandCluster(fs, arg, args, &i); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkShorthandCluster walks a possibly-clustered shorthand: -v, -a main,
// -amain, -va main.
func checkShorthandCluster(fs *pflag.FlagSet, arg string, args []string, i *int) error {
	body := arg[1:]
	for j := 0; j < len(body); j++ {
		short := string(body[j])
		f := fs.ShorthandLookup(short)
		if f == nil {
			return &unknownOptionError{"-" + short}
		}
		if f.NoOptDefVal != "" {
			continue // boolean; keep walking the cluster
		}
		if j+1 < len(body) {
			return nil // the rest of the cluster is this flag's value
		}
		*i++ // the value is the next token
		return nil
	}
	return nil
}

// resetProgramFlags clears the program-flag state between in-process runs.
// Production executes once, so this exists for the tests that drive execute()
// repeatedly against the package-level rootCmd.
func resetProgramFlags() {
	plainFlag, noEmojiFlag, noColorFlag = false, false, false
	authTokenFlag, ct0Flag = "", ""
	chromeProfileFlag, chromeProfileDirFlag, firefoxProfileFlag = "", "", ""
	cookieTimeoutFlag, timeoutFlag, quoteDepthFlag = "", "", ""
	cookieSourceFlag, mediaFlag, altFlag = nil, nil, nil

	pf := rootCmd.PersistentFlags()
	for _, name := range append(append([]string{}, birdBoolProgramFlags...),
		append(append([]string{}, birdValueProgramFlags...), birdRepeatProgramFlags...)...) {
		if f := pf.Lookup(name); f != nil {
			f.Changed = false
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			}
		}
	}
}

var (
	birdBoolProgramFlags  = []string{"plain", "no-emoji", "no-color"}
	birdValueProgramFlags = []string{
		"auth-token", "ct0", "chrome-profile", "chrome-profile-dir",
		"firefox-profile", "cookie-timeout", "timeout", "quote-depth",
	}
	birdRepeatProgramFlags = []string{"cookie-source", "media", "alt"}
)

// birdProgramFlagArgs rebuilds the tokens for bird's program-level flags that
// root's own parser consumed, so the fallback path forwards them instead of
// dropping them.
//
// bird accepts these after the subcommand as well as before (`bird read <id>
// --plain` produces plain output), and parseNativeArgs scans positionally, so
// re-attaching them right after args[0] is correct for both engines.
//
// It is only correct on the root fallback path: when a registered bird
// subcommand matches, DisableFlagParsing means root's parser never ran, so
// Changed is false and the tokens are already inside args.
func birdProgramFlagArgs() []string {
	var out []string
	pf := rootCmd.PersistentFlags()

	for _, name := range birdBoolProgramFlags {
		if f := pf.Lookup(name); f != nil && f.Changed {
			out = append(out, "--"+name)
		}
	}
	for _, name := range birdValueProgramFlags {
		if f := pf.Lookup(name); f != nil && f.Changed {
			out = append(out, "--"+name, f.Value.String())
		}
	}
	for _, name := range birdRepeatProgramFlags {
		f := pf.Lookup(name)
		if f == nil || !f.Changed {
			continue
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				out = append(out, "--"+name, v)
			}
		}
	}
	return out
}

// runRootPassthrough is the fallback for anything cobra could not route: the
// tweet-id/URL shorthand, and bird commands birdy has not registered. It
// normalizes the same way makeBirdCmd does, then re-attaches the program-level
// flags root's parser absorbed.
func runRootPassthrough(cmd *cobra.Command, args []string) error {
	cleaned, err := applyBirdyGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(cleaned) == 0 {
		return cmd.Help()
	}
	if injected := birdProgramFlagArgs(); len(injected) > 0 {
		merged := make([]string, 0, len(cleaned)+len(injected))
		merged = append(merged, cleaned[0])
		merged = append(merged, injected...)
		cleaned = append(merged, cleaned[1:]...)
	}
	return forwardToBird(cmd, cleaned)
}

// passthroughHook intercepts the forwarded argv. Production leaves it nil; the
// routing tests set it to observe which command handled an invocation and with
// what arguments, which is the only way to see the dispatch itself.
var passthroughHook func(cmd *cobra.Command, args []string) error

func forwardToBird(cmd *cobra.Command, args []string) error {
	if passthroughHook != nil {
		return passthroughHook(cmd, args)
	}
	return runPassthrough(cmd, args)
}

// Execute runs the root command.
func Execute() {
	if err := execute(os.Args[1:]); err != nil {
		var unknown *unknownOptionError
		if errors.As(err, &unknown) {
			fmt.Fprintf(rootCmd.ErrOrStderr(), "error: %v\n", unknown)
			os.Exit(1)
		}
		fmt.Fprintf(rootCmd.ErrOrStderr(), "Error: %v\n", err)
		os.Exit(1)
	}
}

// execute is Execute with an injectable argv, for tests.
func execute(args []string) error {
	if err := checkProgramFlags(args); err != nil {
		return err
	}
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}
