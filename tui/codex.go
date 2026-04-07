package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type codexExecEvent struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Item    *codexExecItem `json:"item,omitempty"`
	Error   *codexExecErr  `json:"error,omitempty"`
}

type codexExecItem struct {
	ID               string `json:"id,omitempty"`
	Type             string `json:"type,omitempty"`
	Text             string `json:"text,omitempty"`
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	Status           string `json:"status,omitempty"`
}

type codexExecErr struct {
	Message string `json:"message,omitempty"`
}

func buildCodexPrompt(prompt, cmd string) string {
	var b strings.Builder
	b.WriteString("Follow these instructions exactly.\n\n")
	b.WriteString(buildSystemPrompt(cmd))
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n\nConversation to continue:\n")
		b.WriteString(prompt)
	}
	return b.String()
}

func buildCodexArgs(prompt, model, cmd string) []string {
	args := []string{
		"exec",
		"--json",
		"--skip-git-repo-check",
	}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	args = append(args, buildCodexPrompt(prompt, cmd))
	return args
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

	args := buildCodexArgs(prompt, model, birdyCmd())
	cmd := exec.CommandContext(ctx, "codex", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ch <- claudeErrorMsg{Err: fmt.Errorf("failed to create pipe: %w", err)}
		return
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		ch <- claudeErrorMsg{Err: fmt.Errorf("failed to start codex: %w", err)}
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var assistantParts []string
	seenCommands := make(map[string]bool)
	gotAnyMessage := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event codexExecEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "item.started":
			if event.Item == nil || event.Item.Type != "command_execution" || event.Item.Command == "" {
				continue
			}
			if !seenCommands[event.Item.ID] {
				seenCommands[event.Item.ID] = true
				gotAnyMessage = true
				ch <- claudeToolUseMsg{Command: event.Item.Command}
			}

		case "item.completed":
			if event.Item == nil {
				continue
			}
			switch event.Item.Type {
			case "command_execution":
				if event.Item.Command != "" && !seenCommands[event.Item.ID] {
					seenCommands[event.Item.ID] = true
					gotAnyMessage = true
					ch <- claudeToolUseMsg{Command: event.Item.Command}
				}
			case "agent_message":
				text := strings.TrimSpace(event.Item.Text)
				if text == "" {
					continue
				}
				gotAnyMessage = true
				assistantParts = append(assistantParts, text)
				ch <- claudeSnapshotMsg{Text: strings.Join(assistantParts, "\n\n")}
			}

		case "error":
			errMsg := strings.TrimSpace(event.Message)
			if errMsg == "" && event.Error != nil {
				errMsg = strings.TrimSpace(event.Error.Message)
			}
			if errMsg == "" {
				errMsg = "codex execution failed"
			}
			ch <- claudeErrorMsg{Err: fmt.Errorf("%s", errMsg)}
			_ = cmd.Wait()
			return

		case "turn.failed":
			errMsg := "codex turn failed"
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				errMsg = strings.TrimSpace(event.Error.Message)
			}
			ch <- claudeErrorMsg{Err: fmt.Errorf("%s", errMsg)}
			_ = cmd.Wait()
			return

		case "turn.completed":
			_ = cmd.Wait()
			return
		}
	}

	_ = cmd.Wait()

	if ctx.Err() != nil {
		return
	}

	if !gotAnyMessage {
		errMsg := "no response from codex"
		if stderrBuf.Len() > 0 {
			errMsg = strings.TrimSpace(stderrBuf.String())
		}
		ch <- claudeErrorMsg{Err: fmt.Errorf("%s", errMsg)}
	}
}
