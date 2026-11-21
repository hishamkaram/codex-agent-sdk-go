# Thread configuration

Set thread options on `StartThread` or `ForkThread` to configure one conversation. An explicit option overrides the corresponding SDK default. If both are empty, the app-server resolves the setting. The SDK sends these options in the thread request and leaves the shared Codex configuration file alone.

## Web search and reasoning

Use `WithWebSearchMode` and `WithReasoningEffort` for client defaults. Both `ThreadOptions` and `ResumeOptions` can supply a different value. Use a fresh app-server connection for a persisted thread when applying resume overrides. Codex 0.171.0 can rejoin an already loaded thread and keep its existing settings. If the resume reply reports a different reasoning effort, the SDK returns `*types.ThreadResumeSettingsMismatchError`. The reply has no web-search readback. For an SDK-specified reasoning effort, the SDK reads the thread's model before resuming and supplies that model in the request. If the peer cannot report a model, the SDK returns an error before resuming.

- `WebSearchDisabled` explicitly disables web search. `WebSearchCached` requests cached results. With `danger-full-access`, Codex 0.171.0 can prefer live search if the provider supports it and policy permits it. `WebSearchLive` enables live web search. `WebSearchIndexed` is an app-server mode. Support varies by installed CLI and policy.
- Before sending a thread request with an SDK-specified web-search mode, the SDK reads the provider's managed requirements. A disallowed mode returns `*types.WebSearchModeDisallowedError` before the thread request. If the requirements read fails, its error is returned. With no SDK mode, the SDK leaves web search to Codex.
- Web search is separate from the sandbox's command network access. A live search mode leaves command network policy in effect.
- For reasoning effort, use a value supported by the selected model. A nonempty `RunOptions.ReasoningEffort` is sent as `effort` for that turn and subsequent turns on the thread. Omit it to use the server's current setting. If a collaboration mode is supplied, its reasoning settings take precedence.

```go
defaults := types.NewCodexOptions().
    WithWebSearchMode(types.WebSearchCached).
    WithReasoningEffort("medium")

interactive := &types.ThreadOptions{
    ThreadSource:  "interactive",
    WebSearchMode: types.WebSearchLive,
}
scheduled := &types.ThreadOptions{
    ThreadSource:  "scheduled",
    WebSearchMode: types.WebSearchDisabled,
}
```

`ThreadSource` accepts a custom string for a newly created or forked conversation. The empty string defers to the server. A resume request preserves the conversation's source.

## Workspace writes and network access

Select `SandboxWorkspaceWrite` on the request or through the client default before supplying either workspace-write option. `NetworkAccessEnabled` set to `nil` inherits the server's network policy. A pointer to false restricts command networking, while a pointer to true requests network access within the workspace-write sandbox. Managed settings can restrict which requests the app-server accepts.

When requesting either workspace-write option, use an absolute or launch-relative working directory without `..` components. Home-relative paths such as `~/project` are rejected. Per-call relative directories are based on the app-server's launch directory. If the request inherits the client's `WithCwd` setting, it uses that launch directory directly.

`AdditionalDirectories` lists writable roots alongside the thread's working directory. Use absolute native paths without `..` components. The SDK preserves path spelling and deduplicates exact strings. A peer that changes one of those spellings may fail the effective-policy check. A nil list inherits the server's roots. An explicit empty list asks for zero additional roots.

```go
offline, online := false, true
restricted := &types.ThreadOptions{
    Cwd:                  "/work/project",
    Sandbox:              types.SandboxWorkspaceWrite,
    NetworkAccessEnabled: &offline,
    AdditionalDirectories: []string{
        "/work/project-review-worktree",
    },
}
networked := &types.ThreadOptions{
    Cwd:                  "/work/project",
    Sandbox:              types.SandboxWorkspaceWrite,
    NetworkAccessEnabled: &online,
}
```

For either option, the SDK checks the returned working directory and effective policy before exposing the new handle. If the app-server already created a thread and the reply cannot confirm the requested setting, use `errors.As` with `*types.ThreadPolicyMismatchError` to recover the created thread ID. The caller can inspect or archive it. The SDK leaves the saved workspace-write policy to the server on resume.

## MCP server overrides

`WithMCPServers` provides defaults for start and fork. A nonempty
`ThreadOptions.MCPServers` map supplies that request's server configuration.
Pass the Go values through these options so the SDK encodes the typed fields.
The structs retain their legacy JSON representation for compatibility. For
raw configuration JSON, use the Codex schema, including keys such as `env`
for stdio or `http_headers` for HTTP.

Use `ToolApprovalMode` with one of the MCP-specific values. For a legacy
`DefaultApprovalMode: types.ApprovalOnRequest` initializer, choose
`ToolApprovalMode: types.MCPToolApprovalPrompt` explicitly. Existing native MCP
approval spellings remain accepted in the deprecated field.

Set `BearerTokenEnvVar` to the **name** of the token variable available to the
Codex process. Replace uses of the deprecated raw `BearerToken` field with that
environment reference. For HTTP authentication flows, use the typed `Auth`
option. See the [MCP example](../examples/with_mcp/main.go).

## Approved-plan handoff

With experimental API enabled, call `PrepareApprovedPlan` on a **fresh
destination thread**. This can add the approved Markdown to the destination's
history. Pass the returned prompt unchanged to its first `Run` while you
have exclusive use of that destination. Use same-thread collaboration mode
for interactive planning within an existing thread.

An optional attachment path must be absolute on the app-server host. Codex
checks whether its executor can read the exact plan and chooses how to
prepare the prompt. The SDK requires schema proof from a directly launched
native Linux Codex executable.

Conflicting activity such as an active turn can cause
`ErrThreadExecutionUnconfirmed` before the SDK sends the preparation request.
A pre-write rejection sends no request for that attempt. The same error also
applies when earlier unconfirmed preparation fences the destination against
new preparation and SDK turn starts.

If a sent preparation or the first turn's startup fails, retain the original
thread and plan. Create a fresh destination if you decide to retry.
See the [approved-plan example](../examples/approved_plan/main.go).
