package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	messages     []chatMessage
	streaming    bool
	streamCh     <-chan tea.Msg
	width        int
	height       int
	ready        bool
	accountCount int
	autoQueried  bool
}

func NewChatModel() ChatModel {
	ti := textinput.New()
	ti.Placeholder = "Ask birdy anything..."
	ti.Focus()
	ti.CharLimit = 4096

	m := ChatModel{input: ti}
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
	return tea.Batch(textinput.Blink, func() tea.Msg { return autoQueryMsg{} })
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

		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" || m.streaming {
				break
			}
			m.input.Reset()
			m.messages = append(m.messages, chatMessage{role: "user", content: text})
			m.streaming = true
			m.refreshViewport()
			return m, startClaude(text)
		}

	case autoQueryMsg:
		if m.autoQueried || m.accountCount == 0 {
			return m, nil
		}
		m.autoQueried = true
		prompt := "Give me a brief summary of what's on my home timeline."
		m.messages = append(m.messages, chatMessage{role: "user", content: prompt})
		m.streaming = true
		m.refreshViewport()
		return m, startClaude(prompt)

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
		m.refreshViewport()
		return m, nil

	case claudeErrorMsg:
		m.streaming = false
		m.streamCh = nil
		m.messages = append(m.messages, chatMessage{role: "error", content: msg.Err.Error()})
		m.refreshViewport()
		return m, nil
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

	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userMsgStyle.Render("You: "))
			b.WriteString(msg.content)
			b.WriteString("\n\n")
		case "assistant":
			if msg.content != "" {
				b.WriteString(assistantMsgStyle.Render(msg.content))
				b.WriteString("\n\n")
			}
		case "tool":
			b.WriteString(toolMsgStyle.Render("  > "+msg.content))
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorMsgStyle.Render("Error: "+msg.content))
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
		status = "streaming..."
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
	footer := statusBarStyle.Width(m.width).
		Render("enter: send | tab: accounts | ctrl+c: quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		input,
		footer,
	)
}
