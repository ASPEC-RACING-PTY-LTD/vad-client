# P0-08b SDK-WIRE: Go wire types, part B
Status: completed

## Goal
Go wire structs and enum constants in `sdk/wire` for api-v1 rev 1.1 §6, §6A, §6B, §7, §8, §9, §10, §11, §11A and §11B, plus round-trip and unknown-field tests.

## Changes (repo VaultDock, all created)
| File | Why |
|---|---|
| `sdk/wire/jobs.go` | §6 jobs, runs, issues |
| `sdk/wire/tasks.go` | §6 device tasks (all 11 kinds, typed payloads, decode helper), inventory |
| `sdk/wire/modules.go` | §6A modules |
| `sdk/wire/profiles.go` | §6B profiles, consent, blacklist |
| `sdk/wire/restore.go` | §7 restore |
| `sdk/wire/approvals.go` | §8 approvals |
| `sdk/wire/retention.go` | §9 retention, prune, GC, verify, replication, analytics |
| `sdk/wire/sync.go` | §10 sync folders, change feed, uploads preconditions, ops, versions, conflicts |
| `sdk/wire/readiness.go` | §11 readiness, backup-state, notifications |
| `sdk/wire/ai.go` | §11 AI |
| `sdk/wire/deployenv.go` | §11A deploy env |
| `sdk/wire/superuser.go` | §11B superuser |
| `sdk/wire/wire_b_test.go` | Tests |

`common.go` and P0-08a files were not touched.

## Interfaces exposed (type → contract section)
- **§6 jobs/runs/issues** (`jobs.go`): `Job`, `JobCreateRequest`, `JobUpdateRequest`, `Run`, `RunStartRequest`, `RunStartResponse`, `RunUpdateRequest`, `Issue`, `IssueReport`, `IssueBatchRequest`, `IssueSkipRequest`. Constants: `JobState*` (active, suspended_by_mode, suspended_by_profile), `JobKind*`, `JobMode*`, `JobUseVSS*`, `RunState*`, `IssueSeverity*`, `IssueClass*` (profile_unauthorised, module_unavailable, guard_blocked), `IssueBatchMax = 500`.
- **§6 device tasks/inventory** (`tasks.go`): `DeviceTask` (`Kind string` + `Payload json.RawMessage`), `DeviceTaskPollResponse`, `DeviceTaskCreateRequest`, `DeviceTaskCreated`, `DeviceTaskResultRequest`. Payloads: `TaskBackupNowPayload`, `TaskRestorePayload`, `TaskRetrySkippedPayload`, `TaskVerifyPayload`, `TaskPruneCompactPayload`, `TaskTestRestorePayload`, `TaskAutomationOpPayload`, `TaskKeySharePayload` (§5A, both escrow variants), `TaskRewrapPayload`, `TaskProfileConsentRequestPayload`, `TaskModeChangeRequestPayload`, plus `TaskSIDs` (a JSON array or `"all"`). Helpers: `func DecodeTaskPayload(kind string, payload json.RawMessage) (any, error)` (strict, returns a pointer to the typed payload), `func (DeviceTask) DecodePayload() (any, error)`, `var ErrUnknownTaskKind`. Constants: `TaskKind*` (11), `TaskState*`. Inventory: `InventoryApp`, `InventoryReport`, `InventorySource*`.
- **§6A** (`modules.go`): `ModuleStatus`, `ModuleReport` (PUT /device/modules), `ModuleDeviceState` (GET /devices/{id}/modules), `ModuleCatalogEntry` (catalog is a bare array), `ModuleModeChangeRequest`. Constants: `Module*` ids (core, sync, automation, profiles, vss, discovery, reinstall), `ModuleInstallModeSimple/Advanced`, `ModuleState*`, `ModuleDelivery*`.
- **§6B** (`profiles.go`): `Profile` (DeviceProfile), `ProfileConsentRecord`, `ProfileReportEntry`, `ProfileReport`, `ProfileSecurityEventRequest`, `ProfileConsentRequestCreate`, `ProfileClearRequest`. Constants: `ProfileState*`, `ProfileRequestScope*`, `ProfileSignal*` (also used as blacklist_reason), `ProfileEvidence*`.
- **§7** (`restore.go`): `RestorePlanRequest`, `RestoreMapping`, `RestorePathMapping`, `RestorePlan`, `RestorePlanItem`, `RestoreGuardItem`, `RestorePlanTotals`, `Restore`, `RestoreItem`. Constants: `RestoreConflict*`, `RestoreAction*` (incl. `data_only`), `RestoreState*`.
- **§8** (`approvals.go`): `Approval`, `ApprovalDecisionRequest`. Constants: `ApprovalKindEscrowChange`, `ApprovalKindTwoWayEnable`, `ApprovalState*`, `ApprovalRisk*`, `ApprovalRequester*`.
- **§9** (`retention.go`): `RetentionRules`, `RetentionPolicy`, `RetentionPolicyRequest`, `PrunePlanRequest`, `PruneSnapshot`, `PrunePlan`, `GCLocation`, `GCPlan`, `VerifyRequest`, `ReplicationPolicy`, `ReplicationPolicyRequest`, `AnalyticsPoint`, `AnalyticsJob`, `AnalyticsRepo`. Constants: `VerifyDepth*`, `ReplicationMode*`.
- **§10** (`sync.go`): `SyncFolder`, `SyncMassDeleteThreshold`, `SyncFolderCreateRequest`, `SyncFolderUpdateRequest`, `SyncChange`, `SyncChangesResponse`, `SyncUploadFields` (embed in the upload request), `ConflictDetails` (409 conflict details), `SyncOp`, `SyncOpsRequest`, `Version`, `VersionPinRequest`, `Conflict`, `ConflictResolveRequest`. Constants: `SyncDirection*`, `SyncConflict*` (policies), `SyncDelete*`, `SyncState*`, `SyncMassDeleteDefault{Files,Percent}`, `ChangeOp*`, `SyncMaxWaitSeconds`, `SyncUploadOnConflictKeepBoth`, `ConflictState*`, `ConflictChoice*`.
- **§11** (`readiness.go`, `ai.go`): `ReadinessCheck`, `ReadinessReport`, `ReadinessJobState`, `ReadinessBackupState`, `NotificationPref`, `NotificationPrefs`, `AISettings` (embeds `SecretState`), `AISettingsUpdate` (`Secret SecretUpdate` with `omitzero`), `AIDiagnoseRequest`, `AIRecommendation`, `AIDiagnoseResponse`. Constants: `ReadinessStatus*`, `ReadinessCheck{ProfileAuthorised,InstallMode,ModulesHealthy}`, `NotificationEvent{ProfileBlacklisted,ModuleFailed}`, `NotificationChannel*`, `AIScope*`.
- **§11A** (`deployenv.go`): `DeployEnvStatus`, `DeployEnvAdded`, `DeployEnvObsolete`, `DeployEnvRemovalApproval`, `DeployEnvRemovalRequest`, `func DeployEnvRemovalConfirmText(n int) string` ("REMOVE <n> VARIABLES").
- **§11B** (`superuser.go`): `SuperuserUpdateRequest`. Rotate-server reuses `OperationAccepted`.

## Commands run and results
- `gofmt -l wire`: no output.
- `go vet ./wire/`: clean.
- `go test ./wire/` (with P0-08a files present): ok.
- `go build ./...` in `sdk`: ok.

Tests: 30 representative payloads decoded strictly and round-tripped (JSON-equal); 7 unknown-field rejections; device-task payload decode for every variant, including both key_share forms and `sids` as `"all"` or an array, an unknown kind, an unknown payload field and a bad `sids` string; AI secret keep/clear/set; deploy-env confirm text.

## Contract issues
1. **Rev 1.0 text is missing.** §6, §7, §8, §9, §10.4 and §11 say "as rev 1.0", but no rev 1.0 contract exists in the repo or in git history (§14 says it was never implemented). For those parts I took field shapes from `planning/research/01-server-backend.md` §3.5/§3.7 and `03-web-app.md` §8. INT should confirm or amend the following, which are not spelled out in rev 1.1:
   - Job, Run, Issue and RetentionRules fields;
   - task poll and result shapes (`{"tasks":[…]}`; `{state,result,error}`);
   - payloads for backup_now, restore, retry_skipped, verify, prune_compact, test_restore and automation_op;
   - the restore plan and status shapes;
   - Approval fields;
   - the prune, GC, replication and analytics shapes;
   - the Version and Conflict rows;
   - readiness, backup-state and notification-prefs shapes;
   - the AI diagnose request and response.
2. Run status uses the field name `state` (consistent with operations and jobs). The research table used `status`.
3. `requested_by` in `profile_consent_request` and `mode_change_request` has no type in the contract. I used a string (display name).
4. The contract does not give `consent_id`'s type. I used a string.
5. The contract does not give the `PUT /device/profiles` body shape. I used `{profiles:[{sid,username,profile_path,state}], consents:[{consent_id,sid,consent_at,proof_digest}]}`.
6. Routes that create a task (`mode-request`, `profiles/requests`, `retry-skipped`) have no reply shape in the contract. I used `DeviceTaskCreated{task_id}`.
7. `GET /admin/deploy/env` lists `pending_removals:[…]` without an element type. I used the approvals-file entry `{name, approved_by, approved_at}`. `schema_version` is taken as an int.
8. `base_revision: null` means create in `POST /uploads`, but sync fields must also be absent for non-sync uploads. `SyncUploadFields.BaseRevision` uses `omitempty`, so explicit null and absent are the same thing on the wire. The server should treat absent as create when `folder_id` is set.

## Gaps / follow-ups
- Approval kinds other than `escrow_change` and `two_way_enable` (for example prune or unprotect) are not named in rev 1.1, so no constants were added for them.
- The upload response's `conflict:true` and the `on_conflict` field belong to the existing uploads type (P0-08a or the existing client). Only the constant `SyncUploadOnConflictKeepBoth` is here.

## Notes for dependent tasks / INT
- `DecodeTaskPayload` returns pointers (`*TaskRewrapPayload`, …). Type-switch on pointer types.
- Install-mode constants are named `ModuleInstallMode*` to avoid colliding with P0-08a's enrol fields. Reuse them for `install_mode` everywhere.
- The module catalog is a bare JSON array: decode it into `[]ModuleCatalogEntry`.
