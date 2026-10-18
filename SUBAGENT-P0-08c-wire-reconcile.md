# P0-08c (SDK-WIRE) Reconcile sdk/wire with the restored rev 1.0 contract text
Status: completed

## Goal
Compare every `sdk/wire` type that rev 1.1 defines "as rev 1.0", or that P0-08a/P0-08b inferred, against `sdk/contracts/history/api-v1-rev1.0.md`, with rev 1.1 changes applied on top (rev 1.1a, §14). Fix names, types, optionality and enums. Do not re-add anything rev 1.1 removed: `encryption_mode`, `client_only`/`credential_scope`, the managed-metadata listing, `server_destination` and `encryption_modes`.

## Changes (repo VaultDock)
| File | Change |
|---|---|
| `sdk/wire/common.go` | modified. Rev 1.0 contradicts it in three places; see "common.go deviations" |
| `sdk/wire/operations.go` | modified |
| `sdk/wire/destinations.go` | modified |
| `sdk/wire/repos.go` | modified |
| `sdk/wire/snapshots.go` | modified |
| `sdk/wire/catalog.go` | modified |
| `sdk/wire/jobs.go` | rewritten |
| `sdk/wire/tasks.go` | rewritten (task core, payloads, inventory) |
| `sdk/wire/restore.go` | rewritten |
| `sdk/wire/approvals.go` | rewritten |
| `sdk/wire/retention.go` | rewritten |
| `sdk/wire/readiness.go` | rewritten |
| `sdk/wire/ai.go` | modified (diagnose types) |
| `sdk/wire/sync.go` | modified (`Version`, `SyncUploadFields`) |
| `sdk/wire/wire_a_test.go` | modified: fixtures updated; new cases for objects list, destination test/providers, `OperationAccepted`, `ApprovalAccepted`, `SnapshotDiff`; unknown-field cases for `Snapshot.run_id` and `Lock.kind` |
| `sdk/wire/wire_b_test.go` | modified: all round-trip fixtures rebuilt on rev 1.0 shapes (44 cases); 4 new unknown-field cases; payload decode cases for restore, retry_skipped, verify, test_restore and automation_op |
| `sdk/wire/wire_c_test.go` | created: rejects the rev 1.1-removed fields; checks rev 1.0 always-present and nullable fields |

Unchanged: `capabilities.go`, `keys.go`, `modules.go`, `profiles.go`, `deployenv.go` and `superuser.go`. They are rev 1.1-only or already matched.

### common.go deviations (rev 1.0 §1 contradicts common.go)
| Field | Before → after | Source |
|---|---|---|
| `OperationAccepted.OperationID` | `string` → `int64` | rev 1.0 §1 `202 {"operation_id":N}` |
| `ApprovalAccepted.OperationID` | `*string` → `*int64` | rev 1.0 §1 `{"approval_id":N,"operation_id":N\|null}` |
| `SecretState.SecretUpdatedAt` | `omitempty` → always present | rev 1.0 §1 `{"has_secret":bool,"secret_updated_at":ms}`; flagged by P0-08a issue 5 |

`BulkFailed.ID` stays a `string`. Rev 1.0 writes it as `"id":…` with no type, and its users mix numeric ids with sync paths.

## Change table
| Type.field | Before → after | Source |
|---|---|---|
| `Summary.Readiness` | `json.RawMessage` → `string` (`ok\|warning\|critical\|unknown`) | rev 1.0 §3 |
| `Operation.ID` | `string` → `int64` | rev 1.0 §3 |
| `Operation.Error` | `*ErrorBody` → `string` | rev 1.0 §3 `"error":""` |
| `OperationState*` | none → `queued, running, succeeded, failed, cancelled` | rev 1.0 §3 |
| `DestinationConfig.*` | all `omitempty` → always present | rev 1.0 §4 |
| `DestinationTestResult` | `{health, capabilities, message, checked_at}` → `{ok, checks:[{name, ok, message}]}` (+ `DestinationTestCheck`) | rev 1.0 §4 |
| `DestinationProviderInfo` | `{id, name}` → `DestinationProvider{kind, provider, fields:[{name, label, type, required, secret}]}`, a bare array | rev 1.0 §4 |
| `ObjectInfo` | `{kind, oid, size, created_at}` → `{oid, size}` | rev 1.0 §5 "oids + size" |
| `Lock.LockID` | `int64` → `string` | rev 1.0 §5 `"lock_id":"…"` |
| `Lock.Kind` | present → removed | rev 1.0 §5 `{lock_id, expires_at}` |
| `SnapshotStats` | + `dir_count`, `new_objects`, `duration_ms` | rev 1.0 §5.2 |
| `Snapshot.RunID` | present → removed (not a rev 1.0 field) | rev 1.0 §5.4 |
| `Snapshot.ParentSnapshotID` | `parent_snapshot_id` → `ParentID` `parent_id` | rev 1.0 §5.4 |
| `Snapshot.ProtectedUntil` | `int64` → `*int64` (null) | rev 1.0 §5.4 |
| `Snapshot.Status` | none → `string`; `SnapshotStatus{Committed, Forgotten, Missing}` | rev 1.0 §5.4 |
| `List[CatalogDiffEntry]` (diff reply) | → `SnapshotDiff{added, removed, changed, paths[]}`; `CatalogDiffModified` → `CatalogDiffChanged` ("changed"); `SnapshotDiffMaxPaths` = 1000 | rev 1.0 §5.4 |
| `Job.UserID` | present → removed | rev 1.0 §6 |
| `Job.UseVSS` | top level → `JobSpec.UseVSS` (`spec.use_vss`) | rev 1.0 §6 |
| `Job.Spec` | `RawMessage` → `JobSpec{paths, exclude, ruleset_id*, source_ref, use_vss}` | rev 1.0 §6 |
| `Job.Schedule` | `RawMessage` → `JobSchedule{type, expr, at, weekdays, every_minutes, timezone, catch_up, jitter_minutes}` + `JobSchedule*`/`JobCatchUp*` constants | rev 1.0 §6 |
| `Job.Revision` | none → `int64` | rev 1.0 §6 |
| `JobCreateRequest`/`JobUpdateRequest` | `use_vss` removed; spec/schedule typed; create fields no longer `omitempty` | rev 1.0 §6 |
| `RunStartRequest` | `{client_run_uuid, epoch, started_at}` → `{client_run_uuid, trigger}`; `RunTrigger*` | rev 1.0 §6 |
| `RunUpdateRequest` | → `RunProgress{status, phase, files_total, files_done, bytes_total, bytes_done, bytes_new, bytes_uploaded, queue_packs, warnings, errors, eta_ms}`; dropped snapshot_id, finished_at, summary | rev 1.0 §6 |
| `RunState*` / `Run.State` | → `RunStatus*` / `status` | rev 1.0 §6 |
| `Run` | embeds `RunProgress`; + `trigger`; − `epoch`, `summary` | rev 1.0 §6 (object itself not defined) |
| `Issue` | `{…, message, resolved_at, created_at}` → `{id, run_id, path, class, severity, detail, first_seen, last_seen, count, retryable, resolution, retry_run_id}` | rev 1.0 §6 |
| `IssueClass*` | + 11 rev 1.0 classes (locked … other) | rev 1.0 §6 |
| `IssueResolution*` | none → open, skipped, skipped_all, retried, resolved | rev 1.0 §6 |
| `IssueReport` | `message` → `detail`; + first_seen, last_seen, count | rev 1.0 §6 |
| `DeviceTask.State` | → `Status` `status`; `TaskState*` → `TaskStatus*` | rev 1.0 §6 |
| `DeviceTask.CreatedBy` | `int64` → `string`; `TaskCreatedBy{User, Automation, AI, Server}` | rev 1.0 §6 |
| `DeviceTask.RequiresApproval`, `.Error` | present → removed | rev 1.0 §6 |
| `DeviceTask.ClaimedAt/FinishedAt/Result` | `omitempty` → always present | rev 1.0 §6 |
| `DeviceTaskPollResponse.Tasks` | `tasks` → `Items` `items` | rev 1.0 §6 |
| `DeviceTaskResultRequest` | `{state, result, error}` → `{status, result}` | rev 1.0 §6 |
| `DeviceTaskCreated` | + `approval_id` (omitempty) | rev 1.0 §6 `202 {"task_id","approval_id"}` |
| `TaskRestorePayload.PlanDigest` | present → removed | rev 1.0 §6 `restore {restore_id}` |
| `TaskRetrySkippedPayload.IssueIDs` | required → `omitempty` | rev 1.0 §6 `issue_ids?` |
| `TaskVerifyPayload` | + `depth`, `sample_pct?`; `TaskVerifyDepth{Metadata, Sample, Full}` | rev 1.0 §6 |
| `TaskTestRestorePayload.SampleSize` | `sample_size,omitempty` → `SampleFiles` `sample_files` | rev 1.0 §6 |
| `TaskAutomationOpPayload` | `{automation_id, op:string, params}` → `{automation_id, node_id, op:{kind, params}}` (+ `TaskAutomation`) | rev 1.0 §6 |
| `InventoryReport`/`InventoryApp` | invented shape → `Inventory` = backup-format inventory/1 `{schema, apps, games, devprojects, not_portable}` with full `InventoryApp` and `InventoryGame`/`InventoryDevProject`/`InventoryNotPortable`; + source steam/epic/gog, `InventoryMatch*`, scope and flag constants | rev 1.0 §6 → backup-format rev 1.0 §8.3 |
| `DeviceApp` | none → `{name, version, publisher, source, package_id, match_method, tier, install_scope, mode, data_bytes, last_snapshot_id, flags}` | rev 1.0 §6 `/devices/{id}/apps` |
| `RestorePlanRequest.Selection` | `[]string` → `[]RestoreSelection{path, include}` | rev 1.0 §7 |
| `RestorePlanRequest` | + `target_user{sid?, profile_path?}`, `include_registry`; `reinstall_apps` `bool` → `[]string` | rev 1.0 §7 |
| `RestoreMapping.Overrides` | `omitempty` → always present | rev 1.0 §7 |
| `RestorePlan` | `{snapshot_id, …, items, warnings[]string, totals}` → `{summary{files, bytes, conflicts, reinstalls}, mappings[{token, from, to, auto}], actions[], warnings[{code, message}], not_portable[], guard[]}` | rev 1.0 §7 + rev 1.1 §7 guard |
| `RestorePlanItem`/`RestorePlanTotals` | → `RestorePlanAction`, `RestorePlanSummary` (`reinstall` → `reinstalls`) | rev 1.0 §7 |
| `RestoreExecuteResponse` | none → `{restore_id, approval_id}` | rev 1.0 §7 |
| `RestoreState*` / `.State` | → `RestoreStatus*` / `status`; − planned, + awaiting_approval | rev 1.0 §7 |
| `Approval` | `{…, requester, requested_by_user_id, plan, confirm_text, operation_id, created_at}` → `{id, kind, subject_type, subject_id, plan_digest, summary, risk, requested_by, requester_ref, state, expires_at, decided_by, decided_at, reason}` | rev 1.0 §8 |
| `ApprovalKind*` | + restore, prune, gc, unprotect_snapshot, device_task, automation_op, ai_proposal | rev 1.0 §8 |
| `ApprovalSubject*`, `ApprovalStateConsumed` | none → added | rev 1.0 §8 |
| `ApprovalRequester*` | → `ApprovalRequestedBy*` (+ device) | rev 1.0 §8 |
| `ApprovalRejectRequest` | none → `{reason}` | rev 1.0 §8 |
| `RetentionRules.KeepWithinMS` | `keep_within_ms` → `KeepWithinDays` `keep_within_days` | rev 1.0 §9 |
| `RetentionRules.*` | `omitempty` → always present | rev 1.0 §9 |
| `PrunePlan` | `{repo_id, snapshots[{snapshot_id, keep, reason}], packs_to_delete, bytes_freed, reclaimable_after_compaction}` → `{forget[{snapshot_id, reason}], keep[{snapshot_id, reasons[]}], packs_to_delete, bytes_reclaimed, bytes_reclaimable_after_compaction}` | rev 1.0 §9 |
| `BackupSettings` | none → `{grace_hours, max_object_mb, scrub_cron, db_backup_cron, default_retention_policy_id*}` | rev 1.0 §9 |
| `VerifyRequest` | `{snapshot_id?, depth}` → `{depth, sample_pct}`; `VerifyDepthDeep` → `VerifyDepthSample`, `VerifyDepthFull` | rev 1.0 §9 |
| `ReplicationPolicy` | → `Replication` with `status{lag_objects, lag_bytes, last_run_at*, state}`; − lag_ms, pending_bytes, top-level last_run_at | rev 1.0 §9 |
| `ReplicationPolicyRequest` | → `ReplicationRequest`; schedule no longer omitempty | rev 1.0 §9 |
| `AnalyticsRepo`/`AnalyticsJob` | → `Analytics{logical_bytes, stored_bytes, dedup_ratio, reclaimable_bytes, series[{t, logical, stored}], top[{key, unique_bytes}]}`; `AnalyticsGroup*` | rev 1.0 §9 |
| `SyncUploadFields` | + `on_conflict` (omitempty) | rev 1.0 §10, rev 1.1 §10.2 |
| `Version.NodeID` | present → removed | rev 1.0 §10 |
| `ReadinessReport` | `{device_id, score, checks, last_verified_restore_at, generated_at}` → `{score, status, devices[{device_id, checks[], jobs[{job_id, last_success_at, open_issues, vss_used, verified_at, test_restored_at}]}]}` | rev 1.0 §11 |
| `ReadinessStatus*` | ok, warn, fail, info → ok, warning, critical, unknown | rev 1.0 §3/§11 |
| `ReadinessCheck*` | + 9 rev 1.0 check ids | rev 1.0 §11 |
| `ReadinessCheck.FixLink` | `omitempty` → always present | rev 1.0 §11 |
| `ReadinessBackupState` | `{reported_at, jobs[]}` → `BackupState{recovery_key_confirmed, service_installed, vss_available}` | rev 1.0 §11 |
| `NotificationPrefs{prefs}` | wrapper removed: bare `[]NotificationPref` | rev 1.0 §11 |
| `NotificationChannel*`, `NotificationEvent*` | + webhook; + 7 rev 1.0 events | rev 1.0 §11 |
| `AIDiagnoseRequest` | + `question?` | rev 1.0 §11 |
| `AIDiagnoseResponse` | `{summary, recommendations[{title, detail, approval_id}]}` → `{report_markdown, proposals[{kind, summary, approval_id?}]}` (`AIRecommendation` → `AIProposal`) | rev 1.0 §11 |

## Shapes defined in neither revision (implementer choices, for INT)
- `Run`: the full object, including `started_at`/`finished_at`. Values of `RunProgress.phase`.
- `IssueReport`: the subset of Issue fields a device sends. Rev 1.0 says `Issue…`, but the server assigns id, run_id and resolution.
- `DeviceTask.required_modules`: implied by rev 1.1 §6 but not listed as a field. Reply of a non-gated `POST /device/tasks`.
- `TaskVerifyPayload.sample_pct` and `VerifyRequest.sample_pct` types: `int`. `TaskAutomationOpPayload.node_id` is a `string`, matching `sdk/automation` node ids.
- Restore: element shapes of `actions` (`RestorePlanAction`) and `not_portable` (`[]string`); the package ref type in `reinstall_apps` (`[]string`); the `Restore` and `RestoreItem` objects; the action values.
- `RetentionPolicy` and `RetentionPolicyRequest` wrapper (`name`, `rules`); `GCPlan`; the replication `schedule` (raw) and `status.state` values; whether `status` is nested (chosen) or flat.
- `SnapshotDiff.paths` key and `CatalogDiffEntry` shape; tree and search `CatalogEntry` and search `type=` values.
- `GET /devices/{id}/apps` and `GET /files/{id}/versions`: rev 1.0 writes the reply as `[…]` but takes `?cursor=`. I chose `List[T]`.
- Readiness check `status` values: reused ok|warning|critical|unknown.
- Conflict row and conflict states; AI proposal `kind` values; `LockedDetails.holder` type; lock refresh (PUT) body (none assumed).
- Still as P0-08a/b chose and untouched: destination create/update/test request bodies, `ObjectMissing*`, `BulkFailed.id` (`string`), and the §6A, §6B and §11A items listed in their reports.

## Commands run and results
- `cd sdk && gofmt -l wire`: no output. `go vet ./wire/`: clean. `go test -count=1 ./wire/`: ok. `go build ./...`: ok.
- `cd vad-server && go build ./...`: ok. `go vet ./internal/api/`: clean.
- `cd desktop && go build ./...`: **fails in `internal/modules`** (`undefined: Downloader`, `NoopDownloader`, `ErrDownloadUnavailable`). Another task's in-progress package causes this; it uses only the unchanged `wire.ModuleReport`, `ModuleStatus` and `ModuleState*`. Every other desktop package builds.

## Contract issues
1. Rev 1.0 §1 gives `operation_id`/`id` as integers (N), which contradicts `common.go`'s string ids. Fixed (see above).
2. Rev 1.1 §7 adds `target_profile_sid` while rev 1.0's `target_user{sid?, profile_path?}` stays, so the two overlap. Both are kept. INT should say which one wins, or remove `target_user`.
3. Rev 1.1 §5A/§10.1 add the approval kinds `escrow_change` and `two_way_enable` but no `subject_type` for them (presumably repo and sync folder). No constants were added.
4. Rev 1.0 §5.4 diff has an e2e "pack delta" form. With `encryption_mode` removed it is dropped; only the counts + paths form is modelled.
5. `PATCH /snapshots/{id}` `protected_until?` cannot express "clear to null" with an omitempty pointer. Clearing is unspecified.

## Gaps / follow-ups
- The upload session reply `conflict:true` is still not in wire (existing uploads type).
- No query-parameter structs.

## Notes for dependent tasks / INT
- Renamed or removed identifiers (none used by current consumers):
  - Removed: `InventoryReport`, `NotificationPrefs`, `RunUpdateRequest`, `DestinationProviderInfo`.
  - Renamed:
    - `ReplicationPolicy`/`ReplicationPolicyRequest` → `Replication`/`ReplicationRequest`;
    - `AnalyticsRepo` → `Analytics`;
    - `AIRecommendation` → `AIProposal`;
    - `ReadinessBackupState` → `BackupState`;
    - `RestorePlanItem`/`RestorePlanTotals` → `RestorePlanAction`/`RestorePlanSummary`;
    - `CatalogDiffModified` → `CatalogDiffChanged`;
    - `TaskState*`/`RunState*`/`RestoreState*` → `TaskStatus*`/`RunStatus*`/`RestoreStatus*`;
    - `ApprovalRequester*` → `ApprovalRequestedBy*`;
    - `VerifyDepthDeep` → `VerifyDepthSample`/`VerifyDepthFull`.
- `OperationAccepted.OperationID` and `ApprovalAccepted.OperationID` are now `int64`. Server handlers must emit numbers.
- `Run` embeds `RunProgress`, so the heartbeat fields flatten into the run JSON.
