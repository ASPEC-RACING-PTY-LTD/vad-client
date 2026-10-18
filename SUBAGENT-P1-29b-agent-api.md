# P1-29b Agent API for the desktop Simple screens
Status: completed (by INT, after the sub-agent stopped at design)

## Goal
Add `SaveBackupJob`, `SkipIssues`, `GiveConsent` and `GetMode` to the agent API (Host, InProc and `ipc.Client`, with authz entries), and wire them into the desktop app.

## Changes
- **VaultDock `desktop/internal/agent`:**
  - `api.go`: the four methods were added to `API`, and the reserved list was updated.
  - `api_backup.go`: `SkipIssuesRequest{JobID, IssueIDs, Classes}`.
  - `authz.go`: `SkipIssues` is `AccessUser`.
  - `inproc.go`: forwarders.
  - `host_backup.go`:
    - `BackupServer` gained `CreateJob`, `UpdateJob`, `RunIssues` and `SkipIssues`;
    - the new `profileManager(c)` helper is shared with `consentTask`.
  - `host_backup_jobs.go` (new): the implementations.
  - `host_backup_test.go`: fake server methods.
  - `host_backup_jobs_test.go` (new): tests.
- **`desktop/internal/ipc`:** `methods.go` handlers and `client.go` methods.
- **Desktop app (`desktop/`):**
  - `app_mode.go`: `GetModeState` asks the agent's `GetMode` first.
  - `app_backup.go`: `App.SkipIssues`, and `BackupJobDraft.Revision` is passed to updates.
  - `frontend/src/screens/Issues.tsx`: an "Ignore" action per reported issue.
  - `frontend/src/screens/Backups.tsx`: sends the revision when editing.
  - `frontend/wailsjs/go/main/App.{js,d.ts}` and `models.ts`: hand-updated to the generator's format.
- **VaultDock `sdk/client/jobs.go`:** `RunIssues` and `SkipIssues`.
- **vad-server:**
  - `internal/api/backup_jobs.go`: `GET /backup/runs/{id}/issues`, `…/issues/skip` and `…/retry-skipped` now accept device signatures (`rt.User`), limited to the device's own runs through `loadRun`.
  - `backup_jobs_test.go`: device list allowed, and a foreign device gets 404 on list and skip.
- **Contract:** api-v1 §14 Rev 1.1b gains an "Issue routes" bullet.

## Behaviour
- **`SaveBackupJob`:**
  - Validation:
    - name: 1–200 characters;
    - kind: `folder` or `profile`;
    - mode: `full` or `data_only`;
    - paths: 1–1000, each a token path or absolute.
  - **Profile:** a pipe user always gets their own SID; trusted callers keep the requested one.
  - **Path checks:** pipe users' paths are resolved for their profile, then checked through guard and a read probe as the caller.
  - **New job:** the repository comes from the local first-run record; with none, the call is invalid.
  - **Update:** the job must be visible to the caller and is sent with If-Match revision; a 409 maps to `conflict`.
  - **Afterwards:** jobs re-sync and the scheduler reloads.
- **`SkipIssues`:**
  - Local ids are mapped to server issue ids by path and wire class, on each id's own registered run.
  - Classes apply to the job's latest registered run.
  - Another profile's job gives `not_found`.
- **`GiveConsent`:**
  - The caller SID comes from IPC, or from the current user in-process; a SYSTEM caller is refused.
  - It then calls `RecordConsent` and `Report`, and maps the profile errors.
- **`GetMode`:** returns `Modules().Snapshot()`, or `unavailable`.

## Also fixed (found in verification)
`desktop/internal/backup/store.go`: `Runs` and `RunByServerID` ordered same-millisecond runs by a random uuid, so `TestRunnerCommitsSnapshot` was flaky and the parent after a restart could be wrong.
- They now order by `started_at DESC, rowid DESC`.
- `PutRun` is an upsert, which keeps the rowid.

## Commands run and results
All of these pass:
- desktop: `go build ./...`, `GOOS=linux go build ./...`, `go vet ./...`, `go test -count=1 ./...`;
- desktop/e2e: `go vet ./...`, `go test ./...`;
- frontend: `npm run build`;
- sdk: `go test ./...`;
- vad-server: `go vet ./...`, `go test ./...`, `-tags integration ./tests/...`.

## Gaps / follow-ups
- Moving `modules` and `backup` into `bridgedKinds` (replacing the extra `uiWatch` subscription) was not done. Behaviour is unchanged.
- The `jobSaver` and `consentGiver` type assertions in `app_backup.go`/`app_wizard.go` are now always satisfied. They can be simplified to direct calls.
- Not run in the real Wails app or against an installed service.
