package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/guzus/birdy/internal/xapi"
)

// bird's `followers`/`following` take no positional handle. The target is
// --user <userId>, a numeric id, and it defaults to the authenticated account.
func nativeFollowers(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return renderUserList(ctx, c, args, out, c.FollowersPage)
}

func nativeFollowing(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return renderUserList(ctx, c, args, out, c.FollowingPage)
}

type userListFetcher func(context.Context, string, int, string) (*xapi.UserListPage, error)

func renderUserList(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer, fetch userListFetcher) error {
	if args.pagingErr != nil {
		return args.pagingErr
	}
	if args.maxPages > 0 && !args.all {
		return fmt.Errorf("--max-pages requires --all.")
	}
	userID, err := resolveUserID(ctx, c, args.user)
	if err != nil {
		return err
	}

	var page *xapi.UserListPage
	if args.all {
		page, err = fetchAllListedUsers(ctx, fetch, userID, args)
	} else {
		page, err = fetch(ctx, userID, args.count, args.cursor)
	}
	if err != nil {
		return err
	}
	if args.json {
		if page.Users == nil {
			page.Users = []xapi.ListedUser{}
		}
		if args.all || args.cursor != "" {
			var cursor *string
			if page.NextCursor != "" {
				cursor = &page.NextCursor
			}
			return writeNativeJSON(out, struct {
				Users      []xapi.ListedUser `json:"users"`
				NextCursor *string           `json:"nextCursor"`
			}{page.Users, cursor})
		}
		return writeNativeJSON(out, page.Users)
	}
	if args.all {
		fmt.Fprintf(args.stderr, "%s Total: %d users\n", status("info", "ℹ️", "Info:", args), len(page.Users))
	}
	if (args.all || args.cursor != "") && page.NextCursor != "" {
		prefix := status("info", "ℹ️", "Info:", args)
		if args.all {
			fmt.Fprintf(args.stderr, "%s Stopped at --max-pages. Use --cursor to continue.\n", prefix)
		}
		fmt.Fprintf(args.stderr, "%s Next cursor: %s\n", prefix, page.NextCursor)
	}
	return renderUsers(out, page.Users, args)
}

func fetchAllListedUsers(ctx context.Context, fetch userListFetcher, userID string, args nativeArgs) (*xapi.UserListPage, error) {
	result := &xapi.UserListPage{}
	seenUsers := make(map[string]bool)
	cursor := args.cursor
	for n := 1; ; n++ {
		if !args.json {
			fmt.Fprintf(args.stderr, "%s Fetching page %d...\n", status("info", "ℹ️", "Info:", args), n)
		}
		page, err := fetch(ctx, userID, args.count, cursor)
		if err != nil {
			return nil, err
		}
		before := len(result.Users)
		for _, user := range page.Users {
			if !seenUsers[user.ID] {
				seenUsers[user.ID] = true
				result.Users = append(result.Users, user)
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor || len(result.Users) == before {
			return result, nil
		}
		if n == args.maxPages {
			result.NextCursor = page.NextCursor
			return result, nil
		}
		cursor = page.NextCursor
		// Match bird's one-second delay between user-list requests.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// resolveUserID returns the requested numeric id, or the authenticated
// account's when none was given — matching bird's resolveUserIdOrExit.
func resolveUserID(ctx context.Context, c *xapi.Client, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	viewer, err := c.CurrentUser(ctx)
	if err != nil {
		return "", err
	}
	return viewer.ID, nil
}

func renderUsers(out io.Writer, users []xapi.ListedUser, args nativeArgs) error {
	if len(users) == 0 {
		_, err := fmt.Fprintln(out, "No users found.")
		return err
	}

	for _, user := range users {
		// X withholds the profile of a suspended, deactivated, or hidden
		// member but still lists it. Printing "@ ()" would read as a bug.
		if user.Unavailable {
			fmt.Fprintf(out, "(unavailable account %s)\n", user.ID)
			fmt.Fprintln(out, listSeparator)
			continue
		}
		fmt.Fprintf(out, "@%s (%s)\n", user.Username, user.Name)
		if description := deref(user.Description); description != "" {
			fmt.Fprintf(out, "  %s\n", truncateJS(description, 100))
		}
		// bird prints this line whenever the count is present, including a
		// genuine zero. X omits it for suspended and some protected accounts,
		// which is why FollowersCount is a pointer.
		if user.FollowersCount != nil {
			fmt.Fprintf(out, "  %s %s followers\n", status("info", "ℹ️", "Info:", args), groupThousands(*user.FollowersCount))
		}
		fmt.Fprintln(out, listSeparator)
	}
	return nil
}

// deref reads an optional string. Presence and emptiness are distinct in the
// JSON (bird emits `"description": ""` but omits a genuinely missing one),
// while for rendering both mean "print nothing".
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// truncateJS cuts to n UTF-16 code units and appends "..." when it cut,
// reproducing JavaScript's String.prototype.slice.
//
// Go would otherwise cut by bytes, which truncates a Korean bio to roughly a
// third of the characters bird shows and can split a rune mid-sequence.
func truncateJS(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}
	return string(utf16.Decode(units[:n])) + "..."
}

// groupThousands reproduces Number.prototype.toLocaleString() for the en-US
// default Node runs under, which is what bird's output carries.
func groupThousands(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if digits[0] == '-' {
		sign, digits = "-", digits[1:]
	}

	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	grouped := digits[:lead]
	for i := lead; i < len(digits); i += 3 {
		grouped += "," + digits[i:i+3]
	}
	return sign + grouped
}
