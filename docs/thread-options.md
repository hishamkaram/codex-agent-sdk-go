# Thread configuration

Set thread options on `StartThread` or `ForkThread` to configure one conversation. An explicit option overrides the corresponding SDK default. If both are empty, the app-server resolves the setting. The SDK sends these options in the thread request and leaves the shared Codex configuration file alone.

## Web search and reasoning

Use `WithWebSearchMode` and `WithReasoningEffort` for client defaults. Both `ThreadOptions` and `ResumeOptions` can override them for one request.

- `WebSearchDisabled` explicitly disables web search. `WebSearchCached` uses cached results. `WebSearchLive` enables live web search. `WebSearchIndexed` is an app-server mode. Support varies by installed CLI and policy.
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

`AdditionalDirectories` lists writable roots alongside the thread's working directory. Use absolute native paths without `..` components. Equivalent spellings that differ only by separators or current-directory components are deduplicated without resolving symlinks. A nil list inherits the server's roots. An explicit empty list asks for zero additional roots.

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
