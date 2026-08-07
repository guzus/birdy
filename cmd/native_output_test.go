package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

// These pin the exact bytes birdy prints, because the contract with bird is
// byte-identical output — the birdy skill and TUI parse the human-readable
// form rather than --json. A stray space or a changed emoji is a real break,
// so the expectations are written out in full rather than probed with
// strings.Contains.

func whoamiClient(t *testing.T, body string) *xapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetViewerEndpoints([]string{srv.URL})
	return c
}

func TestNativeWhoamiOutput(t *testing.T) {
	const body = `{"id_str":"1523","screen_name":"guzus","name":"Guzus"}`

	cases := []struct {
		name string
		args nativeArgs
		want string
	}{
		{
			name: "emoji",
			args: nativeArgs{emoji: true, command: "whoami"},
			want: "🙋 @guzus (Guzus)\n🪪 1523\n⚙️ graphql\n🔑 env AUTH_TOKEN\n",
		},
		{
			name: "plain",
			args: nativeArgs{plain: true, command: "whoami"},
			want: "user: @guzus (Guzus)\nuser_id: 1523\nengine: graphql\ncredentials: env AUTH_TOKEN\n",
		},
		{
			name: "no-emoji",
			args: nativeArgs{command: "whoami"},
			want: "User: @guzus (Guzus)\nUser ID: 1523\nEngine: graphql\nCredentials: env AUTH_TOKEN\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := nativeWhoami(context.Background(), whoamiClient(t, body), tc.args, &buf); err != nil {
				t.Fatalf("nativeWhoami: %v", err)
			}
			if buf.String() != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), tc.want)
			}
		})
	}
}

func aboutClient(t *testing.T, profile string) *xapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"user_result_by_screen_name":{"result":{"about_profile":` + profile + `}}}}`))
	}))
	t.Cleanup(srv.Close)

	c, err := xapi.NewClient(xapi.Credentials{AuthToken: "a", CT0: "b"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.SetBaseURL(srv.URL)
	return c
}

func TestNativeAboutOutput(t *testing.T) {
	full := `{"account_based_in":"United States","source":"Verified by X",
		"created_country_accurate":true,"location_accurate":false,
		"learn_more_url":"https://help.x.com/about"}`

	var buf bytes.Buffer
	args := nativeArgs{emoji: true, command: "about", positional: "@guzus"}
	if err := nativeAbout(context.Background(), aboutClient(t, full), args, &buf); err != nil {
		t.Fatalf("nativeAbout: %v", err)
	}

	want := "ℹ️ Account information for @guzus:\n" +
		"  Account based in: United States\n" +
		"  Creation country accurate: Yes\n" +
		"  Location accurate: No\n" +
		"📍 Verified by X\n" +
		"  Learn more: https://help.x.com/about\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

// X omits most of the panel for most accounts. bird prints only the fields it
// received, so a missing boolean must not become a "No".
func TestNativeAboutOmitsUnreportedFields(t *testing.T) {
	var buf bytes.Buffer
	args := nativeArgs{emoji: true, command: "about", positional: "guzus"}
	client := aboutClient(t, `{"account_based_in":"Japan"}`)
	if err := nativeAbout(context.Background(), client, args, &buf); err != nil {
		t.Fatalf("nativeAbout: %v", err)
	}

	want := "ℹ️ Account information for @guzus:\n  Account based in: Japan\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

// location_accurate:false is a reported answer and must print, unlike an
// absent field. This is what the *bool in AboutProfile exists for.
func TestNativeAboutPrintsReportedFalse(t *testing.T) {
	var buf bytes.Buffer
	args := nativeArgs{emoji: true, command: "about", positional: "guzus"}
	client := aboutClient(t, `{"location_accurate":false}`)
	if err := nativeAbout(context.Background(), client, args, &buf); err != nil {
		t.Fatalf("nativeAbout: %v", err)
	}

	want := "ℹ️ Account information for @guzus:\n  Location accurate: No\n"
	if buf.String() != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", buf.String(), want)
	}
}

func TestNativeAboutNormalizesHandle(t *testing.T) {
	var buf bytes.Buffer
	// A profile URL must reduce to the bare handle in the header line.
	args := nativeArgs{emoji: true, command: "about", positional: "https://x.com/guzus"}
	client := aboutClient(t, `{"account_based_in":"Japan"}`)
	if err := nativeAbout(context.Background(), client, args, &buf); err != nil {
		t.Fatalf("nativeAbout: %v", err)
	}
	if got := buf.String(); got != "ℹ️ Account information for @guzus:\n  Account based in: Japan\n" {
		t.Errorf("handle not normalized: %q", got)
	}
}

func TestNativeAboutRequiresUsername(t *testing.T) {
	var buf bytes.Buffer
	err := nativeAbout(context.Background(), aboutClient(t, `{}`), nativeArgs{command: "about"}, &buf)
	if err == nil {
		t.Fatal("expected an error when no username is given")
	}
}

// bird's empty wording for likes is its own; a generic "No tweets found."
// would diverge.
func TestLikesEmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := renderTweets(&buf, nil, nativeArgs{emoji: true, command: "likes"}); err != nil {
		t.Fatalf("renderTweets: %v", err)
	}
	if got := buf.String(); got != "No liked tweets found.\n" {
		t.Errorf("got %q, want %q", got, "No liked tweets found.\n")
	}
}
