package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Envelope is used for type-peeking before full decode.
type Envelope struct {
	Type string `json:"type"`
}

// Session holds a registered session and its canvas entries.
type Session struct {
	ID      string  `json:"session_id"`
	Name    string  `json:"name"`
	CWD     string  `json:"cwd"`
	Entries []Entry `json:"entries"`
}

// Entry is a single canvas append within a session.
type Entry struct {
	Content string `json:"content"`
	Index   int    `json:"index"`
}

// Plugin → Daemon messages

// SessionRegister registers a new Claude session with the daemon.
type SessionRegister struct {
	Type      string `json:"type"` // "session_register"
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	CWD       string `json:"cwd"`
}

// CanvasAppend appends content to a session's canvas.
type CanvasAppend struct {
	Type      string `json:"type"` // "canvas_append"
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

// CanvasClear clears all entries from a session's canvas.
type CanvasClear struct {
	Type      string `json:"type"` // "canvas_clear"
	SessionID string `json:"session_id"`
}

// ListSessions requests the list of registered sessions from the daemon.
// An optional CWD filter restricts results to sessions from that directory.
type ListSessions struct {
	Type string `json:"type"` // "list_sessions"
	CWD  string `json:"cwd,omitempty"`
}

// SessionsList is the daemon's response to ListSessions.
type SessionsList struct {
	Type     string    `json:"type"` // "sessions_list"
	Sessions []Session `json:"sessions"`
}

// Subscribe is sent by a TUI client to become a long-lived subscriber.
type Subscribe struct {
	Type string `json:"type"` // "subscribe"
}

// Daemon → TUI messages

// FullState is sent to a new subscriber with the complete current state.
type FullState struct {
	Type     string    `json:"type"` // "full_state"
	Sessions []Session `json:"sessions"`
}

// SessionAdded is broadcast when a new session registers.
type SessionAdded struct {
	Type      string `json:"type"` // "session_added"
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	CWD       string `json:"cwd"`
}

// CanvasAppended is broadcast when content is appended to a session canvas.
type CanvasAppended struct {
	Type      string `json:"type"` // "canvas_appended"
	SessionID string `json:"session_id"`
	Entry     Entry  `json:"entry"`
}

// CanvasCleared is broadcast when a session canvas is cleared.
type CanvasCleared struct {
	Type      string `json:"type"` // "canvas_cleared"
	SessionID string `json:"session_id"`
}

// SocketPath returns the Unix socket path, respecting XDG_DATA_HOME.
func SocketPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	return filepath.Join(base, "tui-canvas", "tui.sock")
}

// Encode marshals v to JSON and appends a newline delimiter.
func Encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
