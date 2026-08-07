package cmd

import (
	"reflect"
	"strings"
	"testing"
)

// withFreshFlags saves the package-level flag state, zeroes it for the
// test, and restores the originals on cleanup so we don't leak into
// other tests in the package (status_test relies on the cobra default
// of strategyFlag="round-robin").
func withFreshFlags(t *testing.T) {
	t.Helper()
	origAccount := accountFlag
	origStrategy := strategyFlag
	origVerbose := verboseFlag
	t.Cleanup(func() {
		accountFlag = origAccount
		strategyFlag = origStrategy
		verboseFlag = origVerbose
	})
	accountFlag = ""
	strategyFlag = ""
	verboseFlag = false
}

func TestApplyBirdyGlobalFlags_NoFlags(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"@handle", "--max-pages", "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"@handle", "--max-pages", "3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if accountFlag != "" || strategyFlag != "" || verboseFlag {
		t.Fatalf("flags should be untouched, got account=%q strategy=%q verbose=%v",
			accountFlag, strategyFlag, verboseFlag)
	}
}

func TestApplyBirdyGlobalFlags_LongAccount(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"--account", "alt4", "@handle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"@handle"}) {
		t.Fatalf("expected [@handle], got %v", got)
	}
	if accountFlag != "alt4" {
		t.Fatalf("expected accountFlag=alt4, got %q", accountFlag)
	}
}

func TestApplyBirdyGlobalFlags_ShortAccount(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"-a", "main", "user-tweets", "@x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"user-tweets", "@x"}) {
		t.Fatalf("expected [user-tweets @x], got %v", got)
	}
	if accountFlag != "main" {
		t.Fatalf("expected accountFlag=main, got %q", accountFlag)
	}
}

func TestApplyBirdyGlobalFlags_EqualsForm(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"--account=alt4", "--strategy=least-used", "@h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"@h"}) {
		t.Fatalf("expected [@h], got %v", got)
	}
	if accountFlag != "alt4" {
		t.Fatalf("expected accountFlag=alt4, got %q", accountFlag)
	}
	if strategyFlag != "least-used" {
		t.Fatalf("expected strategyFlag=least-used, got %q", strategyFlag)
	}
}

func TestApplyBirdyGlobalFlags_VerboseBool(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"-v", "user-tweets", "@h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"user-tweets", "@h"}) {
		t.Fatalf("expected [user-tweets @h], got %v", got)
	}
	if !verboseFlag {
		t.Fatalf("expected verboseFlag=true")
	}
}

func TestApplyBirdyGlobalFlags_VerboseEquals(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{"--verbose=true", "@h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"@h"}) {
		t.Fatalf("expected [@h], got %v", got)
	}
	if !verboseFlag {
		t.Fatalf("expected verboseFlag=true")
	}

	withFreshFlags(t)
	if _, err := applyBirdyGlobalFlags([]string{"--verbose=false"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verboseFlag {
		t.Fatalf("expected verboseFlag=false")
	}
}

func TestApplyBirdyGlobalFlags_VerboseEqualsInvalid(t *testing.T) {
	withFreshFlags(t)
	_, err := applyBirdyGlobalFlags([]string{"--verbose=maybe"})
	if err == nil {
		t.Fatal("expected error for --verbose=maybe")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--verbose") || !strings.Contains(msg, "maybe") {
		t.Errorf("expected error message to mention --verbose and the bad value, got %q", msg)
	}
}

func TestApplyBirdyGlobalFlags_EmptyValueRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
	}{
		{"empty-account", []string{"--account", "", "user-tweets", "@h"}, "--account"},
		{"empty-strategy", []string{"-s", "", "@h"}, "-s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFreshFlags(t)
			_, err := applyBirdyGlobalFlags(tc.args)
			if err == nil {
				t.Fatalf("expected error for %s with empty value", tc.flag)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("expected error to mention %s, got %q", tc.flag, err.Error())
			}
		})
	}
}

func TestApplyBirdyGlobalFlags_MixedFlags(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{
		"--account", "alt4",
		"-v",
		"user-tweets",
		"@handle",
		"--max-pages", "3",
		"--json",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"user-tweets", "@handle", "--max-pages", "3", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if accountFlag != "alt4" || !verboseFlag {
		t.Fatalf("expected account=alt4 verbose=true, got account=%q verbose=%v",
			accountFlag, verboseFlag)
	}
}

func TestApplyBirdyGlobalFlags_MissingValue(t *testing.T) {
	withFreshFlags(t)
	if _, err := applyBirdyGlobalFlags([]string{"--account"}); err == nil {
		t.Fatal("expected error for trailing --account")
	}
	withFreshFlags(t)
	if _, err := applyBirdyGlobalFlags([]string{"-s"}); err == nil {
		t.Fatal("expected error for trailing -s")
	}
}

func TestApplyBirdyGlobalFlags_DoubleDashPassthrough(t *testing.T) {
	// Anything after `--` must pass through unchanged, even if it looks
	// like one of our flags. This lets users forward an --account arg
	// to bird if bird ever adds such a flag.
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{
		"user-tweets", "@h", "--", "--account", "weird",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"user-tweets", "@h", "--", "--account", "weird"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if accountFlag != "" {
		t.Fatalf("expected accountFlag untouched, got %q", accountFlag)
	}
}

func TestApplyBirdyGlobalFlags_UnknownFlagsPassThrough(t *testing.T) {
	withFreshFlags(t)
	got, err := applyBirdyGlobalFlags([]string{
		"--max-pages", "5", "--json", "@h",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--max-pages", "5", "--json", "@h"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseBoolFlag(t *testing.T) {
	cases := []struct {
		in   string
		want bool
		ok   bool
	}{
		{"true", true, true},
		{"TRUE", true, true},
		{"1", true, true},
		{"yes", true, true},
		{"on", true, true},
		{"false", false, true},
		{"0", false, true},
		{"no", false, true},
		{"off", false, true},
		{" True ", true, true},
		{"maybe", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		got, ok := parseBoolFlag(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseBoolFlag(%q) = (%v,%v), want (%v,%v)",
				tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Every command bird knows must be a registered cobra command, so it gets
// DisableFlagParsing (which is what protects the post-subcommand flag region)
// and a help entry. `help` is excluded: cobra owns that name.
func TestBirdCommandsCoverBirdKnownCommands(t *testing.T) {
	// third_party/@steipete/bird/dist/cli/program.js:19-44.
	known := []string{
		"tweet", "activity", "reply", "query-ids", "read", "replies", "thread",
		"search", "mentions", "bookmarks", "unbookmark", "follow", "unfollow",
		"following", "followers", "likes", "lists", "list-timeline", "home",
		"user-tweets", "news", "trending", "whoami", "check",
	}
	for _, name := range known {
		found, _, err := rootCmd.Find([]string{name})
		if err != nil || found == rootCmd || found.Name() != name {
			t.Errorf("%q is a bird command but is not registered on rootCmd", name)
		}
	}
}
