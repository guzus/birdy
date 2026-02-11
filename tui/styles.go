package tui

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	colorBlue       = lipgloss.Color("#1DA1F2")
	colorDarkBg     = lipgloss.Color("#000000")
	colorPanel      = lipgloss.Color("#000000")
	colorPanelAlt   = lipgloss.Color("#000000")
	colorBorder     = lipgloss.Color("#1A4E7A")
	colorBorderSoft = lipgloss.Color("#133752")
	colorLightFg    = lipgloss.Color("#D6E2EF")
	colorMuted      = lipgloss.Color("#7F90A5")
	colorRed        = lipgloss.Color("#FF5C5C")
	colorGreen      = lipgloss.Color("#17BF63")
	colorWhite      = lipgloss.Color("#FFFFFF")
)

// Styles
var (
	appStyle = lipgloss.NewStyle().
			Background(colorDarkBg).
			Foreground(colorLightFg)

	headerStyle = lipgloss.NewStyle().
			Background(colorPanelAlt).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1).
			Bold(true)

	headerBrandStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true).
				Padding(0, 1)

	headerDeskStyle = lipgloss.NewStyle().
			Foreground(colorLightFg).
			Bold(true).
			Padding(0, 1)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Bold(true)

	headerValueStyle = lipgloss.NewStyle().
				Foreground(colorLightFg).
				Bold(false)

	headerStatusLiveStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	headerStatusThinkingStyle = lipgloss.NewStyle().
					Foreground(colorMuted).
					Italic(true)

	feedPanelStyle = lipgloss.NewStyle().
			Background(colorPanelAlt).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorBorder)

	commandPanelStyle = lipgloss.NewStyle().
				Background(colorPanelAlt).
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorBorder)

	keysPanelStyle = lipgloss.NewStyle().
			Background(colorPanelAlt).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorBorder)

	sectionTitleStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true).
				Padding(0, 1)

	commandMetaStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Padding(0, 1)

	footerPathStyle = lipgloss.NewStyle().
				Foreground(colorBorderSoft).
				Background(colorDarkBg).
				Italic(true)

	scrollbarTrackStyle = lipgloss.NewStyle().
				Foreground(colorBorderSoft).
				Background(colorDarkBg)

	scrollbarThumbStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Background(colorDarkBg).
				Bold(true)

	userMsgStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Background(colorDarkBg).
			Bold(true)

	assistantMsgStyle = lipgloss.NewStyle().
				Foreground(colorLightFg).
				Background(colorDarkBg)

	toolMsgStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorDarkBg).
			Italic(true)

	errorMsgStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Background(colorDarkBg).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Background(colorPanelAlt).
			Foreground(colorMuted).
			Padding(0, 1).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorBorder)

	brandStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Bold(true)

	inputBorderStyle = lipgloss.NewStyle().
				Background(colorPanel).
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorBorderSoft).
				Foreground(colorLightFg).
				Padding(0, 1)

	accountSelectedStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Background(colorPanelAlt).
			Bold(true)

	accountNormalStyle = lipgloss.NewStyle().
				Foreground(colorLightFg).
				Background(colorDarkBg)

	accountListHeaderStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Background(colorDarkBg).
				Bold(true)

	accountHintStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Background(colorDarkBg)

	accountHeaderStyle = lipgloss.NewStyle().
				Background(colorPanel).
				Foreground(colorBlue).
				Bold(true).
				Padding(0, 1)

	accountFormLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Width(12)
)
