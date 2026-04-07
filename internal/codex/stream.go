package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/guzus/birdy/internal/claude"
)

type execEvent struct {
	Type    string    `json:"type"`
	Message string    `json:"message,omitempty"`
	Item    *execItem `json:"item,omitempty"`
	Error   *execErr  `json:"error,omitempty"`
}

type execItem struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`
}

type execErr struct {
	Message string `json:"message,omitempty"`
}

func IsSelected(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "codex", "gpt-5.4", "gpt-5.4-mini":
		return true
	default:
		return false
	}
}

func ResolveModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-5.4", "gpt-5.4-mini":
		return strings.TrimSpace(model)
	case "codex", "":
		if configured := strings.TrimSpace(os.Getenv("BIRDY_CODEX_MODEL")); configured != "" {
			return configured
		}
		if configured := strings.TrimSpace(os.Getenv("BIRDY_TUI_CODEX_MODEL")); configured != "" {
			return configured
		}
		return "gpt-5.4-mini"
	default:
		return strings.TrimSpace(model)
	}
}

func buildPrompt(prompt, birdyCmd string) string {
	var b strings.Builder
	b.WriteString("Follow these instructions exactly.\n\n")
	b.WriteString("Operational rules for this chat session:\n")
	b.WriteString("- Use the birdy CLI first instead of inspecting the repository.\n")
	b.WriteString("- Do not inspect files to discover the command surface unless a birdy command fails twice.\n")
	b.WriteString("- Treat sandbox or permission errors as configuration issues to fix once, then retry the same birdy command.\n\n")
	b.WriteString(claude.BuildSystemPrompt(birdyCmd))
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n\nConversation to continue:\n")
		b.WriteString(prompt)
	}
	return b.String()
}

func writableDirs() []string {
	dirs := []string{"/tmp"}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return dirs
	}
	dirs = append(dirs, filepath.Join(home, ".config", "birdy"))
	return dirs
}

func BuildArgs(prompt, model, birdyCmd string) []string {
	args := []string{
		"exec",
		"--json",
		"--skip-git-repo-check",
	}
	if resolved := ResolveModel(model); resolved != "" {
		args = append(args, "--model", resolved)
	}
	for _, dir := range writableDirs() {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		args = append(args, "--add-dir", dir)
	}
	args = append(args, buildPrompt(prompt, birdyCmd))
	return args
}

func Stream(ctx context.Context, prompt, model, birdyCmd string, emit func(claude.Event)) {
	args := BuildArgs(prompt, model, birdyCmd)
	cmd := exec.CommandContext(ctx, "codex", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: fmt.Sprintf("failed to create pipe: %v", err)})
		emit(claude.Event{Type: claude.EventDone})
		return
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		emit(claude.Event{Type: claude.EventError, Error: fmt.Sprintf("failed to start codex: %v", err)})
		emit(claude.Event{Type: claude.EventDone})
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

		var event execEvent
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
				emit(claude.Event{Type: claude.EventToolUse, Command: event.Item.Command})
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
					emit(claude.Event{Type: claude.EventToolUse, Command: event.Item.Command})
				}
			case "agent_message":
				text := strings.TrimSpace(event.Item.Text)
				if text == "" {
					continue
				}
				gotAnyMessage = true
				assistantParts = append(assistantParts, text)
				emit(claude.Event{Type: claude.EventSnapshot, Text: strings.Join(assistantParts, "\n\n")})
			}

		case "error":
			errMsg := strings.TrimSpace(event.Message)
			if errMsg == "" && event.Error != nil {
				errMsg = strings.TrimSpace(event.Error.Message)
			}
			if errMsg == "" {
				errMsg = "codex execution failed"
			}
			emit(claude.Event{Type: claude.EventError, Error: errMsg})
			emit(claude.Event{Type: claude.EventDone})
			_ = cmd.Wait()
			return

		case "turn.failed":
			errMsg := "codex turn failed"
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				errMsg = strings.TrimSpace(event.Error.Message)
			}
			emit(claude.Event{Type: claude.EventError, Error: errMsg})
			emit(claude.Event{Type: claude.EventDone})
			_ = cmd.Wait()
			return

		case "turn.completed":
			emit(claude.Event{Type: claude.EventDone})
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
		emit(claude.Event{Type: claude.EventError, Error: errMsg})
		emit(claude.Event{Type: claude.EventDone})
	}
}
