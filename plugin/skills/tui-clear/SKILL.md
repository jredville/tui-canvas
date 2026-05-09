---
model: claude-haiku-4-5-20251001
---

# /tui-clear

Clear the canvas for the current session.

## Instructions

1. Clear the canvas using the session ID from the temp file:

```bash
SESSION_ID=$(cat "${XDG_DATA_HOME:-$HOME/.local/share}/tui-canvas/session-$CLAUDE_CODE_SESSION_ID" 2>/dev/null) && tui-canvas clear "$SESSION_ID"
```

2. If the above fails (session file missing), fall back to:

```bash
tui-canvas clear "$(tui-canvas list --cwd "$(pwd)" | tail -1 | cut -f1)"
```

3. Confirm to the user: "Canvas cleared."

## Notes

- If the daemon is not running or no session is found, fail silently and tell the user the canvas is not available.
