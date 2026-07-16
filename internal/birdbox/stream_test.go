package birdbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/claude"
)

func TestEnvironmentForContextUsesAuthoritativeDeadlineAndGrace(t *testing.T) {
	t.Setenv(internalBudgetEnv, "1")
	t.Setenv(internalGraceEnv, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	env, err := environmentForContext(ctx)
	if err != nil {
		t.Fatalf("environmentForContext: %v", err)
	}
	budget, budgetCount := environmentValue(env, internalBudgetEnv)
	grace, graceCount := environmentValue(env, internalGraceEnv)
	if budgetCount != 1 || graceCount != 1 {
		t.Fatalf("expected one internal value each, budget=%d grace=%d", budgetCount, graceCount)
	}
	budgetMs, err := strconv.ParseInt(budget, 10, 64)
	if err != nil || budgetMs <= 0 || budgetMs > 2000 {
		t.Fatalf("unexpected deadline budget %q", budget)
	}
	if grace != strconv.FormatInt(runnerCleanupGrace.Milliseconds(), 10) {
		t.Fatalf("unexpected cancellation grace %q", grace)
	}
}

func TestEnvironmentForContextRejectsExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := environmentForContext(ctx); err == nil {
		t.Fatal("expected expired deadline error")
	}
}

func environmentValue(env []string, name string) (string, int) {
	prefix := name + "="
	var value string
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			value = strings.TrimPrefix(entry, prefix)
			count++
		}
	}
	return value, count
}

func TestEnabledUsesTemplateAsOptIn(t *testing.T) {
	t.Setenv(templateEnv, "")
	t.Setenv(apiKeyEnv, "e2b-test-key")
	if Enabled() {
		t.Fatal("expected API key alone not to enable bird-box")
	}

	t.Setenv(templateEnv, "bird-box-test")
	if !Enabled() {
		t.Fatal("expected configured template to enable bird-box")
	}
}

func TestStreamRequiresAPIKey(t *testing.T) {
	t.Setenv(templateEnv, "bird-box-test")
	t.Setenv(apiKeyEnv, "")

	var events []claude.Event
	Stream(context.Background(), "hello", "sonnet", func(event claude.Event) {
		events = append(events, event)
	})

	if len(events) != 2 {
		t.Fatalf("expected error and done events, got %#v", events)
	}
	if events[0].Type != claude.EventError || events[1].Type != claude.EventDone {
		t.Fatalf("expected error then done, got %#v", events)
	}
}

func TestStreamCancellationGivesRunnerSIGTERMGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal behavior is Unix-specific")
	}

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "signal.txt")
	readyPath := filepath.Join(dir, "ready.txt")
	runnerPath := filepath.Join(dir, "fake-node.sh")
	script := "#!/bin/sh\n" +
		"trap 'printf term > \"$MARKER_PATH\"; exit 0' TERM\n" +
		"printf ready > \"$READY_PATH\"\n" +
		"while :; do sleep 0.1; done\n"
	if err := os.WriteFile(runnerPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}

	t.Setenv(templateEnv, "bird-box-test")
	t.Setenv(apiKeyEnv, "e2b-test-key")
	t.Setenv(nodePathEnv, runnerPath)
	t.Setenv(runnerPathEnv, "/unused/claude.mjs")
	t.Setenv("MARKER_PATH", markerPath)
	t.Setenv("READY_PATH", readyPath)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Stream(ctx, "hello", "sonnet", func(claude.Event) {})
		close(done)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatal("fake runner exited before reporting ready")
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("fake runner did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop within cleanup grace")
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("runner did not handle SIGTERM: %v", err)
	}
	if string(data) != "term" {
		t.Fatalf("unexpected signal marker %q", data)
	}
}
