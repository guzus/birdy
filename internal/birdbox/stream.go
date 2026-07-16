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
	internalBudgetEnv  = "BIRDY_INTERNAL_BIRD_BOX_BUDGET_MS"
	internalGraceEnv   = "BIRDY_INTERNAL_BIRD_BOX_CANCEL_GRACE_MS"
	sandboxBirdyCmd    = "birdy --strategy random"
	// Covers the default 30s E2B create request, 8s sandbox deletion request,
	// and the runner's 5s scheduling margin before Go hard-kills the helper.
	runnerCleanupGrace = 45 * time.Second
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
	args := claude.BuildArgs(prompt, model, sandboxBirdyCmd, claude.ToolPermissions{WebSearch: true})
	cmd := exec.CommandContext(ctx, nodePath, append([]string{runnerPath}, args...)...)
	runnerEnv, err := environmentForContext(ctx)
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: err.Error()})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	cmd.Env = runnerEnv
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

func environmentForContext(ctx context.Context) ([]string, error) {
	env := withoutEnvironment(os.Environ(), internalBudgetEnv, internalGraceEnv)
	env = append(env, fmt.Sprintf("%s=%d", internalGraceEnv, runnerCleanupGrace.Milliseconds()))
	if deadline, ok := ctx.Deadline(); ok {
		budgetMs := time.Until(deadline).Milliseconds()
		if budgetMs <= 0 {
			return nil, fmt.Errorf("bird-box request deadline has expired")
		}
		env = append(env, fmt.Sprintf("%s=%d", internalBudgetEnv, budgetMs))
	}
	return env, nil
}

func withoutEnvironment(env []string, names ...string) []string {
	prefixes := make([]string, len(names))
	for i, name := range names {
		prefixes[i] = name + "="
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		internal := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				internal = true
				break
			}
		}
		if !internal {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
