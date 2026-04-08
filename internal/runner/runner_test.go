package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/store"
)

func TestBuildEnvReplacesCredentialVars(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "old-auth")
	t.Setenv("CT0", "old-ct0")
	t.Setenv("TWITTER_AUTH_TOKEN", "legacy-auth")
	t.Setenv("TWITTER_CT0", "legacy-ct0")
	t.Setenv("KEEP_ME", "still-here")

	env := buildEnv(&store.Account{
		AuthToken: "new-auth",
		CT0:       "new-ct0",
	})
	joined := strings.Join(env, "\n")

	if strings.Contains(joined, "AUTH_TOKEN=old-auth") {
		t.Fatalf("expected stale AUTH_TOKEN to be removed, got %q", joined)
	}
	if strings.Contains(joined, "CT0=old-ct0") {
		t.Fatalf("expected stale CT0 to be removed, got %q", joined)
	}
	if strings.Contains(joined, "TWITTER_AUTH_TOKEN=legacy-auth") {
		t.Fatalf("expected stale TWITTER_AUTH_TOKEN to be removed, got %q", joined)
	}
	if strings.Contains(joined, "TWITTER_CT0=legacy-ct0") {
		t.Fatalf("expected stale TWITTER_CT0 to be removed, got %q", joined)
	}
	if !strings.Contains(joined, "AUTH_TOKEN=new-auth") || !strings.Contains(joined, "CT0=new-ct0") {
		t.Fatalf("expected new credentials in env, got %q", joined)
	}
	if !strings.Contains(joined, "KEEP_ME=still-here") {
		t.Fatalf("expected unrelated env var preserved, got %q", joined)
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

	exitCode, stdout, stderr, err := RunCapture(account, []string{"home"})
	if err != nil {
		t.Fatalf("RunCapture returned error: %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", exitCode)
	}
	if !strings.Contains(stdout, "stdout:token-a:ct0-a") {
		t.Fatalf("expected stdout to include credentials, got %q", stdout)
	}
	if !strings.Contains(stderr, "stderr:token-a:ct0-a") {
		t.Fatalf("expected stderr to include credentials, got %q", stderr)
	}
}

func TestRunCaptureRejectsNilAccount(t *testing.T) {
	exitCode, stdout, stderr, err := RunCapture(nil, []string{"home"})
	if err == nil {
		t.Fatal("expected nil account to return error")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output on nil account error, got stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(err.Error(), "missing account") {
		t.Fatalf("expected missing account error, got %v", err)
	}
}

func TestRunRejectsNilAccount(t *testing.T) {
	exitCode, err := Run(nil, []string{"home"})
	if err == nil {
		t.Fatal("expected nil account to return error")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(err.Error(), "missing account") {
		t.Fatalf("expected missing account error, got %v", err)
	}
}
