# Architecture

tui-canvas is three cooperating subsystems in a single Go binary: a **daemon**, a **TUI client**, and a **Claude Code plugin**. They communicate over a Unix domain socket using newline-delimited JSON.

```
┌─────────────────────────────────────────────────────┐
│  Claude Code session (one per terminal tab)          │
│                                                      │
│  SessionStart hook ──registers──► daemon             │
│                    └──writes──►  session-$ID file     │
│  /tui-canvas skill ──(no-op)──► says "Pushed to canvas."│
│  Stop hook         ──reads transcript tail──► appends│
│  /tui-clear skill  ──(no-op)──► says "Canvas cleared."│
└─────────────────────────────────────────────────────┘
                                       │
                              Unix socket (tui.sock)
                                       │
                             ┌─────────▼─────────┐
                             │      Daemon         │
                             │  (state server)     │
                             └─────────┬─────────┘
                                       │ broadcasts
                              Unix socket (tui.sock)
                                       │
                             ┌─────────▼─────────┐
                             │    TUI Client       │
                             │  (display panel)    │
                             └───────────────────┘
```

---

## Part 1 — Daemon (`internal/daemon/daemon.go`)

The daemon is the single source of truth. It runs as a detached background process (`tui-canvas daemon`) and owns all session state.

**Socket:** `~/.local/share/tui-canvas/tui.sock` (XDG-aware via `protocol.SocketPath()`).

**Connection model:** each inbound connection sends exactly one JSON line. The daemon peeks at the `type` field and routes to one of two handlers:

- `subscribe` → long-lived TUI subscriber; receives `full_state` immediately then all future broadcast events
- anything else → one-shot plugin message; processed then connection closed

**Session state:** a slice of `protocol.Session` structs (ID, Name, CWD, Entries). Entries are indexed sequentially per session; the next index is derived from `len(Entries)+1` at append time — no separate map.

**Persistence:** after every mutation (`session_register`, `canvas_append`, `canvas_clear`, `session_remove`) the full session list is marshaled to `~/.local/share/tui-canvas/sessions.json`. On startup the daemon reloads this file so sessions survive daemon restarts.

**Broadcast:** a `broadcaster` maintains the set of connected TUI subscribers. Every state mutation is broadcast as an incremental event. Slow subscribers drop messages rather than stalling the broadcast loop (buffered channel, non-blocking send).

---

## Part 2 — TUI Client (`internal/tui/`)

The TUI client is what the user looks at. It runs in the foreground (`tui-canvas` with no subcommand) and auto-starts the daemon if it isn't already running.

**Framework:** bubbletea (Elm architecture — Model/Update/View).

**Connection:** connects to the daemon socket, sends `subscribe`, then enters a read loop. The read loop runs in a goroutine and sends lines to a buffered channel; bubbletea polls that channel via a `waitForMsg` command.

**State:** mirrors the daemon's session list locally. Incoming socket messages are applied incrementally:
- `full_state` → replace all sessions (initial sync or post-reconnect)
- `session_added` → append new session
- `canvas_appended` → append entry to matching session
- `canvas_cleared` → clear entries for matching session
- `session_removed` → remove session; clamp `activeTab` if needed
- `daemon_restarting` → enter reconnect state machine (exponential backoff, auto-restart daemon if needed)

**Layout:**
```
┌─ tab bar ─────────────────────────────────────────┐  3 lines
│  session-name  │  other-session                    │
├───────────────────────────────────────────────────┤
│                                                   │
│  scrollable viewport (bubbletea viewport widget)  │  fills remaining
│  rendered markdown via glamour                    │
│                                                   │
├───────────────────────────────────────────────────┤
│  tab/shift+tab: switch  ↑↓/jk: scroll  x: kill…  │  1 line
└───────────────────────────────────────────────────┘
```

**Keybindings:**
| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | switch session tabs |
| `↑↓` / `j` / `k` | scroll viewport |
| `?` | toggle debug mode (shows session IDs) |
| `x` | kill active tab (press `x` again to confirm, any other key cancels) |
| `R` | restart daemon; TUI reconnects automatically |
| `q` / `ctrl+c` | quit |
| mouse click | click a tab to switch to it |

**Debug mode (`?`):** tab labels show `name [first-8-chars-of-id]`; the help bar shows the full session ID of the active tab. This is the in-TUI way to recover a session ID after context loss.

---

## Part 3 — Plugin (`plugin/`)

The plugin integrates tui-canvas into Claude Code. It has two components: a hook and skills.

### Hooks

**`plugin/hooks/session-start`** — runs at `SessionStart`:
1. Exits immediately if `CLAUDE_CODE_SESSION_ID` is unset (returns `{}`)
2. Generates a UUID (via `uuidgen` or Python fallback); reuses existing session for the same CWD
3. Calls `tui-canvas register <id> <name> <cwd>` (typed subcommand — no shell injection risk)
4. Writes the tui-canvas session ID to `~/.local/share/tui-canvas/session-$CLAUDE_CODE_SESSION_ID`
5. Returns `{}` (no `additionalContext` — session ID stays out of Claude's context)

**`plugin/hooks/stop-push`** — runs at `Stop` (after every turn):
1. Reads the hook event JSON from stdin (provides `session_id` and `transcript_path`)
2. Validates `transcript_path` is under `~/.claude/` to prevent path traversal
3. Looks up the canvas session ID from `session-$CLAUDE_SESSION`; exits if not found
4. Reads the last 64 KB of the transcript (tail-scan to avoid O(N²) growth over long sessions)
5. Scans backward for the most recent `<command-name>/tui-canvas...` or `<command-name>/tui-clear...` user message
6. Exits if any assistant message already follows that command (means it was handled in a prior turn)
7. For `/tui-clear`: calls `tui-canvas clear`; for `/tui-canvas`: pushes `<command-args>` verbatim (if present) or the previous assistant response

### Skills

Both skills run as Haiku (specified via `model:` frontmatter in SKILL.md) to minimise token cost. Neither skill makes any tool calls — all work happens in the stop hook.

**`/tui-canvas`** — says exactly "Pushed to canvas." The stop hook detects the invocation from the transcript and performs the push automatically after the turn.

**`/tui-clear`** — says exactly "Canvas cleared." The stop hook detects the invocation and performs the clear automatically.

---

## Protocol Reference (`internal/protocol/protocol.go`)

All messages are newline-terminated JSON. The `type` field is the discriminator.

### Plugin → Daemon

| type | fields | effect |
|------|--------|--------|
| `session_register` | `session_id`, `name`, `cwd` | registers a new session (idempotent); also sent by `tui-canvas register` |
| `canvas_append` | `session_id`, `content` | appends a sanitized markdown entry; no-op if session unknown |
| `canvas_clear` | `session_id` | removes all entries from the session; no-op if session unknown |
| `session_remove` | `session_id` | removes the session and all its entries |
| `list_sessions` | `cwd` (optional) | responds with `sessions_list` then closes |
| `daemon_restart` | — | daemon broadcasts `daemon_restarting` then exits cleanly |

### Daemon → Plugin (response)

| type | fields |
|------|--------|
| `sessions_list` | `sessions[]` — each has `session_id`, `name`, `cwd` |

### Daemon → TUI (broadcast)

| type | fields |
|------|--------|
| `full_state` | `sessions[]` — full snapshot sent to new subscribers |
| `session_added` | `session_id`, `name`, `cwd` |
| `canvas_appended` | `session_id`, `entry{content, index}` |
| `canvas_cleared` | `session_id` |
| `session_removed` | `session_id` |
| `daemon_restarting` | — | signals all TUIs to enter reconnect state machine |
