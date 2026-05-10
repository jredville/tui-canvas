// Package tui implements the bubbletea TUI client for tui-canvas.
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"tui-canvas/internal/protocol"
)

const tabBarHeight = 3

type keyMap struct {
	Quit    key.Binding
	Debug   key.Binding
	Kill    key.Binding
	Restart key.Binding
	TabNext key.Binding
	TabPrev key.Binding
	Search  key.Binding
	Next    key.Binding
	Prev    key.Binding
	Confirm key.Binding
	Cancel  key.Binding
}

var keys = keyMap{
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Debug:   key.NewBinding(key.WithKeys("?"),           key.WithHelp("?", "debug")),
	Kill:    key.NewBinding(key.WithKeys("x"),           key.WithHelp("x", "kill tab")),
	Restart: key.NewBinding(key.WithKeys("R"),           key.WithHelp("R", "restart")),
	TabNext: key.NewBinding(key.WithKeys("tab"),         key.WithHelp("tab", "next")),
	TabPrev: key.NewBinding(key.WithKeys("shift+tab"),   key.WithHelp("shift+tab", "prev")),
	Search:  key.NewBinding(key.WithKeys("/"),           key.WithHelp("/", "search")),
	Next:    key.NewBinding(key.WithKeys("n"),           key.WithHelp("n", "next match")),
	Prev:    key.NewBinding(key.WithKeys("p"),           key.WithHelp("p", "prev match")),
	Confirm: key.NewBinding(key.WithKeys("enter")),
	Cancel:  key.NewBinding(key.WithKeys("esc", "ctrl+c")),
}

var ansiEscRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

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
// Search has two phases: input (typing query) and nav (n/p between hits).
type Model struct {
	sessions        []protocol.Session
	activeTab       int
	viewport        viewport.Model
	ch              chan tea.Msg
	renderer        *glamour.TermRenderer
	ready           bool
	debug           bool
	err             error
	width           int
	height          int
	connGen         int
	reconnecting    bool
	confirming      bool
	tabWidths       []int
	searching       bool   // true while typing search query
	searchActive    bool   // true while navigating confirmed hits
	searchInput     string // in-progress query (while searching)
	searchQuery     string // confirmed query (while searchActive)
	searchHits      []int  // viewport line offsets of matches
	searchIdx       int    // current hit index
	renderedContent string // last content passed to viewport, pre-highlight
}

// New creates a Model and starts the background socket reader goroutine.
// The caller is responsible for closing conn when done.
func New(conn net.Conn) (*Model, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
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
		widthChanged := msg.Width != m.width
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, m.contentHeight())
			m.ready = true
			m.rebuildRenderer()
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = m.contentHeight()
			if widthChanged {
				m.rebuildRenderer()
			}
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
			if msg.Y < tabBarHeight {
				m.handleTabClick(msg.X)
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearchInput(msg)
		}
		if m.searchActive {
			return m.updateSearchNav(msg)
		}
		if m.confirming {
			m.confirming = false
			if key.Matches(msg, keys.Kill) && len(m.sessions) > 0 {
				return m, sendSessionRemoveCmd(m.sessions[m.activeTab].ID)
			}
			return m, nil
		}
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Debug):
			m.debug = !m.debug
		case key.Matches(msg, keys.Kill):
			if len(m.sessions) > 0 {
				m.confirming = true
			}
		case key.Matches(msg, keys.Restart):
			if !m.reconnecting {
				m.reconnecting = true
				// attempt=2 → 200 ms initial delay, giving the daemon time to exit.
				return m, tea.Batch(sendDaemonRestartCmd(), reconnectCmd(2))
			}
		case key.Matches(msg, keys.TabNext):
			if len(m.sessions) > 0 {
				m.activeTab = (m.activeTab + 1) % len(m.sessions)
				m.clearSearch()
				m.refreshViewport()
			}
		case key.Matches(msg, keys.TabPrev):
			if len(m.sessions) > 0 {
				m.activeTab = (m.activeTab - 1 + len(m.sessions)) % len(m.sessions)
				m.clearSearch()
				m.refreshViewport()
			}
		case key.Matches(msg, keys.Search):
			m.enterSearch()
		default:
			// n/p pass through to viewport in normal mode
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil
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
	const helpBarHeight = 1
	h := m.height - tabBarHeight - helpBarHeight
	if h < 1 {
		return 1
	}
	return h
}

// handleTabClick switches to the tab whose rendered width contains column x.
func (m *Model) handleTabClick(x int) {
	cursor := 0
	for i, w := range m.tabWidths {
		if x < cursor+w {
			m.activeTab = i
			m.clearSearch()
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
	content := m.renderCanvas()
	m.renderedContent = content
	if m.searchActive && m.searchQuery != "" {
		m.updateSearch(m.searchQuery)
	} else if m.searching && m.searchInput != "" {
		m.updateSearch(m.searchInput)
	}
	m.viewport.SetContent(m.contentForDisplay())
	if (m.searchActive || m.searching) && len(m.searchHits) > 0 {
		m.viewport.SetYOffset(m.searchHits[m.searchIdx])
	}
}

// rebuildRenderer recreates the glamour renderer at the current terminal width.
func (m *Model) rebuildRenderer() {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.width),
	)
	if err == nil {
		m.renderer = r
	}
}

// contentForDisplay returns renderedContent with the matched text on the
// current hit line highlighted. Active during both search input and nav modes.
func (m *Model) contentForDisplay() string {
	if len(m.searchHits) == 0 || (!m.searchActive && !m.searching) {
		return m.renderedContent
	}
	query := m.searchQuery
	if m.searching {
		query = m.searchInput
	}
	lines := strings.Split(m.renderedContent, "\n")
	hitLine := m.searchHits[m.searchIdx]
	if hitLine < len(lines) {
		plain := ansiEscRe.ReplaceAllString(lines[hitLine], "")
		lowerPlain := strings.ToLower(plain)
		lowerQ := strings.ToLower(query)
		if idx := strings.Index(lowerPlain, lowerQ); idx >= 0 {
			before := plain[:idx]
			match := plain[idx : idx+len(lowerQ)]
			after := plain[idx+len(lowerQ):]
			lines[hitLine] = before + searchHighlightStyle.Render(match) + after
		}
	}
	return strings.Join(lines, "\n")
}

// clearSearch resets all search state.
func (m *Model) clearSearch() {
	m.searching = false
	m.searchActive = false
	m.searchInput = ""
	m.searchQuery = ""
	m.searchHits = nil
	m.searchIdx = 0
}

// enterSearch switches to search input mode.
func (m *Model) enterSearch() {
	m.searching = true
	m.searchActive = false
	m.searchInput = ""
}

// confirmSearch transitions from input mode to navigation mode.
func (m *Model) confirmSearch() {
	m.searching = false
	m.searchQuery = m.searchInput
	m.searchInput = ""
	m.searchActive = true
	m.searchIdx = 0
	m.updateSearch(m.searchQuery)
	m.scrollToHit()
}

// updateSearch rebuilds searchHits from renderedContent for the given query.
func (m *Model) updateSearch(query string) {
	m.searchHits = nil
	if query == "" {
		return
	}
	q := strings.ToLower(query)
	for i, line := range strings.Split(m.renderedContent, "\n") {
		plain := ansiEscRe.ReplaceAllString(line, "")
		if strings.Contains(strings.ToLower(plain), q) {
			m.searchHits = append(m.searchHits, i)
		}
	}
	if m.searchIdx >= len(m.searchHits) {
		m.searchIdx = 0
	}
}

// scrollToHit updates viewport content (for highlight) and scrolls to the hit.
func (m *Model) scrollToHit() {
	if len(m.searchHits) == 0 {
		return
	}
	m.viewport.SetContent(m.contentForDisplay())
	m.viewport.SetYOffset(m.searchHits[m.searchIdx])
}

// nextHit advances to the next search match, looping at the end.
func (m *Model) nextHit() {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + 1) % len(m.searchHits)
	m.scrollToHit()
}

// prevHit goes to the previous search match, looping at the start.
func (m *Model) prevHit() {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx - 1 + len(m.searchHits)) % len(m.searchHits)
	m.scrollToHit()
}

// updateSearchInput handles key events while typing a search query.
func (m *Model) updateSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm):
		if m.searchInput != "" {
			m.confirmSearch()
		} else {
			m.clearSearch()
			m.viewport.SetContent(m.renderedContent)
		}
	case key.Matches(msg, keys.Cancel):
		m.clearSearch()
		m.viewport.SetContent(m.renderedContent)
		m.viewport.GotoTop()
	case msg.String() == "backspace" || msg.String() == "ctrl+h":
		runes := []rune(m.searchInput)
		if len(runes) > 0 {
			m.searchInput = string(runes[:len(runes)-1])
			m.searchIdx = 0
			m.updateSearch(m.searchInput)
			m.scrollToHit()
		}
	default:
		if len(msg.Runes) == 1 {
			m.searchInput += string(msg.Runes)
			m.searchIdx = 0
			m.updateSearch(m.searchInput)
			m.scrollToHit()
		}
	}
	return m, nil
}

// updateSearchNav handles key events while navigating search hits.
func (m *Model) updateSearchNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Next):
		m.nextHit()
	case key.Matches(msg, keys.Prev):
		m.prevHit()
	case key.Matches(msg, keys.Confirm):
		m.clearSearch()
		m.viewport.SetContent(m.renderedContent)
	case key.Matches(msg, keys.Cancel):
		m.clearSearch()
		m.viewport.SetContent(m.renderedContent)
		m.viewport.GotoTop()
	case key.Matches(msg, keys.Search):
		m.clearSearch()
		m.enterSearch()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}
