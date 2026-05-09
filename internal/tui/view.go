package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")).
				Border(lipgloss.RoundedBorder()).
				Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// View renders the full TUI layout: tab bar, viewport, help bar.
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Connection error: %v\n", m.err)
	}
	if !m.ready {
		return "Connecting…\n"
	}

	var sb strings.Builder
	sb.WriteString(m.tabBar())
	sb.WriteString("\n")
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")
	sb.WriteString(m.helpBar())
	return sb.String()
}

// tabBar renders the horizontal tab strip.
func (m *Model) tabBar() string {
	if len(m.sessions) == 0 {
		return inactiveTabStyle.Render("no sessions")
	}
	tabs := make([]string, len(m.sessions))
	for i, s := range m.sessions {
		if i == m.activeTab {
			tabs[i] = activeTabStyle.Render(s.Name)
		} else {
			tabs[i] = inactiveTabStyle.Render(s.Name)
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// helpBar renders the single-line keybinding hint at the bottom.
func (m *Model) helpBar() string {
	return helpStyle.Render("tab/shift+tab: switch  ↑↓/jk: scroll  q: quit")
}

// renderCanvas builds the scrollable content string for the active session.
func (m *Model) renderCanvas() string {
	if len(m.sessions) == 0 {
		return "No sessions yet. Start a Claude session to see content here…\n"
	}
	session := m.sessions[m.activeTab]
	if len(session.Entries) == 0 {
		return "Canvas is empty. Use /tui-canvas in your Claude session to send content here…\n"
	}

	var sb strings.Builder
	for _, entry := range session.Entries {
		divider := dividerStyle.Render(fmt.Sprintf("── [%d] %s", entry.Index, strings.Repeat("─", 40)))
		sb.WriteString(divider)
		sb.WriteString("\n")
		rendered, err := m.renderer.Render(entry.Content)
		if err != nil {
			sb.WriteString(entry.Content)
		} else {
			sb.WriteString(rendered)
		}
	}
	return sb.String()
}
