# /tui-canvas

Send content to the tui-canvas display panel.

## Instructions

1. Find the session ID using the first method that succeeds:
   - **a)** Look for `[tui-canvas] Your session ID is: <UUID>` in your context. Extract the UUID.
   - **b)** Run `tui-canvas list --cwd "$(pwd)"` and use the **last** session ID from the output — it is the most recently registered session. Extract with: `tui-canvas list --cwd "$(pwd)" | tail -1 | cut -f1`
   - **c)** If neither works, tell the user the canvas is not available and suggest pressing `?` in the TUI to reveal session IDs.

2. If the user provided text after `/tui-canvas`, use that as the content. Otherwise, use the previous assistant response.

3. Send to canvas using Python for safe JSON encoding:

```bash
python3 -c "
import json, subprocess
content = '''CONTENT_HERE'''
msg = json.dumps({'type': 'canvas_append', 'session_id': 'SESSION_ID_HERE', 'content': content})
subprocess.run(['tui-canvas', 'send'], input=msg.encode())
"
```

4. Confirm to the user: "Sent to canvas."

## Notes

- If `tui-canvas send` fails or the daemon is not running, fail silently and tell the user the canvas is not available.
- Content is rendered as markdown in the TUI.
- Press `?` in the TUI to toggle debug mode, which shows the session ID for each tab.
