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

	searchHighlightStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("226")).
				Foreground(lipgloss.Color("0"))
)

// View renders the full TUI layout: tab bar, viewport, help bar.
func (m *Model) View() string {
	if m.reconnecting {
		return "Reconnecting to daemon…\n"
	}
	if m.err != nil {
		return fmt.Sprintf("Connection lost: %v\nPress R to retry.\n", m.err)
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

// tabBar renders the horizontal tab strip and records each tab's width for click hit-testing.
func (m *Model) tabBar() string {
	if len(m.sessions) == 0 {
		m.tabWidths = nil
		return inactiveTabStyle.Render("no sessions")
	}
	tabs := make([]string, len(m.sessions))
	for i, s := range m.sessions {
		label := s.Name
		if m.debug {
			short := s.ID
			if len(short) > 8 {
				short = short[:8]
			}
			label = fmt.Sprintf("%s [%s]", s.Name, short)
		}
		if i == m.activeTab {
			tabs[i] = activeTabStyle.Render(label)
		} else {
			tabs[i] = inactiveTabStyle.Render(label)
		}
	}
	m.tabWidths = make([]int, len(tabs))
	for i, t := range tabs {
		m.tabWidths[i] = lipgloss.Width(t)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// helpBar renders the single-line keybinding hint at the bottom.
func (m *Model) helpBar() string {
	if m.searching {
		return helpStyle.Render(fmt.Sprintf(
			"/%s  enter: search  esc: cancel", m.searchInput))
	}
	if m.searchActive {
		matchInfo := "(no matches)"
		if len(m.searchHits) > 0 {
			matchInfo = fmt.Sprintf("(%d/%d)", m.searchIdx+1, len(m.searchHits))
		}
		return helpStyle.Render(fmt.Sprintf(
			"/%s %s  n: next  p: prev  enter: exit  esc: top",
			m.searchQuery, matchInfo))
	}
	if m.confirming && len(m.sessions) > 0 {
		return helpStyle.Render(fmt.Sprintf(
			"Kill %q?  x: confirm  any key: cancel", m.sessions[m.activeTab].Name))
	}
	if m.debug && len(m.sessions) > 0 {
		return helpStyle.Render(fmt.Sprintf(
			"session: %s  │  ?: hide  tab: switch  ↑↓/jk: scroll  /: search  x: kill  R: restart  q: quit",
			m.sessions[m.activeTab].ID))
	}
	return helpStyle.Render(
		"tab/shift+tab: switch  ↑↓/jk: scroll  /: search  x: kill  ?: debug  R: restart  q: quit")
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
		cacheKey := m.renderEntryKey(session.ID, entry.Index)
		rendered, ok := m.renderedEntries[cacheKey]
		if !ok {
			r, err := m.renderer.Render(entry.Content)
			if err != nil {
				r = entry.Content
			}
			m.renderedEntries[cacheKey] = r
			rendered = r
		}
		sb.WriteString(rendered)
	}
	return sb.String()
}
