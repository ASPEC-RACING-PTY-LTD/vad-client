# P1-36 (SRV-ITEST) E2E: enrol (SIK pin) → first-device keys → backup → incremental → second-device key share → catalog browse
Status: completed

## Goal
Add end-to-end tests for the Phase 1 backup flow against a real in-process server:
- SDK-level flows in `vad-server/tests/integration`;
- the desktop engine flow (keystore, pipeline, runner and task loop) in `VaultDock/desktop/e2e`.

## Changes (repo: path, created/modified/deleted, why)
vad-server (all files are new, with no build tag):
- `tests/integration/sdk_helpers_test.go` (created): shared helpers.
  - `enrolKeyDevice` enrols with `box_pub` and approves through the testserver admin session.
  - `checkIdentity` recomputes the SIK fingerprint independently and verifies the SEK signature.
  - `initRepo` runs `CreateRepo` (with `escrow_mode`), then `repo.Init` over `sdk/repo/backend/server`.
  - `snapWriter` writes real snapshots as the pipeline does: data and meta packs, trees, `FlushIndex` and a signed root. It deduplicates against the index and tracks blob→pack, so commits list every referenced pack.
  - Other helpers: `restoreAll` (verifies the signature, then hash and size per file), `waitIngest`, `reportConsent`, `keyTask`.
- `tests/integration/keys_test.go` (created):
  - `TestKeysEnrolPinAndKeyShare`, full escrow:
    - `server_identity` comes back on the approved poll, and the stored SIK pin and `box_pub` are checked;
    - `VerifyServerIdentity` accepts the right pin and returns `sik_mismatch` for a wrong one;
    - after init, key status shows 3 recipients, recovery wrapped and escrow full, and the recovery key opens the repo;
    - device B gets `key_share {key_file_oid}`, opens the repo with its box key, derives identical repo keys, and finishes the task (4 recipients).
  - `TestKeysMetadataEscrowShare`, metadata escrow:
    - the key holder A gets `key_share {target_device_id, target_box_pub, target_sign_pub}` for device C;
    - A runs `AddRecipient` and `FinishKeyShare`;
    - C opens the repo with full keys.
- `tests/integration/backup_test.go` (created): `TestBackupIncrementalSecondDeviceCatalog`.
  - A full backup is committed; a replay is idempotent (200, same id).
  - The catalog ingests it, and browse (roots, `{DOCUMENTS}`, a nested dir) and search work.
  - An incremental backup uploads only the 2 new blobs, reuses the first packs and links `parent_id`. The list is newest first, and search finds the new file only in snapshot 2.
  - Device B gets the keys through `key_share` and restores both snapshots byte for byte.

VaultDock:
- `desktop/e2e/backup_e2e_test.go` (created): `TestBackupEngineEndToEnd`.
  - **Setup.** It uses `keystore.Open`, with a pass-through `Protector`, a temp dir, and `Backend` = the server backend.
  - **Enrolment and pin.** It enrols with `ks.BoxPubB64()`, then `PinServerIdentity`.
  - **First-device keys.** `Verify` checks the pinned identity, then `InitRepo` initialises the repository and `CheckRecoveryKey` passes.
  - **Job.** The device creates the job, and `SyncFromServer` copies it into a temp `backup.Store`.
  - **Engine.** The pipeline uses a fake token provider (`Impersonate` simply runs fn), an allow-all guard, real `OSFS`/`OSOpener` on a temp `Documents` dir, and a fake `{DOCUMENTS}` folder resolver. It is wired as `RepoEngines` → `Runner` → `TaskLoop`.
  - **First run.** It is triggered by a `backup_now` task created through the session API. The run completes (server run `completed`) and excludes `*.tmp`. The catalog ingests the snapshot and browse works.
  - **Incremental run.** `BytesNew` equals only the changed and new bytes, `parent_id` is linked, and search works.
  - **Second device.** A second agent's task loop completes `key_share` through `keystore.HandleTask`. It then runs `ks.OpenRepo` and restores the latest snapshot. The restore verifies the root signature (`Device.KeyID` set), the parent oid, and file content equal to disk.
  - `TestBackupAsSystemSkipped` is skipped with an explicit message. Real tokens (WTS/S4U), machine DPAPI, the path guard and VSS need the LocalSystem service.

No production code was edited. No dependencies were added.

## Interfaces exposed
None. All files are tests.

## Commands run and results
In `D:/VaultDock/vad-server`:

| Command | Result |
|---|---|
| `go vet ./tests/...` | ok |
| `go vet -tags integration ./tests/...` | ok |
| `go test ./tests/integration/... -run 'Backup\|Keys' -count=3` | ok, 3 tests pass (about 0.15–0.3 s each) |
| `go test -tags integration ./tests/... -count=1` | ok (the frozen Phase tests skip without `VAULTDOCK_URL`) |
| `gofmt -l` on my files | clean |

In `D:/VaultDock/VaultDock/desktop/e2e`:

| Command | Result |
|---|---|
| `go vet ./...` | ok |
| `go test ./... -run Backup -count=3 -v` | `TestBackupEngineEndToEnd` passes (about 0.5 s); `TestBackupAsSystemSkipped` skips |
| `gofmt -l backup_e2e_test.go` | clean |

## Contract issues
None.

## Gaps / follow-ups
- **Not exercised:**
  - the SYSTEM-only pieces: real token provider, machine DPAPI, guard and VSS;
  - rewrap/rotation through the engine (P1-13's own tests cover it on the server side);
  - restore through the desktop restore path (no such package yet).
- **Gating.** The new server tests have no build tag, so they run in both CI steps (`go test ./...` and `-tags integration ./tests/...`). The frozen `integration_test.go` is the only file there that needs `VAULTDOCK_URL`.

## Notes for dependent tasks / INT
- **testkit.** `vad-server/testkit` does not re-export `testserver.Password`. `backup_e2e_test.go` hard-codes `"test-password-123"` (`adminPassword`). Suggested fix: add `const Password = testserver.Password` to `testkit/testkit.go`, then use it.
- **Key files of another repository are accepted.**
  - While writing the e2e test I first called `keystore.InitRepo` without `RepoID`, so it generated a fresh random repo id.
  - The server accepted the three key-file PUTs: `POST /repos/{id}/objects/keys/...` does not check the key file's repo id against `repo_uuid`. Projection then skipped them with only a WARN (`key file of another repository`), and key status showed 0 recipients.
  - This is not a test blocker, since callers must pass `RepoID: ParseRepoID(repo_uuid)`. It is a usability trap, though:
    - the agent host wiring should always pass `RepoID`;
    - `keystore.InitParams` could require a non-zero `RepoID` when the backend is a server backend;
    - the server could reject mismatching key files at PUT with `422`.
- **Pre-existing gofmt drift.** `gofmt -l` flags `vad-server/tests/integration/integration_test.go` (frozen) and `desktop/e2e/engine_e2e_test.go`. Neither is mine, and neither was touched.
- **Fixture coupling.** The e2e test reads only exported APIs of `desktop/internal/{backup,keystore,tokens,guard}`. If the concurrent snapshot-id persistence work renames `backup.Run` fields (`SnapshotID`, `RunID`, `FinishedAt`, `Stats`) or `Store.Runs`, update this test.
