package tui

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

type chatMessage struct {
	role    string // "user", "assistant", "tool", "error"
	content string
}

// ChatModel is the main chat screen with viewport, input, and streaming state.
type ChatModel struct {
	viewport         viewport.Model
	input            textinput.Model
	spinner          spinner.Model
	messages         []chatMessage
	queuedPrompts    []string
	queueNotice      string
	queueNoticeID    int
	streaming        bool
	streamCh         <-chan tea.Msg
	cancelStream     context.CancelFunc
	width            int
	height           int
	ready            bool
	accountCount     int
	autoQueried      bool
	copied           bool
	model            string
	mouseSeqMode     bool
	markdownCache    map[string]string
	mdRenderers      map[int]*glamour.TermRenderer
	lastStreamRender time.Time
}

type clearCopiedMsg struct{}
type clearQueueNoticeMsg struct {
	ID int
}

var (
	mouseSeqPattern        = regexp.MustCompile(`(?:\[\<\d+;\d+;\d+[mM])+`)
	mouseSeqTailPattern    = regexp.MustCompile(`(?:\<\d+;\d+;\d+[mM])+`)
	mouseTripletPattern    = regexp.MustCompile(`(?:\d+;\d+;\d+[mM])+`)
	mouseSeqPartialPattern = regexp.MustCompile(`(?:\[\<[\d;]*|<[\d;]*)$`)
	cprSeqPattern          = regexp.MustCompile(`(?:\[\d+;\d+R)+`)
	cprPartialPattern      = regexp.MustCompile(`(?:\[\d+;\d*R?)$`)
	rgbSeqPattern          = regexp.MustCompile(`(?:\\?1[01];)?rgb:[0-9A-Fa-f]{4}/[0-9A-Fa-f]{4}/[0-9A-Fa-f]{4}`)
	rgbSeqPartialPattern   = regexp.MustCompile(`(?:\\?1[01];)?rgb:[0-9A-Fa-f/]*$`)
	oscPrefixPattern       = regexp.MustCompile(`(?:\]|\x1b\])(?:10|11);`)
	colorTripletPattern    = regexp.MustCompile(`[0-9A-Fa-f]{4}/[0-9A-Fa-f]{4}/[0-9A-Fa-f]{4}`)
	oscResiduePattern      = regexp.MustCompile(`\\+;\\*|;\\+|\\+$`)
	ansiCsiPattern         = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOscPattern         = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

func NewChatModel() ChatModel {
	ti := textinput.New()
	ti.Placeholder = "Ask birdy anything..."
	ti.Focus()
	ti.CharLimit = 4096

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	model := "sonnet"
	if s, err := state.Load(); err == nil && s.Model != "" {
		model = s.Model
	}

	m := ChatModel{
		input:         ti,
		spinner:       sp,
		model:         model,
		markdownCache: make(map[string]string, 128),
		mdRenderers:   make(map[int]*glamour.TermRenderer, 8),
	}
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
	inputHeight  = 4 // border top + queue row + input row + border bottom
	footerHeight = 1
	chatOverhead = headerHeight + inputHeight + footerHeight
)

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	// Always update spinner so the animation stays alive across early returns
	if m.streaming {
		var spCmd tea.Cmd
		m.spinner, spCmd = m.spinner.Update(msg)
		if spCmd != nil {
			cmds = append(cmds, spCmd)
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		prevWidth := m.width
		m.width = msg.Width
		m.height = msg.Height
		if prevWidth != 0 && prevWidth != m.width {
			// Wrapped markdown output depends on width.
			m.markdownCache = make(map[string]string, 128)
		}
		inputWidth := m.width - 6 // border (2) + padding (2) + margin
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.input.Width = inputWidth

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
		if m.handleLeakedMouseKey(msg) {
			m.input.SetValue(sanitizePromptInput(m.input.Value()))
			return m, nil
		}

		switch msg.String() {
		case "tab":
			if m.streaming {
				queued, text := m.enqueueCurrentInput()
				if queued {
					m.queueNoticeID++
					id := m.queueNoticeID
					m.queueNotice = "queued: " + summarizeQueueNotice(text, 40)
					return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
						return clearQueueNoticeMsg{ID: id}
					})
				}
				return m, nil
			}
			return m, func() tea.Msg { return switchScreenMsg{target: screenAccount} }

		case "esc":
			if m.streaming && m.cancelStream != nil {
				m.cancelStream()
				m.cancelStream = nil
				m.streaming = false
				m.lastStreamRender = time.Time{}
				m.streamCh = nil
				m.messages = append(m.messages, chatMessage{role: "error", content: "cancelled"})
				m.refreshViewport()
				if cmd := m.startNextQueuedPrompt(); cmd != nil {
					return m, cmd
				}
				return m, nil
			}

		case "ctrl+t":
			if !m.streaming {
				switch m.model {
				case "sonnet":
					m.model = "opus"
				case "opus":
					m.model = "haiku"
				default:
					m.model = "sonnet"
				}
				if s, err := state.Load(); err == nil {
					s.Model = m.model
					_ = s.Save()
				}
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
			text := strings.TrimSpace(sanitizePromptInput(m.input.Value()))
			if text == "" || m.streaming {
				break
			}
			m.input.Reset()
			return m, m.beginPrompt(text)
		}

	case clearCopiedMsg:
		m.copied = false
		return m, nil

	case clearQueueNoticeMsg:
		if msg.ID == m.queueNoticeID {
			m.queueNotice = ""
		}
		return m, nil

	case autoQueryMsg:
		if m.autoQueried || m.accountCount == 0 {
			return m, nil
		}
		m.autoQueried = true
		prompt := "Give me a brief summary of what's on my home timeline."
		return m, m.beginPrompt(prompt)

	case claudeNextMsg:
		m.streamCh = msg.ch
		return m, waitForNext(msg.ch)

	case claudeTokenMsg:
		// Append to last assistant message, or create a new one
		text := sanitizeStreamOutput(msg.Text)
		if text == "" {
			if m.streamCh != nil {
				return m, waitForNext(m.streamCh)
			}
			return m, nil
		}
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, chatMessage{role: "assistant"})
		}
		m.messages[len(m.messages)-1].content += text
		if m.shouldRefreshStream(text) {
			m.refreshViewport()
		}
		if m.streamCh != nil {
			return m, waitForNext(m.streamCh)
		}

	case claudeSnapshotMsg:
		text := sanitizeStreamOutput(msg.Text)
		if text == "" {
			if m.streamCh != nil {
				return m, waitForNext(m.streamCh)
			}
			return m, nil
		}
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, chatMessage{role: "assistant"})
		}
		m.messages[len(m.messages)-1].content = text
		if m.shouldRefreshStream(text) {
			m.refreshViewport()
		}
		if m.streamCh != nil {
			return m, waitForNext(m.streamCh)
		}

	case claudeToolUseMsg:
		cmd := sanitizeStreamOutput(msg.Command)
		if cmd != "" {
			m.messages = append(m.messages, chatMessage{role: "tool", content: cmd})
		}
		m.refreshViewport()
		if m.streamCh != nil {
			return m, waitForNext(m.streamCh)
		}

	case claudeDoneMsg:
		m.streaming = false
		m.lastStreamRender = time.Time{}
		m.streamCh = nil
		m.cancelStream = nil
		saveChatHistory(m.messages)
		m.refreshViewport()
		if cmd := m.startNextQueuedPrompt(); cmd != nil {
			return m, cmd
		}
		return m, nil

	case claudeErrorMsg:
		m.streaming = false
		m.lastStreamRender = time.Time{}
		m.streamCh = nil
		m.cancelStream = nil
		m.messages = append(m.messages, chatMessage{role: "error", content: msg.Err.Error()})
		saveChatHistory(m.messages)
		m.refreshViewport()
		if cmd := m.startNextQueuedPrompt(); cmd != nil {
			return m, cmd
		}
		return m, nil

	case spinner.TickMsg:
		// Keep the inline "thinking..." spinner inside viewport content animated.
		if m.streaming && m.isInlineThinkingShown() {
			m.refreshViewportNoScroll()
		}
	}

	// Update viewport (scroll)
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	if vpCmd != nil {
		cmds = append(cmds, vpCmd)
	}

	// Update text input — skip mouse events and raw mouse escape sequences
	// that leak through as key messages (e.g. "[<65;25;12M" from SGR mouse protocol)
	skipInput := false
	switch v := msg.(type) {
	case tea.MouseMsg:
		skipInput = true
	case tea.KeyMsg:
		if m.isMouseEscapeKey(v) {
			skipInput = true
		}
	}
	if !skipInput {
		var tiCmd tea.Cmd
		m.input, tiCmd = m.input.Update(msg)
		m.input.SetValue(sanitizePromptInput(m.input.Value()))
		if tiCmd != nil {
			cmds = append(cmds, tiCmd)
		}
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

func (m *ChatModel) refreshViewportNoScroll() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderMessages())
}

func (m *ChatModel) enqueueCurrentInput() (bool, string) {
	text := strings.TrimSpace(sanitizePromptInput(m.input.Value()))
	if text == "" {
		return false, ""
	}
	m.queuedPrompts = append(m.queuedPrompts, text)
	m.input.Reset()
	return true, text
}

func (m *ChatModel) startNextQueuedPrompt() tea.Cmd {
	if len(m.queuedPrompts) == 0 {
		return nil
	}
	next := m.queuedPrompts[0]
	m.queuedPrompts = m.queuedPrompts[1:]
	return m.beginPrompt(next)
}

func (m *ChatModel) beginPrompt(prompt string) tea.Cmd {
	m.messages = append(m.messages, chatMessage{role: "user", content: prompt})
	m.streaming = true
	m.lastStreamRender = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelStream = cancel
	m.refreshViewport()
	return tea.Batch(startClaude(ctx, buildTurnPrompt(m.messages), m.model), m.spinner.Tick)
}

func (m *ChatModel) shouldRefreshStream(delta string) bool {
	now := time.Now()
	if m.lastStreamRender.IsZero() {
		m.lastStreamRender = now
		return true
	}

	// Force refresh quickly on structural tokens.
	if strings.Contains(delta, "\n") || strings.Contains(delta, "```") {
		m.lastStreamRender = now
		return true
	}

	// Limit re-render frequency to keep the UI responsive under high token rate.
	if now.Sub(m.lastStreamRender) >= 40*time.Millisecond {
		m.lastStreamRender = now
		return true
	}
	return false
}

func (m *ChatModel) isInlineThinkingShown() bool {
	if len(m.messages) == 0 {
		return false
	}
	last := m.messages[len(m.messages)-1]
	if last.role == "assistant" {
		return last.content == ""
	}
	return last.role == "user" || last.role == "tool"
}

func (m *ChatModel) renderMessages() string {
	if len(m.messages) == 0 {
		return lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(1, 2).
			Render("Type a message and press Enter to chat with birdy.\nbirdy can read tweets, search, post, and manage your accounts.")
	}

	w := m.width - 2 // small margin
	if w < 1 {
		w = 1
	}

	var b strings.Builder
	for i, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userMsgStyle.Copy().PaddingLeft(2).Width(w).Render("You: " + msg.content))
			b.WriteString("\n\n")
		case "assistant":
			if msg.content != "" {
				useCache := !(m.streaming && i == len(m.messages)-1)
				b.WriteString(m.renderAssistantMarkdown(msg.content, w, useCache))
				// Keep assistant replies visually separated from following turns.
				b.WriteString("\n\n")
			}
		case "tool":
			b.WriteString(toolMsgStyle.Width(w).Render("  > " + msg.content))
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorMsgStyle.Width(w).Render("Error: " + msg.content))
			b.WriteString("\n\n")
		}
	}

	// Show thinking indicator when streaming and last message has no assistant content yet.
	if m.streaming && m.isInlineThinkingShown() {
		b.WriteString(toolMsgStyle.Render(m.spinner.View() + " thinking..."))
		b.WriteString("\n\n")
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

	headerWidth := m.width
	if headerWidth < 1 {
		headerWidth = 1
	}
	leftText := " birdy "
	if lipgloss.Width(leftText) > headerWidth {
		leftText = leftText[:headerWidth]
	}

	rightSpace := headerWidth - lipgloss.Width(leftText)
	if rightSpace < 0 {
		rightSpace = 0
	}
	rightText := m.headerRightInfo(accountInfo, rightSpace)
	gap := headerWidth - lipgloss.Width(leftText) - lipgloss.Width(rightText)
	if gap < 0 {
		gap = 0
	}
	headerText := leftText + strings.Repeat(" ", gap) + rightText
	header := headerStyle.Copy().Padding(0).Width(headerWidth).MaxWidth(headerWidth).Render(headerText)

	// Input (queue indicator on the top-right row inside the command bar)
	input := inputBorderStyle.Render(m.renderCommandBarContent())

	// Footer
	footerText := "enter: send | ctrl+t: model | ctrl+y: copy | tab: accounts | ctrl+c: quit"
	if m.streaming {
		footerText = "tab: queue | esc: cancel | ctrl+c: quit"
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

func (m ChatModel) headerRightInfo(accountInfo string, available int) string {
	if available <= 0 {
		return ""
	}

	statusLabel := "ready"
	if m.streaming {
		statusLabel = "thinking"
	}

	candidates := []string{
		fmt.Sprintf("%s | %s | %s", accountInfo, m.model, statusLabel),
		fmt.Sprintf("%s | %s", m.model, statusLabel),
		statusLabel,
		"",
	}

	for _, c := range candidates {
		if lipgloss.Width(c) <= available {
			return c
		}
	}
	return ""
}

func (m ChatModel) renderCommandBarContent() string {
	innerWidth := m.input.Width
	if innerWidth < 1 {
		innerWidth = 1
	}

	queueLabel := m.commandBarQueueLabel(innerWidth)
	queueNotice := m.commandBarQueueNotice(innerWidth)
	topRow := composeTopRow(innerWidth, queueNotice, queueLabel)
	topRow = lipgloss.NewStyle().Foreground(colorMuted).Render(topRow)

	return topRow + "\n" + m.input.View()
}

func (m ChatModel) commandBarQueueLabel(available int) string {
	if available <= 0 || len(m.queuedPrompts) == 0 {
		return ""
	}

	long := fmt.Sprintf("queued: %d", len(m.queuedPrompts))
	short := fmt.Sprintf("q:%d", len(m.queuedPrompts))

	label := long
	if lipgloss.Width(label) > available {
		label = short
	}
	if lipgloss.Width(label) > available {
		return ""
	}

	return label
}

func (m ChatModel) commandBarQueueNotice(available int) string {
	if available <= 0 || m.queueNotice == "" {
		return ""
	}
	return m.queueNotice
}

func summarizeQueueNotice(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return strings.Repeat(".", max)
	}
	return s[:max-3] + "..."
}

func composeTopRow(width int, left, right string) string {
	if width <= 0 {
		return ""
	}

	if right == "" {
		left = summarizeQueueNotice(left, width)
		pad := width - lipgloss.Width(left)
		if pad < 0 {
			pad = 0
		}
		return left + strings.Repeat(" ", pad)
	}

	rw := lipgloss.Width(right)
	if rw >= width {
		return summarizeQueueNotice(right, width)
	}

	leftWidth := width - rw
	left = summarizeQueueNotice(left, leftWidth)
	pad := leftWidth - lipgloss.Width(left)
	if pad < 0 {
		pad = 0
	}
	return left + strings.Repeat(" ", pad) + right
}

func (m *ChatModel) renderAssistantMarkdown(content string, width int, useCache bool) string {
	if content == "" {
		return ""
	}

	if useCache {
		if m.markdownCache == nil {
			m.markdownCache = make(map[string]string, 128)
		}
		key := fmt.Sprintf("%d|%s", width, content)
		if cached, ok := m.markdownCache[key]; ok {
			return cached
		}
		rendered := m.renderMarkdown(content, width)
		if len(m.markdownCache) > 2048 {
			m.markdownCache = make(map[string]string, 128)
		}
		m.markdownCache[key] = rendered
		return rendered
	}

	return m.renderMarkdown(content, width)
}

func (m *ChatModel) renderMarkdown(content string, width int) string {
	if width < 1 {
		width = 1
	}
	r, err := m.markdownRenderer(width)
	if err != nil {
		return assistantMsgStyle.Width(width).Render(content)
	}
	out, err := r.Render(content)
	if err != nil {
		return assistantMsgStyle.Width(width).Render(content)
	}
	return strings.TrimRight(out, "\n")
}

func (m *ChatModel) markdownRenderer(width int) (*glamour.TermRenderer, error) {
	if m.mdRenderers == nil {
		m.mdRenderers = make(map[int]*glamour.TermRenderer, 8)
	}
	if r, ok := m.mdRenderers[width]; ok {
		return r, nil
	}

	r, err := glamour.NewTermRenderer(
		// Fixed style avoids terminal capability/background probes.
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	m.mdRenderers[width] = r
	return r, nil
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

func (m *ChatModel) isMouseEscapeKey(msg tea.KeyMsg) bool {
	s := msg.String()
	if strings.Contains(s, "[<") || strings.Contains(s, "[M") || strings.Contains(s, "[m") {
		return true
	}

	// Some terminals leak mouse protocol in fragmented key events:
	// alt+[  64;20;8  M
	if s == "alt+[" {
		return true
	}
	return false
}

func (m *ChatModel) handleLeakedMouseKey(msg tea.KeyMsg) bool {
	s := msg.String()

	if m.mouseSeqMode {
		// End of leaked sequence ("M" or "m"). We swallow both terminator and body.
		if strings.ContainsAny(s, "Mm") {
			m.mouseSeqMode = false
			return true
		}
		if isMouseSeqBodyFragment(s) {
			return true
		}
		// Unexpected key: abort fragment mode and let normal handling continue.
		m.mouseSeqMode = false
		return false
	}

	if s == "alt+[" {
		m.mouseSeqMode = true
		return true
	}

	return m.isMouseEscapeKey(msg)
}

func isMouseSeqBodyFragment(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == ';' || r == '<' {
			continue
		}
		return false
	}
	return true
}

func sanitizePromptInput(s string) string {
	if s == "" {
		return s
	}

	cleaned := strings.ReplaceAll(s, "\x1b", "")
	cleaned = strings.ReplaceAll(cleaned, "\a", "")

	cleaned = mouseSeqPattern.ReplaceAllString(cleaned, "")
	cleaned = mouseSeqTailPattern.ReplaceAllString(cleaned, "")
	cleaned = mouseTripletPattern.ReplaceAllString(cleaned, "")
	cleaned = mouseSeqPartialPattern.ReplaceAllString(cleaned, "")
	cleaned = cprSeqPattern.ReplaceAllString(cleaned, "")
	cleaned = cprPartialPattern.ReplaceAllString(cleaned, "")

	cleaned = oscPrefixPattern.ReplaceAllString(cleaned, "")
	cleaned = rgbSeqPattern.ReplaceAllString(cleaned, "")
	cleaned = rgbSeqPartialPattern.ReplaceAllString(cleaned, "")
	cleaned = colorTripletPattern.ReplaceAllString(cleaned, "")
	cleaned = oscResiduePattern.ReplaceAllString(cleaned, "")

	// Keep prompt input printable and single-line.
	var b strings.Builder
	for _, r := range cleaned {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}

	return b.String()
}

func sanitizeStreamOutput(s string) string {
	if s == "" {
		return s
	}

	cleaned := ansiOscPattern.ReplaceAllString(s, "")
	cleaned = ansiCsiPattern.ReplaceAllString(cleaned, "")
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	cleaned = strings.ReplaceAll(cleaned, "\x1b", "")
	cleaned = strings.ReplaceAll(cleaned, "\a", "")

	var b strings.Builder
	for _, r := range cleaned {
		// Keep common readable whitespace; strip other control chars.
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
