# Changelog

All notable changes to the Codex Agent SDK for Go are documented in this file.

## [Unreleased]

- Expose the CLI-resolved thread model through `Thread.Model()`, including
  account defaults selected when `thread/start` omits a model override.
- Preserve provider terminal failure text on `TurnCompleted.Error`.

### Fixed
- Decode Unix-second history timestamps and tagged thread status from Codex
  while preserving the public string fields and legacy wire shapes.
- Read full MCP inventory responses up to the new 8 MiB default frame limit.
  Explicit limits still have a 2 MiB minimum. Oversized-frame errors report
  the configured limit.
- Expose `types.ErrCompactResultPending` for a second compaction with an
  outstanding result.
- Return errors matching `types.ErrThreadClosed` for thread operations on
  closed and uninitialized handles. Steering an idle thread now returns an
  error matching `ErrNoActiveTurn`.
- Handle typed-nil errors during error-chain traversal and thread history
  classification. Subscription parse gaps omit absent causes.
- Return the original launch failure on repeated transport connection calls.
- Quote generated hook commands for shim paths containing shell syntax.
- Keep explicit relative `WithCLIPath` values anchored to the caller when
  `WithCwd` launches the app-server from another directory.
- Retry transient `ETXTBSY` failures for short-lived CLI probes and publish
  their fixtures atomically; verify the live schema gate against an explicit
  `workspace-write` sandbox before it consumes quota.
- Bound the live schema sandbox preflight so an unavailable local sandbox
  reaches an actionable failure instead of blocking the release gate, including
  when a descendant retains its output pipes.
- Verify standalone `go.mod` and `go.sum` metadata in the local test target
  and CI so workspace state cannot mask an incomplete module checksum.
- Decode current file-change `kind` payloads while preserving the legacy
  `operation` projection for existing SDK consumers.

### Changed
- Canceling an owned run requests interruption of its exact provider turn.
  Admission stays reserved until matching completion. A follow-up reports
  `ErrThreadExecutionUnconfirmed` when the provider remains silent past the
  five-second reconciliation window. Compaction uses the same admission limit.
  A fresh, ordered idle resume can resolve an unchanged external-only fence.
- Approval callbacks run on a bounded worker while lifecycle events keep
  flowing. Overflow requests receive method-specific denials from a separate
  reply lane. Nested blocking operations and `Close` from the callback's
  Client context return `ErrApprovalCallbackReentry`.
- A disconnect before a matching terminal returns `ErrThreadClosed` from
  buffered `Thread.Run`.
- Buffered `Thread.Run` now reports measured whole-turn usage. Older SDK
  versions could return lifetime totals in that field. `UsageAvailable`
  distinguishes measured zero from unavailable usage, and
  `CacheWriteInputTokens` records writes separately from cached reads.
- Reorganized public client dispatch, hook bridge, thread ID helpers, event
  parser, and subprocess transport internals into smaller files for
  maintainability. This is intended as a backward-compatible source-layout
  change with no public API behavior change.

### Added
- `DiscoverRuntimeControls()` reads approval and sandbox values from the
  installed Codex CLI and intersects them with `configRequirements/read`.
- `FileChangePart.Kind` exposes the current app-server file-change
  discriminator, including optional `move_path` metadata.
- `Client.ReadConfigRequirements()` exposes provider-managed runtime
  constraints without hardcoding CLI-owned values.
- `ErrRuntimeControlsUnsupported` lets callers distinguish an older CLI that
  cannot advertise safe runtime controls from transient discovery failures.
- Workspace release-decision gate coverage. Public API and behavior changes now
  require changelog evidence or an explicit `no-release-needed` decision before
  the workspace release compatibility gate passes.

## [0.5.1] - 2026-06-19

### Fixed
- Patch release after the v0.5 line. See Git history for the exact tagged diff.

## [0.5.0] - 2026-04-21

### Added
- v0.5 SDK release line. See Git history for the exact tagged diff.

## [0.4.0] - 2026-04-19

### Added
- Slash-command-equivalent typed methods and local-helper parity APIs.

## [0.3.0] - 2026-04-18

### Added
- Programmatic Go hook callbacks through the managed hook shim bridge.

## [0.2.0] - 2026-04-18

### Added
- Hook observer events for Codex app-server hook notifications.

## [0.1.0] - 2026-04-18

### Added
- Initial preview SDK for `codex app-server`: typed threads, turns, streaming
  events, approvals, MCP configuration, and JSON-RPC stdio transport.
