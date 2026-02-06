package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/guzus/birdy/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:     "tui",
	Short:   "Launch the interactive terminal UI",
	Long:    "Start birdy's full-screen terminal interface with AI-powered chat, account management, and more.",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		m := tui.NewMainModel()
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
