# Phase 3: Tests, Performance & Polish

## Project context
tui-canvas is a Go binary: background daemon (Unix socket), TUI (bubbletea), CLI/plugin hooks
(bash+Python). Phases 1 and 2 fixed all security and medium+ reliability issues. This phase adds
test coverage, fixes performance bottlenecks, and polishes code quality.

## Pre-implementation validation
Before writing any code, the agent implementing this phase MUST:
1. Read all Go source files and both hook scripts
2. Run `make build` and `make test` (tests may not exist yet — note the baseline)
3. Verify each finding below still matches the actual code before implementing
4. If a finding is already fixed, skip it and note the discrepancy

## Findings addressed

- **Q1 (High):** Zero tests
- **P1 (Medium):** Full re-render of all entries on every state change — O(entries) per event
- **P2 (Medium):** stop-push reads entire transcript every turn — O(N²) over session lifetime
- **Q2 (Medium):** Session temp files accumulate forever
- **Q5 (Medium):** `send` exposed in public help; `daemon` has no safe user story
- **Q6 (Medium):** No session ID discoverability in help for append/clear
- **Q8 (Low):** Magic type strings — no constants
- **Q9 (Low):** No protocol version field
- **Q10 (Low):** `restart` prints nothing on success
- **Q12 (Low):** nextIdx is denormalized — derive instead of store
- **Q13 (Low):** Help text gives no orientation to three-part system

## Changes implemented

### `internal/protocol/protocol.go`

- Protocol type constants (`TypeSessionRegister`, `TypeCanvasAppend`, etc.)

### `internal/daemon/daemon.go`

- Derive `nextIdx` from `len(Entries)` inline (removed the map)
- Session temp file GC on startup
- All switch cases use `protocol.TypeXxx` constants

### `internal/tui/model.go`

- `renderedEntries` cache map added to Model
- Cache invalidated on `canvas_cleared` and `full_state`
- All switch cases use `protocol.TypeXxx` constants

### `internal/tui/view.go`

- `renderCanvas` uses the render cache

### `cmd/tui-canvas/main.go`

- Help text orientation blurb at top
- append/clear descriptions include "(get IDs: tui-canvas list)"
- `send` and `daemon` removed from help output
- `runRestart` prints confirmation message

### `plugin/hooks/stop-push`

- Tail-scan optimization: reads last 64KB of transcript instead of full file

### New test files

- `internal/protocol/protocol_test.go`
- `internal/daemon/daemon_test.go`
- `internal/tui/model_test.go`

## Verification checklist

- [ ] `make test` — all new tests pass
- [ ] Open session with 100+ entries, scroll — no visible lag
- [ ] `tui-canvas help` — shows orientation blurb and "(get IDs: tui-canvas list)"
- [ ] `tui-canvas restart` — prints "tui-canvas: restart sent"
- [ ] All switch cases use `protocol.TypeXxx` constants
- [ ] `go build ./... && go vet ./...` — no errors
