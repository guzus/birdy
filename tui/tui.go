package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenSplash screen = iota
	screenChat
	screenAccount
)

type switchScreenMsg struct {
	target screen
}

// MainModel routes between splash, chat, and account screens.
type MainModel struct {
	currentScreen screen
	width         int
	height        int
	splash        SplashModel
	chat          ChatModel
	account       AccountModel
}

func NewMainModel() MainModel {
	return MainModel{
		currentScreen: screenSplash,
		splash:        NewSplashModel(),
		chat:          NewChatModel(),
		account:       NewAccountModel(),
	}
}

func (m MainModel) Init() tea.Cmd {
	// Start splash animation and begin loading home timeline in background
	return tea.Batch(m.splash.Init(), m.chat.Init())
}

func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.splash, _ = m.splash.Update(msg)
		m.chat, _ = m.chat.Update(msg)
		m.account, _ = m.account.Update(msg)
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.chat.cancelStream != nil {
				m.chat.cancelStream()
			}
			return m, tea.Quit
		}

	case switchScreenMsg:
		m.currentScreen = msg.target
		switch msg.target {
		case screenChat:
			m.chat.refreshAccountCount()
			// Don't re-Init chat — it's already running from startup
			return m, textinput.Blink
		case screenAccount:
			m.account.loadAccounts()
			return m, m.account.Init()
		}
		return m, nil

	// Always route claude streaming messages to chat, even during splash
	case autoQueryMsg, claudeNextMsg, claudeTokenMsg, claudeToolUseMsg, claudeDoneMsg, claudeErrorMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	switch m.currentScreen {
	case screenSplash:
		m.splash, cmd = m.splash.Update(msg)
	case screenChat:
		m.chat, cmd = m.chat.Update(msg)
	case screenAccount:
		m.account, cmd = m.account.Update(msg)
	}

	return m, cmd
}

func (m MainModel) View() string {
	switch m.currentScreen {
	case screenSplash:
		return m.splash.View()
	case screenChat:
		return m.chat.View()
	case screenAccount:
		return m.account.View()
	default:
		return ""
	}
}
