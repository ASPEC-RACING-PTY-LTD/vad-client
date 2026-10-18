# P1-12 (SRV-K) KEK/SIK/SEK custody, `key_audit`, `/keys/server`
Status: completed

## Goal
Server key custody (backup-format-v1 §4.6, MASTER_PLAN §7.2, api-v1 §5A/§11B/§14 Rev 1.1b): load the KEK, seal and open the SIK/SEK, SEK rotation (retired versions stay openable), the SIK fingerprint, `Signer`/`EscrowOpener` with audit, the `server_keys`/`key_audit` stores, `GET /keys/server`, `POST /admin/keys/rotate-server`, and wiring in `internal/app/app.go`.

## Changes (repo: path, created/modified/deleted, why)
All changes are in `vad-server`:
- `internal/keys/keys.go` (created): `Manager`, `Load`/`New`, KEK read and dev/test generation, first-start SIK/SEK creation, identity, `CurrentSEK`, `SEKPublic`, `RotateSEK`, `Fingerprint`, `Signer`.
- `internal/keys/seal.go` (created): XChaCha20-Poly1305 (`x/crypto`, already a dependency) sealing under the KEK. The format is `nonce(24) || ct`, with AD `vaultdock.server-key/1:<kind>:<version>`.
- `internal/keys/escrow.go` (created): `Audit`, purposes and actions, `EscrowOpener`, `OpenKeyFile`, `OpenSEK`. Both audit before any key material is released, and fail closed.
- `internal/keys/keys_test.go` (created): fingerprint format, startup rules, identity, signing, rotation, opening a retired-version escrow file, purpose rejection, foreign-file rejection without an audit row, audit rows, and reload.
- `internal/store/server_keys.go` (created): `ServerKey` CRUD and atomic `RotateServerKey`.
- `internal/store/server_keys_test.go` (created): rotation atomicity, version conflict, single current key.
- `internal/store/key_audit.go` (created): append-only `AppendKeyAudit`, plus `ListKeyAudit` (store-only).
- `internal/api/keys.go` (modified; it was the P0-03 stub): real `GET /keys/server` and `POST /admin/keys/rotate-server`, and `SetKeys`/`Keys` through a package-level `sync.Map`. The P1-13 routes are left as `notImplemented`.
- `internal/api/keys_test.go` (created): 409 `escrow_unavailable` without keys, the identity for both session and signed device (`VerifyServerIdentity`), 403 for non-superusers, the rotation operation and its result, the new SEK signature, and the audit row.
- `internal/app/app.go` (modified, minimal): `km, err := keys.Load(ctx, st, cfg)` before `api.New`, then `srv.SetKeys(km)`.
- `internal/testserver/testserver.go`: not changed. KEK generation in tests relies on `testing.Testing()`.

## Interfaces exposed
Package `github.com/ASPEC-RACING-PTY-LTD/vad-server/internal/keys`:
```go
const KEKSize = 32
var ErrUnavailable, ErrKEKMismatch, ErrWrongServer, ErrPurpose error
func Fingerprint(sikPub []byte) string // "XXXX-XXXX-...", 8 groups, uppercase
type Signer interface { SIKPublic() ed25519.PublicKey; Sign(msg []byte) ([]byte, error) }
type EscrowOpener interface { OpenKeyFile(ctx context.Context, kf *vdcrypt.KeyFile, a Audit) (vdcrypt.Bundle, error) }
type Audit struct { Purpose string; ActorUserID, ActorDeviceID, UserID, RepoID, OperationID *int64; Details string }
const PurposeCatalog="catalog_ingest", PurposeRewrap="rewrap", PurposeDataOperation="data_operation", PurposeRotation="rotation"
const ActionUnwrap="unwrap", ActionOpenSEK="open_sek", ActionRotateSEK="rotate_sek"
type Manager struct{ ... } // a nil *Manager is valid = unavailable; every method returns ErrUnavailable
func Load(ctx context.Context, st *store.Store, cfg *config.Config) (*Manager, error)
func New(ctx context.Context, st *store.Store, kek vdcrypt.Key) (*Manager, error)
func (m *Manager) Available() bool
func (m *Manager) SIKPublic() ed25519.PublicKey
func (m *Manager) Sign(msg []byte) ([]byte, error)
func (m *Manager) Identity() (wire.ServerIdentity, error)
func (m *Manager) CurrentSEK() (pub vdcrypt.Key, server vdcrypt.KeyFileServer, err error) // for NewKeyFile escrow wraps
func (m *Manager) SEKPublic(ctx context.Context, version uint32) (vdcrypt.Key, []byte /*sig*/, error)
func (m *Manager) OpenSEK(ctx context.Context, version uint32, a Audit) (vdcrypt.Key, error) // audited; caller wipes
func (m *Manager) OpenKeyFile(ctx context.Context, kf *vdcrypt.KeyFile, a Audit) (vdcrypt.Bundle, error) // audited; any SEK version
func (m *Manager) RotateSEK(ctx context.Context, a Audit) (uint32, error)
```
Package `store`:
- Types and constants: `ServerKey`, `ServerKeySIK`/`ServerKeySEK`, `KeyAudit`.
- `CountServerKeys`, `CurrentServerKey(kind)`, `ServerKeyVersion(kind, v)`, `ListServerKeys(kind)`.
- `InsertServerKeys(...*ServerKey)`.
- `RotateServerKey(*ServerKey)`: returns `ErrConflict` unless the version is current+1, and `ErrNotFound` when there is no current key.
- `AppendKeyAudit(*KeyAudit)` and `ListKeyAudit(repoID, afterID, limit)`.

Package `api`:
- `func (s *Server) SetKeys(m *keys.Manager)` and `func (s *Server) Keys() *keys.Manager`.
- Internal helpers: `errEscrowUnavailable()` and `keysErr(err)`, which maps `ErrUnavailable` to 409.

Routes:
- `GET /keys/server` (dev, sess) returns `wire.ServerIdentityResponse`, or 409 `escrow_unavailable` when there is no KEK.
- `POST /admin/keys/rotate-server` (super) returns 202 `{operation_id}` with kind `rotate_keys`. The result is `{"sek_version":N}`. It returns 409 `escrow_unavailable` when there is no KEK.

Startup rules in `Load`:
- KEK missing and `server_keys` has rows: refuse to start.
- KEK missing, no rows, and `cfg.DevMode || testing.Testing()`: generate the KEK (0600, `O_EXCL`).
- KEK missing otherwise (or an empty `KEKFile`): degraded, returns `(nil, nil)`.
- KEK the wrong size or not opening the stored keys: refuse to start.
- An invalid SEK signature: refuse to start.
- On Unix, a group- or world-readable KEK file only produces a warning.

## Commands run and results
All commands were run from `D:/VaultDock/vad-server`.
- `gofmt -l` on the owned files: clean. Other pre-existing files are reported by `gofmt -l internal/`; they are not owned and were not touched.
- `go build ./...`: ok.
- `go vet ./internal/keys/ ./internal/store/ ./internal/api/ ./internal/app/`: ok.
- `go test ./internal/keys/ ./internal/store/`: ok.
- `go test ./internal/api/ -run TestServerKeysRoutes`: ok.
- `go test ./internal/api/...`: one failure, `TestBackupJobsCRUDGatingOwnDevice` (`backup_jobs_test.go:122: re-enable without module: 409 conflict`).
  - It fails deterministically and involves no key code: it is job version or module gating.
  - Files outside this task changed during the run: `internal/api/modules.go`, `internal/api/backup_objects.go`, and `sdk/wire/common.go` / `tasks.go` (other agents).
  - All other API tests pass.

## Contract issues
None new. The three issues from the previous attempt (fingerprint format, rotate-server scope and kind, key_audit list route) were settled by api-v1 §14 Rev 1.1b and are implemented as specified.

## Gaps / follow-ups
- SIK rotation is deferred (Rev 1.1b). The code keeps SIK version 1, and `init` assumes a single current SIK.
- After a SEK rotation, the existing escrow wraps are not re-wrapped to the new SEK. MASTER_PLAN §7.2 says "SEK rotation re-wraps the current epoch"; that is repository-key work for P1-13.
- If the audit row of a committed SEK rotation fails to write, it is only logged at error level. The rotation itself is not rolled back.
- `/keys/server` does not compare a device's pinned SIK. `sik_mismatch` on repository key operations is P1-13's.
- The enrolment poll's `server_identity` (`GET /device/enroll/{id}`) is not wired. It belongs to the devices/enrolment owner, who should call `s.Keys().Identity()` and omit the field when it returns `ErrUnavailable`.
- The readiness view does not report a missing KEK or degraded custody. A readiness owner could check `s.Keys() == nil`.

## Notes for dependent tasks / INT
- **P1-13**:
  - Use `s.Keys()` in package `api`.
  - Wrap escrow files with `pub, server, _ := s.Keys().CurrentSEK()`, then `vdcrypt.NewKeyFile(..., KeyFileParams{Kind: KindEscrow, RecipientPub: pub, Server: &server}, bundle)`.
  - Open escrow files with `OpenKeyFile(ctx, kf, keys.Audit{Purpose: keys.PurposeRewrap|..., RepoID, UserID, ActorUserID/ActorDeviceID, OperationID})`.
  - `keysErr(err)` maps `ErrUnavailable` to 409 `escrow_unavailable`.
  - Call `implementFeature(wire.FeatureEscrow)` when P1-13 lands; P1-12 does not.
- **INT**:
  - Production images without `install.sh` (no KEK) start degraded and log a warning.
  - `VAULTDOCK_DEV=true` auto-generates a KEK on an empty database.
  - The `TestBackupJobsCRUDGatingOwnDevice` failure (above) is unrelated to this task and needs its owner.
