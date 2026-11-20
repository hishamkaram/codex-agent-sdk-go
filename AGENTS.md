# Working on the Codex Agent SDK for Go

This SDK exposes typed Go APIs over the Codex CLI app-server's stdio
transport. Use the [architecture and ownership map](docs/architecture.md)
to trace a change from the public API to its owner and focused tests.
[Contributing](CONTRIBUTING.md) covers setup and verification.

## Engineering rules

- Before changing shared state or an interface, identify its owner, writers,
  readers, and invariant. Keep mutable state with that owner. Pass values or
  narrow consumer-owned interfaces across package boundaries.
- Keep behavior separate from I/O. Preserve public import paths, supported wire
  contracts, error identity, and runtime capability gates. Prefer the smallest
  cohesive change. Add abstraction for a concrete boundary or demonstrated reuse.
- Validate inputs at boundaries. Wrap returned errors with context and `%w` so
  callers can use `errors.Is` and `errors.As`. Use injected zap loggers and redact
  sensitive payloads and environment values from diagnostics.
- Derive test assertions from observable behavior or independently specified
  protocol shapes. Synchronize concurrency tests on events, requests, or
  barriers. Use parallel tests only when their state is isolated. Give each
  goroutine, subprocess, and channel an owner and an observable cleanup path.
  Preserve existing goleak coverage for goroutine lifetimes.
- Use focused tests while editing and run `make verify` for completion. Run
  `make verify-harness` for guidance changes. Record exact commands and results
  and identify unverified checks. Keep user-level configuration out of the diff.

## Task guidance

| Task | Skill |
| --- | --- |
| Implement a bounded change, including protocol and live CLI checks | [sdk-implementer](.agents/skills/sdk-implementer/SKILL.md) |
| Design or revise a regression test | [sdk-test-writer](.agents/skills/sdk-test-writer/SKILL.md) |
| Change cancellation, dispatch, or resource lifetime | [sdk-concurrency](.agents/skills/sdk-concurrency/SKILL.md) |
| Independently review an integrated change | [sdk-reviewer](.agents/skills/sdk-reviewer/SKILL.md) |
| Reconcile guides, examples, and routing | [sdk-docs-updater](.agents/skills/sdk-docs-updater/SKILL.md) |

Codex discovers repository skills under `.agents/skills`. Claude Code loads
this entry point through [CLAUDE.md](CLAUDE.md). Follow the selected skill link
to read its `SKILL.md` in Claude Code. Only load guidance relevant to the task.

For Codex subagents, use the [researcher](.codex/agents/researcher.toml),
[implementer](.codex/agents/implementer.toml), and
[reviewer](.codex/agents/reviewer.toml) profiles in a trusted project.
The parent session supplies sandbox permissions. Use a read-only parent
for research and review that must be read-only.

## Coordination

- Start with source. Use an optional code graph or documentation tool when
  available, then confirm the relevant paths in the checkout. Fall back to
  source search when the tool cannot answer.
- Give each concurrent change one writer and an explicit dependency order.
  Integrate prerequisites before validating dependent work. The lead owns
  integration and checks the combined change.
- Review the integrated change independently. Reconcile affected ownership,
  docs, examples, and skill routing. Record this impact and the evidence in the
  [PR template](.github/pull_request_template.md).
- Use Conventional Commit subjects such as `fix(session): preserve turn identity`.
  Omit AI authorship trailers.
