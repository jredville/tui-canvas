// Package daemon implements the tui-canvas state server.
// It maintains session state and broadcasts incremental updates to
// long-lived TUI subscribers over a Unix domain socket.
package daemon

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"tui-canvas/internal/protocol"
)

func sessionsPath(sockPath string) string {
	return filepath.Join(filepath.Dir(sockPath), "sessions.json")
}

func loadSessions(path string) ([]protocol.Session, map[string]int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, make(map[string]int)
	}
	var sessions []protocol.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, make(map[string]int)
	}
	nextIdx := make(map[string]int, len(sessions))
	for _, s := range sessions {
		nextIdx[s.ID] = len(s.Entries) + 1
	}
	return sessions, nextIdx
}

func saveSessions(st *state) {
	if st.stateFile == "" {
		return
	}
	st.mu.RLock()
	data, err := json.Marshal(st.sessions)
	st.mu.RUnlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(st.stateFile, data, 0o644)
}

// subscriber represents a connected TUI client.
type subscriber struct {
	send chan []byte
	conn net.Conn
}

// state is the daemon's canonical session store.
type state struct {
	mu        sync.RWMutex
	sessions  []protocol.Session
	nextIdx   map[string]int // session_id → next entry index
	stateFile string
}

// snapshot returns a deep copy of the current sessions slice, safe to use
// outside the lock because entries slices are copied.
func (s *state) snapshot() []protocol.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]protocol.Session, len(s.sessions))
	for i, sess := range s.sessions {
		cp := sess
		cp.Entries = make([]protocol.Entry, len(sess.Entries))
		copy(cp.Entries, sess.Entries)
		out[i] = cp
	}
	return out
}

// broadcaster manages the set of active TUI subscribers.
type broadcaster struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: make(map[*subscriber]struct{})}
}

func (b *broadcaster) add(sub *subscriber) {
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
}

func (b *broadcaster) remove(sub *subscriber) {
	b.mu.Lock()
	delete(b.subs, sub)
	b.mu.Unlock()
}

// broadcast sends msg to all subscribers. It never blocks: slow subscribers
// have the message dropped rather than stalling the broadcast loop.
func (b *broadcaster) broadcast(msg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		select {
		case sub.send <- msg:
		default:
			// subscriber is too slow; drop message
		}
	}
}

// Run starts the daemon: it binds the socket, handles signals, and accepts
// connections until the process is terminated.
func Run() {
	sockPath := protocol.SocketPath()

	// Ensure the directory exists.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o755); err != nil {
		log.Fatalf("daemon: create socket dir: %v", err)
	}

	// Remove any stale socket from a previous run.
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		// Another daemon won the race – exit cleanly.
		return
	}

	stateFile := sessionsPath(sockPath)
	sessions, nextIdx := loadSessions(stateFile)
	st := &state{sessions: sessions, nextIdx: nextIdx, stateFile: stateFile}
	bc := newBroadcaster()

	// Clean up on signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		ln.Close()
		os.Remove(sockPath)
		os.Exit(0)
	}()

	log.Printf("daemon: listening on %s", sockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener was closed (e.g. signal handler fired).
			return
		}
		go handleConn(conn, st, bc, ln)
	}
}

// handleConn reads the first line from conn to determine whether this is a
// long-lived subscriber or a one-shot plugin message.
func handleConn(conn net.Conn, st *state, bc *broadcaster, ln net.Listener) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return
	}

	var env protocol.Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		conn.Close()
		return
	}

	switch env.Type {
	case "subscribe":
		handleSubscriber(conn, reader, st, bc)
	default:
		handlePluginMessage(env.Type, line, conn, st, bc, ln)
		conn.Close()
	}
}

// handleSubscriber registers conn as a TUI subscriber, sends full_state, then
// blocks reading (to detect disconnect) while a writer goroutine drains sends.
func handleSubscriber(conn net.Conn, _ *bufio.Reader, st *state, bc *broadcaster) {
	sub := &subscriber{
		send: make(chan []byte, 64),
		conn: conn,
	}
	bc.add(sub)
	defer func() {
		bc.remove(sub)
		conn.Close()
	}()

	// Send full state immediately.
	snap := st.snapshot()
	fs := protocol.FullState{Type: "full_state", Sessions: snap}
	if b, err := protocol.Encode(fs); err == nil {
		select {
		case sub.send <- b:
		default:
		}
	}

	// Writer goroutine drains the send channel.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range sub.send {
			if _, err := conn.Write(msg); err != nil {
				return
			}
		}
	}()

	// Block-read to detect disconnect; we don't expect further data from TUI.
	buf := make([]byte, 1)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			break
		}
	}

	close(sub.send)
	<-done
}

// handlePluginMessage processes a one-shot message from the plugin.
func handlePluginMessage(msgType string, raw []byte, conn net.Conn, st *state, bc *broadcaster, ln net.Listener) {
	switch msgType {
	case "session_register":
		var msg protocol.SessionRegister
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		// Ignore duplicate registrations.
		for _, s := range st.sessions {
			if s.ID == msg.SessionID {
				st.mu.Unlock()
				return
			}
		}
		st.sessions = append(st.sessions, protocol.Session{
			ID:   msg.SessionID,
			Name: msg.Name,
			CWD:  msg.CWD,
		})
		st.nextIdx[msg.SessionID] = 1
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.SessionAdded{
			Type:      "session_added",
			SessionID: msg.SessionID,
			Name:      msg.Name,
			CWD:       msg.CWD,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case "canvas_append":
		var msg protocol.CanvasAppend
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		idx := st.nextIdx[msg.SessionID]
		entry := protocol.Entry{Content: msg.Content, Index: idx}
		for i := range st.sessions {
			if st.sessions[i].ID == msg.SessionID {
				st.sessions[i].Entries = append(st.sessions[i].Entries, entry)
				break
			}
		}
		st.nextIdx[msg.SessionID] = idx + 1
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.CanvasAppended{
			Type:      "canvas_appended",
			SessionID: msg.SessionID,
			Entry:     entry,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case "canvas_clear":
		var msg protocol.CanvasClear
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		for i := range st.sessions {
			if st.sessions[i].ID == msg.SessionID {
				st.sessions[i].Entries = nil
				break
			}
		}
		st.nextIdx[msg.SessionID] = 1
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.CanvasCleared{
			Type:      "canvas_cleared",
			SessionID: msg.SessionID,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case "session_remove":
		var msg protocol.SessionRemove
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		for i, s := range st.sessions {
			if s.ID == msg.SessionID {
				st.sessions = append(st.sessions[:i], st.sessions[i+1:]...)
				delete(st.nextIdx, msg.SessionID)
				break
			}
		}
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.SessionRemoved{Type: "session_removed", SessionID: msg.SessionID}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case "daemon_restart":
		if b, err := protocol.Encode(protocol.DaemonRestarting{Type: "daemon_restarting"}); err == nil {
			bc.broadcast(b)
		}
		ln.Close()

	case "list_sessions":
		var msg protocol.ListSessions
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.RLock()
		var sessions []protocol.Session
		for _, s := range st.sessions {
			if msg.CWD == "" || s.CWD == msg.CWD {
				sessions = append(sessions, protocol.Session{
					ID:   s.ID,
					Name: s.Name,
					CWD:  s.CWD,
				})
			}
		}
		st.mu.RUnlock()

		out := protocol.SessionsList{Type: "sessions_list", Sessions: sessions}
		if b, err := protocol.Encode(out); err == nil {
			_, _ = conn.Write(b)
		}
	}
}
