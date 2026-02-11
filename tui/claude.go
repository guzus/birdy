package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// birdyCmd returns the command to invoke birdy. If the current executable
// can be resolved (e.g. when running via "go run"), it uses that path so
// Claude doesn't need "birdy" on PATH. Falls back to "birdy".
func birdyCmd() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "birdy"
}

func buildSystemPrompt(cmd string) string {
	return fmt.Sprintf(`You are birdy, an AI assistant for managing X/Twitter accounts.
You have access to the birdy CLI tool. Available commands:

Reading & Browsing:
  %[1]s read <tweet-id>         Read a tweet by ID or URL
  %[1]s thread <tweet-id>       Read a tweet thread
  %[1]s search "<query>"        Search for tweets
  %[1]s home                    Get your home timeline
  %[1]s mentions                Get your mentions
  %[1]s bookmarks               Get your bookmarked tweets
  %[1]s news                    Get trending news
  %[1]s replies <tweet-id>      Get replies to a tweet

User Info:
  %[1]s about <username>        Get account information for a user
  %[1]s whoami                  Show current authenticated user
  %[1]s followers <username>    Get followers for a user
  %[1]s following <username>    Get following for a user
  %[1]s user-tweets <username>  Get tweets for a user
  %[1]s likes <username>        Get likes for a user

Actions:
  %[1]s tweet "<text>"          Post a new tweet
  %[1]s reply <id> "<text>"     Reply to a tweet
  %[1]s follow <username>       Follow a user
  %[1]s unfollow <username>     Unfollow a user
  %[1]s unbookmark <tweet-id>   Remove a tweet from bookmarks

Lists:
  %[1]s lists <username>        Get lists for a user
  %[1]s list-timeline <list-id> Get tweets from a list

Other:
  %[1]s query-ids <id1> <id2>   Query tweets by IDs
  %[1]s check                   Check credential availability
  %[1]s account list            List configured accounts
  %[1]s status                  Show rotation status

IMPORTANT: Always use the exact command "%[1]s" — never use "go run .", "birdy", or any other alternative.

Execution policy (aggressive tool use):
- Default to running birdy commands first. Do not answer from memory when a command can verify.
- For factual questions, run at least one relevant read command before answering.
- For research/exploration tasks, run multiple commands in sequence without waiting for confirmation.
- If output is ambiguous, run follow-up commands until you can provide a clear, evidence-based answer.
- Include concise evidence by referencing which commands were run.
- Ask for confirmation only before state-changing actions (tweet, reply, follow, unfollow, unbookmark).

Use these commands to help the user. Run commands and explain the results clearly.
When showing tweets, format them nicely. Be concise and helpful.

When the user asks you to "dive deeper", "explore", or "browse" their timeline:
- Start with %[1]s home to get the timeline
- Proactively read interesting tweet threads using %[1]s thread <id>
- Check replies on popular tweets with %[1]s replies <id>
- Look up users who posted interesting content with %[1]s about <username>
- Browse their recent tweets with %[1]s user-tweets <username>
- Follow conversation chains and summarize the most interesting findings
- You can chain multiple commands without asking — explore autonomously and report back`, cmd)
}

func buildClaudeArgs(prompt, model, cmd string) []string {
	return []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "25",
		"--allowedTools", fmt.Sprintf("Bash(%s *),Skill(birdy)", cmd),
		"--append-system-prompt", buildSystemPrompt(cmd),
	}
}

func buildTurnPrompt(messages []chatMessage) string {
	const (
		maxMessages = 20
		maxChars    = 1600
	)

	if len(messages) == 0 {
		return ""
	}

	start := len(messages) - maxMessages
	if start < 0 {
		start = 0
	}

	var b strings.Builder
	b.WriteString("Continue this ongoing birdy TUI chat session.\n")
	b.WriteString("Do not restart with a generic greeting. Respond directly to the latest user message.\n\n")
	b.WriteString("Conversation history (oldest to newest):\n")

	for _, m := range messages[start:] {
		text := strings.TrimSpace(m.content)
		if text == "" {
			continue
		}
		text = truncatePromptText(text, maxChars)

		switch m.role {
		case "user":
			b.WriteString("User: ")
			b.WriteString(text)
			b.WriteString("\n")
		case "assistant":
			b.WriteString("Assistant: ")
			b.WriteString(text)
			b.WriteString("\n")
		case "tool":
			b.WriteString("Tool: ")
			b.WriteString(text)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func truncatePromptText(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	trimmed := strings.TrimSpace(s[:maxChars])
	return trimmed + " ...[truncated " + strconv.Itoa(len(s)-maxChars) + " chars]"
}

// Message types for Bubble Tea streaming
type claudeTokenMsg struct {
	Text string
}

type claudeSnapshotMsg struct {
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

type tokenBatcher struct {
	pending strings.Builder
	last    time.Time
}

func (b *tokenBatcher) add(delta string) (string, bool) {
	if delta == "" {
		return "", false
	}
	b.pending.WriteString(delta)
	now := time.Now()
	if b.last.IsZero() {
		b.last = now
	}

	// Flush quickly on structure or enough buffered text.
	if strings.Contains(delta, "\n") || strings.Contains(delta, "```") || b.pending.Len() >= 240 || now.Sub(b.last) >= 35*time.Millisecond {
		out := b.pending.String()
		b.pending.Reset()
		b.last = now
		return out, true
	}
	return "", false
}

func (b *tokenBatcher) flush() (string, bool) {
	if b.pending.Len() == 0 {
		return "", false
	}
	out := b.pending.String()
	b.pending.Reset()
	b.last = time.Now()
	return out, true
}

// startClaude spawns a claude process and returns a channel-based message
// for the Bubble Tea streaming pattern. The context allows cancelling the
// subprocess when the user presses escape or quits the TUI.
func startClaude(ctx context.Context, prompt, model string) tea.Cmd {
	return func() tea.Msg {
		if _, err := exec.LookPath("claude"); err != nil {
			return claudeErrorMsg{Err: fmt.Errorf("claude CLI not found — install it from https://claude.ai/claude-code")}
		}

		ch := make(chan tea.Msg, 256)
		go runClaudeProcess(ctx, prompt, model, ch)
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
func runClaudeProcess(ctx context.Context, prompt, model string, ch chan<- tea.Msg) {
	defer close(ch)

	args := buildClaudeArgs(prompt, model, birdyCmd())

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
	var batch tokenBatcher
	var pendingSnapshot string
	var lastSnapshot time.Time

	// Also support raw API streaming format (fallback)
	var toolInput strings.Builder
	inToolBlock := false
	flushPendingToken := func() {
		if text, ok := batch.flush(); ok {
			ch <- claudeTokenMsg{Text: text}
		}
	}
	emitToken := func(delta string) {
		if text, ok := batch.add(delta); ok {
			ch <- claudeTokenMsg{Text: text}
		}
	}
	flushPendingSnapshot := func() {
		if pendingSnapshot != "" {
			ch <- claudeSnapshotMsg{Text: pendingSnapshot}
			pendingSnapshot = ""
			lastSnapshot = time.Now()
		}
	}
	emitSnapshot := func(text string, force bool) {
		if text == "" {
			return
		}
		pendingSnapshot = text
		now := time.Now()
		if force || lastSnapshot.IsZero() || now.Sub(lastSnapshot) >= 35*time.Millisecond || strings.Contains(text, "\n") {
			ch <- claudeSnapshotMsg{Text: text}
			pendingSnapshot = ""
			lastSnapshot = now
		}
	}

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
						flushPendingSnapshot()
						flushPendingToken()
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

			// CLI wrapper events carry the full accumulated assistant text.
			// Treat it as authoritative to avoid delta drift/corruption.
			if fullText != prevText {
				emitSnapshot(fullText, false)
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
					emitToken(raw.Delta.Text)
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
				flushPendingSnapshot()
				flushPendingToken()
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
				flushPendingSnapshot()
				flushPendingToken()
				ch <- claudeErrorMsg{Err: fmt.Errorf("%s", event.Result)}
			} else if event.Result != "" && event.Result != prevText {
				emitSnapshot(event.Result, true)
			}
			flushPendingSnapshot()
			flushPendingToken()
			_ = cmd.Wait()
			return

		case "message_stop":
			// Raw API format: conversation turn complete
			flushPendingSnapshot()
			flushPendingToken()
			_ = cmd.Wait()
			return
		}
	}
	flushPendingSnapshot()
	flushPendingToken()

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
