# Changelog

All notable changes to the Codex Agent SDK for Go are documented in this file.

## [Unreleased]

- Expose the CLI-resolved thread model through `Thread.Model()`, including
  account defaults selected when `thread/start` omits a model override.
- Preserve provider terminal failure text on `TurnCompleted.Error`.

### Fixed
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
