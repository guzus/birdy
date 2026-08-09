package chatmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/opencode"
)

func TestClientRegistryIsBoundedAndPreservesDocumentedAliases(t *testing.T) {
	for _, id := range []string{"", "sonnet", "opus", "haiku", "codex", "gpt-5.4", "gpt-5.4-mini", DeepSeekClientModelID, opencode.ModelDeepSeekV4Flash} {
		if _, err := ResolveClient(id); err != nil {
			t.Fatalf("ResolveClient(%q): %v", id, err)
		}
	}
	for _, id := range []string{"claude-attacker-model", "openrouter/model", "opencode-go/other", "deepseek-v4-flash"} {
		if _, err := ResolveClient(id); err == nil {
			t.Fatalf("ResolveClient(%q) accepted an unregistered model", id)
		}
	}
}

func TestServerRegistryKeepsHarnessPolicyFixed(t *testing.T) {
	claudeSelection, err := ResolveServer("claude-code", "claude-server-fixed")
	if err != nil || claudeSelection.Backend != BackendClaudeCode || claudeSelection.RuntimeModel != "claude-server-fixed" {
		t.Fatalf("Claude server selection = %#v, %v", claudeSelection, err)
	}
	openCodeSelection, err := ResolveServer("opencode", "")
	if err != nil || openCodeSelection.RuntimeModel != opencode.ModelDeepSeekV4Flash {
		t.Fatalf("OpenCode server selection = %#v, %v", openCodeSelection, err)
	}
	if _, err := ResolveServer("opencode", "opencode-go/other"); err == nil {
		t.Fatal("server registry accepted another OpenCode model")
	}
	if _, err := ResolveServer("codex", "gpt-5.4-mini"); err == nil {
		t.Fatal("server registry enabled Codex in no-tools harness policy")
	}
}

func TestOpenCodeAvailabilityRequiresKeyCLIAndAccounts(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"test","auth_token":"auth","ct0":"ct0"}]`)
	selection, _ := ResolveClient(DeepSeekClientModelID)
	t.Setenv("OPENCODE_API_KEY", "")
	if Available(selection) {
		t.Fatal("OpenCode unexpectedly available without its provider key")
	}
	t.Setenv("OPENCODE_API_KEY", "provider-key")
	if !Available(selection) {
		t.Fatal("OpenCode unavailable with provider key, CLI, and accounts")
	}
	t.Setenv("BIRDY_ACCOUNTS", "")
	if Available(selection) {
		t.Fatal("OpenCode unexpectedly available without Birdy accounts")
	}
}

func TestStreamRoutesOpenCodeWithoutPromptInArgv(t *testing.T) {
	binDir := t.TempDir()
	stdinPath := filepath.Join(binDir, "stdin")
	argsPath := filepath.Join(binDir, "args")
	script := filepath.Join(binDir, "opencode")
	content := fmt.Sprintf(strings.Join([]string{
		"#!/bin/sh",
		`printf '%%s\n' "$@" > %q`,
		`cat > %q`,
		`printf '%%s\n' '{"type":"step_start","timestamp":1,"sessionID":"ses_test","part":{"id":"prt_1","sessionID":"ses_test","messageID":"msg_test","type":"step-start"}}'`,
		`printf '%%s\n' '{"type":"text","timestamp":2,"sessionID":"ses_test","part":{"id":"prt_2","sessionID":"ses_test","messageID":"msg_test","type":"text","text":"from opencode","time":{"start":2,"end":3}}}'`,
		`printf '%%s\n' '{"type":"step_finish","timestamp":3,"sessionID":"ses_test","part":{"id":"prt_3","sessionID":"ses_test","messageID":"msg_test","type":"step-finish"}}'`,
	}, "\n"), argsPath, stdinPath)
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENCODE_API_KEY", "provider-key")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"test","auth_token":"auth","ct0":"ct0"}]`)
	selection, _ := ResolveClient(DeepSeekClientModelID)
	var events []claude.Event
	Stream(context.Background(), selection, Request{Mode: ModeGeneral, Prompt: "private prompt", SystemPrompt: "restricted tool", BirdyCommand: "/usr/bin/true"}, func(event claude.Event) {
		events = append(events, event)
	})
	if len(events) != 2 || events[0].Type != claude.EventSnapshot || events[1].Type != claude.EventDone {
		t.Fatalf("unexpected events: %#v", events)
	}
	args, _ := os.ReadFile(argsPath)
	stdin, _ := os.ReadFile(stdinPath)
	if strings.Contains(string(args), "private prompt") || string(stdin) != "private prompt" {
		t.Fatalf("prompt transport mismatch: args=%q stdin=%q", args, stdin)
	}
}
