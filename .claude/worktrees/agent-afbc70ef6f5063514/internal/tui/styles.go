package tui

import "charm.land/lipgloss/v2"

var (
	// Labels
	userLabel      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))  // cyan
	assistantLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))  // green
	toolCallLabel  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))  // yellow
	toolResultLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	// Content styles
	reasoningStyle  = lipgloss.NewStyle().Faint(true).Italic(true)
	toolArgStyle    = lipgloss.NewStyle().Faint(true)
	toolOutputStyle = lipgloss.NewStyle().Faint(true)
	errorStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")) // red

	// Status bar
	statusBarStyle = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	statusText     = lipgloss.NewStyle().Faint(true)
)
