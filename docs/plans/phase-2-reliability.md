# Phase 2: Medium Reliability + Code Quality

## Project context
tui-canvas is a Go binary: background daemon (Unix socket), TUI (bubbletea), CLI/plugin hooks
(bash+Python). Phase 1 fixed all security and critical reliability issues. This phase addresses
medium-severity correctness, code quality, and usability issues.

## Pre-implementation validation
Before writing any code, the agent implementing this phase MUST:
1. Read `internal/daemon/daemon.go`, `internal/tui/model.go`, `internal/tui/view.go`,
   `cmd/tui-canvas/main.go`, `internal/protocol/protocol.go`,
   `plugin/hooks/session-start`, `plugin/hooks/stop-push`
2. Run `make build` to confirm the codebase builds cleanly (Phase 1 should already be merged)
3. Verify each finding below still matches the actual code before implementing
4. If a finding is already fixed, skip it and note the discrepancy

## Findings addressed

- **S7 (Medium):** runSend blindly forwards raw stdin JSON — replace with typed `register` subcommand (done in Phase 1)
- **S8 (Medium):** printf-based JSON in session-start — eliminated by typed `register` subcommand (done in Phase 1)
- **S9 (Medium):** stop-push doesn't validate transcript path
- **R5 (Medium):** Subscriber event ordering — bc.add before full_state queued
- **R6 (Medium):** No message size limit on socket
- **R7 (Medium):** IsDaemonRunning TOCTOU — handled by direct dial errors
- **R8 (Medium):** Reconnect fails permanently after max attempts — no recovery UI
- **R9 (Medium):** extract_text misses tool-call-only assistant turns
- **Q3 (Medium):** CWD path normalization missing
- **Q7 (Medium):** bare `except: pass` in stop-push
- **Q4 (Medium):** go.mod all-indirect — run go mod tidy

## Changes implemented

### `internal/daemon/daemon.go`

- Message size limit (2 MiB) via `io.LimitReader` wrapping each connection
- Fixed subscriber event ordering: add subscriber and enqueue full_state atomically under bc.mu
- `filepath.Clean` on CWD comparison in list_sessions handler
- `loadSessions` logs and renames corrupt sessions.json instead of silently returning empty
- `saveSessions` logs rename errors

### `internal/tui/model.go` + `view.go`

- Recovery after max reconnect: pressing R when `m.err != nil` retries connection
- Error view shows "Press R to retry" hint

### `plugin/hooks/stop-push`

- Validates transcript path is under `~/.claude/` before opening
- Fixes bare `except: pass` → `except json.JSONDecodeError: pass`
- Fixes `extract_text` to handle tool-result content blocks

### `go mod tidy`

- Fixes all-indirect annotations on direct dependencies

## Verification checklist

- [ ] `tui-canvas register test-id "myproject" "/home/user/proj"` → session appears in TUI
- [ ] `tui-canvas append <bad-id> <<< "hello"` → daemon logs nothing, no crash, no broadcast
- [ ] Send 3MB payload → daemon closes connection
- [ ] Start TUI, immediately append from another terminal → content appears (ordering fix)
- [ ] Kill daemon, wait for max reconnect → TUI shows "Press R to retry", R works
- [ ] `go mod tidy && go build ./...` → no errors
