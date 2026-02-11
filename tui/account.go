package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/guzus/birdy/internal/store"
)

type accountView int

const (
	accountViewList accountView = iota
	accountViewAdd
)

const (
	accountTopGutterHeight = 1
	accountHeaderHeight    = 3
	accountBodyChrome      = 3 // panel border + section title
	accountFooterHeight    = 3
	accountOverhead        = accountTopGutterHeight + accountHeaderHeight + accountBodyChrome + accountFooterHeight
)

// AccountModel manages the account list and add-account form.
type AccountModel struct {
	width    int
	height   int
	view     accountView
	accounts []store.Account
	cursor   int
	err      string

	// Add form fields
	inputs     [3]textinput.Model
	focusIndex int
}

func NewAccountModel() AccountModel {
	m := AccountModel{}
	m.loadAccounts()
	m.initInputs()
	return m
}

func (m *AccountModel) initInputs() {
	for i := range m.inputs {
		t := textinput.New()
		t.CharLimit = 256
		t.Prompt = ""
		t.PromptStyle = lipgloss.NewStyle().Foreground(colorLightFg).Background(colorDarkBg)
		t.TextStyle = lipgloss.NewStyle().Foreground(colorLightFg).Background(colorDarkBg)
		t.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorMuted).Background(colorDarkBg)
		t.Cursor.Style = lipgloss.NewStyle().Foreground(colorDarkBg).Background(colorBlue)
		switch i {
		case 0:
			t.Placeholder = "account name"
		case 1:
			t.Placeholder = "auth_token value"
			t.EchoMode = textinput.EchoPassword
		case 2:
			t.Placeholder = "ct0 value"
			t.EchoMode = textinput.EchoPassword
		}
		m.inputs[i] = t
	}
}

func (m *AccountModel) loadAccounts() {
	st, err := store.Open()
	if err != nil {
		m.err = err.Error()
		return
	}
	m.accounts = st.List()
	m.err = ""
}

func (m AccountModel) Init() tea.Cmd {
	return nil
}

func (m AccountModel) Update(msg tea.Msg) (AccountModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputWidth := m.accountBodyWidth() - 18
		if inputWidth < 20 {
			inputWidth = 20
		}
		for i := range m.inputs {
			m.inputs[i].Width = inputWidth
		}
		return m, nil

	case tea.KeyMsg:
		if m.view == accountViewAdd {
			return m.updateAddForm(msg)
		}
		return m.updateList(msg)
	}

	// Forward non-key messages (e.g. blink) to focused input
	if m.view == accountViewAdd {
		cmd := m.updateInputs(msg)
		return m, cmd
	}

	return m, nil
}

func (m AccountModel) updateList(msg tea.KeyMsg) (AccountModel, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc":
		return m, func() tea.Msg { return switchScreenMsg{target: screenChat} }

	case "j", "down":
		if m.cursor < len(m.accounts)-1 {
			m.cursor++
		}

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}

	case "a":
		m.view = accountViewAdd
		m.focusIndex = 0
		m.err = ""
		for i := range m.inputs {
			m.inputs[i].Reset()
			if i == 0 {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, textinput.Blink

	case "d":
		if len(m.accounts) > 0 && m.cursor < len(m.accounts) {
			name := m.accounts[m.cursor].Name
			st, err := store.Open()
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if err := st.Remove(name); err != nil {
				m.err = err.Error()
				return m, nil
			}
			if err := st.Save(); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.loadAccounts()
			if m.cursor >= len(m.accounts) && m.cursor > 0 {
				m.cursor--
			}
		}
	}

	return m, nil
}

func (m AccountModel) updateAddForm(msg tea.KeyMsg) (AccountModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = accountViewList
		m.err = ""
		return m, nil

	case "tab", "shift+tab":
		if msg.String() == "shift+tab" {
			m.focusIndex--
			if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs) - 1
			}
		} else {
			m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
		}
		for i := range m.inputs {
			if i == m.focusIndex {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, textinput.Blink

	case "enter":
		name := strings.TrimSpace(m.inputs[0].Value())
		authToken := strings.TrimSpace(m.inputs[1].Value())
		ct0 := strings.TrimSpace(m.inputs[2].Value())

		if name == "" || authToken == "" || ct0 == "" {
			m.err = "all fields are required"
			return m, nil
		}

		st, err := store.Open()
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		if err := st.Add(name, authToken, ct0); err != nil {
			m.err = err.Error()
			return m, nil
		}
		if err := st.Save(); err != nil {
			m.err = err.Error()
			return m, nil
		}

		m.loadAccounts()
		m.view = accountViewList
		m.err = ""
		return m, nil
	}

	// Forward other keys to the focused input
	cmd := m.updateInputs(msg)
	return m, cmd
}

func (m *AccountModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}
	return tea.Batch(cmds...)
}

func (m AccountModel) View() string {
	if m.width == 0 {
		return ""
	}
	panelWidth := m.accountPanelWidth()
	innerWidth := m.accountBodyWidth()

	title := "ACCOUNTS"
	mode := "LIST"
	if m.view == accountViewAdd {
		mode = "ADD"
	}
	right := fmt.Sprintf("%d configured | %s", len(m.accounts), mode)
	headerText := composeTopRow(innerWidth, title, right)
	header := headerStyle.Copy().Width(panelWidth).Render(headerText)

	var content string
	bodyLabel := "LIST"
	if m.view == accountViewAdd {
		content = m.viewAddForm()
		bodyLabel = "ADD ACCOUNT"
	} else {
		content = m.viewList()
	}

	var footer string
	if m.view == accountViewAdd {
		footer = keysPanelStyle.Width(panelWidth).
			Render(composeTopRow(innerWidth, "KEYS", "tab: next | enter: save | esc: back"))
	} else {
		footer = keysPanelStyle.Width(panelWidth).
			Render(composeTopRow(innerWidth, "KEYS", "j/k: move | a: add | d: delete | tab/esc: back"))
	}

	contentHeight := m.height - accountOverhead
	if contentHeight < 1 {
		contentHeight = 1
	}
	content = lipgloss.NewStyle().
		Height(contentHeight).
		Width(innerWidth).
		Background(colorDarkBg).
		Render(content)
	bodyTitle := sectionTitleStyle.Width(innerWidth).Render(bodyLabel)
	bodyPanel := feedPanelStyle.Width(panelWidth).Render(lipgloss.JoinVertical(lipgloss.Left, bodyTitle, content))

	layout := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Width(panelWidth).Render(" "),
		header,
		bodyPanel,
		footer,
	)
	return appStyle.Copy().Width(m.width).Height(m.height).Render(layout)
}

func (m AccountModel) viewList() string {
	w := m.accountBodyWidth()
	if w < 1 {
		w = 1
	}

	if m.err != "" {
		return errorMsgStyle.Width(w).Padding(1, 1).Render("Error: " + m.err)
	}

	if len(m.accounts) == 0 {
		return lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorDarkBg).
			Padding(1, 1).
			Width(w).
			Render("No accounts configured. Press 'a' to add one.")
	}

	var b strings.Builder
	b.WriteString(accountListHeaderStyle.Width(w).Render(fmt.Sprintf("%-24s %-6s %-16s", "NAME", "USES", "LAST USED")))
	b.WriteString("\n")
	for i, a := range m.accounts {
		prefix := "  "
		style := accountNormalStyle.Copy()
		if i == m.cursor {
			prefix = "> "
			style = accountSelectedStyle.Copy()
		}

		lastUsed := "never"
		if !a.LastUsed.IsZero() {
			lastUsed = a.LastUsed.Format("2006-01-02 15:04")
		}

		nameCol := fitAccountText(a.Name, 24)
		line := fmt.Sprintf("%s%-24s %-6d %-16s", prefix, nameCol, a.UseCount, lastUsed)
		b.WriteString(style.Width(w).Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(accountHintStyle.Width(w).Render("Tip: press 'a' to add another account"))

	return b.String()
}

func (m AccountModel) viewAddForm() string {
	w := m.accountBodyWidth()
	if w < 1 {
		w = 1
	}

	var b strings.Builder
	b.WriteString(accountHintStyle.Width(w).Render("Add a new account. Fields are saved locally only."))
	b.WriteString("\n\n")

	labels := [3]string{"Name", "Auth Token", "CT0"}
	for i, label := range labels {
		l := accountFormLabelStyle.Render(label + ":")
		b.WriteString(l + " " + m.inputs[i].View() + "\n\n")
	}

	if m.err != "" {
		b.WriteString(errorMsgStyle.Width(w).Render("Error: " + m.err))
		b.WriteString("\n")
	}

	return b.String()
}

func (m AccountModel) accountPanelWidth() int {
	w := m.width - 2
	if w < 1 {
		w = 1
	}
	return w
}

func (m AccountModel) accountBodyWidth() int {
	w := m.width - 4
	if w < 1 {
		w = 1
	}
	return w
}

func fitAccountText(s string, max int) string {
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
