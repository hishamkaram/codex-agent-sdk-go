# Reporting Security Issues

Please do not report vulnerabilities, credentials, or private session content
in a public issue. Email the maintainer at
[hesham.karam@gmail.com](mailto:hesham.karam@gmail.com) with the subject
"codex-agent-sdk-go security report".

Include the affected version or commit, connection mode, impact, and minimal
reproduction steps using a disposable project. Do not send live credentials,
pairing QR codes, private keys, or customer data. Coordinate any sensitive
reproduction material privately before sending it.

Security fixes target the current development line. There is no published
long-term-support or response-time guarantee. Review release notes and test
compatible component versions before upgrading.

The SDK starts a local Codex process with the caller's authority. Review its
sandbox and approval settings; do not expose it directly to untrusted callers.
