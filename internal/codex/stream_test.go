package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/claude"
)

func TestIsSelected(t *testing.T) {
	cases := map[string]bool{
		"codex":        true,
		"gpt-5.4":      true,
		"gpt-5.4-mini": true,
		"sonnet":       false,
	}
	for input, want := range cases {
		if got := IsSelected(input); got != want {
			t.Fatalf("IsSelected(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestResolveModel(t *testing.T) {
	t.Setenv("BIRDY_CODEX_MODEL", "gpt-5.4")
	if got := ResolveModel("codex"); got != "gpt-5.4" {
		t.Fatalf("expected env override, got %q", got)
	}
	if got := ResolveModel("gpt-5.4-mini"); got != "gpt-5.4-mini" {
		t.Fatalf("expected explicit model to pass through, got %q", got)
	}
}

func TestBuildArgsAddsWritableDirsAndPromptRules(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	args := BuildArgs("continue", "codex", "birdy")
	joined := strings.Join(args, "\n")

	if !strings.Contains(joined, "--add-dir") {
		t.Fatalf("expected add-dir flags, got %q", joined)
	}
	if !strings.Contains(joined, filepath.Join(os.Getenv("HOME"), ".config", "birdy")) {
		t.Fatalf("expected birdy config dir to be writable, got %q", joined)
	}
	if !strings.Contains(joined, "/tmp") {
		t.Fatalf("expected /tmp to be writable, got %q", joined)
	}
	if !strings.Contains(joined, "Use the birdy CLI first instead of inspecting the repository.") {
		t.Fatalf("expected codex-specific prompt rule, got %q", joined)
	}
}

func TestStreamEmitsCommandSnapshotAndDone(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "codex")
	content := strings.Join([]string{
		"#!/bin/sh",
		"cat <<'EOF'",
		"{\"type\":\"thread.started\",\"thread_id\":\"t1\"}",
		"{\"type\":\"turn.started\"}",
		"{\"type\":\"item.started\",\"item\":{\"id\":\"cmd1\",\"type\":\"command_execution\",\"command\":\"birdy home\"}}",
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"msg1\",\"type\":\"agent_message\",\"text\":\"first\"}}",
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"msg2\",\"type\":\"agent_message\",\"text\":\"second\"}}",
		"{\"type\":\"turn.completed\"}",
		"EOF",
	}, "\n")
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var events []claude.Event
	Stream(context.Background(), "prompt", "codex", "birdy", func(ev claude.Event) {
		events = append(events, ev)
	})

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if events[0].Type != claude.EventToolUse || events[0].Command != "birdy home" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[1].Type != claude.EventSnapshot || events[1].Text != "first" {
		t.Fatalf("unexpected second event: %#v", events[1])
	}
	if events[2].Type != claude.EventSnapshot || events[2].Text != "first\n\nsecond" {
		t.Fatalf("unexpected third event: %#v", events[2])
	}
	if events[3].Type != claude.EventDone {
		t.Fatalf("unexpected final event: %#v", events[3])
	}
}
