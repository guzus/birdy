package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/claude"
)

const helperModeEnv = "BIRDY_OPENCODE_TEST_HELPER"

func TestBuildNoToolsArgsPinsProviderAndHasNoPromptOrToolApproval(t *testing.T) {
	args := BuildNoToolsArgs()
	joined := strings.Join(args, " ")
	for _, required := range []string{"run", "--pure", "--model " + ModelDeepSeekV4Flash, "--agent birdy-harness", "--format json"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %q in %#v", required, args)
		}
	}
	for _, forbidden := range []string{"untrusted tweet", "--auto", "--share", "--continue", "--session", "--attach"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden argument %q in %#v", forbidden, args)
		}
	}
}

func TestBuildConfigPinsRouteAndDeniesEveryTool(t *testing.T) {
	raw, err := buildConfig("system boundary")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != ModelDeepSeekV4Flash || got["small_model"] != ModelDeepSeekV4Flash || got["share"] != "disabled" {
		t.Fatalf("route/share not pinned: %s", raw)
	}
	providers := got["enabled_providers"].([]any)
	if len(providers) != 1 || providers[0] != ProviderOpenCodeGo {
		t.Fatalf("provider allowlist not exact: %s", raw)
	}
	if got["permission"].(map[string]any)["*"] != "deny" {
		t.Fatalf("global wildcard permission is not denied: %s", raw)
	}
	agent := got["agent"].(map[string]any)["birdy-harness"].(map[string]any)
	if agent["model"] != ModelDeepSeekV4Flash || agent["prompt"] != "system boundary" || agent["steps"] != float64(1) || agent["permission"].(map[string]any)["*"] != "deny" {
		t.Fatalf("agent boundary not exact: %s", raw)
	}
}

func TestInstalledOpenCodeAcceptsEffectiveConfig(t *testing.T) {
	executable, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode is not installed in this test environment")
	}
	runtimeDir := t.TempDir()
	raw, err := buildConfig("system boundary")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(runtimeDir, "opencode.json")
	if err := os.WriteFile(configPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "--pure", "debug", "config")
	cmd.Dir = runtimeDir
	cmd.Env = isolatedEnvironment([]string{"PATH=" + os.Getenv("PATH"), apiKeyEnv + "=fake-key"}, runtimeDir, configPath)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("opencode rejected isolated config: %v", err)
	}
	var effective map[string]any
	if err := json.Unmarshal(output, &effective); err != nil {
		t.Fatalf("decode effective config: %v", err)
	}
	if effective["model"] != ModelDeepSeekV4Flash || effective["share"] != "disabled" || effective["permission"].(map[string]any)["*"] != "deny" {
		t.Fatalf("unexpected effective config: %s", output)
	}
	agent := effective["agent"].(map[string]any)["birdy-harness"].(map[string]any)
	if agent["model"] != ModelDeepSeekV4Flash || agent["steps"] != float64(1) || agent["permission"].(map[string]any)["*"] != "deny" {
		t.Fatalf("unexpected effective agent config: %s", output)
	}
}

func TestStreamCommandMapsCompletedTextPartsInOrder(t *testing.T) {
	events := runHelper(t, "success")
	if len(events) != 3 || events[0] != (claude.Event{Type: claude.EventSnapshot, Text: "first"}) || events[1] != (claude.Event{Type: claude.EventSnapshot, Text: "first second"}) || events[2].Type != claude.EventDone {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestStreamCommandFailsClosedOnProtocolViolations(t *testing.T) {
	for _, mode := range []string{"malformed", "unknown", "duplicate-finish", "duplicate-completed-part", "mixed-session", "mixed-message", "nested-session-mismatch", "text-before-start", "incomplete-text", "whitespace-text", "mismatched-part", "error-secret"} {
		t.Run(mode, func(t *testing.T) {
			events := runHelper(t, mode)
			if !hasType(events, claude.EventError) || events[len(events)-1].Type != claude.EventDone {
				t.Fatalf("protocol violation did not fail closed: %#v", events)
			}
			serialized, _ := json.Marshal(events)
			if strings.Contains(string(serialized), "secret-auth-token") {
				t.Fatalf("provider error payload leaked: %s", serialized)
			}
		})
	}
}

func TestStreamCommandRejectsToolUse(t *testing.T) {
	events := runHelper(t, "tool")
	if !hasType(events, claude.EventToolUse) || events[len(events)-1].Type != claude.EventDone {
		t.Fatalf("tool use was not surfaced to the harness blocker: %#v", events)
	}
}

func TestStreamCommandReportsNonzeroExitAfterPartialOutput(t *testing.T) {
	events := runHelper(t, "partial-failure")
	if !hasType(events, claude.EventSnapshot) || !hasType(events, claude.EventError) || events[len(events)-1].Type != claude.EventDone {
		t.Fatalf("partial process failure lost its terminal error: %#v", events)
	}
}

func TestStreamCommandHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd := helperCommand(ctx, "block")
	started := time.Now()
	var events []claude.Event
	streamCommand(ctx, cmd, func(event claude.Event) { events = append(events, event) })
	if time.Since(started) > 2*time.Second || len(events) != 1 || events[0].Type != claude.EventDone {
		t.Fatalf("cancellation did not terminate promptly and cleanly: elapsed=%s events=%#v", time.Since(started), events)
	}
}

func TestStreamNoToolsUsesAndRemovesIsolatedRuntime(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	homePath := filepath.Join(dir, "home")
	stdinPath := filepath.Join(dir, "stdin")
	configPath := filepath.Join(dir, "config")
	executable := filepath.Join(dir, "fake-opencode")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$@" > %q
env > %q
printf '%%s' "$HOME" > %q
cat > %q
mode=$(stat -c '%%a' "$OPENCODE_CONFIG" 2>/dev/null || stat -f '%%Lp' "$OPENCODE_CONFIG")
[ "$mode" = 600 ]
cp "$OPENCODE_CONFIG" %q
printf '%%s\n' '{"type":"step_start","timestamp":1,"sessionID":"ses_fixture","part":{"id":"prt_1","sessionID":"ses_fixture","messageID":"msg_assistant","type":"step-start"}}'
printf '%%s\n' '{"type":"text","timestamp":2,"sessionID":"ses_fixture","part":{"id":"prt_2","sessionID":"ses_fixture","messageID":"msg_assistant","type":"text","text":"ok","time":{"start":1,"end":2}}}'
printf '%%s\n' '{"type":"step_finish","timestamp":3,"sessionID":"ses_fixture","part":{"id":"prt_3","sessionID":"ses_fixture","messageID":"msg_assistant","type":"step-finish"}}'
`, argsPath, envPath, homePath, stdinPath, configPath)
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	priorExecutable := executablePath
	executablePath = executable
	defer func() { executablePath = priorExecutable }()
	t.Setenv(apiKeyEnv, "fake-opencode-provider-key")
	t.Setenv("ANTHROPIC_API_KEY", "secret-anthropic")
	t.Setenv("BIRDY_ACCOUNTS", "secret-x-pool")
	t.Setenv("BIRDY_HARNESS_TOKEN_HASHES", "secret-install-hashes")
	t.Setenv("AUTH_TOKEN", "secret-x-auth")
	var events []claude.Event
	StreamNoTools(context.Background(), "untrusted tweet payload", "system boundary", func(event claude.Event) {
		events = append(events, event)
	})
	if !hasType(events, claude.EventSnapshot) || events[len(events)-1].Type != claude.EventDone {
		t.Fatalf("fake isolated run failed: %#v", events)
	}

	args := mustRead(t, argsPath)
	if strings.Contains(args, "untrusted tweet payload") || !strings.Contains(args, "--pure") || strings.Contains(args, "--auto") {
		t.Fatalf("unsafe argv: %q", args)
	}
	if got := mustRead(t, stdinPath); got != "untrusted tweet payload" {
		t.Fatalf("prompt was not sent only over stdin: %q", got)
	}
	env := mustRead(t, envPath)
	for _, forbidden := range []string{"untrusted tweet payload", "system boundary", "ANTHROPIC_API_KEY=", "BIRDY_ACCOUNTS=", "BIRDY_HARNESS_TOKEN_HASHES=", "AUTH_TOKEN=", "OPENCODE_CONFIG_CONTENT="} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("isolated environment leaked %q", forbidden)
		}
	}
	if !strings.Contains(env, apiKeyEnv+"=fake-opencode-provider-key") || !strings.Contains(env, "OPENCODE_CONFIG=") {
		t.Fatalf("required isolated environment missing: %q", env)
	}
	if !strings.Contains(mustRead(t, configPath), `"permission":{"*":"deny"}`) {
		t.Fatal("copied effective config did not deny all tools")
	}
	runtimeHome := mustRead(t, homePath)
	if _, err := os.Stat(runtimeHome); !os.IsNotExist(err) {
		t.Fatalf("disposable runtime still exists: %q err=%v", runtimeHome, err)
	}
}

func TestLiveOpenCodeGoCanary(t *testing.T) {
	if os.Getenv("BIRDY_LIVE_OPENCODE_CANARY") != "1" {
		t.Skip("set BIRDY_LIVE_OPENCODE_CANARY=1 with OPENCODE_API_KEY to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	counts := make(map[claude.EventType]int)
	StreamNoTools(ctx, "Reply with exactly OK.", "Answer with only the requested literal. No tools are available.", func(event claude.Event) {
		counts[event.Type]++
	})
	t.Logf("event_counts token=%d snapshot=%d error=%d tool_use=%d done=%d", counts[claude.EventToken], counts[claude.EventSnapshot], counts[claude.EventError], counts[claude.EventToolUse], counts[claude.EventDone])
	if ctx.Err() != nil || counts[claude.EventSnapshot] == 0 || counts[claude.EventError] != 0 || counts[claude.EventToolUse] != 0 || counts[claude.EventDone] != 1 {
		t.Fatalf("live canary failed; aggregate event contract only")
	}
}

func runHelper(t *testing.T, mode string) []claude.Event {
	t.Helper()
	cmd := helperCommand(context.Background(), mode)
	var events []claude.Event
	streamCommand(context.Background(), cmd, func(event claude.Event) { events = append(events, event) })
	return events
}

func helperCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestOpenCodeHelperProcess", "--")
	cmd.Env = append(os.Environ(), helperModeEnv+"="+mode)
	return cmd
}

func hasType(events []claude.Event, want claude.EventType) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOpenCodeHelperProcess(t *testing.T) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, eventJSON("text", 2, "ses_fixture", "text", "first"))
		fmt.Fprintln(os.Stdout, eventJSON("text", 3, "ses_fixture", "text", " second"))
		fmt.Fprintln(os.Stdout, eventJSON("step_finish", 4, "ses_fixture", "step-finish", ""))
	case "malformed":
		fmt.Fprintln(os.Stdout, `{not-json}`)
	case "unknown":
		fmt.Fprintln(os.Stdout, eventJSON("status", 1, "ses_fixture", "status", ""))
	case "duplicate-finish":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, eventJSON("text", 2, "ses_fixture", "text", "first"))
		fmt.Fprintln(os.Stdout, eventJSON("step_finish", 3, "ses_fixture", "step-finish", ""))
		fmt.Fprintln(os.Stdout, eventJSON("step_finish", 4, "ses_fixture", "step-finish", ""))
	case "duplicate-completed-part":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		line := eventJSON("text", 2, "ses_fixture", "text", "first")
		fmt.Fprintln(os.Stdout, line)
		fmt.Fprintln(os.Stdout, line)
	case "mixed-session":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_a", "step-start", ""))
		fmt.Fprintln(os.Stdout, eventJSON("text", 2, "ses_b", "text", "mixed"))
	case "mixed-message":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, `{"type":"text","timestamp":2,"sessionID":"ses_fixture","part":{"id":"prt_2","sessionID":"ses_fixture","messageID":"msg_unrelated","type":"text","text":"mixed","time":{"start":2,"end":3}}}`)
	case "nested-session-mismatch":
		fmt.Fprintln(os.Stdout, `{"type":"step_start","timestamp":1,"sessionID":"ses_fixture","part":{"id":"prt_fixture","sessionID":"ses_other","messageID":"msg_assistant","type":"step-start"}}`)
	case "text-before-start":
		fmt.Fprintln(os.Stdout, eventJSON("text", 1, "ses_fixture", "text", "first"))
	case "incomplete-text":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, `{"type":"text","timestamp":2,"sessionID":"ses_fixture","part":{"id":"prt_2","sessionID":"ses_fixture","messageID":"msg_assistant","type":"text","text":"unfinished","time":{"start":1}}}`)
	case "whitespace-text":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, eventJSON("text", 2, "ses_fixture", "text", "  \n"))
	case "mismatched-part":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "text", ""))
	case "error-secret":
		fmt.Fprintln(os.Stdout, `{"type":"error","timestamp":1,"sessionID":"ses_fixture","error":{"message":"secret-auth-token"}}`)
	case "tool":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, `{"type":"tool_use","timestamp":2,"sessionID":"ses_fixture","part":{"id":"prt_2","sessionID":"ses_fixture","messageID":"msg_assistant","type":"tool","tool":"bash"}}`)
	case "partial-failure":
		fmt.Fprintln(os.Stdout, eventJSON("step_start", 1, "ses_fixture", "step-start", ""))
		fmt.Fprintln(os.Stdout, eventJSON("text", 2, "ses_fixture", "text", "partial"))
		fmt.Fprintln(os.Stderr, "secret-auth-token")
		os.Exit(7)
	case "block":
		time.Sleep(10 * time.Second)
	default:
		return
	}
	os.Exit(0)
}

func eventJSON(eventType string, timestamp int64, sessionID, partType, text string) string {
	part := map[string]any{
		"id": fmt.Sprintf("prt_%d", timestamp), "sessionID": sessionID,
		"messageID": "msg_assistant", "type": partType,
	}
	if text != "" {
		part["text"] = text
	}
	if partType == "text" || partType == "reasoning" {
		part["time"] = map[string]any{"start": timestamp, "end": timestamp + 1}
	}
	value := map[string]any{
		"type": eventType, "timestamp": timestamp, "sessionID": sessionID,
		"part": part,
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}
