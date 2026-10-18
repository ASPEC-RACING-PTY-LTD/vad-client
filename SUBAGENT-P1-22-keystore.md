# P1-22 (DSK-D) desktop keystore
Status: completed

## Goal
New package `desktop/internal/keystore`:
- the X25519 device box key, sealed with machine DPAPI under `%ProgramData%\VaultDock\keys`;
- the SIK pin (TOFU), checked as described in api-v1 §14 rev 1.1b;
- first-device repo init that returns the `VDRK1-` text once;
- `OpenRepo`;
- `HandleTask` for `key_share` and `rewrap`;
- tests.

This resumes the stopped attempt and implements its design, with the rev 1.1b resolutions applied.

## Changes (repo: path, created/modified/deleted, why)
All in the client repo `VaultDock`, all created:
- `desktop/internal/keystore/keystore.go`:
  - `Options`, `Keystore` and `Open`;
  - the `Protector` interface and its default `MachineDPAPI`;
  - the box key file `box.key`, sealed by DPAPI with entropy `"VaultDock box key v1"`;
  - `writeFileAtomic`: a temp file created 0600, then fsync, then rename. There is no ACL code; the installer ACL is relied on.
- `desktop/internal/keystore/pin.go`:
  - `Fingerprint`, `Identity` and `ParseIdentity`;
  - `sik_pin.json`, which holds `{fingerprint, sik_pub, pinned_at}`;
  - `PinServerIdentity` (TOFU), `Repin`, `Verify`, `Pinned`;
  - `MismatchError`.
- `desktop/internal/keystore/repo.go`: `InitParams`, `InitRepo`, the redacting `RecoveryKey` type, `CheckRecoveryKey` and `OpenRepo` (which verifies escrow key files against the pinned SIK).
- `desktop/internal/keystore/tasks.go`: `HandleTask`, which covers `key_share` under full escrow (confirm the wrap), `key_share` under metadata escrow (write the wrap, idempotent) and `rewrap`.
- `desktop/internal/keystore/keystore_test.go`: tests with a fake protector, a fake server identity and `repo.MemBackend`.
- `desktop/internal/keystore/dpapi_windows_test.go`: a real machine-DPAPI round trip, skipped if DPAPI is unavailable.

## Interfaces exposed
```go
package keystore

const BoxKeyFile = "box.key"; const PinFile = "sik_pin.json"
var ErrClosed, ErrBoxKey, ErrNoBackend, ErrNotPinned /* = client.ErrNoSIKPin */, ErrBadIdentity, ErrPinFile,
    ErrRecoveryMismatch, ErrUnsupportedTask, ErrTaskPayload, ErrTargetRevoked error

type Protector interface {
    Protect(data, entropy []byte) ([]byte, error)
    Unprotect(data, entropy []byte) ([]byte, error)
}
type MachineDPAPI struct{} // platform.ProtectMachine / UnprotectMachine

type Options struct {
    Dir       string    // "" = platform.KeysDir()
    Protector Protector // nil = MachineDPAPI{}
    Backend   func(ctx context.Context, repoID int64) (repo.Backend, error) // for HandleTask
    Revoked   func(ctx context.Context) (map[int64]bool, error)            // device's view of revoked device ids; nil = none
    Rand      io.Reader; Now func() time.Time; Logger *slog.Logger
}
func Open(opts Options) (*Keystore, error) // loads or creates box.key, loads pin
func (ks *Keystore) Close()                 // wipes box private key
func (ks *Keystore) BoxPub() vdcrypt.Key
func (ks *Keystore) BoxPubB64() string      // std base64, for wire.EnrolRequest.BoxPub

func Fingerprint(sikPub ed25519.PublicKey) string // "ABCD-...-WXYZ" (8x4 uppercase hex of SHA-256[:16])
type Identity struct { SIKPub ed25519.PublicKey; Fingerprint string; SEKPub vdcrypt.Key; SEKVersion uint32; SEKSig []byte }
func ParseIdentity(id *wire.ServerIdentity) (*Identity, error)
type MismatchError struct { Pinned, Got string } // Unwrap() -> *client.APIError{409, sik_mismatch}
func (ks *Keystore) PinServerIdentity(id *wire.ServerIdentity) (*Identity, error) // TOFU; differing pin -> *MismatchError
func (ks *Keystore) Repin(id *wire.ServerIdentity) (*Identity, error)             // explicit replace (UI + admin confirm)
func (ks *Keystore) Verify(id *wire.ServerIdentity) (*Identity, error)
func (ks *Keystore) Pinned() string

type InitParams struct {
    Backend repo.Backend; RepoID vdcrypt.RepoID; EscrowMode string
    DeviceID int64; SignPub ed25519.PublicKey; Label string; Server *wire.ServerIdentity
}
func (ks *Keystore) InitRepo(ctx context.Context, p InitParams) (*repo.Repo, RecoveryKey, error)
type RecoveryKey struct{ /* unexported */ } // Text(), Wipe(); String/GoString/Format/LogValue/MarshalText redact
func CheckRecoveryKey(r *repo.Repo, text string) error // ConfirmRecoveryKey
func (ks *Keystore) OpenRepo(ctx context.Context, be repo.Backend) (*repo.Repo, error)

func (ks *Keystore) HandleTask(ctx context.Context, t wire.DeviceTask) (any, error) // result is always client.KeyTaskResult
```

`HandleTask` semantics:
- **`key_share {repo_id, key_file_oid}` (full escrow).** Fetches the key file and checks that it is a `device` wrap to our `BoxPub` and that it opens. Returns `{repo_id, epoch, key_file_oid}`.
- **`key_share {repo_id, target_device_id, target_box_pub, target_sign_pub}` (metadata escrow).**
  - Validates the 32-byte keys and refuses a revoked target.
  - Runs `OpenRepo`, then `AddRecipient(DeviceRecipient(box, id, sign, "device <id>"))`.
  - Is idempotent: if the current epoch already has a wrap for that device id and box pub, it returns that oid.
  - Fails with `repo.ErrStaleEpoch` if this device does not hold the current epoch.
- **`rewrap {repo_id, epoch}`.**
  - If the repo is already at or past `epoch`, it reports done.
  - If it is at `epoch-1`, it rotates and excludes the device key files of revoked device ids (never our own box key).
  - Any other epoch gap is an error.
  - A concurrent rotation that shows up as `ErrStaleEpoch` is retried once.
- A payload carrying both shapes, or neither, is rejected. So are unknown fields and kinds other than these two.
- On failure the result's `Error` field is set and `err` is non-nil, so the result can be passed straight to `client.FinishKeyShare` or `client.FinishRewrap`.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l ./internal/keystore`: clean.
- `go build ./...`: OK.
- `go vet ./internal/keystore/...`: OK.
- `go test -count=1 ./internal/keystore/...`: ok. All 8 tests pass, including the real machine-DPAPI test on this host.
- `GOOS=linux go vet ./internal/keystore/...`: OK.

## Contract issues
None new. Rev 1.1b resolved all four issues from the previous attempt.

`Verify` compares the fingerprint the way §14 specifies: case-insensitive, ignoring `:`, `-` and spaces. It does this through `client.CheckSIKPin`, which already normalises those characters.

## Gaps / follow-ups
- **The box key is created by check-then-rename, which is not an exclusive create.** Two processes creating it at the same instant could race. Only the service should create it.
- **The `Stale` and `rewrap` paths are not coordinated across devices** beyond the single retry.
- **The recovery key text stays in a Go string, which cannot be zeroed.** `RecoveryKey.Wipe` only drops the reference. The text is never written to disk or logged (tested).
- **`InitRepo` skips a post-init `VerifyEscrow`.** The escrow recipient is built from an identity already matched to the pin, with its SEK signature checked. Failing after `repo.Init` would lose the only copy of the recovery text.
- **`confirmShare` (full escrow) only unwraps the one key file.** It does not open the whole repo, to avoid loading the index.

## Notes for dependent tasks / INT
- **P1-26 and the agent host** should:
  - build `keystore.Open(Options{Backend: <remote repo backend for repo id>, Revoked: <local revoked-device view>})` once in the service;
  - send `ks.BoxPubB64()` in the enrolment request;
  - call `ks.PinServerIdentity(status.ServerIdentity)` when enrolment is approved;
  - call `ks.Verify(id)` before key operations;
  - on `client.IsCode(err, wire.CodeSIKMismatch)`, stop backup until `Repin`.
- Route `key_share` and `rewrap` tasks to `ks.HandleTask`, and report the returned `client.KeyTaskResult` with `FinishKeyShare` or `FinishRewrap`.
- The UI shows `RecoveryKey.Text()` once after `InitRepo`, then calls `CheckRecoveryKey` for confirmation. Never log or persist the text.
- The installer must set the ACL on `%ProgramData%\VaultDock\keys` to SYSTEM and Administrators only. The machine-DPAPI blob is decryptable by any process on the machine.
- No new dependencies, and no edits outside `desktop/internal/keystore/`.
