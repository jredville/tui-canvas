// Package tui implements the bubbletea TUI client for tui-canvas.
package tui

import (
	"bufio"
	"encoding/json"
	"net"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"tui-canvas/internal/protocol"
)

// socketMsg carries a raw newline-delimited JSON line from the daemon.
type socketMsg []byte

// socketErrMsg carries a read error from the socket goroutine.
type socketErrMsg error

// Model is the bubbletea model for the TUI client.
type Model struct {
	sessions  []protocol.Session
	activeTab int
	viewport  viewport.Model
	ch        chan tea.Msg
	renderer  *glamour.TermRenderer
	ready     bool
	debug     bool
	err       error
	width     int
	height    int
}

// New creates a Model and starts the background socket reader goroutine.
// The caller is responsible for closing conn when done.
func New(conn net.Conn) (*Model, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return nil, err
	}

	ch := make(chan tea.Msg, 64)
	m := &Model{
		ch:       ch,
		renderer: renderer,
	}

	// Single goroutine owns the bufio.Reader; sends lines (or errors) to ch.
	go func() {
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				ch <- socketErrMsg(err)
				return
			}
			ch <- socketMsg(line)
		}
	}()

	return m, nil
}

// waitForMsg returns a Cmd that blocks until the next message arrives on ch.
func waitForMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// Init sends the first waitForMsg command to kick off the socket reader loop.
func (m *Model) Init() tea.Cmd {
	return waitForMsg(m.ch)
}

// Update handles all incoming messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, m.contentHeight())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = m.contentHeight()
		}
		m.refreshViewport()
		return m, nil

	case socketMsg:
		m.handleSocketMsg(msg)
		return m, waitForMsg(m.ch)

	case socketErrMsg:
		m.err = msg
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "?":
			m.debug = !m.debug
			return m, nil

		case "tab":
			if len(m.sessions) > 0 {
				m.activeTab = (m.activeTab + 1) % len(m.sessions)
				m.refreshViewport()
			}
			return m, nil

		case "shift+tab":
			if len(m.sessions) > 0 {
				m.activeTab = (m.activeTab - 1 + len(m.sessions)) % len(m.sessions)
				m.refreshViewport()
			}
			return m, nil

		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// handleSocketMsg parses a raw daemon message and updates local state.
func (m *Model) handleSocketMsg(raw []byte) {
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}

	switch env.Type {
	case "full_state":
		var msg protocol.FullState
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		m.sessions = msg.Sessions
		if m.activeTab >= len(m.sessions) {
			m.activeTab = 0
		}
		m.refreshViewport()

	case "session_added":
		var msg protocol.SessionAdded
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		m.sessions = append(m.sessions, protocol.Session{
			ID:   msg.SessionID,
			Name: msg.Name,
			CWD:  msg.CWD,
		})
		m.refreshViewport()

	case "canvas_appended":
		var msg protocol.CanvasAppended
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		for i := range m.sessions {
			if m.sessions[i].ID == msg.SessionID {
				m.sessions[i].Entries = append(m.sessions[i].Entries, msg.Entry)
				break
			}
		}
		m.refreshViewport()

	case "canvas_cleared":
		var msg protocol.CanvasCleared
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		for i := range m.sessions {
			if m.sessions[i].ID == msg.SessionID {
				m.sessions[i].Entries = nil
				break
			}
		}
		m.refreshViewport()
	}
}

// contentHeight computes the viewport height given the current terminal size.
func (m *Model) contentHeight() int {
	const tabBarHeight = 3
	const helpBarHeight = 1
	h := m.height - tabBarHeight - helpBarHeight
	if h < 1 {
		return 1
	}
	return h
}

// refreshViewport re-renders the canvas into the viewport. Only called when
// content has actually changed (socket message, tab switch, or resize).
func (m *Model) refreshViewport() {
	if !m.ready {
		return
	}
	m.viewport.Height = m.contentHeight()
	m.viewport.Width = m.width
	m.viewport.SetContent(m.renderCanvas())
}
