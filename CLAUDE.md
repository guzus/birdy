# birdy

Multi-account X/Twitter CLI proxy built in Go with a Bubble Tea TUI.

## Build & Test

- `go build ./...` — build all packages
- `go test ./... -count=1` — run all tests (no cache)
- `go vet ./...` — static analysis
- `go run . tui` — launch TUI (requires interactive TTY)

## Architecture

- `cmd/` — Cobra CLI commands; `tui.go` is the TUI entry point
- `tui/` — Bubble Tea TUI: splash, chat (Claude streaming), account management
- `internal/store/` — Account persistence to `~/.config/birdy/accounts.json`
- `internal/rotation/` — Account rotation strategies (round-robin, LRU, least-used, random)
- `internal/runner/` — Bird subprocess execution with injected credentials
- `internal/state/` — Runtime state persistence (`~/.config/birdy/state.json`)
- `cmd/host.go` — WebSocket + PTY browser host for TUI (`birdy host`)

## TUI Patterns

- Sub-models use value receivers returning `(Model, tea.Cmd)`, not `tea.Model`
- Claude streaming uses channel-based pattern: `startClaude` → `claudeNextMsg` → `waitForNext` loop
- `MainModel` routes claude messages to chat even during splash (background loading)
- Chat history saved as markdown to `~/.config/birdy/chats/`
- `context.Context` used to cancel Claude subprocess on esc/ctrl+c
- Injectable function fields (e.g., `readClipboardFn`, `writeClipboardFn`) for testing external deps
- Clipboard via `atotto/clipboard`; URL detection via compiled regex (`urlPattern`)

## Code Style

- No docstrings on unexported functions unless non-obvious
- Tests use `t.TempDir()` and `t.Setenv()` for isolation
- Store tests use `OpenPath()` with temp paths; TUI tests use `setupTestStore()` helper
- Lipgloss styles defined in `tui/styles.go`; colors follow Twitter/X palette (#1DA1F2)
- `cmd/` tests use `httptest.NewRequest` for HTTP handler testing
- Run `go mod tidy` after changing imports to keep direct/indirect deps correct
