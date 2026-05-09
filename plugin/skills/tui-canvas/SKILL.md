# /tui-canvas

Send content to the tui-canvas display panel.

## Instructions

1. Find `[tui-canvas] Your session ID is: <UUID>` in your context (from the SessionStart hook output). Extract the UUID.
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
