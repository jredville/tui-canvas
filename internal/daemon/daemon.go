// Package daemon implements the tui-canvas state server.
// It maintains session state and broadcasts incremental updates to
// long-lived TUI subscribers over a Unix domain socket.
package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"tui-canvas/internal/protocol"
)

func sessionsPath(sockPath string) string {
	return filepath.Join(filepath.Dir(sockPath), "sessions.json")
}

func loadSessions(path string) []protocol.Session {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sessions []protocol.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		bak := path + ".bak"
		log.Printf("daemon: sessions.json corrupt (%v); starting fresh, backup at %s", err, bak)
		_ = os.Rename(path, bak)
		return nil
	}
	return sessions
}


var oscRe = regexp.MustCompile(`\x1b[\]\[P_^][^\x1b\x07]*(?:\x07|\x1b\\)`)

func sanitizeContent(s string) string {
	s = oscRe.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
	st.saveMu.Lock()
	defer st.saveMu.Unlock()
	tmp := st.stateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		if err := os.Rename(tmp, st.stateFile); err != nil {
			log.Printf("daemon: save sessions: %v", err)
		}
	}
}

// subscriber represents a connected TUI client.
type subscriber struct {
	send chan []byte
	conn net.Conn
}

// state is the daemon's canonical session store.
type state struct {
	mu        sync.RWMutex
	saveMu    sync.Mutex
	sessions  []protocol.Session
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

func shutdown(sockPath string) {
	os.Remove(sockPath)
	os.Exit(0)
}

// Run starts the daemon: it binds the socket, handles signals, and accepts
// connections until the process is terminated.
func Run() {
	sockPath := protocol.SocketPath()

	// Ensure the directory exists with restricted permissions.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		log.Fatalf("daemon: create socket dir: %v", err)
	}

	// Remove any stale socket from a previous run.
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		// Another daemon won the race – exit cleanly.
		return
	}
	_ = os.Chmod(sockPath, 0o600)

	stateFile := sessionsPath(sockPath)
	sessions := loadSessions(stateFile)
	st := &state{sessions: sessions, stateFile: stateFile}
	bc := newBroadcaster()

	// Clean up on signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		ln.Close()
		shutdown(sockPath)
	}()

	log.Printf("daemon: listening on %s", sockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener was closed (e.g. signal handler fired).
			return
		}
		go handleConn(conn, st, bc, ln, sockPath)
	}
}

const maxMsgBytes = 2 * 1024 * 1024 // 2 MiB

// handleConn reads the first line from conn to determine whether this is a
// long-lived subscriber or a one-shot plugin message.
func handleConn(conn net.Conn, st *state, bc *broadcaster, ln net.Listener, sockPath string) {
	reader := bufio.NewReader(io.LimitReader(conn, maxMsgBytes+1))
	line, err := reader.ReadBytes('\n')
	if len(line) > maxMsgBytes {
		conn.Close()
		return
	}
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
	case protocol.TypeSubscribe:
		handleSubscriber(conn, reader, st, bc)
	default:
		handlePluginMessage(env.Type, line, conn, st, bc, ln, sockPath)
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
	defer func() {
		bc.remove(sub)
		conn.Close()
	}()

	// Compute snapshot, then register subscriber and enqueue full_state atomically
	// so no broadcast can slip between registration and the initial state delivery.
	snap := st.snapshot()
	fs := protocol.FullState{Type: protocol.TypeFullState, Sessions: snap}
	b, _ := protocol.Encode(fs)

	bc.mu.Lock()
	bc.subs[sub] = struct{}{}
	if b != nil {
		select {
		case sub.send <- b:
		default:
		}
	}
	bc.mu.Unlock()

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
func handlePluginMessage(msgType string, raw []byte, conn net.Conn, st *state, bc *broadcaster, ln net.Listener, sockPath string) {
	switch msgType {
	case protocol.TypeSessionRegister:
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
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.SessionAdded{
			Type:      protocol.TypeSessionAdded,
			SessionID: msg.SessionID,
			Name:      msg.Name,
			CWD:       msg.CWD,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case protocol.TypeCanvasAppend:
		var msg protocol.CanvasAppend
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		sessionIdx := -1
		for i := range st.sessions {
			if st.sessions[i].ID == msg.SessionID {
				sessionIdx = i
				break
			}
		}
		if sessionIdx < 0 {
			st.mu.Unlock()
			return
		}
		idx := len(st.sessions[sessionIdx].Entries) + 1
		entry := protocol.Entry{Content: sanitizeContent(msg.Content), Index: idx}
		st.sessions[sessionIdx].Entries = append(st.sessions[sessionIdx].Entries, entry)
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.CanvasAppended{
			Type:      protocol.TypeCanvasAppended,
			SessionID: msg.SessionID,
			Entry:     entry,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case protocol.TypeCanvasClear:
		var msg protocol.CanvasClear
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		found := false
		for i := range st.sessions {
			if st.sessions[i].ID == msg.SessionID {
				st.sessions[i].Entries = nil
				found = true
				break
			}
		}
		if !found {
			st.mu.Unlock()
			return
		}
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.CanvasCleared{
			Type:      protocol.TypeCanvasCleared,
			SessionID: msg.SessionID,
		}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case protocol.TypeSessionRemove:
		var msg protocol.SessionRemove
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.Lock()
		for i, s := range st.sessions {
			if s.ID == msg.SessionID {
				st.sessions = append(st.sessions[:i], st.sessions[i+1:]...)
				break
			}
		}
		st.mu.Unlock()
		saveSessions(st)

		out := protocol.SessionRemoved{Type: protocol.TypeSessionRemoved, SessionID: msg.SessionID}
		if b, err := protocol.Encode(out); err == nil {
			bc.broadcast(b)
		}

	case protocol.TypeDaemonRestart:
		if b, err := protocol.Encode(protocol.DaemonRestarting{Type: protocol.TypeDaemonRestarting}); err == nil {
			bc.broadcast(b)
		}
		ln.Close()
		shutdown(sockPath)

	case protocol.TypeListSessions:
		var msg protocol.ListSessions
		if err := json.Unmarshal(raw, &msg); err != nil {
			return
		}

		st.mu.RLock()
		var sessions []protocol.Session
		for _, s := range st.sessions {
			if msg.CWD == "" || filepath.Clean(s.CWD) == filepath.Clean(msg.CWD) {
				sessions = append(sessions, protocol.Session{
					ID:   s.ID,
					Name: s.Name,
					CWD:  s.CWD,
				})
			}
		}
		st.mu.RUnlock()

		out := protocol.SessionsList{Type: protocol.TypeSessionsList, Sessions: sessions}
		if b, err := protocol.Encode(out); err == nil {
			_, _ = conn.Write(b)
		}
	}
}
