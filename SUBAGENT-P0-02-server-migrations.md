# P0-02 (SRV-0) Server DB migrations 2–8
Status: completed

## Goal
Append schema migrations 2–8 to `vad-server/internal/store/migrations.go` per MASTER_PLAN §8.1, so that every persisted field in api-v1 rev 1.1 (and the backup-format §4.6 key custody) has a column. The migrations are additive and run on an existing v1 database. Tests cover fresh → v8 and v1-with-data → v8.

## Changes
- `vad-server/internal/store/migrations.go` (modified): appended migrations 2–8 as purely additive SQL (`CREATE TABLE`/`INDEX`/`TRIGGER`, `ALTER TABLE … ADD COLUMN`, one backfill `UPDATE`). Migration 1 is untouched. The runner (`store.go`) needs no changes.
- `vad-server/internal/store/migrate_test.go` (created): `TestMigrateFresh` and `TestMigrateFromV1`.

### Per-migration summary
| Mig | Contents |
|---|---|
| 2 | `devices.can_delete` INTEGER NOT NULL DEFAULT 1; `users.is_superuser` DEFAULT 0, backfilled to 1 for the earliest admin (`ORDER BY created_at, id`); trigger `users_first_superuser` (see Notes); tables `secrets`, `destinations`, `approvals`, `operations`, `plans` |
| 3 | `repositories`, `repo_objects`, `snapshots`, `snapshot_packs`, `snapshot_files`, `snapshot_file_tokens`, `catalog_keys` |
| 4 | `backup_jobs`, `job_runs`, `run_issues`, `device_tasks`, `app_inventory`, `restores`, `restore_items`, `device_backup_state` |
| 5 | `retention_policies`, `prune_plans`, `repo_locks`, `schedules` |
| 6 | `nodes.revision` INTEGER NOT NULL DEFAULT 1; `uploads.folder_id`, `uploads.base_revision`, `uploads.base_sha256`; `file_versions`, `sync_folders`, `sync_conflicts`, `sync_changes` |
| 7 | `replication_policies`, `replication_state`, `notification_prefs`, `ai_settings`, `ai_audit`; `users.backup_quota_bytes` DEFAULT 0; `automations.schema_version` DEFAULT 1; `automations.runtime` DEFAULT `'device'` |
| 8 | `server_keys`, `key_audit`, `repo_keys`, `device_keys`, `device_modules`, `device_profiles`, `profile_requests` |

Conventions follow migration 1: INTEGER ms timestamps; JSON stored in TEXT (`'{}'`/`'[]'` defaults); `user_id` FKs use `ON DELETE CASCADE`; optional references use `ON DELETE SET NULL`. CHECK constraints are used only where the contract fixes the enum. Some extensible enums (operation `kind`, approval `kind`, task `kind`/run `status`) deliberately have no CHECK, because changing one in SQLite needs a table rebuild.

## Interfaces exposed

### api field → table.column
| api-v1 field(s) | table.column |
|---|---|
| §1 Secrets `has_secret`, `secret_updated_at` | `<owner>.secret_id IS NOT NULL`, `secrets.updated_at` (`secrets.kind,key_id,nonce,ciphertext,rotated_at,owner_user_id`) |
| §1 plan/execute `plan, plan_token, plan_digest, expires_at` | `plans.plan, token_hash` (SHA-256 of the token; the token itself is never stored)`, plan_digest, expires_at`, plus `kind,target_type,target_id,status,executed_at` |
| §1/§8 approval `approval_id`, kinds incl. `escrow_change`, `two_way_enable` | `approvals.id, kind, risk, status, title, summary, payload, target_type, target_id, source, requested_by_user_id, requested_by_device_id, operation_id, decided_by, decided_at, decision_note, expires_at` |
| §3 operation `id, kind, state, progress{done,total,bytes}, result, error, created_at, updated_at` | `operations.id, kind, state, progress_done, progress_total, progress_bytes, result, error, created_at, updated_at` (+ `user_id, device_id, params, finished_at`) |
| §4 Destination `id, scope, name, kind, config{…}, has_secret, secret_updated_at, enabled, health, last_checked_at, capabilities{…}, created_at, updated_at` | `destinations.id, scope (+user_id), name, kind, config (JSON), secret_id→secrets.updated_at, enabled, health, last_checked_at, capabilities (JSON), created_at, updated_at` |
| §5A enroll `box_pub` | `device_keys.box_pub` |
| §5A enroll `install_mode`, `core_version` | `device_modules.install_mode, core_version` |
| §5A `server_identity{sik_pub, sik_fingerprint, sek_pub, sek_version, sek_sig}` | `server_keys.public, fingerprint` (kind `sik`); `server_keys.public, version, signature` (kind `sek`); current = `retired_at IS NULL` |
| §5A device pinned SIK | `device_keys.sik_pinned_fingerprint` |
| §5A keys/status `current_epoch, recipients[{kind,label,epoch,device_id}], recovery_wrapped_current, escrow_mode` | `repositories.current_epoch, escrow_mode`; `repo_keys.kind, label, epoch, device_id` (recovery_wrapped_current = a `repo_keys` row with kind `recovery` at the current epoch) |
| §5A key_share payload `key_file_oid` | `repo_keys.key_file_oid` / `device_tasks.payload` |
| backup-format §4.6 server unwraps audited | `key_audit.*` |
| §5 Repository `id, repo_uuid, name, destination_id, format_version, escrow_mode, current_epoch, created_by_device_id, created_at, last_catalog_sync_at` | `repositories.*` (same names) |
| §5 Repository `usage{objects, stored_bytes, logical_bytes, snapshots}` | derived: `COUNT/SUM(repo_objects.size)`, `SUM(snapshots.logical_bytes)`, `COUNT(snapshots)` |
| §5 objects `kind, oid`, pending-delete | `repo_objects.kind, oid, size, delete_pending_at, last_verified_at, verify_status` |
| §5 locks `{kind, ttl_seconds}`, `repo_locked{holder, expires_at}` | `repo_locks.id (lock_id), kind, holder_kind, holder_device_id, holder (JSON), acquired_at, refreshed_at, expires_at` |
| §5.2 commit `snapshot_oid, job_id, run_id, parent_snapshot_id, kind, mode, hostname, os_user, user_sid, profile_path, client_time, tags, stats, index_oids, pack_oids, epoch, profile_consent_id` | `snapshots.snapshot_oid, job_id, run_id, parent_snapshot_id, kind, mode, hostname, os_user, profile_sid (= user_sid), profile_path, client_time, tags, stats, index_oids`, `snapshot_packs(snapshot_id, object_id→repo_objects)`, `snapshots.epoch, profile_consent_id` |
| §5.4 Snapshot `epoch, profile{sid,username}, ingest_state`, `browsable`, protect | `snapshots.epoch, profile_sid, profile_username, ingest_state (+ingest_error)`, browsable = `ingest_state='done'`; `snapshots.protected, protected_until, status, committed_at, file_count, logical_bytes, new_bytes` |
| §5.4 encrypted catalog, blind-index search, tree | `snapshot_files.path_ct, name_ct, blind_tokens, path_bi, parent_bi, type, size, mtime, mode`; `snapshot_file_tokens(snapshot_id, token, file_id)` |
| §5.4 per-user catalog DEK wrapped by the KEK | `catalog_keys.user_id, dek_wrapped_by_kek, kek_id, created_at, rotated_at` |
| §6 BackupJob `profile_sid, required_modules, state` (+ revision, rev 1.0 fields) | `backup_jobs.profile_sid, required_modules (JSON), state, revision, device_id, repo_id, name, kind, mode, spec, schedule, retention_policy_id, priority, enabled` |
| §6 runs (`client_run_uuid`, progress) | `job_runs.client_run_uuid (UNIQUE per job), status, source, snapshot_id, files_*, bytes_*, warnings, errors, skipped, summary, error, started_at, updated_at, finished_at` |
| §6 issues (classes incl. `profile_unauthorised`, `module_unavailable`, `guard_blocked`), skip, retry-skipped | `run_issues.class, severity, path, message, retryable, skip, resolved_at, retry_run_id` |
| §6 DeviceTask `kind, payload` (+ required modules gating) | `device_tasks.kind, payload, required_modules, status, approval_id, created_by, attempts, result, error_code, error, claimed_at, finished_at, expires_at` |
| §6 inventory / `GET /devices/{id}/apps` | `app_inventory.*` |
| §6A `PUT /device/modules {install_mode, core_version, modules[{id,version,state,error}]}`, `updated_at` | `device_modules.install_mode, core_version, modules (JSON array), updated_at` |
| §6A `GET /devices` `install_mode, modules_summary` | `device_modules.install_mode`; summary derived from `device_modules.modules` |
| §6A mode-request | `device_tasks` (kind `mode_change_request`) |
| §6B DeviceProfile `id, device_id, sid, username, profile_path, vaultdock_user_id, state, consent_id, consent_at, requested_by_user_id, request_scope, blacklisted_at, blacklist_reason, cleared_by, cleared_at, updated_at` | `device_profiles.*` (same names) |
| §6B consent `proof_digest`, security-event `evidence` | `device_profiles.proof_digest, blacklist_evidence` |
| §6B profile requests `{sids[]} / {all}` | `profile_requests.scope, sids, task_id, requested_by_user_id, status` |
| §7 restore `target_profile_sid`, `reinstall_apps`, plan `guard`, item `data_only` + warning | `restores.target_profile_sid, reinstall_apps, plan, guard, options, status, task_id, approval_id, operation_id`; `restore_items.action, warning, status` |
| §9 retention / prune | `retention_policies.*`, `prune_plans.*` |
| §9 replication | `replication_policies.*`, `replication_state.*` |
| §9 server scheduled prune/verify/replicate | `schedules.*` |
| §10.1 SyncFolder `id, device_id, profile_sid, local_path, remote_path, direction, conflict_policy, delete_policy, mass_delete_threshold{files,percent}, ruleset, schedule, enabled, revision, acked_revision, state, created_at, updated_at` | `sync_folders.*`; `mass_delete_threshold` = `mass_delete_files`, `mass_delete_percent` |
| §10.2 change item `seq, node_id, op, path, old_path, revision, sha256, size, modified_at, device_id` | `sync_changes.*` (PK `(user_id, seq)`), `nodes.revision` |
| §10.2 upload `folder_id, base_revision, base_sha256` | `uploads.folder_id, base_revision, base_sha256` |
| §10.4 versions (pin) | `file_versions.node_id, revision, sha256, size, modified_at, origin_device_id, pinned, created_at` |
| §10.4 conflicts (state, resolve choice) | `sync_conflicts.state, resolution, policy, local_*, remote_*, conflict_node_id, resolved_by, resolved_at` |
| §11 `PUT /device/backup-state` | `device_backup_state.state (JSON), updated_at` |
| §11 notification prefs | `notification_prefs(user_id, event, channel, enabled)` |
| §11 AI `enabled, base_url, model, has_secret, secret_updated_at, scopes` | `ai_settings.enabled, base_url, model, secret_id→secrets, scopes (JSON)` |
| §11 AI audit | `ai_audit.*` (no prompts or keys stored) |
| §11 `ai_allow_private_endpoints`, tombstone window | existing `settings` key/value table (no schema change) |
| §11A env removal approvals | state-volume file `deploy/env-removal-approvals.json` (no DB) |
| §11B `is_superuser` | `users.is_superuser` |
| quotas (`backup_quota_exceeded`) | `users.backup_quota_bytes` |
| automations v2 | `automations.schema_version, runtime` |
| device delete permission (`device_cannot_delete`) | `devices.can_delete` |

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./...`: OK
- `go vet ./internal/store/`: OK
- `go test ./internal/store/ ./internal/api/`: both `ok`
- `go test -count=1 -v -run Migrate ./internal/store/`: `TestMigrateFresh` and `TestMigrateFromV1` PASS
- `gofmt -l internal/store`: my two files are clean. It lists other pre-existing store files (`commit.go`, `devices.go`, `misc.go`, `nodes.go`, `store.go`, `uploads.go`), which I did not touch.

## Contract issues
1. §3 lists operation `kind` values, but §11B's `POST /admin/keys/rotate-server` returns an operation and no kind is listed for SEK/SIK rotation. `rotate_keys` may or may not be meant to cover it. `operations.kind` therefore has no CHECK.
2. §5.2 and §5.4 refer to "rev 1.0 fields" for snapshots, jobs, runs, restore and approvals, but rev 1.0 is not available anywhere in the repo. Columns for those were taken from planning/research/01 §3.5/§3.7. SRV-2 and SRV-3 may need additive columns once INT pins those fields.
3. The snapshot commit sends `user_sid`, while the snapshot response uses `profile{sid}`. Both map to `snapshots.profile_sid`.

## Gaps / follow-ups
- No cached repository usage columns: usage is derived by query. Add a cache column later if it is too slow.
- A single-use snapshot-download token (§5.4) has no dedicated table. It is expected to live in `operations.result` as a hash plus a used flag. If SRV-2/SRV-C want a table, that needs a migration 9.
- `automations.runtime` defaults to `'device'`, because existing automations run on devices. SDK-AUTO/SRV owners should confirm the value set.

## Notes for dependent tasks / INT
- **Superuser on fresh installs:** trigger `users_first_superuser` sets `is_superuser = 1` on an inserted admin when no superuser exists yet. Setup code (`users.go`) therefore needs no change for the "first admin is superuser" rule. Consequence: if every superuser is deleted, the next admin inserted becomes superuser. The "at least one must remain" rule (§11B) is still enforced in code by SRV-M.
- **`file_versions.node_id`** references `nodes(id)` with no ON DELETE action. Purging a node that still has versions fails with an FK error. The SRV-5 purge code must delete versions first and decrement `blobs.refcount` for each. This is intentional, so refcounts never leak silently.
- **`snapshot_packs.object_id`** references `repo_objects(id)` (not the oid) with no cascade. GC must delete or forget snapshots before it deletes their pack objects.
- **`repositories.destination_id`** and **`replication_policies.dst_destination_id`** have no cascade, so a destination DELETE fails at the FK unless the handler checks first. SRV-1 should check and return `409 in_use`.
- `backup_jobs.retention_policy_id`, `snapshots.job_id/run_id` and `sync_changes.node_id/device_id` deliberately have no FK, to avoid forward references and keep the feed and history after deletes.
- `server_keys` has a partial unique index: one unretired key per kind. To rotate, set `retired_at` on the old row before inserting the new one, in the same transaction.
- `sync_changes.seq` is per user. Allocate it as `COALESCE(MAX(seq),0)+1` inside the write transaction (the `_txlock=immediate` DSN makes this safe).
- `plans.token_hash`: store the SHA-256 of the plan token and compare in constant time.
