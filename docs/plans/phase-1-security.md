# Phase 1: Security + Critical Reliability

## Project context
tui-canvas is a Go binary that serves as a persistent canvas display panel for Claude Code
sessions. It has three roles in one binary: a background daemon (Unix socket server at
`~/.local/share/tui-canvas/tui.sock`), a TUI client (bubbletea), and a CLI with plugin hooks
(bash + Python). The plugin hooks run as Claude Code `SessionStart` and `Stop` hooks.

## Pre-implementation validation
Before writing any code, the agent implementing this phase MUST:
1. Read `internal/daemon/daemon.go`, `internal/protocol/protocol.go`,
   `cmd/tui-canvas/main.go`, `plugin/hooks/session-start`, `plugin/hooks/stop-push`
2. Verify each finding below still matches the actual code (line numbers may have shifted)
3. If a finding is already fixed or doesn't match, skip it and note the discrepancy
4. Confirm the build still works: `make build`

## Findings addressed

- **S1/R1 (Critical):** saveSessions non-atomic write + write-outside-lock race
- **S2 (High):** ANSI/OSC escape-sequence injection via canvas_append
- **S3 (High):** Socket/dir permissions — world-accessible socket and sessions.json
- **S4 (High):** Binary auto-update — no checksum/signature verification
- **S5/R2 (High):** daemon_restart doesn't os.Exit → daemon zombie, restart race
- **R3 (High):** session-unknown staging-file collision across concurrent sessions
- **R4 (High):** canvas_append to unregistered session mutates nextIdx and broadcasts

## Changes implemented

### `internal/daemon/daemon.go`

- `os.MkdirAll` now uses `0o700` instead of `0o755`
- After `net.Listen(...)` succeeds: `_ = os.Chmod(sockPath, 0o600)`
- `saveSessions` uses atomic rename (write to `.tmp`, then `os.Rename`) and holds `saveMu`
- `sessions.json` written with `0o600` permissions
- `shutdown(sockPath)` helper factored out; called from both signal handler and `daemon_restart`
- `canvas_append` and `canvas_clear`: guard against unregistered session ID
- `sanitizeContent` strips ANSI/OSC sequences before storing content

### `plugin/hooks/session-start`

- Early exit if `CLAUDE_CODE_SESSION_ID` is unset (eliminates `session-unknown` fallback)
- Binary auto-update now verifies SHA256 before installing

### `plugin/hooks/stop-push`

- Removed `session-unknown` fallback; exits cleanly if session file not found

## Verification checklist

- [ ] `make build && make install`
- [ ] `ls -la ~/.local/share/tui-canvas/` — dir 0700, sock 0600, sessions.json 0600
- [ ] Start TUI, append content with embedded `\x1b]52;c;SGVsbG8=\x07` — should not affect clipboard
- [ ] Kill daemon with SIGKILL mid-write, restart — sessions.json should be intact or absent
- [ ] Run two Claude sessions concurrently — each gets its own tab, no routing collision
- [ ] `tui-canvas restart` — daemon fully exits, new one starts, TUI reconnects
