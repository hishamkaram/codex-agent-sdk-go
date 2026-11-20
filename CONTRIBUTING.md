# Contributing

Open an issue describing the problem and how to reproduce it, or submit a
focused pull request with the reason for the change. Use a disposable project
for examples. Read [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Development

Clone this repository independently. Start with [AGENTS.md](AGENTS.md) for
engineering guidance and the [ownership map](docs/architecture.md) for code
navigation. Use the development compiler named by the `toolchain` directive in
[go.mod](go.mod). Its `go` directive is the SDK's consumer minimum.

For verification, install GNU Make, Python 3.11 or later, and the official
[golangci-lint release](https://github.com/golangci/golangci-lint/releases)
named in [.golangci-lint-version](.golangci-lint-version). Use a linter binary
built for at least the development compiler's Go major and minor version. Go
builds and runs the pinned vulnerability scanner through the [Makefile](Makefile). Module downloads and the vulnerability
database may require network access.

Run `make verify` locally. The [CI workflow](.github/workflows/test.yml) uses
the same gate. It checks standalone module metadata, vet, lint and formatting,
short race tests with coverage, build, vulnerabilities, and harness consistency.
Make runs Go commands with `GOWORK=off`. Set `GOWORK=off` for direct Go commands.
For fast feedback, use `make test` or the focused commands in the
[ownership map](docs/architecture.md). Use `make verify-harness` to check
guidance, skill metadata and routes, role configuration, and documented targets.

Changes to provider contracts may also need `make check-schema-drift` with the
intended Codex CLI installed. Use `make test-integration` for relevant live CLI
coverage, or `make test-codex-livecli` for repeated live schema integration.
Live tests need authentication and can consume paid usage. Record the exact
CLI version and which checks ran, skipped, or were blocked. See the
[wire protocol guide](docs/wire-protocol.md) and [runtime prerequisites](README.md#prerequisites).

## Pull requests

Use the [PR template](.github/pull_request_template.md). Explain the behavior
and compatibility impact, add a regression test for a bug, and record the
commands and results. Keep unrelated formatting and generated artifacts out
of the diff. Check that affected links work from a standalone clone.

Keep secrets, pairing material, private deployment details, local
configuration, and unreviewed logs or screenshots out of the repository.
Use placeholders in examples.

Contributions are distributed under this repository's [license](LICENSE).
