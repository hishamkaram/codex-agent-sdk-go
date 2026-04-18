# Codex Agent SDK for Go

## What This Is

Go SDK for the OpenAI Codex CLI **app-server** transport — spawns `codex app-server` as a subprocess, speaks JSON-RPC 2.0 over stdio (line-framed, `jsonrpc` field omitted), and exposes a typed Go API for threads, turns, streaming events, approvals, and MCP configuration.

**Module**: `github.com/hishamkaram/codex-agent-sdk-go` | **Go**: 1.25.8+ | **License**: MIT

Sibling SDK: [`claude-agent-sdk-go`](https://github.com/hishamkaram/claude-agent-sdk-go) wraps the Claude Code CLI the same way.

## Build & Test

```bash
go build ./...                            # Build all packages
go test -race -count=1 -p 4 -short ./... # Unit tests (fast, no CLI needed)
make test-integration                     # Integration tests (require real codex CLI + OPENAI_API_KEY)
go vet ./...                              # Static analysis
golangci-lint run ./...                   # Linter
govulncheck ./...                         # Vulnerability scan
```

## Architecture

| Package | Path | Purpose |
|---------|------|---------|
| root | `./` | Public API: `Codex` client, `Query()`, `NewClient()`, thread operations |
| types | `types/` | Public types: events, items, options, approvals, errors |
| transport | `internal/transport/` | Subprocess spawn, CLI discovery, line-framed stream I/O |
| jsonrpc | `internal/jsonrpc/` | JSON-RPC 2.0 framing + request/notification/server-request demux |
| events | `internal/events/` | Notification payload → typed event translation |
| log | `internal/log/` | zap wrapper (constructor-injected) |

## Critical Rules

**Transport**: JSON-RPC 2.0 line-framed, LF terminator, **omit `"jsonrpc":"2.0"` on the wire** (server tolerates absence; matches upstream Python SDK). Read-side buffer minimum **2 MiB** — user input cap is 1 MiB and envelope overhead pushes some notifications past that.

**Stdin serialization**: a single `sync.Mutex` MUST serialize every stdin write across all frame types (user turn/start, approval responses, config writes). Frames may NOT interleave on the wire.

**Turn serialization**: per-thread `turnMu` MUST serialize `Run()` / `RunStreamed()` calls. Codex server queues concurrent turn/start but collapses their event boundaries — client must serialize to preserve the 1-message → 1-turn → 1-completion contract.

**Demux**: `internal/jsonrpc/demux.go` routes inbound lines by shape:
- `{id, result|error}` → response → `pending[id]` chan
- `{id, method}` → server-initiated request (approvals) → `serverRequests` chan; caller MUST respond with matching id
- `{method}` (no id) → notification → `events` chan

**Errors**: Wrap every error with context:
```go
fmt.Errorf("pkg.Func: context: %w", err)
```
Use `%w` (not `%v`). Define sentinel errors with `errors.New()`. Check with `errors.Is()`/`errors.As()`. Public typed errors are `types.CLINotFoundError`, `types.CLIConnectionError`, `types.ProcessError`, `types.JSONDecodeError`, `types.MessageParseError`, `types.RPCError`, `types.ApprovalDeniedError` — each has an `Is*()` helper.

**Logging**: `go.uber.org/zap` only — constructor-injected, never global. No `fmt.Println`, no `log.Print`, no `log.Printf`. Structured fields: `zap.String()`, `zap.Error()`.

**Tests**: Table-driven with `t.Parallel()` and `tt := tt`. Every package that starts goroutines must have a `main_test.go` using `goleak.VerifyTestMain`. No global state, no `init()`.

**Goroutines**: Context-cancel every goroutine. `defer cancel()` after `context.WithCancel()`. `errgroup` for multiple goroutines that share a failure.

**Commits**: `type(scope): subject` format. Examples:
- `feat(transport): add warm-pool pre-start`
- `fix(events): handle nil item in parser`
- `chore(ci): bump go to 1.25.9`

No AI authorship trailers. No `Co-Authored-By: Claude …`.

## Key wire shapes (verified against real codex app-server)

The AgentD workspace ran a spike against a real `codex app-server` binary. The captured transcripts at `../docs/plans/codex-integration-spike-workdir/*.jsonl` verify these outer shapes:

```json
// initialize
{"id":1,"method":"initialize","params":{"clientInfo":{"name":"my-app","version":"0.1.0","title":"..."},"capabilities":{"experimentalApi":false}}}

// initialized notification (no id)
{"method":"initialized"}

// thread/start
{"id":2,"method":"thread/start","params":{"cwd":"/path","sandbox":"read-only","approvalPolicy":"untrusted"}}

// thread/resume (cwd override confirmed supported)
{"id":3,"method":"thread/resume","params":{"threadId":"...","cwd":"/path"}}

// turn/start
{"id":4,"method":"turn/start","params":{"threadId":"...","input":[{"type":"text","text":"..."},{"type":"localImage","path":"/abs/path.png"}]}}

// turn/interrupt
{"id":5,"method":"turn/interrupt","params":{"threadId":"...","turnId":"..."}}

// thread/archive
{"id":6,"method":"thread/archive","params":{"threadId":"..."}}

// thread/list
{"id":7,"method":"thread/list","params":{}}
```

**Notification types** (server → client, no id): `turn/started`, `turn/completed`, `turn/failed`, `item/started`, `item/updated`, `item/completed`, `thread/tokenUsage/updated`, `compaction_event`, `error`.

**Server-initiated requests** (server → client, with id — caller MUST respond): approval requests like `CommandExecutionRequestApproval`.

## Unknowns deferred to runtime

The spike confirms outer RPC shapes; the full per-subtype payload for every `item.type` is not yet exhaustively captured. Parser strategy: recognize known subtypes (`agent_message`, `user_message`, `command_execution`, `file_change`, `mcp_tool_call`, `web_search`, `memory_read`, `memory_write`, `plan`, `reasoning`) and emit `*types.UnknownItem{Type, Raw}` for anything else. New subtypes get wired up incrementally as real traffic surfaces them.

## Full Specification

See the plan file at `/home/hesham/.claude/plans/jaunty-crafting-flamingo.md` for the full design rationale, execution phases, and open questions.
