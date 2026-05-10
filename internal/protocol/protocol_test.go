package protocol_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tui-canvas/internal/protocol"
)

func TestEncode_roundTrip(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"SessionRegister", protocol.SessionRegister{Type: protocol.TypeSessionRegister, SessionID: "abc", Name: "proj", CWD: "/home/x"}, protocol.TypeSessionRegister},
		{"CanvasAppend", protocol.CanvasAppend{Type: protocol.TypeCanvasAppend, SessionID: "s1", Content: "hello"}, protocol.TypeCanvasAppend},
		{"CanvasClear", protocol.CanvasClear{Type: protocol.TypeCanvasClear, SessionID: "s1"}, protocol.TypeCanvasClear},
		{"ListSessions", protocol.ListSessions{Type: protocol.TypeListSessions, CWD: "/tmp"}, protocol.TypeListSessions},
		{"Subscribe", protocol.Subscribe{Type: protocol.TypeSubscribe}, protocol.TypeSubscribe},
		{"FullState", protocol.FullState{Type: protocol.TypeFullState, Sessions: nil}, protocol.TypeFullState},
		{"SessionAdded", protocol.SessionAdded{Type: protocol.TypeSessionAdded, SessionID: "s1"}, protocol.TypeSessionAdded},
		{"CanvasAppended", protocol.CanvasAppended{Type: protocol.TypeCanvasAppended, SessionID: "s1", Entry: protocol.Entry{Content: "x", Index: 1}}, protocol.TypeCanvasAppended},
		{"CanvasCleared", protocol.CanvasCleared{Type: protocol.TypeCanvasCleared, SessionID: "s1"}, protocol.TypeCanvasCleared},
		{"SessionRemove", protocol.SessionRemove{Type: protocol.TypeSessionRemove, SessionID: "s1"}, protocol.TypeSessionRemove},
		{"SessionRemoved", protocol.SessionRemoved{Type: protocol.TypeSessionRemoved, SessionID: "s1"}, protocol.TypeSessionRemoved},
		{"DaemonRestart", protocol.DaemonRestart{Type: protocol.TypeDaemonRestart}, protocol.TypeDaemonRestart},
		{"DaemonRestarting", protocol.DaemonRestarting{Type: protocol.TypeDaemonRestarting}, protocol.TypeDaemonRestarting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := protocol.Encode(tc.v)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(b) == 0 || b[len(b)-1] != '\n' {
				t.Fatal("expected trailing newline")
			}
			var env protocol.Envelope
			if err := json.Unmarshal(b[:len(b)-1], &env); err != nil {
				t.Fatalf("Unmarshal envelope: %v", err)
			}
			if env.Type != tc.want {
				t.Errorf("type = %q, want %q", env.Type, tc.want)
			}
		})
	}
}

func TestSocketPath_XDGOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	got := protocol.SocketPath()
	want := filepath.Join(tmp, "tui-canvas", "tui.sock")
	if got != want {
		t.Errorf("SocketPath() = %q, want %q", got, want)
	}
}

func TestSocketPath_DefaultHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	home, _ := os.UserHomeDir()
	got := protocol.SocketPath()
	want := filepath.Join(home, ".local", "share", "tui-canvas", "tui.sock")
	if got != want {
		t.Errorf("SocketPath() = %q, want %q", got, want)
	}
}

func TestIsDaemonRunning_NoSocket(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if protocol.IsDaemonRunning() {
		t.Fatal("expected false for non-existent socket")
	}
}

func TestIsDaemonRunning_ActiveSocket(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	sockPath := protocol.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	time.Sleep(10 * time.Millisecond)
	if !protocol.IsDaemonRunning() {
		t.Fatal("expected true for active socket")
	}
}
