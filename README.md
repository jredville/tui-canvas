# tui-canvas

A persistent canvas display panel for Claude Code sessions. Run `tui-canvas` in a separate terminal pane and Claude will push content to it via `/tui-canvas`.

```
┌──────────────────────────────────────────────┐
│ ╭─proj-api─╮  ╭─proj-web─╮                   │
├──────────────────────────────────────────────┤
│                                              │
│  ── [1] ──────────────────────────────────  │
│  # Refactored auth                           │
│  ```go                                       │
│  func auth() { ... }                         │
│  ```                                         │
│                                              │
│  ── [2] ──────────────────────────────────  │
│  The bug was a race condition in...          │
│                                              │
├──────────────────────────────────────────────┤
│ tab/shift+tab: switch  ↑↓/jk: scroll  q: quit│
└──────────────────────────────────────────────┘
```

Multiple TUI windows can run simultaneously (e.g. in split panes) — all show the same live state from a shared daemon.

## Architecture

```
Claude plugin (SessionStart hook)
        │ session_register
        ▼
  tui-canvas daemon          ◄── canvas_append / canvas_clear
  (~/.local/share/tui-canvas/tui.sock)
        │ broadcast
        ▼
  TUI client(s)
```

## Installation

### 1. Build the binary

Requires Go 1.24+.

```bash
git clone https://github.com/jredville/tui-canvas
cd tui-canvas
go build ./cmd/tui-canvas/
```

Place the binary somewhere on your `$PATH`:

```bash
ln -sf "$PWD/tui-canvas" ~/.local/bin/tui-canvas
```

### 2. Install the Claude Code plugin

Add to `~/.claude/settings.json`:

```json
{
  "extraKnownMarketplaces": {
    "tui-canvas": {
      "source": {
        "source": "github",
        "repo": "jredville/tui-canvas"
      }
    }
  },
  "enabledPlugins": {
    "tui-canvas@tui-canvas": true
  }
}
```

Then run `/reload-plugins` in Claude Code.

## Usage

### Open the TUI

```bash
tui-canvas
```

This auto-starts the daemon if it's not running. Open multiple instances in split panes — they all stay in sync.

### In Claude Code

- **`/tui-canvas`** — sends the previous response (or supplied text) to the canvas
- **`/tui-clear`** — clears the canvas for the current session

Each Claude session gets its own tab, registered automatically via the `SessionStart` hook.

### Manual testing

```bash
# Start daemon directly
tui-canvas daemon &

# Register a session
echo '{"type":"session_register","session_id":"s1","name":"my-proj","cwd":"/tmp"}' | tui-canvas send

# Append markdown content
echo '{"type":"canvas_append","session_id":"s1","content":"# Hello\nThis is **markdown**"}' | tui-canvas send

# Clear
echo '{"type":"canvas_clear","session_id":"s1"}' | tui-canvas send
```

## Socket Protocol

Newline-delimited JSON over a Unix socket at `~/.local/share/tui-canvas/tui.sock`.

**Plugin → Daemon:**

| Type | Fields |
|------|--------|
| `session_register` | `session_id`, `name`, `cwd` |
| `canvas_append` | `session_id`, `content` (markdown) |
| `canvas_clear` | `session_id` |

**TUI → Daemon:** `{"type":"subscribe"}`

**Daemon → TUI (broadcast):**

| Type | Fields |
|------|--------|
| `full_state` | `sessions` array (sent on subscribe) |
| `session_added` | `session_id`, `name`, `cwd` |
| `canvas_appended` | `session_id`, `entry` (`content`, `index`) |
| `canvas_cleared` | `session_id` |

## Keyboard shortcuts

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Switch sessions |
| `↑` / `↓` / `j` / `k` | Scroll |
| `q` / `ctrl+c` | Quit |
