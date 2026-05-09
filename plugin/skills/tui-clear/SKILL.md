---
model: claude-haiku-4-5-20251001
---

# /tui-clear

**Execute immediately. No preamble, no clarifying questions. No tool calls.**

Say exactly: "Canvas cleared."

## Notes

- The stop hook detects this invocation from the transcript and clears the canvas automatically.
- If the daemon is not running or no session is found, the clear fails silently.
