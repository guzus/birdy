package cmd

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/guzus/birdy/tui"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:     "tui",
	Short:   "Launch the interactive terminal UI",
	Long:    "Start birdy's full-screen terminal interface with AI-powered chat, account management, and more.",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Force deterministic terminal rendering and avoid background color probes
		// that can leak OSC responses into input on some terminals.
		lipgloss.SetColorProfile(termenv.ANSI256)
		lipgloss.SetHasDarkBackground(true)

		m := tui.NewMainModel()
		opts := []tea.ProgramOption{tea.WithAltScreen()}
		if mouseEnabledFromEnv() {
			opts = append(opts, tea.WithMouseCellMotion())
		}

		p := tea.NewProgram(m, opts...)
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}
		return nil
	},
}

func mouseEnabledFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("BIRDY_TUI_MOUSE")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
