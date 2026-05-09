// Package tui implements the bubbletea TUI client for tui-canvas.
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"tui-canvas/internal/protocol"
)

// socketMsg carries a raw newline-delimited JSON line from the daemon.
// gen tracks which connection the message came from, so stale messages from
// a dead connection can be ignored after a reconnect.
type socketMsg struct {
	data []byte
	gen  int
}

// socketErrMsg carries a read error from the socket goroutine.
type socketErrMsg struct {
	err error
	gen int
}

type reconnectedMsg struct{ conn net.Conn }
type reconnectRetryMsg struct{ attempt int }
type reconnectFailedMsg struct{ err error }
type restartSentMsg struct{}

// Model is the bubbletea model for the TUI client.
type Model struct {
	sessions     []protocol.Session
	activeTab    int
	viewport     viewport.Model
	ch           chan tea.Msg
	renderer     *glamour.TermRenderer
	ready        bool
	debug        bool
	err          error
	width        int
	height       int
	connGen      int   // incremented on each successful reconnect
	reconnecting bool  // true while attempting to reconnect
	confirming   bool  // true when waiting for second x to confirm kill
	tabWidths    []int // rendered width of each tab, set by tabBar()
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
		const myGen = 0
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				ch <- socketErrMsg{err, myGen}
				return
			}
			ch <- socketMsg{line, myGen}
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
		if msg.gen != m.connGen {
			// Stale message from an old connection — drain but ignore.
			return m, waitForMsg(m.ch)
		}
		var env protocol.Envelope
		if json.Unmarshal(msg.data, &env) == nil && env.Type == "daemon_restarting" {
			if !m.reconnecting {
				m.reconnecting = true
				// No waitForMsg: the goroutine will send socketErrMsg next;
				// reconnectedMsg handler re-establishes the consumer.
				return m, reconnectCmd(0)
			}
			return m, waitForMsg(m.ch)
		}
		if !m.reconnecting {
			m.handleSocketMsg(msg.data)
		}
		return m, waitForMsg(m.ch)

	case socketErrMsg:
		if msg.gen != m.connGen {
			// Stale error from an old connection — drain but ignore.
			return m, waitForMsg(m.ch)
		}
		if !m.reconnecting {
			m.reconnecting = true
			return m, reconnectCmd(0)
		}
		// Already reconnecting (e.g. R was pressed); reader goroutine is dead,
		// reconnectCmd is in flight — don't add another waiter.
		return m, nil

	case reconnectedMsg:
		m.connGen++
		myGen := m.connGen
		m.reconnecting = false
		m.err = nil
		go func() {
			reader := bufio.NewReader(msg.conn)
			for {
				line, err := reader.ReadBytes('\n')
				if err != nil {
					m.ch <- socketErrMsg{err, myGen}
					return
				}
				m.ch <- socketMsg{line, myGen}
			}
		}()
		return m, waitForMsg(m.ch)

	case reconnectRetryMsg:
		return m, reconnectCmd(msg.attempt)

	case reconnectFailedMsg:
		m.reconnecting = false
		m.err = msg.err
		return m, nil

	case restartSentMsg:
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.handleTabClick(msg.X, msg.Y)
		}
		return m, nil

	case tea.KeyMsg:
		if m.confirming {
			m.confirming = false
			if msg.String() == "x" && len(m.sessions) > 0 {
				return m, sendSessionRemoveCmd(m.sessions[m.activeTab].ID)
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "?":
			m.debug = !m.debug
			return m, nil

		case "x":
			if len(m.sessions) > 0 {
				m.confirming = true
			}
			return m, nil

		case "R":
			if !m.reconnecting {
				m.reconnecting = true
				// attempt=2 → 200 ms initial delay, giving the daemon time to exit.
				return m, tea.Batch(sendDaemonRestartCmd(), reconnectCmd(2))
			}
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

	case "session_removed":
		var msg protocol.SessionRemoved
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}
		for i, s := range m.sessions {
			if s.ID == msg.SessionID {
				m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
				if m.activeTab >= len(m.sessions) && m.activeTab > 0 {
					m.activeTab = len(m.sessions) - 1
				}
				break
			}
		}
		m.refreshViewport()
	}
}

// reconnectCmd attempts to connect to the daemon, starting it if needed.
// attempt drives exponential backoff; attempt=0 tries immediately.
func reconnectCmd(attempt int) tea.Cmd {
	return func() tea.Msg {
		const maxAttempts = 8
		if attempt >= maxAttempts {
			return reconnectFailedMsg{
				err: fmt.Errorf("daemon did not come back after %d attempts", attempt),
			}
		}
		if attempt > 0 {
			delay := time.Duration(100<<uint(attempt-1)) * time.Millisecond
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
			time.Sleep(delay)
		}
		if !protocol.IsDaemonRunning() {
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "daemon")
			_ = cmd.Start()
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if protocol.IsDaemonRunning() {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
		if err != nil {
			return reconnectRetryMsg{attempt: attempt + 1}
		}
		b, _ := protocol.Encode(protocol.Subscribe{Type: "subscribe"})
		if _, err := conn.Write(b); err != nil {
			conn.Close()
			return reconnectRetryMsg{attempt: attempt + 1}
		}
		return reconnectedMsg{conn: conn}
	}
}

// sendSessionRemoveCmd asks the daemon to remove a session and its canvas entries.
func sendSessionRemoveCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
		if err != nil {
			return nil
		}
		b, _ := protocol.Encode(protocol.SessionRemove{Type: "session_remove", SessionID: sessionID})
		conn.Write(b)
		conn.Close()
		return nil
	}
}

// sendDaemonRestartCmd asks the daemon to restart itself.
func sendDaemonRestartCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
		if err != nil {
			return restartSentMsg{}
		}
		b, _ := protocol.Encode(protocol.DaemonRestart{Type: "daemon_restart"})
		conn.Write(b)
		conn.Close()
		return restartSentMsg{}
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

// handleTabClick switches to the tab whose rendered width contains column x.
// The tab bar occupies the top rows; y is ignored.
func (m *Model) handleTabClick(x, _ int) {
	cursor := 0
	for i, w := range m.tabWidths {
		if x < cursor+w {
			m.activeTab = i
			m.refreshViewport()
			return
		}
		cursor += w
	}
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
