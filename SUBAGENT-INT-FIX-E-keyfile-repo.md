# INT-FIX-E Reject key files of another repository + testkit password
Status: completed

## Goal
- The server refuses to store a `keys` object that is not a key file of the target repository.
- `keystore.InitRepo` refuses a zero `RepoID`.
- `testkit` re-exports the test admin password, so the e2e test no longer hard-codes it.

## Changes (repo: path, created/modified/deleted, why)
- **vad-server: `internal/api/backup_objects.go` (modified)**
  - On `PUT /repos/{id}/objects/keys/{oid}`, a `Content-Length` above `maxKeyFileBytes` (64 KiB, the existing projection cap in `repo_keys.go`) is rejected early with `413 too_large`. Using the same cap means every accepted key file can also be projected.
  - New `checkKeyFile` handles the accepted body in this order:
    1. Buffers the body. A short body returns `400 body_mismatch`.
    2. Verifies the signed hash. A mismatch returns `400 body_mismatch`.
    3. Checks the oid against the hash. A mismatch returns `422 object_hash_mismatch`, so existing behaviour and tests are unchanged.
    4. Decodes the plaintext header with `vdcrypt.DecodeKeyFile`, without unwrapping. A failure returns `422 validation` with `fields.body`.
    5. Compares `ParseRepoID(kf.Repo)` with `ParseRepoID(rp.RepoUUID)`. A mismatch returns `422 validation` with `fields.repo`.
  - It then hands a fresh hashing `streamedBody` over the buffer to `putObject`, so quota, idempotency and storage work as before. The header comment is updated.
- **vad-server: `internal/api/backup_objects_test.go` (modified)**
  - New `TestBackupKeyFilePutChecksRepo` covers:
    - another repository's key file returns 422 `repo` and nothing is recorded;
    - a body that is not a key file returns 422 `body`;
    - a body over 64 KiB returns 413;
    - the repository's own key file returns 201, a replay returns 200, and the stored content matches.
- **vad-server: `testkit/testkit.go` (modified):** adds `const Password = testserver.Password`.
- **VaultDock: `desktop/internal/keystore/repo.go` (modified)**
  - New `ErrNoRepoID`, returned before any server or backend work when `InitParams.RepoID` is zero.
  - The `RepoID` field comment now says it is required.
- **VaultDock: `desktop/internal/keystore/keystore_test.go` (modified)**
  - New `newRepoID(t)` helper (random UUIDv4).
  - `initRepo` and the wrong-server case now pass a `RepoID`.
  - New case: a zero `RepoID` returns `ErrNoRepoID`, and no config is written.
- **VaultDock: `desktop/e2e/backup_e2e_test.go` (modified):** the local `adminPassword` constant is removed and `testkit.Password` is used instead.

## Interfaces exposed
- `testkit.Password` (untyped string const, equal to `testserver.Password`).
- `keystore.ErrNoRepoID` (`error`). `InitRepo` returns it, unwrapped, when `p.RepoID == (vdcrypt.RepoID{})`.
- `PUT /api/v1/repos/{id}/objects/keys/{oid}` errors:
  - 413 `too_large` when the body is over 65536 bytes;
  - 422 `validation` with `details.fields.body` when the body is not a key file;
  - 422 `validation` with `details.fields.repo` when it is a key file of another repository.

## Commands run and results
- vad-server:
  - `go build ./...`: ok.
  - `go vet ./internal/api/ ./testkit/`: ok.
  - `go test ./internal/... ./tests/...`: all ok.
- desktop:
  - `go build ./...`: ok.
  - `go vet ./internal/keystore/`: ok.
  - `go test ./internal/keystore/...`: ok.
- desktop/e2e:
  - `go vet ./...`: ok.
  - `go test ./... -run Backup`: ok.
- `gofmt -l` on all touched files: clean. It does list other files I did not touch: `engine_e2e_test.go` in e2e, and several vad-server `internal/api` files including the frozen `api_test.go`. I left them alone.

## Contract issues
- None. api-v1 doesn't list the key-file checks among the object PUT errors; they use the generic 422 `validation` and 413 `too_large` shapes.

## Gaps / follow-ups
- Key file PUTs are now buffered (≤64 KiB) instead of streamed. Other kinds still stream.

## Notes for dependent tasks / INT
- `desktop/internal/agent/host_backup_api.go` already passes `RepoID: ParseRepoID(target.RepoUUID)`, so it is unaffected. Any other `InitRepo` caller must pass `RepoID` now.
- Any client that wrote placeholder or non-key-file bytes under kind `keys` will now get 422. In-tree tests all pass.
