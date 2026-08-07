package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func TestNativeCheckOutput(t *testing.T) {
	var buf bytes.Buffer
	args := nativeArgs{
		emoji:     true,
		command:   "check",
		authToken: "abcdefghijklmnopqrstuvwxyz",
		ct0:       "0123456789abcdef",
	}
	if err := nativeCheck(context.Background(), nil, args, &buf); err != nil {
		t.Fatalf("nativeCheck: %v", err)
	}

	want := "ℹ️ Credential check\n" +
		checkSeparator + "\n" +
		"✅ auth_token: abcdefghij...\n" +
		"✅ ct0: 0123456789...\n" +
		"📍 env AUTH_TOKEN\n" +
		"\n✅ Ready to tweet!\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

// bird draws check's rule with 40 dashes and the tweet list's with 50.
func TestCheckSeparatorWidth(t *testing.T) {
	if n := len([]rune(checkSeparator)); n != 40 {
		t.Errorf("check separator is %d runes, want 40", n)
	}
	if n := len([]rune(listSeparator)); n != 50 {
		t.Errorf("list separator is %d runes, want 50", n)
	}
}

func TestPreviewTruncatesShortSecrets(t *testing.T) {
	if got := preview("short"); got != "short..." {
		t.Errorf("a secret shorter than the preview should not panic, got %q", got)
	}
}

// mentions is a search for "@handle", not a notifications timeline.
func TestNativeMentionsSearchesForTheHandle(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("variables")
		w.Write([]byte(`{"data":{"search_by_raw_query":{"search_timeline":{"timeline":{"instructions":[]}}}}}`))
	}))
	defer srv.Close()

	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetBaseURL(srv.URL)

	var buf bytes.Buffer
	args := nativeArgs{emoji: true, command: "mentions", user: "@steipete", count: 10}
	if err := nativeMentions(context.Background(), c, args, &buf); err != nil {
		t.Fatalf("nativeMentions: %v", err)
	}
	if !strings.Contains(gotQuery, `@steipete`) {
		t.Errorf("search query did not carry the handle: %s", gotQuery)
	}
	if got := buf.String(); got != "No mentions found.\n" {
		t.Errorf("empty wording = %q, want bird's", got)
	}
}

func TestNativeMentionsRejectsABadHandle(t *testing.T) {
	var buf bytes.Buffer
	args := nativeArgs{command: "mentions", user: "not a handle!"}
	err := nativeMentions(context.Background(), nil, args, &buf)
	if err == nil || !strings.Contains(err.Error(), "Invalid --user handle") {
		t.Errorf("got %v, want bird's invalid-handle wording", err)
	}
}

// bird's mentions defaults to 10 where most commands default to 20, and an
// explicit -n must still win.
func TestMentionsCountDefault(t *testing.T) {
	if got := defaultCounts["mentions"]; got != 10 {
		t.Errorf("mentions default = %d, want 10", got)
	}
	if parseNativeArgs(nil).countSet {
		t.Error("no -n means countSet is false")
	}
	if !parseNativeArgs([]string{"-n", "5"}).countSet {
		t.Error("an explicit -n must set countSet so the default cannot override it")
	}
}

// query-ids describes birdy's resolver, not bird's cache; the report must at
// least name every operation birdy ships a hash for.
func TestNativeQueryIDsReportsEveryOperation(t *testing.T) {
	var buf bytes.Buffer
	if err := nativeQueryIDs(context.Background(), nil, nativeArgs{emoji: true, command: "query-ids"}, &buf); err != nil {
		t.Fatalf("nativeQueryIDs: %v", err)
	}
	for _, operation := range []string{"TweetDetail", "CreateTweet", "Following", "AboutAccountQuery"} {
		if !strings.Contains(buf.String(), operation) {
			t.Errorf("report omits %s:\n%s", operation, buf.String())
		}
	}
}

func TestNativeQueryIDsJSON(t *testing.T) {
	var buf bytes.Buffer
	args := nativeArgs{json: true, command: "query-ids"}
	if err := nativeQueryIDs(context.Background(), nil, args, &buf); err != nil {
		t.Fatalf("nativeQueryIDs: %v", err)
	}
	if !strings.Contains(buf.String(), `"operations"`) {
		t.Errorf("json output missing operations: %s", buf.String())
	}
}

// An env override must be reported as the reason birdy picked a hash.
func TestQueryIDSnapshotNamesTheEnvOverride(t *testing.T) {
	t.Setenv("BIRDY_TWEET_DETAIL_QUERY_ID", "OVERRIDE")
	for _, entry := range xapi.QueryIDSnapshot().Operations {
		if entry.Operation != "TweetDetail" {
			continue
		}
		if entry.IDs[0] != "OVERRIDE" {
			t.Errorf("override not first: %v", entry.IDs)
		}
		if entry.Source != "BIRDY_TWEET_DETAIL_QUERY_ID" {
			t.Errorf("source = %q, want the env var name", entry.Source)
		}
		return
	}
	t.Fatal("TweetDetail missing from the snapshot")
}
