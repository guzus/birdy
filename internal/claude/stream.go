package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type EventType string

const (
	EventSnapshot EventType = "snapshot"
	EventToken    EventType = "token"
	EventToolUse  EventType = "tool_use"
	EventError    EventType = "error"
	EventDone     EventType = "done"
)

type Event struct {
	Type    EventType `json:"type"`
	Text    string    `json:"text,omitempty"`
	Command string    `json:"command,omitempty"`
	Error   string    `json:"error,omitempty"`
}

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

func BuildSystemPrompt(cmd string) string {
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

func BuildArgs(prompt, model, birdyCmd string) []string {
	return []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "25",
		"--allowedTools", fmt.Sprintf("Bash(%s *),Skill(birdy)", birdyCmd),
		"--append-system-prompt", BuildSystemPrompt(birdyCmd),
	}
}

// Stream runs the claude CLI and emits events as they arrive.
func Stream(ctx context.Context, prompt, model, birdyCmd string, emit func(Event)) {
	args := BuildArgs(prompt, model, birdyCmd)
	cmd := exec.CommandContext(ctx, "claude", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(Event{Type: EventError, Error: fmt.Sprintf("failed to create pipe: %v", err)})
		emit(Event{Type: EventDone})
		return
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		emit(Event{Type: EventError, Error: fmt.Sprintf("failed to start claude: %v", err)})
		emit(Event{Type: EventDone})
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

	flushPendingToken := func() {
		if text, ok := batch.flush(); ok {
			emit(Event{Type: EventToken, Text: text})
		}
	}
	emitToken := func(delta string) {
		if text, ok := batch.add(delta); ok {
			emit(Event{Type: EventToken, Text: text})
		}
	}
	flushPendingSnapshot := func() {
		if pendingSnapshot != "" {
			emit(Event{Type: EventSnapshot, Text: pendingSnapshot})
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
			emit(Event{Type: EventSnapshot, Text: text})
			pendingSnapshot = ""
			lastSnapshot = now
		}
	}

	var toolInput strings.Builder
	inToolBlock := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if after, found := strings.CutPrefix(line, "data: "); found {
			line = after
		}

		var event cliEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant":
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
								emit(Event{Type: EventToolUse, Command: input.Command})
							}
						}
					}
				}
			}

			if fullText != prevText {
				emitSnapshot(fullText, false)
				prevText = fullText
			}

		case "content_block_delta":
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
			if inToolBlock {
				flushPendingSnapshot()
				flushPendingToken()
				var input struct {
					Command string `json:"command"`
				}
				if err := json.Unmarshal([]byte(toolInput.String()), &input); err == nil && input.Command != "" {
					emit(Event{Type: EventToolUse, Command: input.Command})
				}
				inToolBlock = false
			}

		case "result":
			gotAnyMessage = true
			if event.IsError && event.Result != "" {
				flushPendingSnapshot()
				flushPendingToken()
				emit(Event{Type: EventError, Error: event.Result})
			} else if event.Result != "" && event.Result != prevText {
				emitSnapshot(event.Result, true)
			}
			flushPendingSnapshot()
			flushPendingToken()
			_ = cmd.Wait()
			emit(Event{Type: EventDone})
			return

		case "message_stop":
			flushPendingSnapshot()
			flushPendingToken()
			_ = cmd.Wait()
			emit(Event{Type: EventDone})
			return
		}
	}

	flushPendingSnapshot()
	flushPendingToken()
	_ = cmd.Wait()

	if ctx.Err() != nil {
		emit(Event{Type: EventDone})
		return
	}
	if !gotAnyMessage {
		errMsg := "no response from claude"
		if stderrBuf.Len() > 0 {
			errMsg = strings.TrimSpace(stderrBuf.String())
		}
		emit(Event{Type: EventError, Error: errMsg})
		emit(Event{Type: EventDone})
		return
	}
	emit(Event{Type: EventDone})
}
