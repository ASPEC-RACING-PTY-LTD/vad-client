# P0-08a (SDK-WIRE) Go wire types, part A (api-v1 §2, §3, §4, §5A, §5, §5.2, §5.4)
Status: completed

## Goal
Write Go structs and enum constants in `sdk/wire` for every request/response payload of api-v1 rev 1.1 §2, §3, §4, §5A and §5 (including §5.2 commit and §5.4 catalog), and add round-trip and unknown-field tests. `common.go` is reused and was not edited.

## Changes (repo: path, created/modified/deleted, why)
All files are in the VaultDock (client) repo and were created by this task:
- `sdk/wire/capabilities.go`: §2 capabilities, plus the escrow mode constants.
- `sdk/wire/operations.go`: §3 summary and operations.
- `sdk/wire/destinations.go`: §4 destinations.
- `sdk/wire/keys.go`: §5A enrolment, server identity, rotation, escrow change and key status.
- `sdk/wire/repos.go`: §5 repositories, objects and locks.
- `sdk/wire/snapshots.go`: §5.2 commit and the §5.4 Snapshot entry.
- `sdk/wire/catalog.go`: §5.4 tree, search, diff, patch, download and rebuild.
- `sdk/wire/wire_a_test.go`: round-trip, unknown-field rejection, secret tri-state and null/omit tests.

## Interfaces exposed (type / const -> contract section)
- **capabilities.go**
  - `Capabilities` -> §2 `GET /backup/capabilities`.
  - Constants: `CapabilitiesFormatRepoV1` (§2) and `EscrowModeFull`/`EscrowModeMetadata` (§2/§5A/§5).
- **operations.go**
  - `Summary` -> §3 `GET /summary`. `Readiness` is a `json.RawMessage`.
  - `Operation` and `OperationProgress` -> §3 `GET /operations/{id}`. `Result` is a `json.RawMessage` and `Error` is a `*ErrorBody`; both are written as null when unset.
  - Constants: `OperationKind{Prune,GC,Verify,Replicate,CatalogRebuild,Restore,RotateKeys,SnapshotDownload}` (§3).
- **destinations.go**
  - `Destination` -> §4. It embeds `SecretState` for `has_secret` and `secret_updated_at`.
  - `DestinationConfig` and `DestinationCapabilities` -> §4 nested objects.
  - `DestinationRef` -> §4 device view of `GET /destinations` (ids and names only).
  - `DestinationCreateRequest` -> §4 `POST /destinations` and `/admin/destinations`. `Secret` is a `SecretUpdate` with `omitzero`.
  - `DestinationUpdateRequest` -> §4 PATCH. Fields are pointers or `omitempty`, and `Secret` is a `SecretUpdate`.
  - `DestinationTestRequest` -> §4 `POST /destinations/test`. `DestinationTestResult` is the reply of both test routes.
  - `DestinationProviderInfo` -> §4 `GET /destinations/providers`.
  - Constants (§4):
    - `DestinationScope{User,Global}`;
    - `DestinationKind{Server,Local,S3}`;
    - `DestinationProvider{AWS,R2,B2,MinIO,Other}`;
    - `DestinationHealth{OK,Degraded,Unreachable,Unknown}`.
- **keys.go**
  - `EnrolRequest` -> §5A `POST /device/enroll`: the existing fields plus `box_pub`, `install_mode` and `core_version`.
  - `EnrolResponse` -> existing enrolment reply, unchanged.
  - `EnrolStatus` -> §5A `GET /device/enroll/{id}`. `device_id` and `server_identity` are only set after approval.
  - `ServerIdentity` -> §5A `server_identity`. `SEKVersion` is a `uint32`, matching `u32(sek_version)` in backup-format §4.3.
  - `ServerIdentityResponse` -> §5A `GET /keys/server`.
  - `EscrowChangeRequest` -> §5A `PATCH /repos/{id}`. The reply is the existing `ApprovalAccepted`.
  - `KeyStatus` and `KeyRecipient` -> §5A `GET /repos/{id}/keys/status`.
  - Constants: `EnrolStatus{Pending,Approved,Rejected,Expired}` (existing server behaviour) and `KeyRecipient{Device,Escrow,Recovery,Passphrase}` (backup-format §4.1).
  - `POST /repos/{id}/keys/rotate` has no body and replies with the existing `OperationAccepted`.
- **repos.go**
  - `Repo` and `RepoUsage` -> §5 Repository.
  - `RepoCreateRequest` -> §5 `POST /repos`. `escrow_mode` is `omitempty` because it defaults to full.
  - `ObjectInfo` -> §5 `GET /repos/{id}/objects`, returned as `List[ObjectInfo]`.
  - `ObjectMissingRequest` and `ObjectMissingResponse` -> §5 `/objects/missing`.
  - `LockRequest` and `Lock` -> §5 `/locks`.
  - `LockedDetails` -> §1.1 `repo_locked` details.
  - Constants: `RepoFormatVersion` = 1, `ObjectKind{Config,Keys,Chain,Index,Snapshots,Data,Locks}`, `ObjectConfigOID` (64 zeros), `ObjectMissingMaxOIDs` = 20000 and `LockKind{Shared,Exclusive}`.
- **snapshots.go**
  - `SnapshotCommitRequest` -> §5.2 commit body. `parent_snapshot_id` is a nullable `*int64`.
  - `SnapshotStats` -> §5.2 `stats`.
  - `SnapshotCommitResponse` -> §5.2 `{snapshot_id}`.
  - `Snapshot` and `SnapshotProfile` -> §5.4 catalog Snapshot.
  - Constants: `SnapshotIngest{Pending,Done,Failed}` (§5.4).
- **catalog.go**
  - `CatalogEntry` -> §5.4 tree and search items, returned as `List[CatalogEntry]`.
  - `CatalogDiffEntry` -> §5.4 diff items, returned as `List[CatalogDiffEntry]`.
  - `SnapshotPatchRequest` -> §5.4 PATCH (tags/protect).
  - `SnapshotDownloadRequest` and `SnapshotDownloadResult` -> §5.4 download, carried in `Operation.result`.
  - `CatalogRebuildRequest` -> §5.4 `/catalog/rebuild`.
  - Constants: `CatalogEntry{File,Dir}` and `CatalogDiff{Added,Removed,Modified}`.

## Commands run and results
- `cd sdk && go vet ./wire/` passed.
- `go test -count=1 ./wire/` passed. The package includes P0-08b's files.
- `gofmt -l` on my 8 files reported nothing.
- `gofmt -l wire` lists `wire/readiness.go` and `wire/retention.go`. Both are P0-08b's files and were mid-edit; I did not touch them.

## Contract issues
1. **Many fields are only defined as "as rev 1.0", and the rev 1.0 text is not in the repo.** I inferred the following from `planning/research/01-server-backend.md`; INT should confirm them:
   - the `SnapshotCommitRequest` field types;
   - the `SnapshotStats` fields `{file_count, logical_bytes, new_bytes}`;
   - the `Snapshot` catalog fields: `run_id`, `device_id`, `committed_at`, `protected` and `protected_until`.
2. **These shapes are not defined in the contract; I chose them:**
   - `Operation.state` values: no constants were added.
   - `Summary.readiness`: kept raw.
   - `ObjectInfo` items.
   - `Lock` reply and `lock_id` type: `int64`, following the "rows are int64" rule.
   - `LockedDetails.holder` type: `string`.
   - Destination create, update and test request bodies, and the test result.
   - `/destinations/providers` reply: `List`, or a bare array?
   - Tree, search and diff item shapes, and the search `type=` values.
3. **`GET /keys/server` is ambiguous.** The contract says it "returns the current `server_identity`". I wrapped it as `{"server_identity":{...}}`, the same shape as the enrolment poll.
4. **`DestinationConfig` fields use `omitempty`** because they depend on the kind (for example, local has only `path`). The contract lists them without marking any optional.
5. **`Destination.secret_updated_at` is omitted when 0**, inherited from `SecretState` in common.go. The contract lists the field without marking it optional.

## Gaps / follow-ups
- No query-parameter structs: the `/snapshots` filters and the `kind=`/`cursor=` parameters are plain query strings.
- Snapshot `kind` and `mode` stay plain strings here. P0-08b defines `JobKind*` and `JobMode*` in `jobs.go`.

## Notes for dependent tasks / INT
- The `key_share` payloads of §5A are P0-08b's `TaskKeySharePayload` in `tasks.go`. I removed my own duplicates to avoid two types for one payload.
- Install-mode constants are P0-08b's `ModuleInstallMode*`. The approval kind `escrow_change` belongs to the §8 approval constants in P0-08b's files.
- Test helpers are prefixed `wireA*`, so they cannot collide with P0-08b's tests.
- Destination GET on a device returns `List[DestinationRef]`; on a session it returns `List[Destination]`.
