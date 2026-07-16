package e2b

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/guzus/birdy/internal/claude"
)

const (
	templateEnv        = "BIRDY_E2B_TEMPLATE"
	apiKeyEnv          = "E2B_API_KEY"
	runnerPathEnv      = "BIRDY_E2B_RUNNER_PATH"
	nodePathEnv        = "BIRDY_E2B_NODE_PATH"
	runnerCleanupGrace = 10 * time.Second
)

// Enabled reports whether hosted Claude execution has been configured for
// E2B. The template is the opt-in switch so an unrelated E2B_API_KEY does not
// change local TUI behavior.
func Enabled() bool {
	return strings.TrimSpace(os.Getenv(templateEnv)) != ""
}

// Stream starts the bundled Node runner. The runner uses E2B's official SDK
// to create a fresh sandbox, execute the baked Claude Code binary, stream its
// JSONL output, and destroy the sandbox.
func Stream(ctx context.Context, prompt, model string, emit func(claude.Event)) {
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		emit(claude.Event{Type: claude.EventError, Error: fmt.Sprintf("%s is required when %s is set", apiKeyEnv, templateEnv)})
		emit(claude.Event{Type: claude.EventDone})
		return
	}

	runnerPath := strings.TrimSpace(os.Getenv(runnerPathEnv))
	if runnerPath == "" {
		runnerPath = "e2b-runner/claude.mjs"
	}
	nodePath := strings.TrimSpace(os.Getenv(nodePathEnv))
	if nodePath == "" {
		nodePath = "node"
	}

	// A fresh sandbox has no durable birdy rotation state. Random selection
	// avoids every chat request starting with the first configured account.
	args := claude.BuildArgs(prompt, model, "birdy --strategy random")
	cmd := exec.CommandContext(ctx, nodePath, append([]string{runnerPath}, args...)...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	// Give the runner's signal handler time to kill the remote sandbox before
	// Go escalates to terminating the local helper process.
	cmd.WaitDelay = runnerCleanupGrace
	claude.StreamCommand(ctx, cmd, emit)
}
