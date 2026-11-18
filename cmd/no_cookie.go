package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/xapi"
	"github.com/guzus/birdy/pkg/tweet"
	"github.com/spf13/cobra"
)

// --no-cookie: read through Birdy's hosted public post API instead of X.
// No account, cookie or accounts file is opened. The API returns only a
// post's ID, text and author, so output shows only those (and the URL built
// from them); counts and dates are left out rather than shown as zeros.
//
//	birdy read <id|url> --no-cookie [--json|--json-full|--plain]
//	birdy search <query> --no-cookie [-n N]   (Birdy's public post cache, not X search)
//
// BIRDY_NO_COOKIE=1 turns it on by default; --no-cookie=false turns it off.
// --api-url or BIRDY_PUBLIC_API_URL picks another instance.

const (
	noCookieEnv  = "BIRDY_NO_COOKIE"
	publicAPIEnv = "BIRDY_PUBLIC_API_URL"
)

// noCookieCommands are the commands the public API can serve.
var noCookieCommands = map[string]bool{"read": true, "search": true}

// noCookieFlags are the command flags that mean something without cookies.
var noCookieFlags = map[string]map[string]bool{
	"read":   {"--json": true, "--json-full": true, "--plain": true, "--no-emoji": true, "--no-color": true},
	"search": {"--json": true, "--json-full": true, "--plain": true, "--no-emoji": true, "--no-color": true, "-n": true, "--count": true, "--limit": true},
}

type noCookieMode struct {
	on     bool
	apiURL string
	// fromEnv says the mode came from BIRDY_NO_COOKIE, for the error text.
	fromEnv bool
}

// extractNoCookie removes --no-cookie and --api-url from args (before any
// "--") and resolves the mode with the environment. The flag wins.
func extractNoCookie(args []string) (noCookieMode, []string, error) {
	mode := noCookieMode{apiURL: strings.TrimSpace(os.Getenv(publicAPIEnv))}
	if v := strings.TrimSpace(os.Getenv(noCookieEnv)); v != "" {
		on, ok := parseBoolFlag(v)
		if !ok {
			return mode, nil, fmt.Errorf("invalid %s=%q (use 1 or 0)", noCookieEnv, v)
		}
		mode.on, mode.fromEnv = on, on
	}
	apiURLFlag := false
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			rest = append(rest, args[i:]...)
			i = len(args)
		case arg == "--no-cookie":
			mode.on, mode.fromEnv = true, false
		case strings.HasPrefix(arg, "--no-cookie="):
			on, ok := parseBoolFlag(strings.TrimPrefix(arg, "--no-cookie="))
			if !ok {
				return mode, nil, fmt.Errorf("invalid value for --no-cookie: %q", strings.TrimPrefix(arg, "--no-cookie="))
			}
			mode.on, mode.fromEnv = on, false
		case arg == "--api-url":
			if i+1 >= len(args) || args[i+1] == "" {
				return mode, nil, errors.New("--api-url requires a value")
			}
			mode.apiURL, apiURLFlag = args[i+1], true
			i++
		case strings.HasPrefix(arg, "--api-url="):
			mode.apiURL, apiURLFlag = strings.TrimPrefix(arg, "--api-url="), true
		default:
			rest = append(rest, arg)
		}
	}
	if apiURLFlag && !mode.on {
		return mode, nil, errors.New("--api-url only applies with --no-cookie")
	}
	return mode, rest, nil
}

// runNoCookie serves read or search from the public API. argv is the
// forwarded invocation: a command name, or a bare post ID or URL.
func runNoCookie(cmd *cobra.Command, mode noCookieMode, argv []string) error {
	if len(argv) == 0 {
		return cmd.Help()
	}
	command, tail := nativeCommandName(argv[0]), argv[1:]
	if !noCookieCommands[command] && (shorthandRead.MatchString(argv[0]) || allDigits(argv[0])) {
		command, tail = "read", argv
	}
	if !noCookieCommands[command] {
		why := "--no-cookie"
		if mode.fromEnv {
			why = noCookieEnv + "=1 (pass --no-cookie=false to use your accounts)"
		}
		return fmt.Errorf("%s: %q needs X cookies; Birdy's public API serves only read and search", why, argv[0])
	}
	strategy := rootCmd.PersistentFlags().Lookup("strategy")
	strategySet := strategy != nil && (strategy.Changed || strategyFlag != strategy.DefValue)
	for name, set := range map[string]bool{"--account": accountFlag != "", "--strategy": strategySet, "--bird": birdFlag, "--vpn": vpnFlag || vpnServerFlag != ""} {
		if set {
			return fmt.Errorf("%s does not apply with --no-cookie: no account is used and the request goes to Birdy's public API", name)
		}
	}
	for _, arg := range tail {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name := arg
			if eq := strings.IndexByte(arg, '='); eq > 0 {
				name = arg[:eq]
			}
			if !noCookieFlags[command][name] {
				return fmt.Errorf("%s is not available with --no-cookie (the public API returns only a post's ID, text and author)", name)
			}
		}
	}
	args := parseNativeArgs(tail)
	args.command = command
	args.stderr = cmd.ErrOrStderr()
	if args.countErr != nil {
		return args.countErr
	}
	opts := tweet.ReaderOptions{NoCookie: true, PublicAPIURL: mode.apiURL}
	if verboseFlag {
		base := mode.apiURL
		if base == "" {
			base = tweet.DefaultPublicAPIURL
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "[birdy] no-cookie: %s (no account)\n", base)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	out := cmd.OutOrStdout()
	switch command {
	case "read":
		if len(args.positionals) != 1 {
			return errors.New("read --no-cookie: give one post ID or URL")
		}
		reader, err := tweet.NewReader(opts)
		if err != nil {
			return err
		}
		post, err := reader.Read(ctx, args.positional)
		if err != nil {
			return noCookieError(err, args.positional)
		}
		if args.json {
			return writeNativeJSON(out, publicPostView(post, args.jsonFull))
		}
		return renderTweet(out, xapiFromPublic(post), args, false)
	default: // search
		query := strings.Join(args.positionals, " ")
		count := defaultCountFor("search")
		if args.countSet {
			count = args.count
		}
		if count > tweet.MaxPublicSearch {
			return fmt.Errorf("search --no-cookie returns at most %d posts", tweet.MaxPublicSearch)
		}
		posts, err := tweet.SearchPublic(ctx, opts, query, count)
		if err != nil {
			return noCookieError(err, "")
		}
		if args.json {
			views := make([]publicPostJSON, 0, len(posts))
			for _, p := range posts {
				views = append(views, publicPostView(p, args.jsonFull))
			}
			return writeNativeJSON(out, views)
		}
		if len(posts) == 0 {
			_, err := fmt.Fprintln(out, "No posts in Birdy's public cache match.")
			return err
		}
		for _, p := range posts {
			if err := renderTweet(out, xapiFromPublic(p), args, false); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, listSeparator); err != nil {
				return err
			}
		}
		return nil
	}
}

// publicPostJSON is the no-cookie JSON shape: the --json field names for what
// the API returns, and nothing it does not (no counts, no dates). source
// marks where the post came from.
type publicPostJSON struct {
	ID     string      `json:"id"`
	Text   string      `json:"text"`
	Author xapi.Author `json:"author"`
	URL    string      `json:"url,omitempty"`
	Source string      `json:"source,omitempty"`
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func publicPostView(p *tweet.Tweet, full bool) publicPostJSON {
	v := publicPostJSON{ID: p.ID, Text: p.Text, Author: xapi.Author{Username: p.Author.Username, Name: p.Author.Name}}
	if full {
		v.URL, v.Source = p.URL(), "birdy-public"
	}
	return v
}

func xapiFromPublic(p *tweet.Tweet) xapi.Tweet {
	return xapi.Tweet{ID: p.ID, Text: p.Text, Author: xapi.Author{Username: p.Author.Username, Name: p.Author.Name}}
}

func noCookieError(err error, ref string) error {
	var apiErr *tweet.PublicPostError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.StatusCode {
	case 404:
		what := "the post"
		if ref != "" {
			what = "post " + ref
		}
		return fmt.Errorf("%s is not in Birdy's public cache, or X refused it (deleted, protected, or not visible without an account)", what)
	case 429:
		if apiErr.RetryAfter > 0 {
			return fmt.Errorf("Birdy's public API is rate limited (60 requests a minute per IP); retry after %s", apiErr.RetryAfter)
		}
		return errors.New("Birdy's public API is rate limited (60 requests a minute per IP); retry shortly")
	case 400:
		return errors.New("Birdy's public API refused the request as invalid")
	}
	if apiErr.RetryAfter > 0 {
		return fmt.Errorf("Birdy's public API answered HTTP %d; retry after %s", apiErr.StatusCode, apiErr.RetryAfter)
	}
	return fmt.Errorf("Birdy's public API answered HTTP %d", apiErr.StatusCode)
}

// writeNoCookieHelp adds the no-cookie section to read's and search's help.
func writeNoCookieHelp(w io.Writer, command string) {
	if !noCookieCommands[command] {
		return
	}
	fmt.Fprintln(w, "No-cookie mode (no account, no cookies):")
	fmt.Fprintf(w, "      --no-cookie       go through Birdy's public post API instead of X; env %s=1 turns it on, --no-cookie=false off\n", noCookieEnv)
	fmt.Fprintf(w, "      --api-url URL     another Birdy instance (https, or http on localhost); env %s; default %s\n", publicAPIEnv, tweet.DefaultPublicAPIURL)
	if command == "search" {
		fmt.Fprintln(w, "  Searches Birdy's public post cache (not X search); -n up to 100. Output has ID, text and author only.")
	} else {
		fmt.Fprintln(w, "  Output has ID, text and author only: no counts, dates or media.")
	}
	fmt.Fprintln(w)
}
