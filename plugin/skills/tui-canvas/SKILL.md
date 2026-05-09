---
model: claude-haiku-4-5-20251001
---

# /tui-canvas

Push content to the tui-canvas display panel.

## Instructions

1. Determine the content:
   - If the user provided text after `/tui-canvas`, use that text.
   - If nothing was provided, use the literal string `PUSH_PREV_ASSISTANT` as the content — the stop hook will push the previous assistant response.

2. Write the content to the staging file with a heredoc:

```bash
tee "${XDG_DATA_HOME:-$HOME/.local/share}/tui-canvas/pending-$CLAUDE_CODE_SESSION_ID" > /dev/null <<'EOF'
CONTENT_HERE
EOF
```

3. Tell the user: "Pushed to canvas."

## Notes

- The stop hook reads the staging file after this turn and performs the actual append — no further tool calls needed.
- Content is rendered as markdown in the TUI.
- If the daemon is not running the push fails silently.
