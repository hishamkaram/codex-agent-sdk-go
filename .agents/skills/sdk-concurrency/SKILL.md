---
name: sdk-concurrency
description: "Change or review SDK goroutines, locks, contexts, streams, or subprocess shutdown with explicit lifetime ownership."
---

# SDK concurrency

Read the [architecture map and lifetime contracts](../../../docs/architecture.md). Trace the caller, the live resource, and every normal and error-path owner. State who starts, cancels, closes, waits, and publishes terminal state. Distinguish the connection attempt, the shared client and dispatcher, and a thread or turn.

- Keep a single serialized writer for app-server stdin and use the existing demux to correlate request IDs. Ensure shutdown can unblock a pending write. Avoid holding locks across waits or callbacks if the release path needs the same lock.
- Preserve per-thread turn serialization and turn identity. Close or drain a local consumer through its owner. Where a provider interruption is required, trace the specific thread and turn through the app-server request and check what the server acknowledged. Local context cancellation, stream closure, and provider termination are distinct observations.
- Preserve bounded delivery and each subscriber's own lifetime. When changing overflow or shutdown behavior, check the active stream, subscription, or callback consumer affected by it.
- Keep process waiting and pipe cleanup with the existing process owner. Preserve error identity and make progress under an unresponsive peer, a blocked consumer, or a canceled caller. Use the existing cancellation or shutdown path before adding another timeout or goroutine.

Prove the changed interleaving with a rendezvous at the affected boundary and a finite test watchdog. Check representative normal, canceled, and closed paths under the race detector. Preserve leak checks and verify native process behavior when a claim depends on it. See [SDK test guidance](../sdk-test-writer/SKILL.md) for test selection.
