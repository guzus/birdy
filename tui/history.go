package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// chatHistoryDir returns the directory for storing chat history markdown files.
func chatHistoryDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "birdy", "chats"), nil
}

// chatHistoryDisplayDir returns a compact user-facing chat history path.
func chatHistoryDisplayDir() string {
	dir, err := chatHistoryDir()
	if err != nil || dir == "" {
		return "~/.config/birdy/chats"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(dir, home) {
		trimmed := strings.TrimPrefix(dir, home)
		if trimmed == "" {
			return "~"
		}
		if !strings.HasPrefix(trimmed, string(filepath.Separator)) {
			trimmed = string(filepath.Separator) + trimmed
		}
		return "~" + trimmed
	}
	return dir
}

// listChatHistoryFiles returns markdown history files sorted newest-first.
func listChatHistoryFiles(limit int) ([]string, error) {
	dir, err := chatHistoryDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type item struct {
		path    string
		modTime time.Time
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{
			path:    filepath.Join(dir, name),
			modTime: info.ModTime(),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].modTime.After(items[j].modTime)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.path)
	}
	return out, nil
}

// loadChatHistoryPreview reads chat history content for TUI preview.
func loadChatHistoryPreview(path string, maxBytes int) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		return string(raw[:maxBytes]) + "\n\n... (truncated)", nil
	}
	return string(raw), nil
}

// loadChatHistoryMessages parses a saved markdown transcript back to chat messages.
func loadChatHistoryMessages(path string) ([]chatMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	var (
		messages []chatMessage
		role     string
		buf      []string
	)
	flush := func() {
		if role == "" {
			buf = nil
			return
		}
		content := strings.TrimSpace(strings.Join(buf, "\n"))
		if content != "" {
			messages = append(messages, chatMessage{role: role, content: content})
		}
		buf = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "## You":
			flush()
			role = "user"
		case trimmed == "## birdy":
			flush()
			role = "assistant"
		case strings.HasPrefix(trimmed, "> `") && strings.HasSuffix(trimmed, "`"):
			flush()
			cmd := strings.TrimSuffix(strings.TrimPrefix(trimmed, "> `"), "`")
			if cmd != "" {
				messages = append(messages, chatMessage{role: "tool", content: cmd})
			}
			role = ""
		case strings.HasPrefix(trimmed, "**Error:**"):
			flush()
			errMsg := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Error:**"))
			if errMsg != "" {
				messages = append(messages, chatMessage{role: "error", content: errMsg})
			}
			role = ""
		case strings.HasPrefix(trimmed, "# "):
			// Skip markdown title row.
			continue
		default:
			if role != "" {
				buf = append(buf, line)
			}
		}
	}
	flush()

	if len(messages) == 0 {
		return nil, fmt.Errorf("no chat messages found in %s", filepath.Base(path))
	}
	return messages, nil
}

func chatHistoryFileLabel(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if ts, err := time.Parse("2006-01-02_150405", base); err == nil {
		return ts.Format("2006-01-02 15:04:05")
	}
	return filepath.Base(path)
}

// saveChatHistory writes the current chat messages to a markdown file.
// Returns the file path or an error.
func saveChatHistory(messages []chatMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	dir, err := chatHistoryDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating chat history dir: %w", err)
	}

	now := time.Now()
	filename := now.Format("2006-01-02_150405") + ".md"
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# birdy chat — %s\n\n", now.Format("2006-01-02 15:04:05")))

	for _, msg := range messages {
		switch msg.role {
		case "user":
			b.WriteString("## You\n\n")
			b.WriteString(msg.content)
			b.WriteString("\n\n")
		case "assistant":
			if msg.content != "" {
				b.WriteString("## birdy\n\n")
				b.WriteString(msg.content)
				b.WriteString("\n\n")
			}
		case "tool":
			b.WriteString(fmt.Sprintf("> `%s`\n\n", msg.content))
		case "error":
			b.WriteString(fmt.Sprintf("**Error:** %s\n\n", msg.content))
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return "", fmt.Errorf("writing chat history: %w", err)
	}
	return path, nil
}
