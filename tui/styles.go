package tui

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	colorBlue    = lipgloss.Color("#1DA1F2")
	colorDarkBg  = lipgloss.Color("#15202B")
	colorLightFg = lipgloss.Color("#E1E8ED")
	colorMuted   = lipgloss.Color("#657786")
	colorRed     = lipgloss.Color("#E0245E")
	colorGreen   = lipgloss.Color("#17BF63")
	colorWhite   = lipgloss.Color("#FFFFFF")
)

// Styles
var (
	headerStyle = lipgloss.NewStyle().
			Background(colorBlue).
			Foreground(colorWhite).
			Bold(true).
			Padding(0, 1)

	userMsgStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	assistantMsgStyle = lipgloss.NewStyle().
				Foreground(colorLightFg)

	toolMsgStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	brandStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBlue).
				Padding(0, 1)

	accountSelectedStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	accountNormalStyle = lipgloss.NewStyle().
				Foreground(colorLightFg)

	accountHeaderStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true).
				Padding(0, 1)

	accountFormLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Width(12)
)
