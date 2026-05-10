// Command tui-canvas is a three-in-one binary:
//
//	tui-canvas                   — TUI client (auto-starts daemon if not running)
//	tui-canvas daemon            — background state server
//	tui-canvas send              — reads JSON from stdin, writes to socket
//	tui-canvas append <id>       — reads content from stdin, appends to session canvas
//	tui-canvas clear <id>        — clears a session canvas
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tui-canvas/internal/daemon"
	"tui-canvas/internal/protocol"
	"tui-canvas/internal/tui"
)

// Version is set at build time via -ldflags "-X main.Version=vX.Y.Z".
var Version = "dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "daemon":
			daemon.Run()
			return
		case "register":
			runRegister()
			return
		case "send":
			runSend()
			return
		case "list":
			runList()
			return
		case "append":
			runAppend()
			return
		case "clear":
			runClear()
			return
		case "restart":
			runRestart()
			return
		case "version":
			fmt.Println(Version)
			return
		case "--help", "-h", "help":
			printUsage()
			return
		}
	}
	runTUI()
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `tui-canvas runs as a background daemon + TUI display + Claude Code plugin.
Start the TUI, then use /tui-canvas in a Claude session to push content.

Usage:
  tui-canvas                  Start the TUI client (auto-starts daemon)
  tui-canvas append <id>      Read markdown from stdin and append to session canvas (get IDs: tui-canvas list)
  tui-canvas clear <id>       Clear a session canvas (get IDs: tui-canvas list)
  tui-canvas list [--cwd DIR] List registered sessions (tab-separated: id name cwd)
  tui-canvas restart          Restart the background daemon (TUIs reconnect automatically)
  tui-canvas version          Print the version string
  tui-canvas help             Show this help message`)
}

// runRegister registers a session with the daemon using typed arguments (no shell injection risk).
func runRegister() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: tui-canvas register <id> <name> <cwd>")
		os.Exit(1)
	}
	id, name, cwd := os.Args[2], os.Args[3], os.Args[4]
	if !protocol.IsDaemonRunning() {
		return
	}
	msg, err := protocol.Encode(protocol.SessionRegister{
		Type: protocol.TypeSessionRegister, SessionID: id, Name: name, CWD: cwd,
	})
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write(msg)
}

// runTUI starts the daemon if needed, connects, subscribes, and runs the TUI.
func runTUI() {
	if !protocol.IsDaemonRunning() {
		// Launch daemon as a detached background process.
		cmd := exec.Command(os.Args[0], "daemon")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "tui-canvas: failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		go cmd.Wait() //nolint // reap child so it doesn't become a zombie

		// Poll until the socket is ready (up to 1 s).
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
		fmt.Fprintf(os.Stderr, "tui-canvas: cannot connect to daemon: %v\n", err)
		os.Exit(1)
	}

	// Send subscribe message.
	sub := protocol.Subscribe{Type: protocol.TypeSubscribe}
	b, err := protocol.Encode(sub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui-canvas: encode subscribe: %v\n", err)
		os.Exit(1)
	}
	if _, err := conn.Write(b); err != nil {
		fmt.Fprintf(os.Stderr, "tui-canvas: write subscribe: %v\n", err)
		os.Exit(1)
	}

	model, err := tui.New(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui-canvas: init TUI: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui-canvas: %v\n", err)
		os.Exit(1)
	}
}

// runList queries the daemon for registered sessions and prints them to stdout,
// one per line, tab-separated: session_id<TAB>name<TAB>cwd.
// Pass --cwd <dir> to filter by working directory.
func runList() {
	if !protocol.IsDaemonRunning() {
		return
	}

	var cwd string
	args := os.Args[2:]
	for i, arg := range args {
		if arg == "--cwd" && i+1 < len(args) {
			cwd = args[i+1]
			break
		}
	}

	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	req := protocol.ListSessions{Type: protocol.TypeListSessions, CWD: cwd}
	b, err := protocol.Encode(req)
	if err != nil {
		return
	}
	if _, err := conn.Write(b); err != nil {
		return
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}

	var resp protocol.SessionsList
	if err := json.Unmarshal(line, &resp); err != nil {
		return
	}

	for _, s := range resp.Sessions {
		fmt.Printf("%s\t%s\t%s\n", s.ID, s.Name, s.CWD)
	}
}

// runAppend reads content from stdin and appends it to the named session's canvas.
func runAppend() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tui-canvas append <session-id>")
		os.Exit(1)
	}
	sessionID := os.Args[2]
	content, err := io.ReadAll(os.Stdin)
	if err != nil || !protocol.IsDaemonRunning() {
		return
	}
	msg, err := protocol.Encode(protocol.CanvasAppend{
		Type:      protocol.TypeCanvasAppend,
		SessionID: sessionID,
		Content:   string(content),
	})
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write(msg)
}

// runClear clears the canvas for the named session.
func runClear() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tui-canvas clear <session-id>")
		os.Exit(1)
	}
	sessionID := os.Args[2]
	if !protocol.IsDaemonRunning() {
		return
	}
	msg, err := protocol.Encode(protocol.CanvasClear{
		Type:      protocol.TypeCanvasClear,
		SessionID: sessionID,
	})
	if err != nil {
		return
	}
	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write(msg)
}

// runSend reads a JSON message from stdin and forwards it to the daemon socket.
// It exits 0 silently if the daemon is not running (plugin must never fail a session).
func runSend() {
	if !protocol.IsDaemonRunning() {
		return
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		// Silent exit – don't fail the Claude session.
		return
	}

	// Normalise: ensure exactly one trailing newline.
	msg := raw
	for len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	msg = append(msg, '\n')

	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	_, _ = conn.Write(msg)
}

// runRestart asks the daemon to restart itself. Connected TUIs will
// automatically reconnect via their built-in reconnect state machine.
func runRestart() {
	if !protocol.IsDaemonRunning() {
		fmt.Fprintln(os.Stderr, "tui-canvas: daemon is not running")
		return
	}
	conn, err := net.DialTimeout("unix", protocol.SocketPath(), time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui-canvas: %v\n", err)
		return
	}
	defer conn.Close()
	b, _ := protocol.Encode(protocol.DaemonRestart{Type: protocol.TypeDaemonRestart})
	_, _ = conn.Write(b)
	fmt.Fprintln(os.Stderr, "tui-canvas: restart sent")
}
