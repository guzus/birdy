package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const streamHelperEnv = "BIRDY_STREAM_HELPER"

func TestBuildArgsUsesExactDefaultToolPermissions(t *testing.T) {
	args := BuildArgs("hello", "sonnet", "birdy", ToolPermissions{})

	if got := argumentValue(t, args, "--allowedTools"); got != "Bash(birdy *),Skill(birdy)" {
		t.Fatalf("unexpected default allowed tools %q", got)
	}
}

func TestBuildArgsAddsOnlyWebSearchWhenPermitted(t *testing.T) {
	args := BuildArgs("hello", "sonnet", "birdy --strategy random", ToolPermissions{WebSearch: true})

	if got := argumentValue(t, args, "--allowedTools"); got != "Bash(birdy --strategy random *),Skill(birdy),WebSearch" {
		t.Fatalf("unexpected bird-box allowed tools %q", got)
	}
}

func TestBuildNoToolsArgsHasNoBirdyOrGeneralToolCapability(t *testing.T) {
	args := BuildNoToolsArgs("untrusted context", "sonnet", "read-only system prompt")

	if got := argumentValue(t, args, "--tools"); got != "" {
		t.Fatalf("expected tools disabled, got %q", got)
	}
	if got := argumentValue(t, args, "--max-turns"); got != "1" {
		t.Fatalf("expected one model turn, got %q", got)
	}
	joined := strings.Join(args, " ")
	for _, forbidden := range []string{"--allowedTools", "Bash(", "Skill(birdy)", "danger-full-access", "tweet <", "whoami"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("no-tools invocation contains %q: %#v", forbidden, args)
		}
	}
}

func TestNoToolsEnvironmentRemovesXAndHarnessSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=model-provider-secret",
		"BIRDY_ACCOUNTS=x-cookie-pool",
		"BIRDY_HARNESS_ACCOUNTS=harness-x-cookie-pool",
		"BIRDY_HOST_INVITE_CODE=shared-invite",
		"BIRDY_HOST_TOKEN=legacy-invite",
		"BIRDY_HARNESS_TOKEN_HASHES=install-hashes",
		"AUTH_TOKEN=x-auth",
		"CT0=x-csrf",
		"TWITTER_AUTH_TOKEN=legacy-x-auth",
		"TWITTER_CT0=legacy-x-csrf",
	}
	got := strings.Join(withoutSensitiveRuntimeEnv(env), "\n")
	if !strings.Contains(got, "ANTHROPIC_API_KEY=model-provider-secret") || !strings.Contains(got, "PATH=/usr/bin") {
		t.Fatalf("required model environment removed: %q", got)
	}
	for _, secret := range []string{"BIRDY_ACCOUNTS=", "BIRDY_HARNESS_ACCOUNTS=", "BIRDY_HOST_INVITE_CODE=", "BIRDY_HOST_TOKEN=", "BIRDY_HARNESS_TOKEN_HASHES=", "AUTH_TOKEN=", "CT0=", "TWITTER_AUTH_TOKEN=", "TWITTER_CT0="} {
		if strings.Contains(got, secret) {
			t.Fatalf("sensitive runtime environment retained %q in %q", secret, got)
		}
	}
}

func argumentValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for i, arg := range args {
		if arg == name {
			if i+1 == len(args) {
				t.Fatalf("%s has no value in %#v", name, args)
			}
			return args[i+1]
		}
	}
	t.Fatalf("%s not found in %#v", name, args)
	return ""
}

func TestStreamCommandReportsFailureAfterPartialOutput(t *testing.T) {
	events := runStreamHelper(t, "partial-failure")

	if !hasEvent(events, EventSnapshot, "partial response") {
		t.Fatalf("expected partial snapshot, got %#v", events)
	}
	if !hasEvent(events, EventError, "E2B transport failed") {
		t.Fatalf("expected runner failure after partial output, got %#v", events)
	}
	if events[len(events)-1].Type != EventDone {
		t.Fatalf("expected final done event, got %#v", events)
	}
}

func TestStreamCommandReportsFailureAfterTerminalResult(t *testing.T) {
	events := runStreamHelper(t, "terminal-failure")

	if !hasEvent(events, EventSnapshot, "complete response") {
		t.Fatalf("expected terminal result snapshot, got %#v", events)
	}
	if !hasEvent(events, EventError, "sandbox cleanup failed") {
		t.Fatalf("expected cleanup failure before done, got %#v", events)
	}
	if events[len(events)-1].Type != EventDone {
		t.Fatalf("expected final done event, got %#v", events)
	}
}

func runStreamHelper(t *testing.T, mode string) []Event {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestStreamCommandHelperProcess")
	cmd.Env = append(os.Environ(), streamHelperEnv+"="+mode)

	var events []Event
	StreamCommand(context.Background(), cmd, func(event Event) {
		events = append(events, event)
	})
	return events
}

func hasEvent(events []Event, eventType EventType, contains string) bool {
	for _, event := range events {
		if event.Type == eventType && strings.Contains(event.Text+event.Error, contains) {
			return true
		}
	}
	return false
}

func TestStreamCommandHelperProcess(t *testing.T) {
	switch os.Getenv(streamHelperEnv) {
	case "partial-failure":
		fmt.Fprintln(os.Stdout, `{"type":"assistant","message":{"content":[{"type":"text","text":"partial response"}]}}`)
		fmt.Fprintln(os.Stderr, "E2B transport failed")
		os.Exit(7)
	case "terminal-failure":
		fmt.Fprintln(os.Stdout, `{"type":"result","result":"complete response"}`)
		fmt.Fprintln(os.Stderr, "sandbox cleanup failed")
		os.Exit(8)
	}
}
