package cmd

import (
	"bytes"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const sampleOutput = `@stratechery (Stratechery):
Mythos, Muse, and the Opportunity Cost of Compute

Does Aggregation Theory survive in a world of constrained compute? Yes, insomuch as controlling demand will give power over supply.

https://t.co/f7cUXA8uBg
📅 Mon Apr 13 10:00:49 +0000 2026
🔗 https://x.com/stratechery/status/2043630479690367182
──────────────────────────────────────────────────

@ns123abc (NIK):
NEWS: META is building an AI clone of Zucc

Zucc is personally spending 5-10 hours a week coding AI projects btw https://t.co/R4YfWnsNEz
🖼️ https://pbs.twimg.com/media/HFxhhRgWkAAC6Dg.jpg
🖼️ https://pbs.twimg.com/media/HFxhhRfWUAEVkPn.jpg
📅 Mon Apr 13 09:01:34 +0000 2026
🔗 https://x.com/ns123abc/status/2043615567660105776
──────────────────────────────────────────────────
`

func TestParseBookmarkOutput(t *testing.T) {
	bms := parseBookmarkOutput(sampleOutput)
	if len(bms) != 2 {
		t.Fatalf("want 2 bookmarks, got %d", len(bms))
	}

	b := bms[0]
	if b.Handle != "stratechery" {
		t.Errorf("handle = %q, want stratechery", b.Handle)
	}
	if b.DisplayName != "Stratechery" {
		t.Errorf("display = %q, want Stratechery", b.DisplayName)
	}
	if b.URL != "https://x.com/stratechery/status/2043630479690367182" {
		t.Errorf("url = %q", b.URL)
	}
	if len(b.MediaURLs) != 0 {
		t.Errorf("want 0 media, got %d", len(b.MediaURLs))
	}

	b2 := bms[1]
	if b2.Handle != "ns123abc" {
		t.Errorf("handle = %q, want ns123abc", b2.Handle)
	}
	if len(b2.MediaURLs) != 2 {
		t.Errorf("want 2 media, got %d", len(b2.MediaURLs))
	}
	if b2.Date != "Mon Apr 13 09:01:34 +0000 2026" {
		t.Errorf("date = %q", b2.Date)
	}
}

func TestParseBookmarkOutput_empty(t *testing.T) {
	bms := parseBookmarkOutput("")
	if len(bms) != 0 {
		t.Fatalf("want 0 bookmarks, got %d", len(bms))
	}
}

func TestFormatBookmarkTelegram(t *testing.T) {
	b := parsedBookmark{
		Handle:      "test",
		DisplayName: "Test <User>",
		Body:        "Hello & goodbye <world>",
	}
	got := formatBookmarkTelegram(b)
	want := "<b>@test</b> · Test &lt;User&gt;\n\nHello &amp; goodbye &lt;world&gt;"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProcessBookmarksNativeDryRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"ci","auth_token":"t","ct0":"c"}]`)
	t.Setenv("BIRDY_USE_BIRD", "")
	t.Setenv("BIRDY_BIRD_PATH", filepath.Join(t.TempDir(), "missing-bird"))
	prevDry, prevDigest, prevCount, prevAccount := pbDryRun, pbDigest, pbCount, accountFlag
	t.Cleanup(func() { pbDryRun, pbDigest, pbCount, accountFlag = prevDry, prevDigest, prevCount, prevAccount })
	pbDryRun, pbDigest, pbCount, accountFlag = true, false, "1", "ci"
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = commandRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/Bookmarks") {
			return nil, fmt.Errorf("unexpected operation %s", r.URL.Path)
		}
		const data = `{"data":{"bookmark_timeline_v2":{"timeline":{"instructions":[{"entries":[{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"123","core":{"user_results":{"result":{"rest_id":"9","legacy":{"screen_name":"tester","name":"Tester"}}}},"legacy":{"full_text":"fixture post"}}}}}}]}]}}}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
	})
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&out)
	if err := runProcessBookmarks(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<b>@tester</b> · Tester\n\nfixture post") || !strings.Contains(out.String(), "[View on X → https://x.com/tester/status/123]") {
		t.Fatalf("unexpected preview: %q", out.String())
	}
}
