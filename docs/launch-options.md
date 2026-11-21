# Codex launch environment and experimental turn options

The SDK uses the same resolved executable, working directory, and child
environment for a client connection and its CLI probes. Runtime discovery
without a client uses the supplied options and does not start a hook bridge.

## Child environment

The default overlays the parent process environment. `WithEnv("KEY=value")`
adds or replaces a value. `WithEnv("KEY=")` removes a value from the overlay.
To start from an allowlist, use `WithEnvReplacement`:

```go
opts := types.NewCodexOptions().
    WithCLIPath("/path/to/codex").
    WithEnvReplacement("PATH=/path/to/required/tools", "CODEX_HOME=/path/to/codex-config")
```

Calling `WithEnvReplacement()` starts with an empty list. In replacement mode
`KEY=` passes an empty value, and later `WithEnv` calls append to the allowlist.
For duplicate keys the last value wins. Key matching is case-insensitive on
Windows, where Go supplies the parent `SYSTEMROOT` if the list omits it. An
explicit `SYSTEMROOT=` suppresses that addition.

Executable discovery still uses the SDK process's lookup rules. The child
receives the PATH and authentication settings supplied in the allowlist.
Provide entries required by your executable and launch environment. The SDK
copies no credentials. A connected hook bridge supplies its socket variable
and sets `CODEX_HOME` to the directory where it installed `hooks.json`. Isolated
mode uses a temporary directory. User-home mode pins the SDK user's default
Codex directory even when the child's `HOME` differs. An effective `CODEX_HOME`
pointing elsewhere in user-home mode is rejected before the SDK writes hook
configuration.

## Experimental turn selection

`RunOptions.CyberAccessProgram` accepts the typed values
`CyberAccessProgramStandard`, `CyberAccessProgramDaybreakBlue`, and
`CyberAccessProgramDaybreakRed`. The app-server wire values are `standard`,
`daybreakBlue`, and `daybreakRed`. A nil field leaves program selection to
the provider.

Set `WithExperimentalAPI(true)` before connecting to request an explicit
selection. On the first explicit request the SDK checks the connected CLI's
experimental schema for the field and enum, then caches successful proof for
that connection. This check currently requires a directly launched native
binary on Linux, where the SDK can verify and open the running process image
through `/proc`. Use the native binary as `CLIPath`. The SDK rejects an explicit
selection when an interpreter or delegating launcher leaves a different
image running, or when the host cannot prove runtime identity. The selection applies only to that turn. The provider still
decides account and model eligibility, and provider rejection is returned to
the caller.
