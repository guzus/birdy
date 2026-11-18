package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// runNoCookieCLI drives the real root command with an empty HOME, a broken
// BIRDY_ACCOUNTS and a passthrough that fails the test: a no-cookie command
// must never reach the account store or the native engine.
func runNoCookieCLI(t *testing.T, api string, args ...string) (stdout, stderr string, passthrough bool, err error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", "not json: must never be parsed")
	if api != "" {
		t.Setenv(publicAPIEnv, api)
	}
	restoreHook := passthroughHook
	passthroughHook = func(*cobra.Command, []string) error {
		passthrough = true
		return nil
	}
	before := struct {
		account, strategy, vpnServer string
		verbose, bird, vpn           bool
	}{accountFlag, strategyFlag, vpnServerFlag, verboseFlag, birdFlag, vpnFlag}
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		passthroughHook = restoreHook
		accountFlag, strategyFlag, vpnServerFlag = before.account, before.strategy, before.vpnServer
		verboseFlag, birdFlag, vpnFlag = before.verbose, before.bird, before.vpn
		resetProgramFlags()
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
	resetProgramFlags()
	err = execute(args)
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("no-cookie wrote into HOME: %v", entries)
	}
	return out.String(), errOut.String(), passthrough, err
}

type publicAPIStub struct {
	*httptest.Server
	calls atomic.Int32
	last  atomic.Value // *http.Request
}

func newPublicAPIStub(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *publicAPIStub {
	s := &publicAPIStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.last.Store(r.Clone(r.Context()))
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("credentials sent to the public API")
		}
		handle(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestReadNoCookieOutputs(t *testing.T) {
	api := newPublicAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/posts/20" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"id":"20","author_handle":"jack","author_name":"jack","text":"just setting up my twttr","url":"https://x.com/jack/status/20","verified_at":"2026-11-18T03:36:36Z"}`)
	})
	out, _, through, err := runNoCookieCLI(t, api.URL, "read", "20", "--no-cookie", "--plain")
	if err != nil || through {
		t.Fatalf("plain: %v passthrough=%v", err, through)
	}
	if want := "\n@jack (jack):\njust setting up my twttr\nurl: https://x.com/jack/status/20\n"; out != want {
		t.Fatalf("plain output %q, want %q", out, want)
	}
	if strings.Contains(out, "Likes") || strings.Contains(out, "date") {
		t.Fatal("plain output invents counts or a date")
	}

	out, _, _, err = runNoCookieCLI(t, api.URL, "read", "https://x.com/jack/status/20", "--no-cookie", "--json")
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("json: %v %q", err, out)
	}
	if got["id"] != "20" || got["text"] != "just setting up my twttr" || len(got) != 3 {
		t.Fatalf("json keys %v", got)
	}
	for _, absent := range []string{"likeCount", "replyCount", "retweetCount", "createdAt"} {
		if _, ok := got[absent]; ok {
			t.Fatalf("json invents %s", absent)
		}
	}

	out, _, _, err = runNoCookieCLI(t, api.URL, "read", "20", "--no-cookie", "--json-full")
	got = nil
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["url"] != "https://x.com/jack/status/20" || got["source"] != "birdy-public" {
		t.Fatalf("json-full: %v %q", err, out)
	}
	if _, ok := got["viewCount"]; ok {
		t.Fatal("json-full invents counts")
	}

	// The environment turns it on; a bare ID is read too.
	t.Setenv(noCookieEnv, "1")
	if out, _, through, err = runNoCookieCLI(t, api.URL, "20"); err != nil || through || !strings.Contains(out, "@jack") {
		t.Fatalf("env + shorthand: %v %v %q", err, through, out)
	}
	// The flag wins over the environment: back to the account path.
	if _, _, through, err = runNoCookieCLI(t, api.URL, "read", "20", "--no-cookie=false"); err != nil || !through {
		t.Fatalf("--no-cookie=false did not reach the account path: %v %v", err, through)
	}
}

func TestReadNoCookieErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		status           int
		retry            string
	}{
		{name: "not cached", status: 404, body: `{"error":"public post unavailable"}`, want: "not in Birdy's public cache, or X refused it"},
		{name: "rate limited", status: 429, retry: "17", body: `{"error":"60 requests per minute per IP"}`, want: "retry after 17s"},
		{name: "busy", status: 503, retry: "2", body: `{}`, want: "HTTP 503; retry after 2s"},
		{name: "malformed", status: 200, body: `{"id":`, want: "birdy public post"},
		{name: "wrong post", status: 200, body: `{"id":"21","author_handle":"x"}`, want: "invalid response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newPublicAPIStub(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.retry != "" {
					w.Header().Set("Retry-After", tt.retry)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			out, _, through, err := runNoCookieCLI(t, api.URL, "read", "20", "--no-cookie")
			if err == nil || through || out != "" || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v passthrough=%v out=%q, want %q", err, through, out, tt.want)
			}
		})
	}
}

// Commands the public API cannot serve refuse the flag before any network or
// account access, and so do flags that need X data.
func TestNoCookieRefusals(t *testing.T) {
	api := newPublicAPIStub(t, func(w http.ResponseWriter, _ *http.Request) { t.Error("network reached") })
	for _, tt := range []struct {
		args []string
		env  bool
		want string
	}{
		{args: []string{"thread", "20", "--no-cookie"}, want: `"thread" needs X cookies`},
		{args: []string{"user-tweets", "@jack", "--no-cookie"}, want: `"user-tweets" needs X cookies`},
		{args: []string{"home", "--no-cookie"}, want: `"home" needs X cookies`},
		{args: []string{"user-tweets", "@jack"}, env: true, want: "BIRDY_NO_COOKIE=1 (pass --no-cookie=false"},
		{args: []string{"read", "20", "--no-cookie", "--min-likes", "3"}, want: "--min-likes is not available with --no-cookie"},
		{args: []string{"read", "20", "-n", "3", "--no-cookie"}, want: "-n is not available with --no-cookie"},
		{args: []string{"--account", "main", "read", "20", "--no-cookie"}, want: "--account does not apply"},
		{args: []string{"read", "20", "--no-cookie", "--bird"}, want: "--bird does not apply"},
		{args: []string{"read", "20", "--api-url", "https://a.example"}, want: "--api-url only applies with --no-cookie"},
		{args: []string{"read", "20", "--no-cookie", "--api-url", "http://a.example"}, want: "must use https"},
		{args: []string{"read", "20", "--no-cookie", "--api-url=ftp://a.example"}, want: "must use https"},
		{args: []string{"read", "20", "--no-cookie=maybe"}, want: "invalid value for --no-cookie"},
		{args: []string{"search", "x", "--no-cookie", "-n", "101"}, want: "at most 100"},
		{args: []string{"read", "--no-cookie"}, want: "give one post ID or URL"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if tt.env {
				t.Setenv(noCookieEnv, "1")
			}
			_, _, through, err := runNoCookieCLI(t, api.URL, tt.args...)
			if err == nil || through || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v passthrough=%v, want %q", err, through, tt.want)
			}
		})
	}
	if n := api.calls.Load(); n != 0 {
		t.Fatalf("%d network calls", n)
	}
}

func TestSearchNoCookie(t *testing.T) {
	api := newPublicAPIStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/posts" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"posts":[{"id":"21","author_handle":"a","author_name":"A","text":"one"},{"id":"22","author_handle":"b","author_name":"B","text":"two"}],"next_before":"22"}`)
	})
	out, _, through, err := runNoCookieCLI(t, api.URL, "search", "codex limits", "--no-cookie", "-n", "2", "--plain")
	if err != nil || through {
		t.Fatalf("%v %v", err, through)
	}
	req := api.last.Load().(*http.Request)
	if req.URL.Query().Get("q") != "codex limits" || req.URL.Query().Get("limit") != "2" {
		t.Fatalf("query %q", req.URL.RawQuery)
	}
	if !strings.Contains(out, "@a (A):\none\nurl: https://x.com/a/status/21\n") || strings.Count(out, listSeparator) != 2 {
		t.Fatalf("plain %q", out)
	}
	// The default count is search's default.
	if _, _, _, err := runNoCookieCLI(t, api.URL, "search", "x", "--no-cookie", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := api.last.Load().(*http.Request).URL.Query().Get("limit"); got != fmt.Sprint(defaultCountFor("search")) {
		t.Fatalf("default limit %s", got)
	}
	out, _, _, err = runNoCookieCLI(t, api.URL, "search", "x", "--no-cookie", "--json")
	var posts []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &posts) != nil || len(posts) != 2 || posts[1]["id"] != "22" {
		t.Fatalf("json %v %q", err, out)
	}
}

func TestNoCookieHelp(t *testing.T) {
	for _, command := range []string{"read", "search"} {
		var b bytes.Buffer
		renderNativeHelp(&b, birdCommandHelp[command])
		if !strings.Contains(b.String(), "--no-cookie") || !strings.Contains(b.String(), "--api-url") {
			t.Fatalf("%s help lacks the no-cookie flags:\n%s", command, b.String())
		}
	}
	var b bytes.Buffer
	renderNativeHelp(&b, birdCommandHelp["thread"])
	if strings.Contains(b.String(), "--no-cookie") {
		t.Fatal("thread help lists --no-cookie")
	}
}

// The hosted chat tools run reads with model-chosen arguments: the CLI-only
// flags must not pass their validation (an --api-url would point a hosted
// read at any server).
func TestNoCookieFlagsAreCLIOnly(t *testing.T) {
	for _, args := range [][]string{{"20", "--no-cookie"}, {"20", "--api-url", "https://a.example"}, {"20", "--api-url=https://a.example"}} {
		if _, err := chubbyNativeArgs("read", args); err == nil {
			t.Errorf("chat tool accepted %v", args)
		}
		if nativeInvocation(append([]string{"read"}, args...)) != nil {
			t.Errorf("native HTTP invocation accepted %v", args)
		}
	}
}
