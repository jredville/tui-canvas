# /tui-clear

Clear the canvas for the current session.

## Instructions

1. Find the session ID using the first method that succeeds:
   - **a)** Look for `[tui-canvas] Your session ID is: <UUID>` in your context. Extract the UUID.
   - **b)** Run `tui-canvas list --cwd "$(pwd)"` and use the first session ID from the output (tab-separated: `id\tname\tcwd`).
   - **c)** If neither works, tell the user the canvas is not available and suggest pressing `?` in the TUI to reveal session IDs.

2. Send the clear message:

```bash
python3 -c "
import json, subprocess
msg = json.dumps({'type': 'canvas_clear', 'session_id': 'SESSION_ID_HERE'})
subprocess.run(['tui-canvas', 'send'], input=msg.encode())
"
```

3. Confirm to the user: "Canvas cleared."
