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

const systemPrompt = `You are birdy, an AI assistant for managing X/Twitter accounts.
You have access to the birdy CLI tool. Available commands:

Reading & Browsing:
  birdy read <tweet-id>         Read a tweet by ID or URL
  birdy thread <tweet-id>       Read a tweet thread
  birdy search "<query>"        Search for tweets
  birdy home                    Get your home timeline
  birdy mentions                Get your mentions
  birdy bookmarks               Get your bookmarked tweets
  birdy news                    Get trending news
  birdy replies <tweet-id>      Get replies to a tweet

User Info:
  birdy about <username>        Get account information for a user
  birdy whoami                  Show current authenticated user
  birdy followers <username>    Get followers for a user
  birdy following <username>    Get following for a user
  birdy user-tweets <username>  Get tweets for a user
  birdy likes <username>        Get likes for a user

Actions:
  birdy tweet "<text>"          Post a new tweet
  birdy reply <id> "<text>"     Reply to a tweet
  birdy follow <username>       Follow a user
  birdy unfollow <username>     Unfollow a user
  birdy unbookmark <tweet-id>   Remove a tweet from bookmarks

Lists:
  birdy lists <username>        Get lists for a user
  birdy list-timeline <list-id> Get tweets from a list

Other:
  birdy query-ids <id1> <id2>   Query tweets by IDs
  birdy check                   Check credential availability
  birdy account list            List configured accounts
  birdy status                  Show rotation status

Use these commands to help the user. Run commands and explain the results clearly.
When showing tweets, format them nicely. Be concise and helpful.`

// Message types for Bubble Tea streaming
type claudeTokenMsg struct {
	Text string
}

type claudeToolUseMsg struct {
	Command string
}

type claudeDoneMsg struct{}

type claudeErrorMsg struct {
	Err error
}

type claudeNextMsg struct {
	ch <-chan tea.Msg
}

// autoQueryMsg triggers the initial home feed query after splash.
type autoQueryMsg struct{}

// JSON structs for parsing Claude Code CLI stream-json output.
// The CLI outputs JSON lines with type "assistant" containing the full
// accumulated message content, not just deltas. We track previous text
// to compute what's new.
type cliEvent struct {
	Type    string      `json:"type"`
	Subtype string      `json:"subtype,omitempty"`
	Message *cliMessage `json:"message,omitempty"`
	Result  string      `json:"result,omitempty"`
	IsError bool        `json:"is_error,omitempty"`
}

type cliMessage struct {
	Content []cliContentBlock `json:"content"`
}

type cliContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// startClaude spawns a claude process and returns a channel-based message
// for the Bubble Tea streaming pattern. The context allows cancelling the
// subprocess when the user presses escape or quits the TUI.
func startClaude(ctx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("claude"); err != nil {
			return claudeErrorMsg{Err: fmt.Errorf("claude CLI not found — install it from https://claude.ai/claude-code")}
		}

		ch := make(chan tea.Msg, 64)
		go runClaudeProcess(ctx, prompt, ch)
		return claudeNextMsg{ch: ch}
	}
}

// waitForNext blocks on the channel and returns the next message.
// Standard Bubble Tea pattern for channel-based streaming.
func waitForNext(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return claudeDoneMsg{}
		}
		return msg
	}
}

// runClaudeProcess executes the claude CLI, scans stdout line-by-line,
// parses stream-json, and sends messages to the channel.
func runClaudeProcess(ctx context.Context, prompt string, ch chan<- tea.Msg) {
	defer close(ch)

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "25",
		"--allowedTools", "Bash(birdy *)",
		"--append-system-prompt", systemPrompt,
	}

	cmd := exec.CommandContext(ctx, "claude", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ch <- claudeErrorMsg{Err: fmt.Errorf("failed to create pipe: %w", err)}
		return
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		ch <- claudeErrorMsg{Err: fmt.Errorf("failed to start claude: %w", err)}
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var prevText string
	seenToolIDs := make(map[string]bool)
	gotAnyMessage := false

	// Also support raw API streaming format (fallback)
	var toolInput strings.Builder
	inToolBlock := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Strip SSE "data: " prefix if present
		if after, found := strings.CutPrefix(line, "data: "); found {
			line = after
		}

		var event cliEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // silently skip malformed lines
		}

		switch event.Type {
		case "assistant":
			// CLI wrapper format: message.content contains full accumulated content.
			// Each event has the complete content so far; compute the delta.
			if event.Message == nil {
				continue
			}
			gotAnyMessage = true

			var fullText string
			for _, block := range event.Message.Content {
				switch block.Type {
				case "text":
					fullText += block.Text
				case "tool_use":
					if block.ID != "" && !seenToolIDs[block.ID] {
						seenToolIDs[block.ID] = true
						var input struct {
							Command string `json:"command"`
						}
						if len(block.Input) > 0 {
							if err := json.Unmarshal(block.Input, &input); err == nil && input.Command != "" {
								ch <- claudeToolUseMsg{Command: input.Command}
							}
						}
					}
				}
			}

			// Emit only the new text since last event
			if len(fullText) > len(prevText) {
				delta := fullText[len(prevText):]
				ch <- claudeTokenMsg{Text: delta}
				prevText = fullText
			}

		case "content_block_delta":
			// Raw Anthropic API streaming format (fallback)
			gotAnyMessage = true
			var raw struct {
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}
			switch raw.Delta.Type {
			case "text_delta":
				if raw.Delta.Text != "" {
					ch <- claudeTokenMsg{Text: raw.Delta.Text}
				}
			case "input_json_delta":
				if inToolBlock {
					toolInput.WriteString(raw.Delta.PartialJSON)
				}
			}

		case "content_block_start":
			// Raw API format: tool_use block start
			var raw struct {
				ContentBlock struct {
					Type string `json:"type"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(line), &raw); err == nil && raw.ContentBlock.Type == "tool_use" {
				inToolBlock = true
				toolInput.Reset()
			}

		case "content_block_stop":
			// Raw API format: tool_use block complete
			if inToolBlock {
				var input struct {
					Command string `json:"command"`
				}
				if err := json.Unmarshal([]byte(toolInput.String()), &input); err == nil && input.Command != "" {
					ch <- claudeToolUseMsg{Command: input.Command}
				}
				inToolBlock = false
			}

		case "result":
			// Final result from CLI wrapper format
			gotAnyMessage = true
			if event.IsError && event.Result != "" {
				ch <- claudeErrorMsg{Err: fmt.Errorf("%s", event.Result)}
			} else if event.Result != "" && len(event.Result) > len(prevText) {
				// Emit any remaining text not yet streamed
				delta := event.Result[len(prevText):]
				if delta != "" {
					ch <- claudeTokenMsg{Text: delta}
				}
			}
			_ = cmd.Wait()
			return

		case "message_stop":
			// Raw API format: conversation turn complete
			_ = cmd.Wait()
			return
		}
	}

	_ = cmd.Wait()

	// If context was cancelled, don't report an error
	if ctx.Err() != nil {
		return
	}

	// If we never got any messages, report an error
	if !gotAnyMessage {
		errMsg := "no response from claude"
		if stderrBuf.Len() > 0 {
			errMsg = strings.TrimSpace(stderrBuf.String())
		}
		ch <- claudeErrorMsg{Err: fmt.Errorf("%s", errMsg)}
	}
}
