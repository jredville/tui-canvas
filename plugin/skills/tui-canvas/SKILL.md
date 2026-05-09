---
model: claude-haiku-4-5-20251001
---

# /tui-canvas

Send content to the tui-canvas display panel.

## Instructions

1. Find the session ID using the first method that succeeds:
   - **a)** Look for `[tui-canvas] Your session ID is: <UUID>` in your context. Extract the UUID.
   - **b)** Run `tui-canvas list` (no extra args, no pipes) and read the session ID from the last line of output. The format is `id<TAB>name<TAB>cwd` — take the first field of the last row.
   - **c)** If neither works, tell the user the canvas is not available and suggest pressing `?` in the TUI to reveal session IDs.

2. Determine the content:
   - If the user provided text after `/tui-canvas`, use that.
   - Otherwise, use the **visible assistant response text only** — the text the user can read. Do not include internal thinking or reasoning.

3. Send to canvas using `tui-canvas append` with a heredoc:

```bash
tui-canvas append SESSION_ID_HERE <<'EOF'
CONTENT_HERE
EOF
```

4. Confirm to the user: "Sent to canvas."

## Notes

- If `tui-canvas append` fails or the daemon is not running, fail silently and tell the user the canvas is not available.
- Content is rendered as markdown in the TUI.
- Press `?` in the TUI to toggle debug mode, which shows the session ID for each tab.
