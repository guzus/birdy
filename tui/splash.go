package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

// SplashModel displays the animated bird splash screen.
type SplashModel struct {
	frame  int
	ticks  int
	width  int
	height int
}

func NewSplashModel() SplashModel {
	return SplashModel{}
}

func (m SplashModel) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m SplashModel) Update(msg tea.Msg) (SplashModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.ticks++
		if m.ticks >= 9 {
			return m, func() tea.Msg { return switchScreenMsg{target: screenChat} }
		}
		m.frame = (m.frame + 1) % 4
		return m, tickCmd()

	case tea.KeyMsg:
		return m, func() tea.Msg { return switchScreenMsg{target: screenChat} }

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

func (m SplashModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	bird := birdFrames[m.frame]
	brand := brandStyle.Render("b  i  r  d  y")
	content := lipgloss.JoinVertical(lipgloss.Center, bird, "", brand)

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		content,
	)
}
