---
model: claude-haiku-4-5-20251001
---

# /tui-canvas

**Execute immediately. No preamble, no clarifying questions. No tool calls.**

Say exactly: "Pushed to canvas."

## Notes

- The stop hook detects this invocation from the transcript and handles the push automatically.
- If text was provided after `/tui-canvas`, it is pushed verbatim. Otherwise the previous assistant response is pushed.
- Content is rendered as markdown in the TUI.
