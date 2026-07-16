package birdbox

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
	sandboxBirdyCmd    = "birdy --strategy random"
	runnerCleanupGrace = 10 * time.Second
)

// Enabled reports whether the hosted bird-box runtime has been configured.
// The E2B template is the opt-in switch so an unrelated E2B_API_KEY does not
// change local TUI behavior.
func Enabled() bool {
	return strings.TrimSpace(os.Getenv(templateEnv)) != ""
}

// Stream starts bird-box through the bundled E2B provider runner. The runner
// creates a fresh sandbox, executes the baked Claude Code binary, streams its
// JSONL output, and destroys the sandbox.
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

	// A fresh bird-box has no durable birdy rotation state. Random selection
	// avoids every chat request starting with the first configured account.
	args := claude.BuildArgs(prompt, model, sandboxBirdyCmd)
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
