package tui

import (
	"context"
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guzus/birdy/internal/claude"
	internalcodex "github.com/guzus/birdy/internal/codex"
)

func buildCodexArgs(prompt, model, cmd string) []string {
	return internalcodex.BuildArgs(prompt, model, cmd)
}

func startAgent(ctx context.Context, prompt, selection string) tea.Cmd {
	model := modelCLIName(selection)
	switch modelBackend(selection) {
	case backendCodex:
		return startCodex(ctx, prompt, model)
	default:
		return startClaude(ctx, prompt, model)
	}
}

func startCodex(ctx context.Context, prompt, model string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("codex"); err != nil {
			return claudeErrorMsg{Err: fmt.Errorf("codex CLI not found — install it from https://github.com/openai/codex")}
		}

		ch := make(chan tea.Msg, 256)
		go runCodexProcess(ctx, prompt, model, ch)
		return claudeNextMsg{ch: ch}
	}
}

func runCodexProcess(ctx context.Context, prompt, model string, ch chan<- tea.Msg) {
	defer close(ch)
	internalcodex.Stream(ctx, prompt, model, birdyCmd(), func(ev claude.Event) {
		switch ev.Type {
		case claude.EventSnapshot:
			ch <- claudeSnapshotMsg{Text: ev.Text}
		case claude.EventToken:
			ch <- claudeTokenMsg{Text: ev.Text}
		case claude.EventToolUse:
			ch <- claudeToolUseMsg{Command: ev.Command}
		case claude.EventError:
			ch <- claudeErrorMsg{Err: fmt.Errorf("%s", ev.Error)}
		case claude.EventDone:
			// Channel close emits claudeDoneMsg via waitForNext.
		}
	})
}
