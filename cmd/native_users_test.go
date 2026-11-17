package cmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func intPtr(n int) *int { return &n }

func strPtr(s string) *string { return &s }

func TestRenderUsersOutput(t *testing.T) {
	users := []xapi.ListedUser{
		{Username: "guzus", Name: "Guzus", Description: strPtr("builder"), FollowersCount: intPtr(1234567)},
		// No description and no reported count: both lines are omitted.
		{Username: "quiet", Name: "Quiet"},
	}

	var buf bytes.Buffer
	if err := renderUsers(&buf, users, nativeArgs{emoji: true, command: "followers"}); err != nil {
		t.Fatalf("renderUsers: %v", err)
	}

	want := "@guzus (Guzus)\n" +
		"  builder\n" +
		"  ℹ️ 1,234,567 followers\n" +
		listSeparator + "\n" +
		"@quiet (Quiet)\n" +
		listSeparator + "\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

// A genuine zero is a reported answer and must print; an absent count must not.
// This is the distinction ListedUser's pointer fields exist to carry.
func TestRenderUsersDistinguishesZeroFromAbsent(t *testing.T) {
	var zero bytes.Buffer
	renderUsers(&zero, []xapi.ListedUser{{Username: "a", Name: "A", FollowersCount: intPtr(0)}}, nativeArgs{emoji: true})
	if !strings.Contains(zero.String(), "0 followers") {
		t.Errorf("a reported zero should print, got %q", zero.String())
	}

	var absent bytes.Buffer
	renderUsers(&absent, []xapi.ListedUser{{Username: "a", Name: "A"}}, nativeArgs{emoji: true})
	if strings.Contains(absent.String(), "followers") {
		t.Errorf("an unreported count must not print, got %q", absent.String())
	}
}

func TestRenderUsersEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderUsers(&buf, nil, nativeArgs{emoji: true, command: "followers"}); err != nil {
		t.Fatalf("renderUsers: %v", err)
	}
	if got := buf.String(); got != "No users found.\n" {
		t.Errorf("got %q, want %q", got, "No users found.\n")
	}
}

// bird truncates a bio with JavaScript's slice, which counts UTF-16 code
// units. Cutting by Go bytes would shorten a Korean bio to about a third of
// the characters and can split a rune.
func TestTruncateJSCountsUTF16Units(t *testing.T) {
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"ascii under limit", "hello", 100, "hello"},
		{"ascii at limit", strings.Repeat("a", 100), 100, strings.Repeat("a", 100)},
		{"ascii over limit", strings.Repeat("a", 101), 100, strings.Repeat("a", 100) + "..."},
		// Hangul is 1 UTF-16 unit but 3 bytes each: 100 chars fit exactly.
		{"hangul at limit", strings.Repeat("가", 100), 100, strings.Repeat("가", 100)},
		{"hangul over limit", strings.Repeat("가", 101), 100, strings.Repeat("가", 100) + "..."},
		// An emoji outside the BMP is a surrogate pair: 2 units for 1 rune.
		{"surrogate pair counts twice", strings.Repeat("😀", 3), 4, "😀😀..."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateJS(tc.in, tc.n); got != tc.want {
				t.Errorf("truncateJS(%d units) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

// A cut landing between the halves of a surrogate pair leaves a lone
// surrogate. JavaScript strings permit that, and Node emits U+FFFD when it
// encodes the result as UTF-8 — so parity means reproducing the replacement
// character, not avoiding it.
//
// Verified against the vendored runtime:
//
//	node -e 'process.stdout.write(Buffer.from("😀😀".slice(0,3)+"...").toString("hex"))'
//	f09f9880efbfbd2e2e2e
func TestTruncateJSMatchesNodeOnASplitSurrogatePair(t *testing.T) {
	got := truncateJS("😀😀", 3)
	const wantHex = "f09f9880efbfbd2e2e2e"
	if hex.EncodeToString([]byte(got)) != wantHex {
		t.Errorf("truncateJS = %x, want %s (Node's bytes)", got, wantHex)
	}
}

func TestGroupThousands(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{-4321, "-4,321"},
	}
	for _, tc := range cases {
		if got := groupThousands(tc.in); got != tc.want {
			t.Errorf("groupThousands(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUserListFlagRouting(t *testing.T) {
	// --user is accepted only by the listing commands.
	if !nativeAcceptsFlags("followers", []string{"--user", "123"}) {
		t.Error("followers should accept --user")
	}
	if nativeAcceptsFlags("search", []string{"--user", "123"}) {
		t.Error("search should not accept --user")
	}
	// The value of --user must not be read as a flag.
	if !nativeAcceptsFlags("following", []string{"--user", "123", "--json"}) {
		t.Error("--user value should be skipped, not parsed as a flag")
	}
	if got := parseNativeArgs([]string{"--user", "123"}).user; got != "123" {
		t.Errorf("parsed user = %q, want 123", got)
	}
	if got := parseNativeArgs([]string{"--user=456"}).user; got != "456" {
		t.Errorf("parsed user = %q, want 456", got)
	}
}

func TestNativeUserListPagination(t *testing.T) {
	for _, command := range []string{"following", "followers"} {
		t.Run(command, func(t *testing.T) {
			argv := []string{"--all", "--user", "456", "--max-pages=2", "--cursor=start", "-n", "200", "--plain", "--json"}
			if !nativeAcceptsFlags(command, argv) {
				t.Fatal("paged command should run natively")
			}
			seen := []string{}
			client, err := xapi.NewClient(xapi.Credentials{AuthToken: "test", CT0: "test"})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var v struct {
					UserID string `json:"userId"`
					Count  int
					Cursor string
				}
				if err := json.Unmarshal([]byte(r.URL.Query().Get("variables")), &v); err != nil {
					t.Errorf("decoding variables: %v", err)
				}
				if !strings.HasSuffix(strings.ToLower(r.URL.Path), "/"+command) || v.UserID != "456" || v.Count != 200 {
					t.Errorf("unexpected request %s %+v", r.URL.Path, v)
				}
				seen = append(seen, v.Cursor)
				const user = `{"content":{"itemContent":{"user_results":{"result":{"__typename":"User","rest_id":"%s","legacy":{"screen_name":"name%s"}}}}}}`
				entries := fmt.Sprintf(user, "1", "1")
				next := "second"
				if v.Cursor == "second" {
					entries += "," + fmt.Sprintf(user, "2", "2")
					next = "resume"
				}
				fmt.Fprintf(w, `{"data":{"user":{"result":{"timeline":{"timeline":{"instructions":[{"entries":[%s,{"content":{"cursorType":"Bottom","value":%q}}]}]}}}}}}`, entries, next)
			}))
			defer srv.Close()
			client.SetBaseURL(srv.URL)
			var out bytes.Buffer
			if err := nativeCommands[command](t.Context(), client, parseNativeArgs(argv), &out); err != nil {
				t.Fatal(err)
			}
			var page struct {
				Users      []xapi.ListedUser
				NextCursor string
			}
			if err := json.Unmarshal(out.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(seen, []string{"start", "second"}) || page.NextCursor != "resume" || len(page.Users) != 2 || page.Users[0].ID != "1" || page.Users[1].ID != "2" {
				t.Fatalf("requests %v returned %+v", seen, page)
			}
		})
	}
}

func TestNativeUserListCancelsBetweenPages(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	fetch := func(context.Context, string, int, string) (*xapi.UserListPage, error) {
		calls++
		cancel()
		return &xapi.UserListPage{Users: []xapi.ListedUser{{ID: "1"}}, NextCursor: "next"}, nil
	}
	_, err := fetchAllListedUsers(ctx, fetch, "123", nativeArgs{json: true, all: true})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("got %v after %d requests", err, calls)
	}
}
