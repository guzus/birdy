package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/envcheck"
	"github.com/guzus/birdy/internal/store"
)

func TestBuildEnvReplacesCredentialVars(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "old-auth")
	t.Setenv("CT0", "old-ct0")
	t.Setenv("TWITTER_AUTH_TOKEN", "legacy-auth")
	t.Setenv("TWITTER_CT0", "legacy-ct0")
	t.Setenv("OPENCODE_API_KEY", "must-not-reach-bird")
	t.Setenv("CHUBBY_API_KEY", "must-not-reach-bird")
	t.Setenv("KEEP_ME", "still-here")

	env := buildEnv(&store.Account{
		AuthToken: "new-auth",
		CT0:       "new-ct0",
	})
	// buildEnv starts from the real process environment: report names only.
	if leaked := envcheck.Present(env, "TWITTER_AUTH_TOKEN", "TWITTER_CT0", "OPENCODE_API_KEY", "CHUBBY_API_KEY"); len(leaked) > 0 {
		t.Fatalf("stale or unrelated credentials reached bird subprocess: %v", leaked)
	}
	for _, name := range []string{"AUTH_TOKEN", "CT0"} {
		count := 0
		for _, entry := range env {
			if strings.HasPrefix(entry, name+"=") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one %s, found %d", name, count)
		}
	}
	auth, _ := envcheck.Value(env, "AUTH_TOKEN")
	ct0, _ := envcheck.Value(env, "CT0")
	if auth != "new-auth" || ct0 != "new-ct0" {
		t.Fatal("expected the account's new credentials in env")
	}
	if keep, _ := envcheck.Value(env, "KEEP_ME"); keep != "still-here" {
		t.Fatalf("expected unrelated env var preserved; names: %v", envcheck.Names(env))
	}
}

func TestFindBirdUsesTrimmedOverride(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	if err := os.WriteFile(birdPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}

	t.Setenv("BIRDY_BIRD_PATH", "  "+birdPath+"  ")
	got, err := findBird()
	if err != nil {
		t.Fatalf("findBird returned error: %v", err)
	}
	if got != birdPath {
		t.Fatalf("expected trimmed override path, got %q", got)
	}
}

func TestRunCaptureReturnsExitCodeAndOutput(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo \"stdout:$AUTH_TOKEN:$CT0\"",
		"echo \"stderr:$AUTH_TOKEN:$CT0\" 1>&2",
		"exit 7",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}

	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	account := &store.Account{Name: "alpha", AuthToken: "token-a", CT0: "ct0-a"}

	res, stdout, stderr, err := RunCapture(account, []string{"home"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", res.ExitCode)
	}
	if res.RateLimited {
		t.Fatalf("expected RateLimited=false on clean stderr")
	}
	if !strings.Contains(stdout, "stdout:token-a:ct0-a") {
		t.Fatalf("expected stdout to include credentials, got %q", stdout)
	}
	if !strings.Contains(stderr, "stderr:token-a:ct0-a") {
		t.Fatalf("expected stderr to include credentials, got %q", stderr)
	}
}

func TestRunCaptureRejectsNilAccount(t *testing.T) {
	res, stdout, stderr, err := RunCapture(nil, []string{"home"})
	if err == nil {
		t.Fatal("expected nil account to return error")
	}
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", res.ExitCode)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output on nil account error, got stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(err.Error(), "missing account") {
		t.Fatalf("expected missing account error, got %v", err)
	}
}

func TestRunRejectsNilAccount(t *testing.T) {
	res, err := Run(nil, []string{"home"})
	if err == nil {
		t.Fatal("expected nil account to return error")
	}
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", res.ExitCode)
	}
	if !strings.Contains(err.Error(), "missing account") {
		t.Fatalf("expected missing account error, got %v", err)
	}
}

func TestRunCaptureDetectsHTTP429(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo \"some output\"",
		"echo \"✗ Failed to fetch tweets: HTTP 429: rate limit exceeded\" 1>&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	account := &store.Account{Name: "alpha", AuthToken: "t", CT0: "c"}

	res, _, stderr, err := RunCapture(account, []string{"user-tweets", "@x"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if !res.RateLimited {
		t.Fatalf("expected RateLimited=true on HTTP 429 in stderr, got false; stderr=%q", stderr)
	}
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", res.ExitCode)
	}
}

func TestRunCaptureDoesNotMatchHTTP429InStdout(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	// HTTP 429 only on stdout — should NOT flag as rate-limited.
	// (Mention in JSON payload, error_messages array, etc.)
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo '{\"error\":\"HTTP 429: rate limit\"}'",
		"echo \"normal stderr message\" 1>&2",
		"exit 0",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	account := &store.Account{Name: "alpha", AuthToken: "t", CT0: "c"}

	res, _, _, err := RunCapture(account, []string{"home"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if res.RateLimited {
		t.Fatalf("expected RateLimited=false when 429 is only on stdout")
	}
}

func TestRunCaptureDetectsNotFound(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	// The native engine's wording for a missing user (internal/xapi/user.go),
	// as it appears on stderr when birdy itself is the subprocess.
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo 'Error: x api: user \"CohereAI\" not found' 1>&2",
		"exit 1",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	account := &store.Account{Name: "alpha", AuthToken: "t", CT0: "c"}

	res, _, stderr, err := RunCapture(account, []string{"user-tweets", "@CohereAI"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if !res.NotFound {
		t.Fatalf("expected NotFound=true, stderr=%q", stderr)
	}
	if res.RateLimited {
		t.Fatalf("expected RateLimited=false on a not-found error")
	}
	if res.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", res.ExitCode)
	}
}

func TestRunCaptureDoesNotMatchNotFoundInStdout(t *testing.T) {
	birdPath := filepath.Join(t.TempDir(), "bird")
	script := strings.Join([]string{
		"#!/bin/sh",
		"echo '[{\"text\":\"page not found lol\"}]'",
		"exit 0",
	}, "\n")
	if err := os.WriteFile(birdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)
	account := &store.Account{Name: "alpha", AuthToken: "t", CT0: "c"}

	res, _, _, err := RunCapture(account, []string{"search", "x"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if res.NotFound {
		t.Fatalf("expected NotFound=false when the marker is only on stdout")
	}
}

func TestIsNotFoundMessage(t *testing.T) {
	cases := map[string]bool{
		`x api: user "CohereAI" not found`:                      true,
		"Tweet not found":                                       true,
		"User not found":                                        true,
		"HTTP 429: rate limit exceeded":                         false,
		"bird CLI not found, and --bird requires it.":           false,
		"Error: bird CLI not found\nbirdy no longer ships bird": false,
		"": false,
	}
	for msg, want := range cases {
		if got := IsNotFoundMessage([]byte(msg)); got != want {
			t.Errorf("IsNotFoundMessage(%q) = %v, want %v", msg, got, want)
		}
	}
}
