package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
)

// writeServer captures what birdy sends X and replies with a canned body.
type writeServer struct {
	*httptest.Server
	requests atomic.Int32
	lastBody []byte
	lastPath string
	lastRef  string
}

func newWriteServer(t *testing.T, status int, response string) *writeServer {
	t.Helper()
	ws := &writeServer{}
	ws.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		ws.lastBody = body
		ws.lastPath = r.URL.Path
		ws.lastRef = r.Header.Get("referer")
		w.WriteHeader(status)
		w.Write([]byte(response))
	}))
	t.Cleanup(ws.Close)
	return ws
}

func writeClient(t *testing.T, srv *writeServer) *xapi.Client {
	t.Helper()
	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetBaseURL(srv.URL)
	return c
}

const createdOK = `{"data":{"create_tweet":{"tweet_results":{"result":{"rest_id":"999"}}}}}`

func TestNativeTweetOutput(t *testing.T) {
	srv := newWriteServer(t, 200, createdOK)

	var buf bytes.Buffer
	args := nativeArgs{emoji: true, command: "tweet", positional: "hello world", positionals: []string{"hello world"}}
	if err := nativeTweet(context.Background(), writeClient(t, srv), args, &buf); err != nil {
		t.Fatalf("nativeTweet: %v", err)
	}

	want := "✅ Tweet posted successfully!\n🔗 https://x.com/i/status/999\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}

	// The composer referer is what X checks on a create.
	if srv.lastRef != "https://x.com/compose/post" {
		t.Errorf("referer = %q, want the composer", srv.lastRef)
	}

	var sent struct {
		Variables map[string]any `json:"variables"`
		QueryID   string         `json:"queryId"`
	}
	if err := json.Unmarshal(srv.lastBody, &sent); err != nil {
		t.Fatalf("request body was not JSON: %v", err)
	}
	if sent.Variables["tweet_text"] != "hello world" {
		t.Errorf("tweet_text = %v", sent.Variables["tweet_text"])
	}
	if _, isReply := sent.Variables["reply"]; isReply {
		t.Error("a plain tweet must not carry a reply block")
	}
	if sent.QueryID == "" {
		t.Error("queryId must be in the body; X routes the mutation by it")
	}
}

func TestNativeReplyTargetsTheTweet(t *testing.T) {
	srv := newWriteServer(t, 200, createdOK)

	var buf bytes.Buffer
	args := nativeArgs{
		emoji:       true,
		command:     "reply",
		positionals: []string{"https://x.com/guzus/status/12345", "nice"},
	}
	if err := nativeReply(context.Background(), writeClient(t, srv), args, &buf); err != nil {
		t.Fatalf("nativeReply: %v", err)
	}

	want := "✅ Reply posted successfully!\n🔗 https://x.com/i/status/999\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}

	var sent struct {
		Variables struct {
			Text  string `json:"tweet_text"`
			Reply *struct {
				InReplyTo string `json:"in_reply_to_tweet_id"`
			} `json:"reply"`
		} `json:"variables"`
	}
	json.Unmarshal(srv.lastBody, &sent)
	if sent.Variables.Reply == nil || sent.Variables.Reply.InReplyTo != "12345" {
		t.Errorf("reply target not extracted from the URL: %+v", sent.Variables.Reply)
	}
	if sent.Variables.Text != "nice" {
		t.Errorf("text = %q, want nice", sent.Variables.Text)
	}
}

func TestNativeReplyRequiresBothArguments(t *testing.T) {
	srv := newWriteServer(t, 200, createdOK)
	var buf bytes.Buffer
	args := nativeArgs{command: "reply", positionals: []string{"12345"}}
	if err := nativeReply(context.Background(), writeClient(t, srv), args, &buf); err == nil {
		t.Fatal("expected an error when the reply text is missing")
	}
	if srv.requests.Load() != 0 {
		t.Error("a malformed reply must not reach X")
	}
}

// A create that answers 200 with neither an id nor an error is ambiguous: the
// tweet may exist. It must be reported, never retried.
func TestNativeTweetReportsMissingID(t *testing.T) {
	srv := newWriteServer(t, 200, `{"data":{}}`)
	var buf bytes.Buffer
	args := nativeArgs{command: "tweet", positional: "hi", positionals: []string{"hi"}}

	err := nativeTweet(context.Background(), writeClient(t, srv), args, &buf)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "no ID returned") {
		t.Errorf("error should name the ambiguity, got %v", err)
	}
	if srv.requests.Load() != 1 {
		t.Errorf("a write must not be retried; got %d requests", srv.requests.Load())
	}
}

func TestNativeTweetDoesNotRetryOnError(t *testing.T) {
	srv := newWriteServer(t, 500, `{"errors":[{"message":"boom"}]}`)
	var buf bytes.Buffer
	args := nativeArgs{command: "tweet", positional: "hi", positionals: []string{"hi"}}

	if err := nativeTweet(context.Background(), writeClient(t, srv), args, &buf); err == nil {
		t.Fatal("expected an error")
	}
	if got := srv.requests.Load(); got != 1 {
		t.Errorf("a 500 must not be retried (the post may have landed); got %d requests", got)
	}
}

func TestNativeUnbookmarkReportsEachID(t *testing.T) {
	srv := newWriteServer(t, 200, `{"data":{"tweet_bookmark_delete":"Done"}}`)

	var buf bytes.Buffer
	args := nativeArgs{
		emoji:       true,
		command:     "unbookmark",
		positionals: []string{"1900000000000000001", "https://x.com/a/status/1900000000000000002"},
	}
	if err := nativeUnbookmark(context.Background(), writeClient(t, srv), args, &buf); err != nil {
		t.Fatalf("nativeUnbookmark: %v", err)
	}

	want := "✅ Removed bookmark for 1900000000000000001\n" +
		"✅ Removed bookmark for 1900000000000000002\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestNativeUnbookmarkRequiresAnID(t *testing.T) {
	srv := newWriteServer(t, 200, `{}`)
	var buf bytes.Buffer
	if err := nativeUnbookmark(context.Background(), writeClient(t, srv), nativeArgs{command: "unbookmark"}, &buf); err == nil {
		t.Fatal("expected an error with no ids")
	}
	if srv.requests.Load() != 0 {
		t.Error("no ids means no request")
	}
}

// The mutating commands must reject flags bird does not offer them, so those
// invocations fall back rather than being answered differently. --media
// matters most: birdy has no upload path and must never drop it silently.
func TestWriteCommandsRejectUnsupportedFlags(t *testing.T) {
	for _, command := range []string{"tweet", "reply", "follow", "unfollow", "unbookmark"} {
		if nativeAcceptsFlags(command, []string{"--json"}) {
			t.Errorf("%s should not accept --json natively", command)
		}
		if nativeAcceptsFlags(command, []string{"--media", "a.png"}) {
			t.Errorf("%s must fall back to bird for --media, not drop it", command)
		}
		if !nativeAcceptsFlags(command, []string{"--plain"}) {
			t.Errorf("%s should still accept --plain", command)
		}
	}
}

// The read-only gate lives in passthrough.go, ahead of the engine choice, so it
// covers the native path too. This pins that: making a write native must never
// be a way around BIRDY_READ_ONLY or a read_only account.
func TestReadOnlyGateCoversNativeWrites(t *testing.T) {
	for _, command := range []string{"tweet", "reply", "follow", "unfollow", "unbookmark"} {
		if !nativeSupports(command) {
			t.Fatalf("%s is expected to be native by now", command)
		}
		blocked, name := isMutatingBirdCommand([]string{command, "x"})
		if !blocked || name != command {
			t.Errorf("%s is native but not recognized as mutating; "+
				"it would bypass BIRDY_READ_ONLY", command)
		}

		t.Setenv("BIRDY_READ_ONLY", "1")
		if err := ensureBirdCommandAllowed(nil, []string{command, "x"}); err == nil {
			t.Errorf("%s must be refused in read-only mode", command)
		}

		t.Setenv("BIRDY_READ_ONLY", "")
		readOnly := &store.Account{Name: "ro", ReadOnly: true}
		if err := ensureBirdCommandAllowed(readOnly, []string{command, "x"}); err == nil {
			t.Errorf("%s must be refused for a read-only account", command)
		}
	}
}
