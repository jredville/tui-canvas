# /tui-clear

Clear the canvas for the current session.

## Instructions

1. Find `[tui-canvas] Your session ID is: <UUID>` in your context. Extract the UUID.
2. Send the clear message:

```bash
python3 -c "
import json, subprocess
msg = json.dumps({'type': 'canvas_clear', 'session_id': 'SESSION_ID_HERE'})
subprocess.run(['tui-canvas', 'send'], input=msg.encode())
"
```

3. Confirm to the user: "Canvas cleared."
