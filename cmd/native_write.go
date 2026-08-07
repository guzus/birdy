package cmd

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
	"github.com/guzus/birdy/pkg/tweet"
)

// The mutating commands.
//
// Read-only enforcement is not repeated here on purpose: passthrough.go calls
// ensureBirdCommandAllowed and filterWritableAccounts before dispatching to
// either engine, so BIRDY_READ_ONLY and per-account read_only already cover the
// native path. TestReadOnlyGateCoversNativeWrites pins that, because the gate
// living in the caller is exactly the kind of thing a later refactor drops.
//
// None of these retry. A timed-out CreateTweet may have posted, and a duplicate
// post is worse than a reported failure.

var onlyDigits = regexp.MustCompile(`^\d+$`)

func nativeTweet(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	text := args.positional
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("tweet: missing text")
	}

	id, err := c.CreateTweet(ctx, text, "")
	if err != nil {
		return fmt.Errorf("failed to post tweet: %w", err)
	}

	fmt.Fprintf(out, "%s Tweet posted successfully!\n", status("ok", "✅", "OK:", args))
	fmt.Fprintf(out, "%s %s\n", label("url", "🔗", "URL:", args), postedTweetURL(id))
	return nil
}

func nativeReply(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if len(args.positionals) < 2 {
		return fmt.Errorf("reply: expected a tweet id or url and the reply text")
	}
	target, err := tweet.ExtractTweetID(args.positionals[0])
	if err != nil {
		return err
	}
	text := args.positionals[1]
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("reply: missing text")
	}

	id, err := c.CreateTweet(ctx, text, target)
	if err != nil {
		return fmt.Errorf("failed to post reply: %w", err)
	}

	fmt.Fprintf(out, "%s Reply posted successfully!\n", status("ok", "✅", "OK:", args))
	fmt.Fprintf(out, "%s %s\n", label("url", "🔗", "URL:", args), postedTweetURL(id))
	return nil
}

// postedTweetURL is the /i/status/ form bird prints after a write. It differs
// from Tweet.URL(), which uses the author's handle — not known here without an
// extra lookup bird does not perform either.
func postedTweetURL(id string) string {
	return "https://x.com/i/status/" + id
}

func nativeFollow(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return runFriendship(ctx, c, args, out, "Now following", c.Follow)
}

func nativeUnfollow(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return runFriendship(ctx, c, args, out, "Unfollowed", c.Unfollow)
}

func runFriendship(
	ctx context.Context,
	c *xapi.Client,
	args nativeArgs,
	out io.Writer,
	verb string,
	act func(context.Context, string) (string, error),
) error {
	userID, display, err := resolveFollowTarget(ctx, c, args.positional)
	if err != nil {
		return err
	}

	canonical, err := act(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to %s %s: %w", strings.ToLower(strings.Fields(verb)[0]), display, err)
	}
	if canonical != "" {
		display = "@" + canonical
	}

	fmt.Fprintf(out, "%s %s %s\n", status("ok", "✅", "OK:", args), verb, display)
	return nil
}

// resolveFollowTarget accepts a handle or a numeric id, mirroring bird: a
// handle is looked up, and an all-digits argument that fails lookup is treated
// as an id rather than an error.
func resolveFollowTarget(ctx context.Context, c *xapi.Client, raw string) (userID, display string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("missing username or user id")
	}

	numeric := onlyDigits.MatchString(raw)
	if handle := xapi.NormalizeHandle(raw); handle != "" {
		user, lookupErr := c.UserByScreenName(ctx, handle)
		if lookupErr == nil {
			return user.ID, "@" + user.Username, nil
		}
		if !numeric {
			return "", "", fmt.Errorf("failed to find user @%s: %w", handle, lookupErr)
		}
	}
	if numeric {
		return raw, raw, nil
	}
	return "", "", fmt.Errorf("invalid username: %s", raw)
}

// nativeUnbookmark removes one or more bookmarks. bird takes a variadic list
// and reports each separately, continuing past a failure so one bad id does not
// abandon the rest.
func nativeUnbookmark(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	if len(args.positionals) == 0 {
		return fmt.Errorf("unbookmark: missing tweet id or url")
	}

	var failures []string
	for _, ref := range args.positionals {
		id, err := tweet.ExtractTweetID(ref)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", ref, err))
			continue
		}
		if err := c.DeleteBookmark(ctx, id); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		fmt.Fprintf(out, "%s Removed bookmark for %s\n", status("ok", "✅", "OK:", args), id)
	}

	if len(failures) > 0 {
		return fmt.Errorf("failed to remove bookmark for %s", strings.Join(failures, "; "))
	}
	return nil
}
