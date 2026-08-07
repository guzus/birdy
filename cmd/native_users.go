package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"unicode/utf16"

	"github.com/guzus/birdy/internal/xapi"
)

// bird's `followers`/`following` take no positional handle. The target is
// --user <userId>, a numeric id, and it defaults to the authenticated account.
func nativeFollowers(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return renderUserList(ctx, c, args, out, c.Followers)
}

func nativeFollowing(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer) error {
	return renderUserList(ctx, c, args, out, c.Following)
}

type userListFetcher func(context.Context, string, int, string) (*xapi.UserListPage, error)

func renderUserList(ctx context.Context, c *xapi.Client, args nativeArgs, out io.Writer, fetch userListFetcher) error {
	userID, err := resolveUserID(ctx, c, args.user)
	if err != nil {
		return err
	}

	page, err := fetch(ctx, userID, args.count, "")
	if err != nil {
		return err
	}

	if args.json {
		users := page.Users
		if users == nil {
			users = []xapi.ListedUser{}
		}
		return writeNativeJSON(out, users)
	}
	return renderUsers(out, page.Users, args)
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
