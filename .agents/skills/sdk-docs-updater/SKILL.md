---
name: sdk-docs-updater
description: "Reconcile SDK documentation, examples, or contributor guidance with the implemented contracts and observed validation."
---

# SDK docs updater

1. Use the [architecture map](../../../docs/architecture.md) to identify the authority for the changed contract. Check what the integrated source, tests, and public examples actually do. Verify CLI flags and wire claims against the intended version when the change depends on them.
2. Update affected guidance at its owner. Keep navigation and shared rules in [project guidance](../../../AGENTS.md), setup and gate usage in the [contributor guide](../../../CONTRIBUTING.md), package ownership in the architecture map, and wire details in the [wire guide](../../../docs/wire-protocol.md). Use links for the other entry points. Update public examples if their behavior or documented contract changes.
3. Distinguish observed runtime behavior, known gaps, and proposed behavior. State what a check exercised. Preserve existing useful caveats and supported versions. Keep private plans and local paths out of contributor guidance.
4. Verify local references and contributor commands with `make verify-harness`. Use the relevant executable checks from the contributor guide if an example or script changes. Report changed guidance, actual results, and any affected guidance that stays accurate.
