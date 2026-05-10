package daemon_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tui-canvas/internal/daemon"
	"tui-canvas/internal/protocol"
)

// startDaemon starts the daemon in a goroutine using a temp XDG_DATA_HOME.
// Returns the socket path. The test must call cleanup() when done.
func startDaemon(t *testing.T) (sockPath string, cleanup func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	sockPath = protocol.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	go daemon.Run()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleanup = func() { os.Remove(sockPath) }
	return sockPath, cleanup
}

func sendMsg(t *testing.T, sockPath string, v any) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	b, err := protocol.Encode(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := conn.Write(b); err != nil {
		t.Fatalf("write: %v", err)
	}
	return conn
}

func readResp(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("readResp: %v", err)
	}
	return line
}

func listSessions(t *testing.T, sockPath string) []protocol.Session {
	t.Helper()
	conn := sendMsg(t, sockPath, protocol.ListSessions{Type: protocol.TypeListSessions})
	defer conn.Close()
	raw := readResp(t, conn)
	var resp protocol.SessionsList
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal sessions_list: %v", err)
	}
	return resp.Sessions
}

func registerSession(t *testing.T, sockPath, id, name, cwd string) {
	t.Helper()
	conn := sendMsg(t, sockPath, protocol.SessionRegister{
		Type: protocol.TypeSessionRegister, SessionID: id, Name: name, CWD: cwd,
	})
	conn.Close()
	time.Sleep(20 * time.Millisecond) // let daemon process
}

func TestDaemon_DuplicateRegister_Idempotent(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	registerSession(t, sockPath, "s1", "proj", "/tmp/proj")
	registerSession(t, sockPath, "s1", "proj", "/tmp/proj") // duplicate

	sessions := listSessions(t, sockPath)
	count := 0
	for _, s := range sessions {
		if s.ID == "s1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 session, got %d", count)
	}
}

func TestDaemon_AppendToUnregistered_NoOp(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	// Subscribe to watch for any broadcasts
	subConn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer subConn.Close()
	b, _ := protocol.Encode(protocol.Subscribe{Type: protocol.TypeSubscribe})
	subConn.Write(b)

	// Read the initial full_state
	subConn.SetDeadline(time.Now().Add(time.Second))
	bufio.NewReader(subConn).ReadBytes('\n')

	// Append to non-existent session
	conn := sendMsg(t, sockPath, protocol.CanvasAppend{
		Type: protocol.TypeCanvasAppend, SessionID: "does-not-exist", Content: "hello",
	})
	conn.Close()
	time.Sleep(30 * time.Millisecond)

	// No broadcast should arrive
	subConn.SetDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = bufio.NewReader(subConn).ReadBytes('\n')
	if err == nil {
		t.Fatal("expected no broadcast for unregistered session")
	}
}

func TestDaemon_CanvasClear(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	registerSession(t, sockPath, "s2", "proj", "/tmp/proj")

	// Append an entry
	conn := sendMsg(t, sockPath, protocol.CanvasAppend{
		Type: protocol.TypeCanvasAppend, SessionID: "s2", Content: "data",
	})
	conn.Close()
	time.Sleep(30 * time.Millisecond)

	// Clear
	conn = sendMsg(t, sockPath, protocol.CanvasClear{
		Type: protocol.TypeCanvasClear, SessionID: "s2",
	})
	conn.Close()
	time.Sleep(30 * time.Millisecond)

	// Verify via list (entries are not returned by list, so we subscribe and check full_state)
	subConn, _ := net.DialTimeout("unix", sockPath, time.Second)
	defer subConn.Close()
	b, _ := protocol.Encode(protocol.Subscribe{Type: protocol.TypeSubscribe})
	subConn.Write(b)

	subConn.SetDeadline(time.Now().Add(time.Second))
	raw, _ := bufio.NewReader(subConn).ReadBytes('\n')
	var fs protocol.FullState
	json.Unmarshal(raw, &fs)
	for _, s := range fs.Sessions {
		if s.ID == "s2" && len(s.Entries) != 0 {
			t.Errorf("expected 0 entries after clear, got %d", len(s.Entries))
		}
	}
}

func TestDaemon_SessionRemove(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	registerSession(t, sockPath, "s3", "proj", "/tmp/proj")

	conn := sendMsg(t, sockPath, protocol.SessionRemove{
		Type: protocol.TypeSessionRemove, SessionID: "s3",
	})
	conn.Close()
	time.Sleep(30 * time.Millisecond)

	sessions := listSessions(t, sockPath)
	for _, s := range sessions {
		if s.ID == "s3" {
			t.Fatal("session s3 should have been removed")
		}
	}
}

func TestDaemon_ListSessions_CWDFilter(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	registerSession(t, sockPath, "a", "A", "/tmp/dirA")
	registerSession(t, sockPath, "b", "B", "/tmp/dirB")

	conn := sendMsg(t, sockPath, protocol.ListSessions{
		Type: protocol.TypeListSessions, CWD: "/tmp/dirA",
	})
	defer conn.Close()
	raw := readResp(t, conn)
	var resp protocol.SessionsList
	json.Unmarshal(raw, &resp)
	if len(resp.Sessions) != 1 || resp.Sessions[0].ID != "a" {
		t.Errorf("expected only session a, got %+v", resp.Sessions)
	}
}

func TestDaemon_ConcurrentAppends_MonotonicIndex(t *testing.T) {
	sockPath, cleanup := startDaemon(t)
	defer cleanup()

	registerSession(t, sockPath, "concurrent", "proj", "/tmp/proj")

	const goroutines = 5
	const perGoroutine = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				conn := sendMsg(t, sockPath, protocol.CanvasAppend{
					Type: protocol.TypeCanvasAppend, SessionID: "concurrent", Content: "x",
				})
				conn.Close()
			}
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Check full_state for monotonic indices
	subConn, _ := net.DialTimeout("unix", sockPath, time.Second)
	defer subConn.Close()
	b, _ := protocol.Encode(protocol.Subscribe{Type: protocol.TypeSubscribe})
	subConn.Write(b)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	raw, _ := bufio.NewReader(subConn).ReadBytes('\n')
	var fs protocol.FullState
	json.Unmarshal(raw, &fs)
	for _, s := range fs.Sessions {
		if s.ID != "concurrent" {
			continue
		}
		if len(s.Entries) != goroutines*perGoroutine {
			t.Errorf("expected %d entries, got %d", goroutines*perGoroutine, len(s.Entries))
		}
		seen := make(map[int]bool)
		for _, e := range s.Entries {
			if seen[e.Index] {
				t.Errorf("duplicate index %d", e.Index)
			}
			seen[e.Index] = true
			if e.Index < 1 || e.Index > goroutines*perGoroutine {
				t.Errorf("index %d out of range", e.Index)
			}
		}
	}
}

func TestDaemon_LoadSessions_CorruptFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	dataDir := filepath.Join(tmp, "tui-canvas")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessFile := filepath.Join(dataDir, "sessions.json")
	os.WriteFile(sessFile, []byte("not json {{{"), 0o600)

	go daemon.Run()
	time.Sleep(150 * time.Millisecond)

	// sessions.json should be renamed to .bak
	if _, err := os.Stat(sessFile + ".bak"); os.IsNotExist(err) {
		t.Error("expected sessions.json.bak to exist after corrupt load")
	}

	os.Remove(protocol.SocketPath())
}
