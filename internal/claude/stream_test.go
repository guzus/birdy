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
