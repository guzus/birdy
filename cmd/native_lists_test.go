package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func TestParseActivityTypes(t *testing.T) {
	// bird prints sections in a fixed order regardless of --types order.
	got, err := parseActivityTypes("quotes,likes")
	if err != nil {
		t.Fatalf("parseActivityTypes: %v", err)
	}
	if strings.Join(got, ",") != "likes,quotes" {
		t.Errorf("got %v, want the canonical print order", got)
	}

	// Aliases and duplicates collapse.
	got, err = parseActivityTypes("retweet,reposts,LIKER")
	if err != nil {
		t.Fatalf("parseActivityTypes: %v", err)
	}
	if strings.Join(got, ",") != "likes,reposts" {
		t.Errorf("aliases did not collapse: %v", got)
	}

	// Empty means everything.
	got, _ = parseActivityTypes("")
	if len(got) != 3 {
		t.Errorf("default should select all three, got %v", got)
	}

	if _, err := parseActivityTypes("nonsense"); err == nil {
		t.Error("expected an error for an unknown type")
	}
}

// activity's user block is deliberately not the followers listing: a counted
// heading, a profile URL per user, and no separator.
func TestRenderActivityUsersShape(t *testing.T) {
	var buf bytes.Buffer
	users := []xapi.ListedUser{{Username: "guzus", Name: "Guzus", Description: "builder", FollowersCount: intPtr(1234)}}
	renderActivityUsers(&buf, "Likes", users, nativeArgs{emoji: true})

	want := "\nLikes (1)\n" +
		"@guzus (Guzus)\n" +
		"  builder\n" +
		"  ℹ️ 1,234 followers\n" +
		"  🔗 https://x.com/guzus\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
	if strings.Contains(buf.String(), listSeparator) {
		t.Error("activity blocks must not carry the list separator")
	}
}

func TestRenderActivityUsersEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderActivityUsers(&buf, "Reposts", nil, nativeArgs{emoji: true})
	if got := buf.String(); got != "\nReposts (0)\nNo users found.\n" {
		t.Errorf("got %q", got)
	}
}

// bird's --json for activity always carries arrays, never null.
func TestActivityReportNormalizesNilSlices(t *testing.T) {
	var report activityReport
	report.normalize()
	if report.Likes.Users == nil || report.Reposts.Users == nil || report.Quotes.Tweets == nil {
		t.Error("nil slices must become empty so the JSON has [] not null")
	}
}

func TestListsDefaultCount(t *testing.T) {
	if got := defaultCounts["lists"]; got != 100 {
		t.Errorf("lists default = %d, want 100 (bird's)", got)
	}
}

func TestListsFlagRouting(t *testing.T) {
	if !nativeAcceptsFlags("lists", []string{"--member-of"}) {
		t.Error("lists should accept --member-of")
	}
	if nativeAcceptsFlags("search", []string{"--member-of"}) {
		t.Error("--member-of is only meaningful for lists")
	}
	if !nativeAcceptsFlags("activity", []string{"--types", "likes"}) {
		t.Error("activity should accept --types")
	}
	if !parseNativeArgs([]string{"--member-of"}).memberOf {
		t.Error("--member-of not parsed")
	}
	if got := parseNativeArgs([]string{"--types", "likes"}).types; got != "likes" {
		t.Errorf("types = %q", got)
	}
}
