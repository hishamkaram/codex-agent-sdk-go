# Slash-command-equivalent typed methods (v0.4.0)

Codex's TUI slash commands (e.g., `/compact`, `/model`, `/review`,
`/status`) are all **client-side text parsing** — there is no
`slashCommand/execute` JSON-RPC method. The operations they trigger
ARE exposed as individual app-server methods though. v0.4.0 maps
every wireable slash command to a typed Go method so callers never
need to construct raw JSON-RPC frames.

This doc is the map. Every row is tested live against codex 0.121.0
— see `tests/coverage_matrix.md` for the one-row-per-test breakdown.

## Mid-session controls

### `/compact` → `Thread.Compact` / `Thread.Summarize`

```go
result, err := thread.Compact(ctx, nil)  // hybrid sync/async
if err != nil { return err }

// Fire-and-forget: just ignore result — the notification is teed
// to the normal event stream anyway.

// OR block for completion (codex 0.121.0 quirk: see caveat):
ev, err := result.Wait(ctx)
```

`Thread.Summarize(ctx)` is a discoverable alias for
`Thread.Compact(ctx, nil)`.

**Upstream caveat (codex 0.121.0, verified 2026-04-19):** the RPC
ACK is reliable, but the `thread/compacted` notification that Wait
depends on is NOT reliably emitted on populated threads. After a
Wait timeout the subprocess transport may enter an unresponsive
state ("write: file already closed"). Prefer fire-and-forget OR
short-timeout Wait with "might have worked" semantics.

### `/model` → `Client.SetModel` (+ `ListModels` for discovery)

```go
models, _ := client.ListModels(ctx)
for _, m := range models {
    if m.IsDefault { fmt.Println("default:", m.ID) }
}
_ = client.SetModel(ctx, "gpt-5.4")
```

### `/permissions` → `Client.SetApprovalPolicy`

```go
_ = client.SetApprovalPolicy(ctx, types.ApprovalOnRequest)
```

### `/fast` / sandbox toggles → `Client.SetSandbox`

```go
_ = client.SetSandbox(ctx, types.SandboxReadOnly)
```

### `/experimental` → `Client.SetExperimentalFeature(s)` (+ `ListExperimentalFeatures`)

```go
feats, _ := client.ListExperimentalFeatures(ctx)
for _, f := range feats {
    fmt.Printf("%s: enabled=%v stage=%s\n", f.Name, f.Enabled, f.Stage)
}

// Single feature:
_ = client.SetExperimentalFeature(ctx, "tool_search", true)

// Bulk:
_ = client.SetExperimentalFeatures(ctx, map[string]bool{
    "tool_search":  true,
    "tool_suggest": true,
})
```

**Note (codex 0.121.0):** only 5 features are runtime-toggleable via
this RPC — `apps`, `plugins`, `tool_search`, `tool_suggest`,
`tool_call_mcp_elicitation`. Others (e.g., `shell_tool`) error with
`unsupported feature enablement` — edit `~/.codex/config.toml`
directly for those.

### `/review` → `Thread.StartReview`

```go
result, err := thread.StartReview(ctx, types.ReviewOptions{
    Target:   types.ReviewTargetUncommittedChanges(),
    Delivery: types.ReviewDetached, // returns a new thread id
})
if err != nil { return err }

// For detached reviews, ResumeThread on result.ReviewThreadID to
// observe streamed review events:
reviewThread, _ := client.ResumeThread(ctx, result.ReviewThreadID, nil)
```

ReviewTarget variants:
- `ReviewTargetUncommittedChanges()` — staged + unstaged + untracked
- `ReviewTargetBaseBranch("main")` — diff current branch vs base
- `ReviewTargetCommit("abc123", "optional-label")` — one commit
- `ReviewTargetCustom("arbitrary reviewer instructions")` — free-form

## Inventory / discovery

| Slash command | Typed method | Notes |
|---|---|---|
| `/status` — account | `Client.ReadAccount()` | Returns auth type, email (chatgpt), plan tier |
| `/status` — usage | `Client.ReadRateLimits()` | Dual shape: legacy + `rateLimitsByLimitId` map |
| `/status` — auth | `Client.GetAuthStatus()` | **SECURITY:** AuthToken is a live JWT — don't log |
| `/mcp` | `Client.ListMCPServerStatus()` | Returns `{data, nextCursor}`; tools is a map keyed by name |
| `/apps` | `Client.ListApps()` | **Caveat:** often 403s on ChatGPT auth (upstream Cloudflare) |
| `/plugins` | `Client.ListSkills()` | Grouped by cwd; iterate `[]SkillsCwdGroup` |
| `/debug-config` | `Client.ReadConfig()` | Wrapped under `config.*`; use `Config.Raw` map for fields the SDK doesn't curate |

## Thread lifecycle

| Slash command | Typed method | Notes |
|---|---|---|
| `/new` | `Client.StartThread()` | Already available pre-v0.4.0 |
| `/resume` | `Client.ResumeThread()` | Already available; Thread.Cwd() now returns the start-time cwd |
| `/fork` | `Client.ForkThread()` | Already available |
| `/stop` (background terminals) | `Thread.CleanBackgroundTerminals()` | **Requires** `WithExperimentalAPI(true)` at NewClient |
| — (rollback) | `Thread.Rollback(ctx, numTurns)` | Drops last N turns; does NOT revert file changes |
| — (rename) | `Thread.SetName(ctx, name)` | Emits `thread/name/updated` notification |
| — (steer) | `Thread.Steer(ctx, text)` | Appends to in-flight turn; errors with "no active turn" otherwise |

## Destructive operations

| Slash command | Typed method | Guard |
|---|---|---|
| `/logout` | `Client.Logout(ctx)` | **Invalidates ~/.codex/auth.json.** Call `Close()` after Logout. See `CODEX_SDK_LOGOUT_OK=1` test gating. |
| `/feedback` | `Client.UploadFeedback(ctx, report)` | Sends data to OpenAI servers. `IncludeLogs=true` bundles transcript logs; SDK logs a WARN in that case. |

## Local helpers (no wire method)

These mirror slash commands whose behavior is entirely client-side
(`git diff`, scaffold files) — codex exposes no JSON-RPC equivalent.

### `/diff` → `Thread.GitDiff` (local) or `Client.GitDiffToRemote` (wire)

Two distinct operations:

| Helper | Backing | Diff against |
|---|---|---|
| `Thread.GitDiff(ctx, opts)` | `os/exec git -C <cwd>` | Working tree (staged/unstaged/HEAD, configurable) |
| `Client.GitDiffToRemote(ctx, cwd)` | `gitDiffToRemote` wire method | Remote tracking branch |

```go
// Local working-tree diff (matches TUI /diff default):
result, _ := thread.GitDiff(ctx, &types.GitDiffOptions{
    Staged:     false,  // unstaged by default
    MaxBytes:   1 << 20, // 1 MiB cap
})
fmt.Println(result.Stdout)

// Remote tracking branch diff (what changed since last push):
remote, _ := client.GitDiffToRemote(ctx, "/abs/path/to/repo")
fmt.Println("HEAD:", remote.Sha)
fmt.Println("vs remote:", remote.Diff)
```

### `/init` → `Client.InitAgentsMD`

```go
_ = client.InitAgentsMD(ctx, "/abs/path/to/repo", nil)
// Refuses to overwrite existing AGENTS.md; pass &InitAgentsMDOptions{Overwrite: true} to force.
// Pass Template: "..." to use custom content.
```

## TUI-only slash commands (no SDK equivalent)

These are interactive-TUI behaviors that have no programmatic
mapping. Callers compose them locally if needed.

| Slash | Why TUI-only |
|---|---|
| `/clear` | TUI screen reset — callers call `ArchiveThread` + `StartThread` |
| `/copy` | Clipboard (TUI-specific) |
| `/exit`, `/quit` | TUI process exit |
| `/mention` | File picker UI; equivalent is `RunOptions.Images` or including file path in prompt text |
| `/plan` | Narrative instruction; compose into prompt text |
| `/personality` | Config write of personality key; use `Client.WriteConfigValue` |
| `/ps` | TUI background-terminal list — not exposed over the wire |
| `/statusline`, `/title` | Config writes; use `Client.WriteConfigValue` |
| `/sandbox-add-read-dir` | Windows-sandbox config; use `Client.WriteConfigValue` |
| `/agent` | Multi-thread TUI switcher; SDK already exposes thread switching via StartThread |

## Design principle: TUI helpers

The SDK ships local-command helpers ONLY for slash commands codex
itself ships. `GitDiff` ↔ `/diff`, `InitAgentsMD` ↔ `/init`.
Anything else (e.g., `git branch`, `git stash`) goes into the
caller's own code — the SDK's scope stays tied to codex's surface.

## Error handling

Three new typed errors in v0.4.0:

```go
// Returned when an RPC requires a capability not negotiated at handshake.
// Example: thread/backgroundTerminals/clean requires WithExperimentalAPI(true).
types.IsFeatureNotEnabledError(err)

// Returned by inventory methods when an MCP server needs OAuth (rare).
types.IsMCPServerOAuthRequiredError(err)

// Returned by Client.InitAgentsMD when AGENTS.md already exists and
// Overwrite=false.
types.IsAGENTSMDExistsError(err)
```

All other errors flow through the existing `*types.RPCError` or
wrapped `fmt.Errorf` chain.

## Version & testing

Verified against codex **0.121.0** on **2026-04-19**. Coverage
matrix at [`tests/coverage_matrix.md`](../tests/coverage_matrix.md)
— every typed method × every option variant × every documented
error path has a live-CLI integration test OR a `TEST-DEFERRED-vX.Y`
annotation with rationale.

To re-verify against a newer codex release, run the schema probe:

```bash
CODEX_SDK_PROBE=1 go test -tags=integration -count=1 -p 1 \
  -run TestProbeV040Shapes . -timeout=180s
```

Captured fixtures land under `tests/fixtures/v040_probes/` (PII
redacted, committed to git).
