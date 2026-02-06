package tui

import (
	"fmt"
	"os"
	"path/filepath"
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
