package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/guzus/birdy/internal/store"
)

type chatMessage struct {
	role    string // "user", "assistant", "tool", "error"
	content string
}

// ChatModel is the main chat screen with viewport, input, and streaming state.
type ChatModel struct {
	viewport     viewport.Model
	input        textinput.Model
	spinner      spinner.Model
	messages     []chatMessage
	streaming    bool
	streamCh     <-chan tea.Msg
	cancelStream context.CancelFunc
	width        int
	height       int
	ready        bool
	accountCount int
	autoQueried  bool
	copied       bool
}

type clearCopiedMsg struct{}

func NewChatModel() ChatModel {
	ti := textinput.New()
	ti.Placeholder = "Ask birdy anything..."
	ti.Focus()
	ti.CharLimit = 4096

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	m := ChatModel{input: ti, spinner: sp}
	m.refreshAccountCount()
	return m
}

func (m *ChatModel) refreshAccountCount() {
	st, err := store.Open()
	if err == nil {
		m.accountCount = st.Len()
	}
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick, func() tea.Msg { return autoQueryMsg{} })
}

const (
	headerHeight = 1
	inputHeight  = 3 // border top + input + border bottom
	footerHeight = 1
	chatOverhead = headerHeight + inputHeight + footerHeight
)

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = m.width - 6 // border (2) + padding (2) + margin

		vpHeight := m.height - chatOverhead
		if vpHeight < 1 {
			vpHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.viewport.SetContent(m.renderMessages())
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderMessages())
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			if !m.streaming {
				return m, func() tea.Msg { return switchScreenMsg{target: screenAccount} }
			}

		case "esc":
			if m.streaming && m.cancelStream != nil {
				m.cancelStream()
				m.cancelStream = nil
				m.streaming = false
				m.streamCh = nil
				m.messages = append(m.messages, chatMessage{role: "error", content: "cancelled"})
				m.refreshViewport()
				return m, nil
			}

		case "ctrl+y":
			if text := m.lastAssistantContent(); text != "" {
				if copyToClipboard(text) == nil {
					m.copied = true
					return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearCopiedMsg{} })
				}
			}

		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.streaming {
				break
			}
			m.input.Reset()
			m.messages = append(m.messages, chatMessage{role: "user", content: text})
			m.streaming = true
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelStream = cancel
			m.refreshViewport()
			return m, startClaude(ctx, text)
		}

	case clearCopiedMsg:
		m.copied = false
		return m, nil

	case autoQueryMsg:
		if m.autoQueried || m.accountCount == 0 {
			return m, nil
		}
		m.autoQueried = true
		prompt := "Give me a brief summary of what's on my home timeline."
		m.messages = append(m.messages, chatMessage{role: "user", content: prompt})
		m.streaming = true
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelStream = cancel
		m.refreshViewport()
		return m, startClaude(ctx, prompt)

	case claudeNextMsg:
		m.streamCh = msg.ch
		return m, waitForNext(msg.ch)

	case claudeTokenMsg:
		// Append to last assistant message, or create a new one
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, chatMessage{role: "assistant"})
		}
		m.messages[len(m.messages)-1].content += msg.Text
		m.refreshViewport()
		if m.streamCh != nil {
			return m, waitForNext(m.streamCh)
		}

	case claudeToolUseMsg:
		m.messages = append(m.messages, chatMessage{role: "tool", content: msg.Command})
		m.refreshViewport()
		if m.streamCh != nil {
			return m, waitForNext(m.streamCh)
		}

	case claudeDoneMsg:
		m.streaming = false
		m.streamCh = nil
		m.cancelStream = nil
		saveChatHistory(m.messages)
		m.refreshViewport()
		return m, nil

	case claudeErrorMsg:
		m.streaming = false
		m.streamCh = nil
		m.cancelStream = nil
		m.messages = append(m.messages, chatMessage{role: "error", content: msg.Err.Error()})
		saveChatHistory(m.messages)
		m.refreshViewport()
		return m, nil
	}

	// Update spinner when streaming
	if m.streaming {
		var spCmd tea.Cmd
		m.spinner, spCmd = m.spinner.Update(msg)
		if spCmd != nil {
			cmds = append(cmds, spCmd)
		}
	}

	// Update viewport (scroll)
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	if vpCmd != nil {
		cmds = append(cmds, vpCmd)
	}

	// Update text input
	var tiCmd tea.Cmd
	m.input, tiCmd = m.input.Update(msg)
	if tiCmd != nil {
		cmds = append(cmds, tiCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) refreshViewport() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m ChatModel) renderMessages() string {
	if len(m.messages) == 0 {
		return lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(1, 2).
			Render("Type a message and press Enter to chat with birdy.\nbirdy can read tweets, search, post, and manage your accounts.")
	}

	w := m.width - 2 // small margin
	if w < 20 {
		w = 20
	}

	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userMsgStyle.Width(w).Render("You: " + msg.content))
			b.WriteString("\n\n")
		case "assistant":
			if msg.content != "" {
				rendered := renderMarkdown(msg.content, w)
				b.WriteString(rendered)
				b.WriteString("\n")
			}
		case "tool":
			b.WriteString(toolMsgStyle.Width(w).Render("  > " + msg.content))
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorMsgStyle.Width(w).Render("Error: " + msg.content))
			b.WriteString("\n\n")
		}
	}

	// Show thinking indicator when streaming and last message has no assistant content yet
	if m.streaming {
		lastIsAssistantEmpty := len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" && m.messages[len(m.messages)-1].content == ""
		lastIsUser := len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "user"
		lastIsTool := len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "tool"
		if lastIsAssistantEmpty || lastIsUser || lastIsTool {
			b.WriteString(toolMsgStyle.Render(m.spinner.View() + " thinking..."))
			b.WriteString("\n\n")
		}
	}

	return b.String()
}

func (m ChatModel) View() string {
	if !m.ready {
		return ""
	}

	// Header
	accountInfo := "no accounts"
	if m.accountCount > 0 {
		accountInfo = fmt.Sprintf("%d account", m.accountCount)
		if m.accountCount > 1 {
			accountInfo += "s"
		}
	}

	status := "ready"
	if m.streaming {
		status = m.spinner.View() + " thinking..."
	}

	leftHeader := headerStyle.Render(" birdy ")
	rightInfo := fmt.Sprintf(" %s | %s ", accountInfo, status)
	rightHeader := headerStyle.Render(rightInfo)

	gap := m.width - lipgloss.Width(leftHeader) - lipgloss.Width(rightHeader)
	if gap < 0 {
		gap = 0
	}
	headerFill := headerStyle.Render(strings.Repeat(" ", gap))
	header := leftHeader + headerFill + rightHeader

	// Input
	input := inputBorderStyle.Render(m.input.View())

	// Footer
	footerText := "enter: send | ctrl+y: copy | tab: accounts | ctrl+c: quit"
	if m.streaming {
		footerText = "esc: cancel | ctrl+c: quit"
	}
	if m.copied {
		footerText = "copied to clipboard!"
	}
	histDir, _ := chatHistoryDir()
	if histDir != "" {
		footerText += "  |  history: " + histDir
	}
	footer := statusBarStyle.Width(m.width).Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		input,
		footer,
	)
}

func renderMarkdown(content string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return assistantMsgStyle.Width(width).Render(content)
	}
	out, err := r.Render(content)
	if err != nil {
		return assistantMsgStyle.Width(width).Render(content)
	}
	return strings.TrimRight(out, "\n")
}

func (m ChatModel) lastAssistantContent() string {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" && m.messages[i].content != "" {
			return m.messages[i].content
		}
	}
	return ""
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return fmt.Errorf("unsupported OS")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
