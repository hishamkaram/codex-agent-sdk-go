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

The callback runs on the Client's single dispatcher goroutine — **do
not block**. Never call `Run`, `RunStreamed`, or any other SDK method
from inside the callback; that will deadlock. Defer user-interaction
(e.g., prompting a human) to another goroutine and respond via a
channel, or return a decision based solely on the request data.

## Request taxonomy

Type-switch on `req`:

```go
switch r := req.(type) {
case *types.CommandExecutionApprovalRequest:
    // r.Command, r.Cwd, r.Reason
case *types.FileChangeApprovalRequest:
    // r.Path, r.Operation ("create"|"modify"|"delete"), r.Diff, r.Reason
case *types.PermissionsApprovalRequest:
    // r.Permission, r.Scope, r.Reason  (e.g., network egress)
case *types.ElicitationRequest:
    // r.ServerName, r.Prompt, r.Schema  (MCP server asking for user input)
case *types.UnknownApprovalRequest:
    // r.Method, r.Params  (server introduced a method the SDK doesn't yet type)
}
```

## Decision taxonomy

Return exactly one of:

```go
types.ApprovalAccept{}               // proceed with the action
types.ApprovalAcceptForSession{}     // proceed + remember for this session
types.ApprovalDeny{Reason: "..."}    // refuse this action; turn continues
types.ApprovalCancel{Reason: "..."}  // abort the entire turn
```

Unknown decision types are treated as `Deny` by the encoder — safer
than accept.

## Approval policies

`CodexOptions.WithApprovalPolicy` sets when the server prompts:

Accepted values are fixed by the codex server (as of CLI 0.121.0 it
rejects anything outside this set with a JSON-RPC "unknown variant"
error at `thread/start`):

| Policy | Behavior |
|---|---|
| `ApprovalUntrusted` | Known-safe reads auto-approved. Every state-mutating command prompts. Strictest practical policy. |
| `ApprovalOnFailure` | Agent runs its plan optimistically; prompts only after a command fails. |
| `ApprovalOnRequest` (**default**) | Prompts for destructive or out-of-workspace operations; workspace-local reads auto-approved. |
| `ApprovalGranular` | Delegates per-action policy to a server-side ruleset (see codex config). |
| `ApprovalNever` | Nothing prompts. Combine with `SandboxReadOnly` or trust the agent completely. |

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

`ApprovalCancel` aborts the entire turn. The server typically emits a
`turn/completed{Status:"cancelled"}` notification shortly after. Your
running `Thread.Run` will return a `*Turn` with `Status == "cancelled"`.
Subsequent `Run` calls on the same thread are unaffected; the turn lock
releases normally on terminus.

## What the SDK does NOT do

- Prompt the user on your behalf. You own UX; the SDK only routes the
  request.
- Auto-approve based on heuristics. Explicit callback, explicit decision.
- Retry denied actions. If the agent retries, you'll see a new
  approval request and can decide again.
- Record decisions to disk. `ApprovalAcceptForSession` is a server-side
  concept — the SDK forwards it verbatim.
