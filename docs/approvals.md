# Approvals

The codex agent sandboxes side effects. When the model wants to do
something the sandbox/approval policy doesn't auto-approve, the server
sends an `item/*/requestApproval` server-initiated request. The SDK
dispatches it to your registered callback; your decision becomes the
response.

## The callback

```go
type ApprovalCallback func(ctx context.Context, req ApprovalRequest) ApprovalDecision
```

Register via `CodexOptions.WithApprovalCallback`. If no callback is
registered, the SDK uses `DefaultDenyApprovalCallback` — every request
is denied. This is a safer default than silent auto-approve; if your
use-case is "auto-approve everything", you must say so explicitly.

The callback runs on the client's single dispatcher goroutine. Keep it short
when possible. For human approval, send the request to an independent UI and
wait on a decision channel with a `select` that also handles `ctx.Done()`;
return a denial on cancellation. Other notifications are paused while the
callback waits. Do not call `Run`, `RunStreamed`, or `Close`, or wait for SDK events from
inside the callback: those need the same dispatcher to make progress.

## Request taxonomy

Type-switch on `req`:

```go
switch r := req.(type) {
case *types.CommandExecutionApprovalRequest:
    // r.Command, r.Cwd, r.Reason
case *types.FileChangeApprovalRequest:
    // r.Changes: every path, operation, diff, and optional rename destination
    // r.GrantRoot: optional proposed session-wide write root
case *types.PermissionsApprovalRequest:
    // r.Permissions: exact JSON grant requested by the provider
    // r.ThreadID, r.TurnID, r.ItemID, r.EnvironmentID, r.Reason
case *types.ElicitationRequest:
    // r.Mode, r.ServerName, r.ThreadID, r.TurnID, r.EnvironmentID
    // form: r.Message and r.RequestedSchema
    // URL: r.Message, r.URL, and r.ElicitationID
case *types.ToolRequestUserInputRequest:
    // r.ItemID, r.ThreadID, r.TurnID, r.Questions  (Codex request_user_input)
case *types.UnknownApprovalRequest:
    // r.Method, r.Params  (server introduced a method the SDK doesn't yet type)
}
```

For file approvals, inspect every entry in `Changes`. The SDK resolves the
active file-change item by thread, turn, and item ID, including the latest
patch-update event, before calling you.
Missing or invalid context is denied without invoking the callback. For a
single-file request, `Path`, `Operation`, and `Diff` remain populated for older
callers; they cannot describe a multi-file request.

If `GrantRoot` is non-nil, also review that proposed write root for the remainder
of the session. A root-only request can have no file changes. The upstream field
is unstable and whether the CLI honors it depends on the CLI implementation;
do not treat it as a guarantee that access was granted. Invalid cached patch
context is still denied even when a root is supplied.

## Decision taxonomy

Return exactly one of:

```go
types.ApprovalAccept{}               // proceed with the action
types.ApprovalAcceptForSession{}     // proceed + remember for this session
types.ApprovalDeny{Reason: "..."}    // refuse this action; turn continues
types.ApprovalCancel{Reason: "..."}  // cancel a command, file, or MCP approval
types.PermissionsApprovalResponse{Scope: types.PermissionGrantTurn}
types.ElicitationResponse{Action: types.ElicitationDecline}
types.ToolRequestUserInputResponse{  // answer Codex request_user_input
    Answers: map[string]types.ToolRequestUserInputAnswer{
        "question_id": {Answers: []string{"Proceed"}},
    },
}
```

Use generic approve and cancel decisions for command and file requests.
Permission grants and MCP elicitation use their own response values.

### Permission grants

Inspect the requested profile in `PermissionsApprovalRequest.Permissions`.
After the caller approves a specific subset, return only that subset. This
example grants read access to one selected directory for the current turn.

```go
return types.PermissionsApprovalResponse{
    Scope: types.PermissionGrantTurn,
    Permissions: types.GrantedPermissionProfile{
        FileSystem: &types.GrantedFileSystemPermissions{
            Entries: []types.FileSystemPermissionEntry{{
                Path: types.FileSystemPermissionPath{Type: types.FileSystemPathLiteral, Path: "/work/approved"},
                Access: types.FileSystemPermissionRead,
            }},
        },
    },
}
```

Use absolute paths in the target environment's syntax. The provider gives
`Entries` precedence over the legacy `Read` and `Write` root lists, including
an explicitly empty entries list. For network, set
`GrantedNetworkPermissions.Enabled` explicitly. The provider decides the
effective access. Use `PermissionGrantTurn` to limit the grant to this turn
or `PermissionGrantSession` to let later turns in the same session use it.
`StrictAutoReview` may be enabled only for a turn-scoped grant. The provider
treats glob entries as read denials. [Codex 0.171 core permission rules](https://github.com/openai/codex/blob/rust-v0.171.0/codex-rs/protocol/src/permissions.rs#L441-L450)

To deny the permission request, return an empty turn-scoped grant.

```go
return types.ApprovalDeny{Reason: "permission not approved"}
```

Nil, invalid, mismatched, and canceled decisions also grant nothing.
Use turn interruption to stop the running turn.

### MCP elicitation

For a form, present the request's `Message` and `RequestedSchema` to your
caller. After validating their submitted form against that schema, return
structured JSON as the content. For a form with a `format` field, one
accepted response is:

```go
return types.ElicitationResponse{
    Action:  types.ElicitationAccept,
    Content: json.RawMessage(`{"format":"brief"}`),
}
```

Set `Meta` to send optional JSON under `_meta`. Supply `ElicitationDecline`
or `ElicitationCancel` to send an action without accepted content.
`ApprovalDeny` maps to decline and `ApprovalCancel` maps to cancel for this
method. The caller owns opening and completing an external URL flow.
Only return an accept decision when the caller approves the elicitation.

`TurnID` and `EnvironmentID` may be nil. The SDK retains unknown modes in
`RawParams` and declines an attempt to accept one. Current `serverName`,
`message`, and `requestedSchema` take precedence over the legacy
`server_name`, `prompt`, and `schema` fields. `Prompt` and `Schema` remain
compatibility projections. A complete legacy form can use the typed form
response. See the [wire protocol notes](wire-protocol.md#server-initiated-requests-approvals).

## Approval policies

`CodexOptions.WithApprovalPolicy` sets when the server prompts:

Accepted values are fixed by the Codex server and vary by CLI version. The SDK
returns a local `*types.UnsupportedApprovalPolicyError` for a known incompatible
peer instead of sending a request that the server will reject.

| Policy | Behavior |
|---|---|
| `ApprovalUntrusted` | Known-safe reads auto-approved. Every state-mutating command prompts. Strictest practical policy. |
| `ApprovalOnFailure` (**deprecated**) | Older peers only. Codex 0.143+ removed this policy; use `ApprovalOnRequest`. |
| `ApprovalOnRequest` (**default**) | Prompts for destructive or out-of-workspace operations; workspace-local reads auto-approved. |
| `ApprovalGranular` | Delegates per-action policy to a server-side ruleset (see codex config). |
| `ApprovalNever` | Nothing prompts. Combine with `SandboxReadOnly` or trust the agent completely. |

The public Go API remains string-typed. For ordinary policies the SDK
writes that string verbatim. For `ApprovalGranular`, the SDK writes the
structured Codex wire form on both `thread/start` and
`config/value/write`:

```json
{
  "granular": {
    "mcp_elicitations": true,
    "rules": true,
    "sandbox_approval": true
  }
}
```

`Client.ReadConfig()` also accepts Codex's object-form
`approval_policy`: `types.Config.ApprovalPolicy` is normalized to
`"granular"` while `types.Config.Raw["approval_policy"]` preserves the
original JSON payload.

## Sandbox modes

`CodexOptions.WithSandbox` is orthogonal to approval policy — it
controls the technical boundary, not the prompt cadence:

| Mode | Capability |
|---|---|
| `SandboxReadOnly` | Read files + run read-only commands; mutations blocked at OS level |
| `SandboxWorkspaceWrite` | Read + write inside cwd; network blocked; approvals per policy |
| `SandboxDangerFullAccess` | No sandbox. Equivalent to `--yolo`. Use with care. |

Combine them deliberately: `SandboxReadOnly + ApprovalNever` = read-only
agent with zero user interaction. `SandboxWorkspaceWrite +
ApprovalUntrusted` = the typical "assisted coding" setup.

## Worked example

See `examples/with_approvals/main.go`. The callback there auto-approves
an allowlist of read-only commands (`ls`, `cat`, `grep`, etc.) and
denies everything else with a reason. File-change requests are all
denied. Unknown approval methods route to
`*types.UnknownApprovalRequest` — the example's final `default` clause
denies them too.

## Cancellation semantics

`ApprovalCancel` on a command or file request asks the provider to abort the
turn. The server typically emits a
`turn/completed{Status:"cancelled"}` notification shortly after. Your
running `Thread.Run` will return a `*Turn` with `Status == "cancelled"`.
Subsequent `Run` calls on the same thread are unaffected; the turn lock
releases normally on terminus. This describes a callback decision sent to the
provider. Canceling the context of an active Go turn currently ends local
consumption without interrupting the provider. See
[#45](https://github.com/hishamkaram/codex-agent-sdk-go/issues/45).

## What the SDK does NOT do

- Prompt the user on your behalf. You own UX; the SDK only routes the
  request.
- Auto-approve based on heuristics. Explicit callback, explicit decision.
- Retry denied actions. If the agent retries, you'll see a new
  approval request and can decide again.
- Record decisions to disk. `ApprovalAcceptForSession` is a server-side
  concept — the SDK forwards it verbatim.
