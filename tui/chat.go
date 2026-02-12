package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
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
	viewport             viewport.Model
	input                textinput.Model
	spinner              spinner.Model
	messages             []chatMessage
	queuedPrompts        []string
	queueNotice          string
	queueNoticeID        int
	streaming            bool
	streamCh             <-chan tea.Msg
	cancelStream         context.CancelFunc
	width                int
	height               int
	ready                bool
	accountCount         int
	autoQueried          bool
	copied               bool
	model                string
	mouseSeqMode         bool
	followOutput         bool
	historyMode          bool
	historyFiles         []string
	historyIndex         int
	historyPreview       string
	historyError         string
	lastWheelAt          time.Time
	streamTailContent    string
	streamTailRendered   string
	streamTailWidth      int
	streamTailRenderedAt time.Time
	markdownCache        map[string]string
	mdRenderers          map[int]*glamour.TermRenderer
	lastStreamRender     time.Time
	nowFn                func() time.Time
	readClipboardFn      func() (string, error)
	writeClipboardFn     func(string) error
}

type clearCopiedMsg struct{}
type clearQueueNoticeMsg struct {
	ID int
}
type clipboardReadMsg struct {
	Text string
	Err  error
}
type clipboardWriteMsg struct {
	Err error
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
	urlPattern             = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"'` + "`" + `]+`)
	ansiCsiPattern         = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOscPattern         = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

func NewChatModel() ChatModel {
	ti := textinput.New()
	ti.Placeholder = "Ask birdy anything..."
	ti.Focus()
	ti.CharLimit = 4096
	ti.PromptStyle = lipgloss.NewStyle().
		Foreground(colorLightFg).
		Background(colorDarkBg).
		Bold(true)
	ti.TextStyle = lipgloss.NewStyle().
		Foreground(colorLightFg).
		Background(colorDarkBg)
	ti.PlaceholderStyle = lipgloss.NewStyle().
		Foreground(colorMuted).
		Background(colorDarkBg)
	ti.Cursor.Style = lipgloss.NewStyle().
		Foreground(colorDarkBg).
		Background(colorBlue)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	model := "sonnet"
	if s, err := state.Load(); err == nil && s.Model != "" {
		model = s.Model
	}

	m := ChatModel{
		input:            ti,
		spinner:          sp,
		model:            model,
		followOutput:     true,
		markdownCache:    make(map[string]string, 128),
		mdRenderers:      make(map[int]*glamour.TermRenderer, 8),
		nowFn:            time.Now,
		readClipboardFn:  clipboard.ReadAll,
		writeClipboardFn: clipboard.WriteAll,
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
	topGutterHeight = 1
	headerHeight    = 3
	feedChrome      = 3 // panel border + section title row
	commandHeight   = 5 // panel border + title row + simple input block
	footerHeight    = 4 // panel border + key hints row + save-path row
	chatOverhead    = topGutterHeight + headerHeight + feedChrome + commandHeight + footerHeight
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

	if mm, ok := msg.(tea.MouseMsg); ok {
		if m.historyMode {
			// Keep history list anchored; history navigation is keyboard-driven.
			return m, nil
		}
		if !m.shouldHandleMouseWheel(mm) {
			return m, nil
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
		inputWidth := m.width - 4 // command panel border + simple inline input
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.input.Width = inputWidth

		vpWidth := m.viewportContentWidth()

		vpHeight := m.height - chatOverhead
		if vpHeight < 1 {
			vpHeight = 1
		}

		if !m.ready {
			m.viewport = viewport.New(vpWidth, vpHeight)
			m.viewport.SetContent(m.renderMessages())
			m.ready = true
		} else {
			m.viewport.Width = vpWidth
			m.viewport.Height = vpHeight
			m.viewport.SetContent(m.renderMessages())
		}
		return m, nil

	case tea.KeyMsg:
		if m.handleLeakedMouseKey(msg) {
			// Leaked mouse/scroll escape keys are noise; swallow quickly.
			// Avoid running full prompt sanitization on every burst event,
			// which can stall the UI under heavy scroll input.
			return m, nil
		}

		if m.historyMode {
			switch msg.String() {
			case "esc", "/":
				m.historyMode = false
				m.historyError = ""
				m.refreshViewport()
				return m, nil
			case "up", "k":
				if m.historyIndex > 0 {
					m.historyIndex--
					m.refreshHistoryPreview()
					m.refreshViewport()
					m.viewport.GotoTop()
				}
				return m, nil
			case "down", "j":
				if m.historyIndex < len(m.historyFiles)-1 {
					m.historyIndex++
					m.refreshHistoryPreview()
					m.refreshViewport()
					m.viewport.GotoTop()
				}
				return m, nil
			case "home", "g":
				if len(m.historyFiles) > 0 {
					m.historyIndex = 0
					m.refreshHistoryPreview()
					m.refreshViewport()
					m.viewport.GotoTop()
				}
				return m, nil
			case "end", "G":
				if n := len(m.historyFiles); n > 0 {
					m.historyIndex = n - 1
					m.refreshHistoryPreview()
					m.refreshViewport()
					m.viewport.GotoTop()
				}
				return m, nil
			case "enter":
				return m, m.loadSelectedHistory()
			default:
				return m, nil
			}
		}

		switch msg.String() {
		case "/":
			if strings.TrimSpace(m.input.Value()) != "" {
				break
			}
			m.openHistoryMode()
			m.refreshViewport()
			m.viewport.GotoTop()
			return m, nil

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
				return m, m.writeClipboardCmd(text)
			}
		case "ctrl+v":
			return m, m.readClipboardCmd()

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

	case clipboardReadMsg:
		if msg.Err != nil {
			m.queueNoticeID++
			id := m.queueNoticeID
			m.queueNotice = "paste failed"
			return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
				return clearQueueNoticeMsg{ID: id}
			})
		}
		if msg.Text != "" {
			next := sanitizePromptInput(m.input.Value() + msg.Text)
			if next != m.input.Value() {
				m.input.SetValue(next)
				m.input.CursorEnd()
			}
		}
		return m, nil

	case clipboardWriteMsg:
		if msg.Err != nil {
			m.queueNoticeID++
			id := m.queueNoticeID
			m.queueNotice = "copy failed"
			return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
				return clearQueueNoticeMsg{ID: id}
			})
		}
		m.copied = true
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearCopiedMsg{} })

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
		if m.followOutput && m.shouldRefreshStream(text) {
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
		if m.followOutput && m.shouldRefreshStream(text) {
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
		if m.followOutput {
			m.refreshViewport()
		}
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
		if m.historyMode {
			m.queueNoticeID++
			id := m.queueNoticeID
			m.queueNotice = "stream failed in background"
			m.refreshViewport()
			if cmd := m.startNextQueuedPrompt(); cmd != nil {
				return m, cmd
			}
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return clearQueueNoticeMsg{ID: id}
			})
		}
		m.messages = append(m.messages, chatMessage{role: "error", content: msg.Err.Error()})
		saveChatHistory(m.messages)
		m.refreshViewport()
		if cmd := m.startNextQueuedPrompt(); cmd != nil {
			return m, cmd
		}
		return m, nil

	case spinner.TickMsg:
		// Keep the inline "thinking..." spinner inside viewport content animated.
		if m.streaming && m.followOutput && m.isInlineThinkingShown() {
			m.refreshViewportNoScroll()
		}
	}

	// Update viewport (scroll)
	prevFollow := m.followOutput
	prevYOffset := m.viewport.YOffset
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	if m.viewport.YOffset != prevYOffset {
		// User scrolled; stop auto-follow unless they've reached bottom again.
		m.followOutput = m.viewport.AtBottom()
		if m.followOutput && !prevFollow {
			// Re-entering follow mode should immediately refresh the latest tail.
			m.streamTailContent = ""
			m.streamTailRendered = ""
			m.streamTailRenderedAt = time.Time{}
			m.streamTailWidth = 0
			// User returned to bottom; catch up with latest streamed content.
			m.refreshViewport()
		}
	}
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
		prevValue := m.input.Value()
		m.input, tiCmd = m.input.Update(msg)
		// Sanitize only when the input actually changed.
		if nextValue := m.input.Value(); nextValue != prevValue {
			m.input.SetValue(sanitizePromptInput(nextValue))
		}
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
	if m.followOutput {
		m.viewport.GotoBottom()
	}
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
	m.followOutput = true
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
	if m.historyMode {
		return m.renderHistoryMessages()
	}

	if len(m.messages) == 0 {
		return lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(1, 2).
			Render("BIRDY terminal ready.\nType a message and press Enter.")
	}

	w := m.viewport.Width
	if w < 1 {
		w = m.width - 2
	}
	if w < 1 {
		w = 1
	}

	var b strings.Builder
	for i, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userMsgStyle.Copy().PaddingLeft(2).Width(w).Render("You: " + linkifyURLs(msg.content)))
			b.WriteString("\n\n")
		case "assistant":
			if msg.content != "" {
				isStreamingTail := m.streaming && i == len(m.messages)-1
				if isStreamingTail {
					b.WriteString(m.renderStreamingTailMarkdown(msg.content, w))
				} else {
					b.WriteString(m.renderAssistantMarkdown(msg.content, w, true))
				}
				// Keep assistant replies visually separated from following turns.
				b.WriteString("\n\n")
			}
		case "tool":
			b.WriteString(toolMsgStyle.Width(w).Render("  > " + linkifyURLs(msg.content)))
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorMsgStyle.Width(w).Render("Error: " + linkifyURLs(msg.content)))
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

func (m *ChatModel) openHistoryMode() {
	m.historyMode = true
	m.historyError = ""
	m.historyFiles = nil
	m.historyIndex = 0
	m.historyPreview = ""

	files, err := listChatHistoryFiles(128)
	if err != nil {
		m.historyError = fmt.Sprintf("failed to load history: %v", err)
		return
	}
	m.historyFiles = files
	if len(m.historyFiles) == 0 {
		m.historyPreview = "No saved chats yet."
		return
	}
	m.refreshHistoryPreview()
}

func (m *ChatModel) refreshHistoryPreview() {
	if len(m.historyFiles) == 0 {
		m.historyPreview = "No saved chats yet."
		return
	}
	if m.historyIndex < 0 {
		m.historyIndex = 0
	}
	if m.historyIndex >= len(m.historyFiles) {
		m.historyIndex = len(m.historyFiles) - 1
	}
	path := m.historyFiles[m.historyIndex]
	preview, err := loadChatHistoryPreview(path, 24000)
	if err != nil {
		m.historyError = fmt.Sprintf("failed to read %s: %v", filepath.Base(path), err)
		m.historyPreview = ""
		return
	}
	m.historyError = ""
	m.historyPreview = preview
}

func (m *ChatModel) renderHistoryMessages() string {
	w := m.viewport.Width
	if w < 1 {
		w = 1
	}

	var b strings.Builder
	b.WriteString(toolMsgStyle.Width(w).Render("saved at: " + chatHistoryDisplayDir()))
	b.WriteString("\n")
	b.WriteString(toolMsgStyle.Width(w).Render("up/down: select | enter: open full chat | esc or /: close"))
	b.WriteString("\n\n")

	if m.historyError != "" {
		b.WriteString(errorMsgStyle.Width(w).Render(m.historyError))
		b.WriteString("\n")
		return b.String()
	}

	if len(m.historyFiles) == 0 {
		b.WriteString(toolMsgStyle.Width(w).Render("No saved chats yet."))
		b.WriteString("\n")
		return b.String()
	}

	start := 0
	const maxListRows = 8
	if m.historyIndex >= maxListRows {
		start = m.historyIndex - maxListRows + 1
	}
	end := start + maxListRows
	if end > len(m.historyFiles) {
		end = len(m.historyFiles)
	}
	for i := start; i < end; i++ {
		line := fmt.Sprintf("  %2d. %s", i+1, chatHistoryFileLabel(m.historyFiles[i]))
		style := toolMsgStyle
		if i == m.historyIndex {
			line = fmt.Sprintf("> %2d. %s", i+1, chatHistoryFileLabel(m.historyFiles[i]))
			style = accountSelectedStyle
		}
		b.WriteString(style.Width(w).Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	selected := m.historyFiles[m.historyIndex]
	selectedLine := strings.Replace(selected, os.Getenv("HOME"), "~", 1)
	b.WriteString(toolMsgStyle.Width(w).Render(selectedLine))
	b.WriteString("\n\n")
	b.WriteString(sectionTitleStyle.Width(w).Render("GLIMPSE"))
	b.WriteString("\n")
	b.WriteString(toolMsgStyle.Width(w).Render("Press Enter to open full transcript in feed."))
	b.WriteString("\n")
	previewLines := m.historyPreviewLines()
	b.WriteString(m.renderMarkdownSnippet(m.historyPreview, w, previewLines))
	b.WriteString("\n")
	return b.String()
}

func (m ChatModel) View() string {
	if !m.ready {
		return ""
	}
	panelWidth := m.width - 2
	if panelWidth < 1 {
		panelWidth = 1
	}

	// Header
	accountInfo := "no accounts"
	if m.accountCount > 0 {
		accountInfo = fmt.Sprintf("%d account", m.accountCount)
		if m.accountCount > 1 {
			accountInfo += "s"
		}
	}

	headerWidth := m.width - 4
	if headerWidth < 1 {
		headerWidth = 1
	}
	leftCandidates := []string{
		headerBrandStyle.Render("BIRDY") + " " + headerDeskStyle.Render("X DESK"),
		"BIRDY X DESK",
		headerBrandStyle.Render("BIRDY"),
		"BIRDY",
	}

	headerText := summarizeQueueNotice("BIRDY", headerWidth)
	fallbackHeader := ""
	for _, leftText := range leftCandidates {
		leftWidth := lipgloss.Width(leftText)
		if leftWidth > headerWidth {
			continue
		}
		rightSpace := headerWidth - leftWidth
		if rightSpace < 0 {
			rightSpace = 0
		}
		rightText := m.headerRightInfo(accountInfo, rightSpace)
		rightWidth := lipgloss.Width(rightText)
		if leftWidth+rightWidth > headerWidth {
			continue
		}

		gap := headerWidth - leftWidth - rightWidth
		// Keep compact status labels (READY/LIVE/PAUSED) visually attached
		// to the desk label on narrow layouts.
		if rightText != "" && !strings.Contains(rightText, "|") && leftWidth+1+rightWidth <= headerWidth {
			gap = 1
		}
		if gap < 0 {
			gap = 0
		}
		candidate := leftText + strings.Repeat(" ", gap) + rightText
		if fallbackHeader == "" {
			fallbackHeader = candidate
		}
		if rightText != "" {
			headerText = candidate
			break
		}
	}
	if headerText == summarizeQueueNotice("BIRDY", headerWidth) && fallbackHeader != "" {
		headerText = fallbackHeader
	}
	headerStrip := inverseLineStyle.Width(headerWidth).Render(headerText)
	header := headerStyle.Copy().Width(panelWidth).Render(headerStrip)

	feedLabel := "FEED"
	if m.historyMode {
		feedLabel = "HISTORY"
	}
	feedBodyWidth := m.feedBodyWidth()
	feedTitle := sectionTitleStyle.Width(feedBodyWidth).Render(feedLabel)
	body := lipgloss.NewStyle().
		Background(colorDarkBg).
		Foreground(colorLightFg).
		Width(feedBodyWidth).
		Height(m.viewport.Height).
		Render(m.renderFeedBodyWithScrollbar())
	feedContent := lipgloss.JoinVertical(lipgloss.Left, feedTitle, body)
	feed := feedPanelStyle.Width(panelWidth).Render(feedContent)

	commandTitleRowWidth := m.width - 4
	if commandTitleRowWidth < 1 {
		commandTitleRowWidth = 1
	}
	commandLeft := "COMMAND"
	commandRight := "QUEUE " + m.commandQueueState()
	if m.historyMode {
		commandLeft = "HISTORY NAV"
		commandRight = m.historySelectionStatus()
	}
	commandTitleRow := composeTopRow(commandTitleRowWidth, commandLeft, commandRight)
	commandTitle := commandMetaStyle.Copy().Bold(true).Render(commandTitleRow)
	input := m.renderCommandBarContent()
	command := commandPanelStyle.Width(panelWidth).Render(lipgloss.JoinVertical(lipgloss.Left, commandTitle, input))

	keysRowWidth := m.width - 4 // keys panel border + row padding
	if keysRowWidth < 1 {
		keysRowWidth = 1
	}
	keysLabel := "KEYS  "
	keysHintWidth := keysRowWidth - lipgloss.Width(keysLabel)
	if keysHintWidth < 0 {
		keysHintWidth = 0
	}
	keysText := m.footerHintText(keysHintWidth)
	if m.copied {
		keysText = "COPIED"
	}
	keysRow := fitFooterRow(keysLabel+keysText, keysRowWidth)
	saveRow := fitFooterRow("save: "+chatHistoryDisplayDir(), keysRowWidth)
	keysTop := inverseSubtleLineStyle.Width(keysRowWidth).Render(keysRow)
	keysBottom := footerPathStyle.Render(saveRow)
	keysContent := lipgloss.NewStyle().Padding(0, 1).Render(keysTop + "\n" + keysBottom)
	footer := keysPanelStyle.Width(panelWidth).Render(keysContent)

	layout := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Width(panelWidth).Render(" "),
		header,
		feed,
		command,
		footer,
	)
	return appStyle.Copy().Width(m.width).Height(m.height).Render(layout)
}

func (m ChatModel) headerRightInfo(accountInfo string, available int) string {
	if available <= 0 {
		return ""
	}

	statusLabel := "READY"
	if m.historyMode {
		statusLabel = "HISTORY"
	} else if m.streaming {
		statusLabel = "LIVE"
		if !m.followOutput {
			statusLabel = "PAUSED"
		}
	}

	model := strings.ToUpper(m.model)
	thinkingLabel := ""
	if m.streaming && !m.historyMode {
		thinkingLabel = " thinking..."
	}

	candidates := []string{
		fmt.Sprintf("ACCTS %s | MODEL %s | %s%s", accountInfo, model, statusLabel, thinkingLabel),
		fmt.Sprintf("%s | %s%s", model, statusLabel, thinkingLabel),
		statusLabel + thinkingLabel,
		thinkingLabel,
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
	if m.historyMode {
		help := summarizeQueueNotice("enter: open full chat | up/down: navigate | esc or /: close", innerWidth)
		selected := "no history selected"
		if len(m.historyFiles) > 0 && m.historyIndex >= 0 && m.historyIndex < len(m.historyFiles) {
			selected = strings.Replace(m.historyFiles[m.historyIndex], os.Getenv("HOME"), "~", 1)
		}
		selected = summarizeQueueNotice(selected, innerWidth)
		return lipgloss.NewStyle().Foreground(colorMuted).Render(help) + "\n" + toolMsgStyle.Width(innerWidth).Render(selected)
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

func (m ChatModel) commandQueueState() string {
	if len(m.queuedPrompts) == 0 {
		return "IDLE"
	}
	return fmt.Sprintf("%d", len(m.queuedPrompts))
}

func (m ChatModel) feedBodyWidth() int {
	w := m.width - 2 // feed panel border
	if w < 1 {
		w = 1
	}
	return w
}

func (m ChatModel) viewportContentWidth() int {
	w := m.feedBodyWidth()
	if w > 1 {
		return w - 1 // reserve one column for scrollbar
	}
	return w
}

func (m ChatModel) renderFeedBodyWithScrollbar() string {
	content := m.viewport.View()
	feedW := m.feedBodyWidth()
	contentW := m.viewport.Width
	if contentW < 1 {
		contentW = 1
	}
	if feedW <= contentW {
		return content
	}

	h := m.viewport.Height
	if h < 1 {
		h = 1
	}
	lines := strings.Split(content, "\n")
	if len(lines) < h {
		pad := make([]string, h-len(lines))
		lines = append(lines, pad...)
	} else if len(lines) > h {
		lines = lines[:h]
	}

	thumbTop, thumbHeight := m.feedScrollbarGeometry(h)
	var b strings.Builder
	for i := 0; i < h; i++ {
		line := lipgloss.NewStyle().
			MaxWidth(contentW).
			Width(contentW).
			Render(lines[i])

		barStyle := scrollbarTrackStyle
		barRune := "│"
		if i >= thumbTop && i < thumbTop+thumbHeight {
			barStyle = scrollbarThumbStyle
			barRune = "█"
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, line, barStyle.Render(barRune)))
		if i < h-1 {
			b.WriteRune('\n')
		}
	}
	return b.String()
}

func (m ChatModel) historyPreviewLines() int {
	h := m.viewport.Height
	if h <= 0 {
		return 6
	}
	// Keep timestamp list visible and preview intentionally concise.
	const fixedRows = 18
	n := h - fixedRows
	if n < 4 {
		return 4
	}
	if n > 10 {
		return 10
	}
	return n
}

func (m ChatModel) renderMarkdownSnippet(content string, width, maxLines int) string {
	rendered := m.renderAssistantMarkdown(content, width, true)
	lines := strings.Split(rendered, "\n")
	if maxLines <= 0 || len(lines) <= maxLines {
		return rendered
	}
	lines = lines[:maxLines]
	lines = append(lines, toolMsgStyle.Width(width).Render("..."))
	return strings.Join(lines, "\n")
}

func (m ChatModel) feedScrollbarGeometry(height int) (thumbTop, thumbHeight int) {
	if height <= 0 {
		return 0, 0
	}
	total := m.viewport.TotalLineCount()
	if total <= 0 {
		return 0, height
	}
	if total <= height {
		return 0, height
	}

	thumbHeight = (height*height + total - 1) / total
	if thumbHeight < 1 {
		thumbHeight = 1
	}
	if thumbHeight > height {
		thumbHeight = height
	}

	maxOffset := total - height
	if maxOffset <= 0 || height <= thumbHeight {
		return 0, thumbHeight
	}

	y := m.viewport.YOffset
	if y < 0 {
		y = 0
	}
	if y > maxOffset {
		y = maxOffset
	}
	thumbTop = (y*(height-thumbHeight) + maxOffset/2) / maxOffset
	if thumbTop < 0 {
		thumbTop = 0
	}
	maxTop := height - thumbHeight
	if thumbTop > maxTop {
		thumbTop = maxTop
	}
	return thumbTop, thumbHeight
}

func (m ChatModel) historySelectionStatus() string {
	if len(m.historyFiles) == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d", m.historyIndex+1, len(m.historyFiles))
}

func (m ChatModel) footerHintText(available int) string {
	if available <= 0 {
		return ""
	}

	var candidates []string
	if m.historyMode {
		candidates = []string{
			"^/v: select | enter: open | esc: close | /: close | ctrl+c: quit",
			"^/v: select | enter: open | esc: close | ctrl+c: quit",
			"enter: open | esc: close | ctrl+c: quit",
			"esc: close",
		}
	} else if m.streaming {
		candidates = []string{
			"^/v: scroll | tab: queue | ctrl+v: paste | esc: cancel | home/end: pause/live | ctrl+c: quit | hist: /",
			"^/v: scroll | tab: queue | esc: cancel | home/end: pause/live | ctrl+c: quit | hist: /",
			"^/v: scroll | tab: queue | ctrl+v: paste | esc: cancel | ctrl+c: quit | hist: /",
			"^/v: scroll | tab: queue | esc: cancel | ctrl+c: quit | hist: /",
			"^/v: scroll | tab: queue | esc: cancel | ctrl+c: quit",
			"^/v: scroll | esc: cancel | ctrl+c: quit",
			"esc: cancel",
		}
	} else {
		candidates = []string{
			"^/v: scroll | enter: send | ctrl+t: model | ctrl+y: copy | ctrl+v: paste | tab: accounts | ctrl+c: quit | hist: /",
			"^/v: scroll | enter: send | ctrl+t: model | ctrl+y: copy | tab: accounts | ctrl+c: quit | hist: /",
			"^/v: scroll | enter: send | ctrl+v: paste | tab: accounts | ctrl+c: quit | hist: /",
			"^/v: scroll | enter: send | ctrl+t: model | tab: accounts | ctrl+c: quit | hist: /",
			"^/v: scroll | enter: send | tab: accounts | ctrl+c: quit | hist: /",
			"^/v: scroll | enter: send | tab: accounts | ctrl+c: quit",
			"^/v: scroll | enter: send | ctrl+c: quit",
		}
	}

	for _, c := range candidates {
		if lipgloss.Width(c) <= available {
			return c
		}
	}
	return summarizeQueueNotice(candidates[len(candidates)-1], available)
}

func fitFooterRow(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = summarizeQueueNotice(text, width)
	pad := width - lipgloss.Width(text)
	if pad < 0 {
		pad = 0
	}
	return text + strings.Repeat(" ", pad)
}

func (m *ChatModel) loadSelectedHistory() tea.Cmd {
	if len(m.historyFiles) == 0 {
		return nil
	}
	if m.historyIndex < 0 || m.historyIndex >= len(m.historyFiles) {
		m.historyIndex = 0
	}
	path := m.historyFiles[m.historyIndex]
	messages, err := loadChatHistoryMessages(path)
	if err != nil {
		m.historyError = fmt.Sprintf("failed to load %s: %v", filepath.Base(path), err)
		m.refreshViewport()
		return nil
	}

	m.messages = messages
	m.streaming = false
	m.streamCh = nil
	m.cancelStream = nil
	m.historyMode = false
	m.historyError = ""
	m.followOutput = true
	m.lastStreamRender = time.Time{}
	m.refreshViewport()

	m.queueNoticeID++
	id := m.queueNoticeID
	m.queueNotice = "loaded: " + summarizeQueueNotice(chatHistoryFileLabel(path), 40)
	return tea.Tick(1600*time.Millisecond, func(time.Time) tea.Msg {
		return clearQueueNoticeMsg{ID: id}
	})
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

func (m *ChatModel) renderStreamingTailMarkdown(content string, width int) string {
	if content == "" {
		return ""
	}
	if width < 1 {
		width = 1
	}

	// New turn or wrap-width change invalidates the tail cache.
	if m.streamTailWidth != width || len(content) < len(m.streamTailContent) {
		m.streamTailContent = ""
		m.streamTailRendered = ""
		m.streamTailRenderedAt = time.Time{}
		m.streamTailWidth = width
	}

	if content == m.streamTailContent && m.streamTailRendered != "" {
		return m.streamTailRendered
	}

	// While user is reviewing history, keep the last rendered snapshot and
	// avoid expensive markdown work on every incoming token.
	if !m.followOutput && m.streamTailRendered != "" {
		return m.streamTailRendered
	}

	now := time.Now()
	if m.nowFn != nil {
		now = m.nowFn()
	}

	shouldRender := m.streamTailRendered == "" ||
		now.Sub(m.streamTailRenderedAt) >= 120*time.Millisecond ||
		hasStreamingStructureDelta(m.streamTailContent, content)

	if !shouldRender && m.streamTailRendered != "" {
		return m.streamTailRendered
	}

	rendered := m.renderMarkdown(content, width)
	m.streamTailContent = content
	m.streamTailRendered = rendered
	m.streamTailRenderedAt = now
	m.streamTailWidth = width
	return rendered
}

func hasStreamingStructureDelta(prev, next string) bool {
	if prev == "" {
		return true
	}
	if !strings.HasPrefix(next, prev) {
		return true
	}
	delta := next[len(prev):]
	if delta == "" {
		return false
	}
	if len(delta) >= 256 {
		return true
	}
	return strings.Contains(delta, "\n") || strings.Contains(delta, "```")
}

func (m *ChatModel) renderMarkdown(content string, width int) string {
	if width < 1 {
		width = 1
	}
	r, err := m.markdownRenderer(width)
	if err != nil {
		return linkifyURLs(assistantMsgStyle.Width(width).Render(content))
	}
	out, err := r.Render(content)
	if err != nil {
		return linkifyURLs(assistantMsgStyle.Width(width).Render(content))
	}
	return linkifyURLs(strings.TrimRight(out, "\n"))
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

func (m ChatModel) readClipboardCmd() tea.Cmd {
	readFn := m.readClipboardFn
	if readFn == nil {
		return nil
	}
	return func() tea.Msg {
		text, err := readFn()
		return clipboardReadMsg{Text: text, Err: err}
	}
}

func (m ChatModel) writeClipboardCmd(text string) tea.Cmd {
	writeFn := m.writeClipboardFn
	if writeFn == nil {
		return nil
	}
	return func() tea.Msg {
		return clipboardWriteMsg{Err: writeFn(text)}
	}
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

func (m *ChatModel) shouldHandleMouseWheel(msg tea.MouseMsg) bool {
	if msg.Action != tea.MouseActionPress {
		return false
	}
	if !isWheelButton(msg.Button) {
		return false
	}

	now := time.Now()
	if m.nowFn != nil {
		now = m.nowFn()
	}

	// Coalesce dense wheel bursts to avoid rendering stalls.
	if !m.lastWheelAt.IsZero() && now.Sub(m.lastWheelAt) < 6*time.Millisecond {
		return false
	}
	m.lastWheelAt = now
	return true
}

func isWheelButton(btn tea.MouseButton) bool {
	switch btn {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown, tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	default:
		return false
	}
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

func linkifyURLs(s string) string {
	if s == "" {
		return ""
	}

	return urlPattern.ReplaceAllStringFunc(s, func(raw string) string {
		url, tail := splitURLAndTrailingPunctuation(raw)
		if url == "" {
			return raw
		}
		href := url
		if strings.HasPrefix(strings.ToLower(href), "www.") {
			href = "https://" + href
		}
		return terminalHyperlink(href, url) + tail
	})
}

func splitURLAndTrailingPunctuation(raw string) (url, tail string) {
	url = raw
	for len(url) > 0 {
		r, sz := utf8.DecodeLastRuneInString(url)
		if sz == 0 {
			break
		}

		trim := false
		switch r {
		case '.', ',', ';', ':', '!', '?':
			trim = true
		case ')':
			trim = strings.Count(url, "(") < strings.Count(url, ")")
		case ']':
			trim = strings.Count(url, "[") < strings.Count(url, "]")
		case '}':
			trim = strings.Count(url, "{") < strings.Count(url, "}")
		}
		if !trim {
			break
		}

		tail = string(r) + tail
		url = url[:len(url)-sz]
	}
	return url, tail
}

func terminalHyperlink(target, label string) string {
	if target == "" || label == "" {
		return label
	}
	// OSC 8 hyperlink sequence.
	return "\x1b]8;;" + target + "\x1b\\" + label + "\x1b]8;;\x1b\\"
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
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(' ')
			continue
		}
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
