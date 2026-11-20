---
name: sdk-implementer
description: "Implement a bounded Go SDK change while preserving its public API, wire behavior, and state ownership. Use for SDK features, bug fixes, and refactors."
---

# SDK implementer

1. Read [project guidance](../../../AGENTS.md) and use the [architecture map](../../../docs/architecture.md) to locate the existing owner. Trace the public caller, its tests, and failure paths. Before changing state or a boundary, name the owner, writers, readers, and invariant. Keep the change with the existing owner unless current dependencies support an extraction.
2. Preserve public signatures, zero values, error identity, and caller-owned data unless the requested change revises that contract. Keep thread options scoped to the intended request. Use the existing path for equivalent decisions. Avoid a second state owner or a helper that needs the entire client.
3. Make and validate the smallest implementation through the public entry point. For behavior changes, use [SDK test guidance](../sdk-test-writer/SKILL.md). For lifetime changes, use [SDK concurrency guidance](../sdk-concurrency/SKILL.md).

## Protocol changes

Use the [wire guide](../../../docs/wire-protocol.md) to locate the method, then check the relevant vendored schema and the actual supported CLI contract. Trace the request or event through framing, routing, and its public typed result. Preserve line framing and the single writer, keep request IDs paired with their replies, and validate reply payloads against the specific server method. Keep unknown/raw compatibility where the public surface exposes it. Use independent expected frames so a fixture cannot repeat an SDK encoding mistake.

## Verification

Run focused checks during edits. Use the [contributor guide](../../../CONTRIBUTING.md) for the complete gate and optional Codex schema or live checks. Check the CLI version and prerequisites before an authorized live run. Report the observed CLI behavior separately from fixture or build results. Hand off the actual changes, commands, outcomes, and docs impact to the integrator.
