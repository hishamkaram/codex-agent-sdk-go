# Changelog

All notable changes to the Codex Agent SDK for Go are documented in this file.

## [Unreleased]

### Added
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
