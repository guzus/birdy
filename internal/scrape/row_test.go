package scrape

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func TestTweetRowAndCSV(t *testing.T) {
	src := xapi.Tweet{
		ID:        "100",
		Text:      "hello, world",
		CreatedAt: "Wed Aug 05 07:59:09 +0000 2026",
		LikeCount: 3,
		Author:    xapi.Author{Username: "nasa", Name: "NASA"},
		AuthorID:  "1",
		Media:     []xapi.Media{{Type: "photo", URL: "https://pbs.twimg.com/a.jpg"}},
	}
	row := TweetRow(src, "search", "moon", true)
	if row.URL != "https://x.com/nasa/status/100" || row.AuthorUsername != "nasa" || row.TweetURL == "" {
		t.Fatalf("row = %+v", row)
	}
	if len(row.ImageURLs) != 1 || row.ImageURLs[0] != "https://pbs.twimg.com/a.jpg" {
		t.Fatalf("image urls = %v", row.ImageURLs)
	}

	var buf bytes.Buffer
	if err := WriteCSV(&buf, []Row{row, DiagnosticRow("zero-output", "none")}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "id,text,createdAt") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, `"hello, world"`) {
		t.Fatalf("CSV did not quote comma text: %s", out)
	}
	if strings.Contains(out, "zero-output") {
		t.Fatal("diagnostic rows should be omitted from CSV")
	}
}

func TestWriteJSONKeepsZeroCounts(t *testing.T) {
	row := TweetRow(xapi.Tweet{
		ID:        "1",
		Text:      "zero",
		LikeCount: 0,
		Author:    xapi.Author{Username: "nasa"},
	}, "profile", "", false)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, []Row{row}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"likeCount": 0`) {
		t.Fatalf("zero likeCount omitted: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"quoteCount": 0`) {
		t.Fatalf("zero quoteCount omitted: %s", buf.String())
	}
}

func TestWriteJSONEmptyDataset(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Fatalf("empty JSON = %q, want []", buf.String())
	}
}
