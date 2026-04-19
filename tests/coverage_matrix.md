# v0.4.0 Coverage Matrix

One row per (method × variant/error-path) per
`feedback_integration_tests_complete.md` — every code path in the
v0.4.0 surface has a live-CLI integration test or an explicit
`TEST-DEFERRED-vX.Y` annotation with rationale.

Verified against **codex 0.121.0** on **2026-04-19**.

## Legend

| Symbol | Meaning |
|---|---|
| ✅ | Test exists, passes 5-consecutive stress runs |
| 🔒 | Gated on `CODEX_SDK_RUN_TURNS=1` (quota) |
| 🔐 | Gated on destructive env (e.g., `CODEX_SDK_LOGOUT_OK=1`) |
| 🟡 | Test exists but documents an upstream quirk (skip path) |
| ⏭ | `TEST-DEFERRED-vX.Y` with rationale |

## Read-only client methods (Batch 2)

| Method | Variant / Path | Test | Status |
|---|---|---|---|
| `ReadConfig` | happy path | `TestIntCmd_ReadConfig_Happy` | ✅ |
| `ReadConfig` | closed client | `TestIntCmd_ReadConfig_ClosedClient` | ✅ |
| `ReadConfig` | concurrent with other op | `TestIntCmd_ReadConfig_ConcurrentDuringTurn` | ✅ |
| `ListModels` | happy path | `TestIntCmd_ListModels_Happy` | ✅ |
| `ListModels` | closed client | `TestIntCmd_ListModels_ClosedClient` | ✅ |
| `ListModels` | 8-goroutine race | `TestIntCmd_ListModels_RaceUnderConcurrency` | ✅ |
| `ListExperimentalFeatures` | happy path | `TestIntCmd_ListExperimentalFeatures_Happy` | ✅ |
| `ListExperimentalFeatures` | closed client | `TestIntCmd_ListExperimentalFeatures_ClosedClient` | ✅ |
| `ListMCPServerStatus` | happy path | `TestIntCmd_ListMCPServerStatus_Happy` | ✅ |
| `ListMCPServerStatus` | closed client | `TestIntCmd_ListMCPServerStatus_ClosedClient` | ✅ |
| `ListMCPServerStatus` | OAuth-pending server | — | ⏭ TEST-DEFERRED-v0.4.1: requires contriving an OAuth-requiring MCP server fixture |
| `ListApps` | happy or 403 auth error | `TestIntCmd_ListApps_HappyOrAuthError` | ✅ |
| `ListApps` | closed client | `TestIntCmd_ListApps_ClosedClient` | ✅ |
| `ListSkills` | happy path | `TestIntCmd_ListSkills_Happy` | ✅ |
| `ListSkills` | closed client | `TestIntCmd_ListSkills_ClosedClient` | ✅ |
| `ReadAccount` | happy path | `TestIntCmd_ReadAccount_Happy` | ✅ |
| `ReadAccount` | closed client | `TestIntCmd_ReadAccount_ClosedClient` | ✅ |
| `ReadRateLimits` | happy path | `TestIntCmd_ReadRateLimits_Happy` | ✅ |
| `ReadRateLimits` | closed client | `TestIntCmd_ReadRateLimits_ClosedClient` | ✅ |
| `GetAuthStatus` | happy path | `TestIntCmd_GetAuthStatus_Happy` | ✅ |
| `GetAuthStatus` | closed client | `TestIntCmd_GetAuthStatus_ClosedClient` | ✅ |

## Mutating client methods (Batch 3)

| Method | Variant / Path | Test | Status |
|---|---|---|---|
| `WriteConfigValue` | round-trip (model) | `TestIntCmd_WriteConfigValue_RoundTrip` | ✅ |
| `WriteConfigValue` | unknown key | `TestIntCmd_WriteConfigValue_UnknownKey` | ✅ |
| `WriteConfigValue` | empty keyPath | `TestIntCmd_WriteConfigValue_EmptyKeyPath` | ✅ |
| `SetModel` | round-trip | `TestIntCmd_SetModel_RoundTrip` | ✅ |
| `SetApprovalPolicy` | round-trip | `TestIntCmd_SetApprovalPolicy_RoundTrip` | ✅ |
| `SetSandbox` | round-trip | `TestIntCmd_SetSandbox_RoundTrip` | ✅ |
| `WriteConfigBatch` | 2-key happy | `TestIntCmd_WriteConfigBatch_Happy` | ✅ |
| `WriteConfigBatch` | empty edits | `TestIntCmd_WriteConfigBatch_EmptyEdits` | ✅ |
| `SetExperimentalFeature` | toggle on/off | `TestIntCmd_SetExperimentalFeature_ToggleOnOff` | ✅ |
| `SetExperimentalFeature` | unsupported feature | `TestIntCmd_SetExperimentalFeature_UnsupportedFeature` | ✅ |
| `SetExperimentalFeature` | empty name | `TestIntCmd_SetExperimentalFeature_EmptyName` | ✅ |
| `SetExperimentalFeatures` | bulk toggle + restore | `TestIntCmd_SetExperimentalFeatures_Bulk` | ✅ |
| `SetExperimentalFeatures` | empty map (no-op probe) | `TestIntCmd_SetExperimentalFeatures_EmptyMap` | ✅ |
| `UploadFeedback` | minimal | `TestIntCmd_UploadFeedback_Minimal` | 🔐 gated `CODEX_SDK_FEEDBACK_OK=1` |
| `UploadFeedback` | empty Classification | `TestIntCmd_UploadFeedback_EmptyClassification` | ✅ |
| `Logout` | happy + post-auth check | `TestIntCmd_Logout_Behavior` | 🔐 gated `CODEX_SDK_LOGOUT_OK=1` |
| `Thread.Rollback` | numTurns=0 (input validation) | `TestIntCmd_ThreadRollback_NumTurnsZero` | ✅ |
| `Thread.Rollback` | larger-than-history | `TestIntCmd_ThreadRollback_LargerThanHistory` | ✅ |
| `Thread.SetName` | happy path | `TestIntCmd_ThreadSetName_Happy` | ✅ |
| `Thread.SetName` | empty name | `TestIntCmd_ThreadSetName_EmptyName` | ✅ |
| `Thread.Steer` | no active turn | `TestIntCmd_ThreadSteer_NoActiveTurn` | ✅ |
| `Thread.Steer` | happy with active turn | — | ⏭ TEST-DEFERRED-v0.4.1: requires streaming turn scaffolding beyond the simple `thread.Run` path |
| `Thread.CleanBackgroundTerminals` | feature not enabled | `TestIntCmd_CleanBackgroundTerminals_FeatureNotEnabled` | ✅ |
| `Thread.CleanBackgroundTerminals` | happy with capability | `TestIntCmd_CleanBackgroundTerminals_HappyWithCapability` | ✅ |

## Async pair — Compact + Review (Batch 4)

| Method | Variant / Path | Test | Status |
|---|---|---|---|
| `Thread.Compact` | empty-thread ack | `TestIntCmd_Compact_EmptyThreadAckSucceeds` | ✅ |
| `Thread.Compact` | in-flight second call rejected | `TestIntCmd_Compact_InFlightSecondCallRejected` | ✅ |
| `CompactResult.Wait` | ctx-cancel + re-enter | `TestIntCmd_CompactResult_WaitCtxCancelAndReEnter` | ✅ |
| `Thread.Summarize` | alias for Compact | `TestIntCmd_Summarize_IsAliasForCompact` | ✅ |
| `Thread.Compact` | populated thread, Wait success | — | 🟡 codex 0.121.0 does NOT reliably emit `thread/compacted` — see `TestIntInteract_CompactThenRun` for graceful-skip test |
| `Thread.StartReview` | target required | `TestIntCmd_StartReview_TargetRequired` | ✅ |
| `Thread.StartReview` | commit target (invalid SHA) | `TestIntCmd_StartReview_CommitTarget` | ✅ |
| `Thread.StartReview` | uncommittedChanges detached | `TestIntCmd_StartReview_UncommittedChanges_Detached` | 🔒 |

## TUI parity helpers (Batch 5)

| Method | Variant / Path | Test | Status |
|---|---|---|---|
| `Thread.GitDiff` | clean repo | `TestThread_GitDiff_CleanRepoIsEmpty` | ✅ (unit) |
| `Thread.GitDiff` | unstaged change | `TestThread_GitDiff_UnstagedChange` | ✅ (unit) |
| `Thread.GitDiff` | staged only | `TestThread_GitDiff_StagedOnly` | ✅ (unit) |
| `Thread.GitDiff` | IncludeAll (HEAD) | `TestThread_GitDiff_IncludeAll` | ✅ (unit) |
| `Thread.GitDiff` | StatusOnly (porcelain) | `TestThread_GitDiff_StatusOnly` | ✅ (unit) |
| `Thread.GitDiff` | MaxBytes truncation | `TestThread_GitDiff_MaxBytesTruncation` | ✅ (unit) |
| `Thread.GitDiff` | non-git cwd | `TestThread_GitDiff_NonGitCwd` | ✅ (unit) |
| `Thread.GitDiff` | empty cwd | `TestThread_GitDiff_EmptyCwdErrors` | ✅ (unit) |
| `Thread.GitDiff` | closed thread | `TestThread_GitDiff_ClosedThreadErrors` | ✅ (unit) |
| `Client.GitDiffToRemote` | empty cwd | `TestIntCmd_GitDiffToRemote_EmptyCwd` | ✅ |
| `Client.GitDiffToRemote` | non-git dir | `TestIntCmd_GitDiffToRemote_NonGitDir` | ✅ |
| `Client.GitDiffToRemote` | real repo | `TestIntCmd_GitDiffToRemote_RealRepo` | ✅ |
| `Client.InitAgentsMD` | fresh dir | `TestInitAgentsMD_FreshDir` | ✅ (unit) |
| `Client.InitAgentsMD` | refuses existing | `TestInitAgentsMD_RefusesExistingDefault` | ✅ (unit) |
| `Client.InitAgentsMD` | Overwrite=true | `TestInitAgentsMD_OverwriteTrue` | ✅ (unit) |
| `Client.InitAgentsMD` | custom template | `TestInitAgentsMD_CustomTemplate` | ✅ (unit) |
| `Client.InitAgentsMD` | nonexistent dir | `TestInitAgentsMD_NonExistentDir` | ✅ (unit) |
| `Client.InitAgentsMD` | file-not-dir | `TestInitAgentsMD_NotADirectory` | ✅ (unit) |
| `Client.InitAgentsMD` | empty dir | `TestInitAgentsMD_EmptyDirErrors` | ✅ (unit) |

## Cross-cutting interactions (Batch 6)

| Scenario | Test | Status |
|---|---|---|
| Concurrent reads across 4 RPCs × 4 goroutines | `TestIntInteract_ConcurrentReads_NoRace` | ✅ |
| WriteConfig during concurrent reads | `TestIntInteract_WriteConfig_DuringConcurrentReads` | ✅ |
| ReadConfig → Write → ReadConfig round-trip | `TestIntInteract_ConfigReadWriteRoundTrip` | ✅ |
| Rollback → Run new turn | `TestIntInteract_RollbackThenRun` | 🔒 |
| Compact → Run new turn | `TestIntInteract_CompactThenRun` | 🟡 codex 0.121.0 quirk — graceful skip |
| Interrupt with no active turn → subsequent op works | `TestIntInteract_InterruptNoActiveTurn` | ✅ |
| Batch write vs serial write equivalence | `TestIntInteract_BatchVsSerialEquivalence` | ✅ |
| Thread cwd stored from ThreadOptions | `TestIntInteract_ThreadCwd_GitDiffUsesIt` | ✅ |
| Thread cwd override via ResumeOptions | `TestIntInteract_ThreadCwd_ResumeCarriesCwd` | ✅ (skipped on empty thread) |
| WriteConfigValue(`hooks.*`) while HookCallback active | — | ⏭ TEST-DEFERRED-v0.4.1: runtime guard planned; see Risk R7 in plan |

## Summary

- **Total integration tests**: 71 (across 4 test files)
- **Total unit tests added for v0.4.0**: 47
- **Stress**: every batch passes 3-5 consecutive `-count=5` runs clean
- **Upstream quirks documented**: 3 (Compact notification, ListApps 403, Detached review needs rollout)
- **Deferred with rationale**: 3 (MCP OAuth probe, Steer happy-path, hooks.* guard)

## How to run

```bash
# No-quota subset (every read-only + most mutating methods, ~70 tests, ~60s)
go test -tags=integration -race -count=1 -p 1 ./tests/... -timeout=300s

# With quota (adds Compact, Steer, Review, Rollback+Run, ~5 more tests, ~500 tokens)
CODEX_SDK_RUN_TURNS=1 go test -tags=integration -race -count=1 -p 1 ./tests/... -timeout=600s

# Full stress (5-clean per stress-test-flake-fixes rule)
go test -tags=integration -race -count=5 -p 1 ./tests/... -timeout=900s

# Destructive (only when you want to validate Logout / Feedback wire)
CODEX_SDK_LOGOUT_OK=1 CODEX_SDK_FEEDBACK_OK=1 \
  go test -tags=integration -race -count=1 -p 1 ./tests/... -timeout=300s

# Wire-shape probe (regenerate fixtures; opt-in)
CODEX_SDK_PROBE=1 go test -tags=integration -count=1 -p 1 -run TestProbeV040Shapes . -timeout=180s
```
