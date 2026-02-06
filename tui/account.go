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

	// Header
	title := "Accounts"
	if m.view == accountViewAdd {
		title = "Add Account"
	}
	header := accountHeaderStyle.
		Width(m.width).
		Render(title)

	// Content
	var content string
	if m.view == accountViewAdd {
		content = m.viewAddForm()
	} else {
		content = m.viewList()
	}

	// Footer
	var footer string
	if m.view == accountViewAdd {
		footer = statusBarStyle.Width(m.width).
			Render("tab: next field | enter: save | esc: cancel")
	} else {
		footer = statusBarStyle.Width(m.width).
			Render("j/k: navigate | a: add | d: delete | tab/esc: back")
	}

	// Fill middle area
	contentHeight := m.height - 2 // header + footer
	if contentHeight < 1 {
		contentHeight = 1
	}
	content = lipgloss.NewStyle().
		Height(contentHeight).
		Width(m.width).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
}

func (m AccountModel) viewList() string {
	if m.err != "" {
		return "\n" + errorMsgStyle.Padding(0, 2).Render("Error: "+m.err)
	}

	if len(m.accounts) == 0 {
		return lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(1, 2).
			Render("No accounts configured. Press 'a' to add one.")
	}

	var b strings.Builder
	b.WriteString("\n")
	for i, a := range m.accounts {
		cursor := "  "
		style := accountNormalStyle
		if i == m.cursor {
			cursor = "> "
			style = accountSelectedStyle
		}

		lastUsed := "never"
		if !a.LastUsed.IsZero() {
			lastUsed = a.LastUsed.Format("2006-01-02 15:04")
		}

		line := fmt.Sprintf("%s%-16s  %3d uses   last: %s",
			cursor, a.Name, a.UseCount, lastUsed)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	return b.String()
}

func (m AccountModel) viewAddForm() string {
	var b strings.Builder
	b.WriteString("\n")

	labels := [3]string{"Name:", "Auth Token:", "CT0:"}
	for i, label := range labels {
		l := accountFormLabelStyle.Render(label)
		b.WriteString("  " + l + " " + m.inputs[i].View() + "\n\n")
	}

	if m.err != "" {
		b.WriteString("  " + errorMsgStyle.Render("Error: "+m.err) + "\n")
	}

	return b.String()
}
