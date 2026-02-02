package tui

import tea "github.com/charmbracelet/bubbletea"

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
	return m.splash.Init()
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
			return m, tea.Quit
		}

	case switchScreenMsg:
		m.currentScreen = msg.target
		switch msg.target {
		case screenChat:
			m.chat.refreshAccountCount()
			return m, m.chat.Init()
		case screenAccount:
			m.account.loadAccounts()
			return m, m.account.Init()
		}
		return m, nil
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
