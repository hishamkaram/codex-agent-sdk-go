---
name: sdk-test-writer
description: "Design or update SDK tests for public behavior, independent app-server protocol expectations, and deterministic failure paths."
---

# SDK test writer

- Trace the behavior from the public call to its owner using the [architecture map](../../../docs/architecture.md). Choose a test boundary that can observe the defect. Use a parser test for event shape, a framing/demux test for correlation, or the existing fake app-server flow tests for behavior that crosses the public client and transport.
- For a fix, demonstrate the failing behavior under the old implementation when feasible. Derive expected JSON independently from the app-server contract and compare responses by method, ID, and payload. If the outcome depends on ordering, observe frames or events at the affected boundary. A round trip through the same encoder and decoder can conceal a shared mistake.
- Give distinct cases to relevant failures such as cancellation, stream closure, malformed input, or server rejection. Use explicit rendezvous to control interleavings. Bound watchdogs separately from behavior assertions. Register cleanup as soon as a resource is acquired and join the goroutines and processes the test owns.
- Parallelize isolated cases. Keep tests that mutate process-wide environment or shared configuration serial. Preserve existing leak checks.

Run the smallest affected tests that actually exercise the behavior. Add race detection for concurrent paths. Keep authenticated checks separate and use the [contributor guide](../../../CONTRIBUTING.md) for the complete gate. Report what ran, any skips, and which behavior remains unverified.
