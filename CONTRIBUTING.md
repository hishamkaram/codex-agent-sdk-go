# Contributing

Open an issue describing the problem and how to reproduce it, or submit a
focused pull request with the reason for the change. Use a disposable project
for examples. Read [SECURITY.md](SECURITY.md) before reporting a vulnerability.

## Development

Clone this repository independently and follow [README.md](README.md).
Use the toolchain and dependency versions declared by the repository.
Run Go commands with `GOWORK=off` to verify the pinned module dependencies.

Run `go vet ./...`, `go build ./...`, `golangci-lint run ./...`, and
`go test -race -count=1 -p 4 -short ./...`.
The workflows under `.github/workflows/` define additional checks and coverage
thresholds. Real CLI tests require authentication and can consume paid usage;
passing mock or unit tests does not prove a live CLI workflow.

## Pull requests

- Explain the user-visible behavior and any compatibility implications.
- Add a regression test for a bug; update affected guides and examples.
- Include the commands you ran and their results.
- Keep unrelated formatting and generated artifacts out of the diff.
- Use conventional commit subjects such as `fix(session): describe the fix`.

Never commit secrets, pairing material, private deployment details, local
configuration, or unreviewed logs/screenshots. Use placeholders in examples.
Check that links work from a standalone clone.

Contributions are distributed under this repository's [license](LICENSE).
