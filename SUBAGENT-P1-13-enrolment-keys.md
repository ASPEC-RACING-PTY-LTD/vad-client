# P1-13 (SRV-K) Enrolment key exchange, escrow re-wrap, `key_share` tasks
Status: completed (one cross-task test conflict for INT; see "Notes for dependent tasks / INT")

## Goal
Implement the following from api-v1 §5A, §6 and Rev 1.1b, backup-format-v1 §4 and MASTER_PLAN §7.2:
- the enrolment key exchange (`box_pub` in, `server_identity` out);
- the repository key routes;
- the escrow-mode change;
- key sharing to newly approved devices through `key_share` device tasks;
- rotation on revocation and on request;
- the re-wrap of the current epoch after a SEK rotation;
- advertising `escrow.v1`.

## Changes (repo: path, created/modified/deleted, why)
All changes are in `vad-server`.
- `internal/store/device_keys.go` (created): stores for `device_keys` (`box_pub`, SIK pin) and the `repo_keys` projection. It also has small queries for repository epoch and escrow mode, open tasks, and all repository ids. **No migration was needed**: migration 8 already has `device_keys` and `repo_keys`.
- `internal/keys/rewrap.go` (created):
  - `RewrapToDevice`: opens a full-escrow bundle (audited) and writes a `device` key file.
  - `RewrapEscrow`: re-wraps under the current SEK.
- `internal/keys/rewrap_test.go` (created).
- `internal/api/repo_keys.go` (created): all the P1-13 routes and flows (see "Interfaces exposed"). Its `init()` calls `implementFeature(wire.FeatureEscrow)`, registers the `escrow_change` approval handler, and registers task result hooks for `key_share` and `rewrap`.
- `internal/api/repo_keys_test.go` (created): two end-to-end tests.
- `internal/api/devices.go` (modified):
  - Enrolment decodes `wire.EnrolRequest`, which now accepts `box_pub`, `install_mode` and `core_version`. `box_pub` is validated as 32-byte std base64 X25519 (`400 bad_box_pub`) and stored. `install_mode` is validated (`400 bad_install_mode`).
  - The approved poll returns `server_identity` (omitted without a KEK) and records the SIK pin.
  - Approval calls `shareKeysToDevice`.
  - Revocation calls `rotateAfterRevoke`.
- `internal/api/keys.go` (modified; **not in my owned list**; minimal and necessary): P1-12 had registered the three P1-13 routes there as `notImplemented`, and that file is the only mount point.
  - The three stub lines are replaced by `s.mountRepoKeys(rt)`.
  - One line is added in the `rotate-server` operation: `s.rewrapEscrowAfterSEKRotation(ctx, opID, uid)`.
  - The file is now gofmt'd. The previous edit tool had saved it with CRLF line endings; it is LF again, and its content is otherwise unchanged.

## Interfaces exposed
Routes:
- `POST /device/enroll`: accepts `box_pub`, `install_mode` and `core_version`. `box_pub` is optional, so older clients enrol without key sharing.
- `GET /device/enroll/{id}`: when approved, returns `server_identity` (a `wire.ServerIdentity`). It is omitted when `s.Keys()` is nil. The fingerprint is stored as the device's pin; the first pin wins.
- `GET /repos/{id}/keys/status` (dev, sess; owner, else `404 repo_not_found`): returns `wire.KeyStatus`.
  - `current_epoch` is the larger of `repositories.current_epoch` and the highest key-file epoch.
  - `recipients` lists every projected key file.
  - A signed device whose recorded pin differs from the server's SIK gets `409 sik_mismatch`.
  - As a side effect, it shares keys idempotently with owner devices that lack the current epoch.
- `POST /repos/{id}/keys/rotate` (sess; owner): returns `202 {operation_id}` with kind `rotate_keys`.
  - The result is `{repo_id, epoch, task_id, device_id}`.
  - It answers `409 no_key_holder` when no approved device holds the current epoch, and `409 repo_not_initialised` when the repository has no key files.
- `PATCH /repos/{id}` (sess; owner), body `wire.EscrowChangeRequest`:
  - Returns `202 {approval_id}` with kind `escrow_change`, risk high, and `confirm_text` equal to the repository name. The digest covers `{repo_id, repo_uuid, from, to}`.
  - Errors: `422 validation` for a bad mode, `409 no_change` for the same mode, `409 no_key_holder`.
  - On approval it sets `repositories.escrow_mode` and queues `rewrap {repo_id, epoch: e+1}`.

Device tasks:
- **Full escrow with key custody:** the server opens the current-epoch escrow wrap in memory (`key_audit`, purpose `rewrap`), writes the new device's `device` key file (object, `repo_objects` row, projection), and sends `key_share {repo_id, key_file_oid}` to the new device.
- **Metadata escrow, no KEK, or a meta bundle:** `key_share {repo_id, target_device_id, target_box_pub, target_sign_pub}` goes to the most recently seen approved device that has a wrap at the current epoch.
- A share is skipped while a `key_share` for the same repo and target is still open.
- Rotation is always delegated with `rewrap {repo_id, epoch: e+1}` to a key holder, and an open rewrap reaching that epoch is reused. Device revocation queues it, excluding the revoked device.
- Task results arrive through the existing `/device/tasks/{id}/result`. The hooks re-project the repository's key files and raise the epoch.

SEK rotation:
- After `RotateSEK`, every repository whose current-epoch escrow wraps all name an older SEK gets one new escrow key file under the current SEK, audited with purpose `rotation`.
- The old wraps are kept, since retired SEKs stay openable.

Go:
```go
// keys
var ErrMetaBundle error
type DeviceTarget struct { ID int64; BoxPub vdcrypt.Key; SignPub ed25519.PublicKey; Label string }
func (m *Manager) RewrapToDevice(ctx, kf *vdcrypt.KeyFile, dev DeviceTarget, a Audit) (*vdcrypt.KeyFile, error)
func (m *Manager) RewrapEscrow(ctx, kf *vdcrypt.KeyFile, a Audit) (*vdcrypt.KeyFile, error) // nil,nil when already current
// store
type DeviceKey struct{ DeviceID int64; BoxPub, SIKPinnedFingerprint string; CreatedAt, UpdatedAt int64 }
func (s *Store) SetDeviceBoxPub(ctx, deviceID int64, boxPub string) error
func (s *Store) DeviceKey(ctx, deviceID int64) (*DeviceKey, error)           // ErrNotFound without box_pub
func (s *Store) PinDeviceSIK(ctx, deviceID int64, fp string) error            // only when unpinned
func (s *Store) ActiveDeviceKeys(ctx, userID int64) (map[int64]DeviceKey, error)
type RepoKey struct{ RepoID int64; KeyFileOID, KeyID, Kind string; Epoch int64; Label string; DeviceID int64; RecipientPub string; CreatedAt int64 }
func (s *Store) AddRepoKey(ctx, k *RepoKey) error
func (s *Store) ListRepoKeys(ctx, repoID int64) ([]RepoKey, error)
func (s *Store) UnprojectedKeyObjects(ctx, repoID int64, limit int) ([]string, error)
func (s *Store) RaiseRepoEpoch(ctx, repoID, epoch int64) error
func (s *Store) SetRepoEscrowMode(ctx, repoID int64, mode string) error
type OpenTask struct{ ID, DeviceID int64; Payload string }
func (s *Store) OpenTasks(ctx, userID int64, kind string) ([]OpenTask, error)
func (s *Store) AllRepositoryIDs(ctx) ([]int64, error)
// api (package-internal)
func (s *Server) mountRepoKeys(rt routes)
func (s *Server) shareKeysToDevice(ctx, d *store.Device)
func (s *Server) rotateAfterRevoke(ctx, d *store.Device)
func (s *Server) rewrapEscrowAfterSEKRotation(ctx, opID, actorUserID int64) int
func (s *Server) checkSIKPin(ctx, p *Principal) error
```

## Commands run and results
All commands were run from `D:/VaultDock/vad-server`.
- `go build ./...`: ok.
- `go vet ./internal/api/ ./internal/store/ ./internal/keys/`: ok.
- `gofmt -l` on all touched files: clean.
- `go test ./internal/keys/`: ok.
- `go test ./internal/api/ -run 'TestEnrolmentKeyExchange|TestRevocationQueues|TestServerKeysRoutes'`: ok.
- `go test ./internal/...`: every package passes except one test in `internal/api`, `TestApprovalResumeThroughOperation` (P1-17's `approvals_test.go:198`). See "Notes for dependent tasks / INT".

## Contract issues
1. **The `key_share`/`rewrap` result shape is undefined.** The server ignores the result body and re-reads the key files (`client.KeyTaskResult` is fine).
2. **Rotation is delegated to a device.**
   - The contract lets the server rotate under full escrow, but doing so would also need the config MAC and wraps for every recipient. In v1 the server always sends `rewrap` to a key holder, so the `rotate_keys` operation succeeds once the task is queued, not once the rotation is done.
3. **An escrow change carries no target mode in the task.**
   - `rewrap {repo_id, epoch}` has no field for the new mode, so the device must read `escrow_mode` from `GET /repos/{id}`, rewrite `config` under an exclusive lock, and write the new epoch's escrow wrap as a full or meta bundle accordingly.
   - P1-22's `HandleTask(rewrap)` should be checked for this.
4. **The `PATCH /repos/{id}` errors are implementer choices:** `409 no_change` and `409 no_key_holder`. The contract defines only the 202 reply.

## Gaps / follow-ups
- `install_mode`/`core_version` from enrolment are validated but not stored. Writing `device_modules` would make the device look as if it had reported modules; the `PUT /device/modules` report remains the source.
- Admin revocation (`POST /admin/devices/{id}/revoke`, `admin.go`, not owned) does not yet trigger rotation. It should call `s.rotateAfterRevoke(ctx, d)` like the owner route.
- Key files that do not decode are logged and re-tried on every sync, because no negative cache exists.
- A recorded SIK pin is never cleared. A "re-pin with admin confirmation" route is not defined in the contract.

## Notes for dependent tasks / INT
- **Test conflict.** `internal/api/approvals_test.go` `TestApprovalResumeThroughOperation` (P1-17) uses `wire.ApprovalKindEscrowChange` as a stand-in kind with no handler and expects the approval to stay `approved`. P1-13 now registers the real `escrow_change` handler, so that synthetic approval, which has no data or repository, gets `409 plan_stale`. Fix: change that test to a kind without a handler, for example `wire.ApprovalKindTwoWayEnable`. The file belongs to P1-17 and was not edited.
- **`keys.go` edit.** The `internal/api/keys.go` change (above) touched a P1-12 file to replace its P1-13 stubs. INT should accept it or move the mount.
- **P1-26 / desktop:**
  - send `box_pub`;
  - pin `server_identity` from the approved poll;
  - handle `key_share` (both shapes) and `rewrap` through `keystore.HandleTask`;
  - for `rewrap` after an escrow change, honour `escrow_mode` from `GET /repos/{id}`.
- **P1-36:**
  - `enrollBox` in `repo_keys_test.go` shows the enrol-with-`box_pub` flow;
  - full-escrow sharing needs a KEK (`keys.New` + `SetKeys`).
