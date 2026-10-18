# P1-29 (DSK-I) Desktop first-run, Simple screens, module-gated nav
Status: partial

The UI, the Go bindings and the generated Wails bindings are done, and the build passes. Three steps can't finish end to end: the agent API has no method for them yet, and those methods are in `internal/agent` or `internal/ipc`, which this task does not own. The steps are:
- saving a job;
- recording profile consent;
- an authoritative GetMode.

The UI already calls these methods through optional interfaces (see Gaps). They start working, with no UI change, once the agent adds them.

## Goal
Add to the desktop app:
- a first-run wizard: connect → SIK fingerprint pin → create the repository (escrow default `full`) → show the recovery key once → re-type it to confirm → profile consent;
- the Simple screens: folder picker, schedule editor, backups list with Run now, skipped files with Retry Skipped, and a readiness summary;
- navigation gated by modules, with the mode shown.

## Changes (repo: path, created/modified/deleted, why)
VaultDock `desktop/`:
- **`app_backup.go`** (created): backup forwarders and folder tools.
  - `uiError`: an agent error is returned as JSON `{"code","message"}`, so the UI can tell the codes apart.
  - Forwarders: `KeyStatus`, `ListBackupJobs`, `ListBackupRuns`, `ListIssues`, `RunBackup`, `RetrySkipped`, `Readiness`.
  - `SaveBackupJob(BackupJobDraft)`:
    - validates the draft;
    - builds a `wire.Job` with kind folder, mode full, VSS auto and `profile_sid` = the caller's SID;
    - calls the agent only if it implements the reserved `SaveBackupJob`. Otherwise it returns `not_implemented` with a message pointing to the web UI.
  - Folder tools (UI process, as the user):
    - `KnownFolders` and `EncodeFolder` (picker path → token path) both use `backup.OSFolders`, the resolver the pipeline uses, so folder redirection is handled the same way. `EncodeFolder` refuses paths that no token covers.
    - `ResolveFolder` turns a token path back into a folder.
- **`app_wizard.go`** (created): first-run calls.
  - `ConfirmServerIdentity`, `InitRepository(escrowMode)` (empty means `full`) and `ConfirmRecoveryKey`.
  - `GetConsentStatus` and `GiveProfileConsent`. The second creates the DPAPI proof with `profiles.CreateConsentProof(decimal device id, own SID)` in the user's process, then calls the reserved agent `GiveConsent(ctx, proof)` if it exists. The recovery key is never stored or logged; it only passes through the return value.
- **`app_mode.go`** (created): mode state and texts.
  - `GetModeState() ModeView`, from one of these sources, in order:
    1. the agent `modules` event (authoritative);
    2. `config\mode.json` plus `sdk/modules.ModulesForMode`. In-process only the per-user DataDir copy is read; with the service, ProgramData is tried first, then DataDir;
    3. `unknown`, in which case the UI hides nothing.
  - `ModeTexts()` returns the P1-31 texts, embedded with `go:embed` from `build/windows/installer/modes/*.en.txt` (single source of truth).
  - A lazily started second agent subscription forwards `modules` → Wails `"modules"` and `backup` → Wails `"backup"`. The P0-29 bridge lives in `app.go`, which this task does not own.
- **`frontend/wailsjs/go/main/App.{d.ts,js}` and `frontend/wailsjs/go/models.ts`**: regenerated with the real generator:
  - command: `GOPROXY=off go run github.com/wailsapp/wails/v2/cmd/wails generate module` (v2.16.0 from the module cache, offline; no global install);
  - the diff only adds entries; the existing (P0-29 hand-edited) bindings were byte-identical;
  - `go.mod` and `go.sum` are unchanged.
- **`frontend/src/`** (all created except `App.tsx`):
  - `api.ts`: `apiError` and `apiMessage`.
    - `forbidden` → "needs a Windows administrator".
    - `sik_mismatch` and `identity_unpinned` get friendly text.
    - `access_denied`, `updates_managed` and all other codes are shown verbatim.
  - `modules/registry.ts`: `ScreenId`, `screenRequires`, `enabledModules`, `screenVisible` and `modeLabel`.
  - `modules/useMode.ts`: a hook over `GetModeState` and the `modules` event.
  - `screens/FirstRun.tsx`: `needsFirstRun`, `formatFingerprint` and the wizard. Its steps are identity → repository → recovery → confirm → consent → done.
    - **Identity:** shows the server and pinned fingerprints, with an "I compared" checkbox.
    - **Repository:** escrow radios, default full.
    - **Recovery key:** shown once, kept only in component state and cleared after confirmation. It needs a "written down" checkbox. Paste is blocked in the confirmation field.
    - **Skipping:** "Later" is offered except while a just-created key is outstanding.
  - `screens/Backups.tsx`: jobs with state badges, paths, schedule, last run, Run now, Retry skipped (after a partial or skipped run) and Edit. A row expands to the last 10 runs. A modal job editor uses the folder picker and schedule editor.
  - `screens/Issues.tsx`: skipped files grouped by job, a job filter and "Retry skipped files".
  - `screens/Readiness.tsx`: stats, readiness items with fix actions, the key state, and a refresh on the `backup` event.
  - `screens/Mode.tsx`: `ModeScreen` (mode, modules, texts) and `ModeInfo` (ⓘ dialogs on the first-run done step).
  - `screens/backup/`:
    - `FolderPicker.tsx`: known-folder chips plus "Choose folder…" → `EncodeFolder`; also `displayPath`;
    - `ScheduleEditor.tsx`: daily, weekly (ISO weekdays), interval (1–12 h, jitter 0) or manual, plus catch-up;
    - `schedule.ts`: default daily 12:30, `run_once`, jitter 10, local IANA timezone; also `describeSchedule` and `scheduleError`;
    - `labels.ts`: run, trigger, issue-class and job-state labels.
  - `App.tsx` (modified):
    - **Wizard gate:** `KeyStatus` decides whether the first-run wizard shows after connect. It also runs on the `backup` event and from Readiness → Fix.
    - **Nav gate:** Readiness, Backups and Skipped files need `core` and an available backup stack. Overview, Sync folders and Transfers need `sync`. Automations needs `automation`. Mode and Settings are always shown.
    - **Mode badge:** in the sidebar.
    - **Sync-only header:** the sync status and Pause are shown only when `sync` is enabled.
    - **Default screen:** Overview if sync is on, else the first visible screen.

## Interfaces exposed
Go (Wails-bound on `*App`):
```go
KeyStatus() (agent.KeyStatus, error); ListBackupJobs() ([]agent.BackupJob, error)
ListBackupRuns(jobID int64) ([]agent.BackupRun, error); ListIssues(q agent.IssueQuery) ([]agent.BackupIssue, error)
RunBackup(jobID int64) (int64, error); RetrySkipped(jobID int64) (int64, error); Readiness() (agent.BackupReadiness, error)
SaveBackupJob(d BackupJobDraft) (agent.BackupJob, error)
KnownFolders() ([]KnownFolder, error); EncodeFolder(path string) (string, error); ResolveFolder(tokenPath string) (string, error)
ConfirmServerIdentity(fp string) (agent.KeyStatus, error); InitRepository(escrowMode string) (agent.InitRepositoryResult, error)
ConfirmRecoveryKey(text string) error; GetConsentStatus() ConsentStatus; GiveProfileConsent() error
GetModeState() ModeView; ModeTexts() map[string]string
type BackupJobDraft struct{ ID int64; Name string; Paths, Exclude []string; Schedule wire.JobSchedule; Enabled bool } // json id,name,paths,exclude,schedule,enabled
type KnownFolder struct{ Token, Path string; Exists bool }
type ConsentStatus struct{ Supported bool; User string }
type ModeView struct{ Source, InstallMode string; Modules []modules.ModuleInfo; PendingRequest *modules.ModeRequest } // source: agent|mode_file|unknown
```
Optional agent methods that the UI detects by type assertion on `agent.API`:
```go
SaveBackupJob(ctx context.Context, j wire.Job) (agent.BackupJob, error) // j.ID==0 → create
GiveConsent(ctx context.Context, proof []byte) error
```
- **Errors:** every binding above returns errors as the JSON text of `agent.Error`.
- **Wails events:** `"modules"` (`ModeView`) and `"backup"` (`agent.KeyStatus`).

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok.
- `go vet .`: ok.
- `go vet -tags bindings .`: ok.
- `GOOS=linux go vet .`: ok.
- `go test .`: no test files.
- `gofmt -l app_backup.go app_wizard.go app_mode.go`: clean.
- `GOPROXY=off go run github.com/wailsapp/wails/v2/cmd/wails generate module`: ok. It printed "Not found: wire.JobSpec", so `BackupJob.spec` is typed `any` in `models.ts`; the UI uses a local `JobSpec` type.
- `frontend: npm run build` (`tsc -b && vite build`): ok. The frontend has no test runner or lint script.
- Not run: the Wails GUI, and the service-mode path, which needs an installed service.

## Contract issues
None. These are internal agent and UI interfaces.

## Gaps / follow-ups
1. **Saving jobs is not possible yet.** `agent.API` has no `SaveBackupJob` (reserved), so the folder picker and schedule editor produce a validated draft, but saving answers `not_implemented` ("create the job in the web interface"). The agent and IPC owners should add it (see INT notes).
2. **Profile consent is not possible yet.** There is no `GiveConsent` (P1-23 suggested `RecordProfileConsent`). The wizard shows "Later", and the proof creation is already wired.
3. **No authoritative GetMode.** There is no `GetMode`/`ListModules`. Until the agent publishes its first `modules` event, the nav is gated from `mode.json` and that mode's default modules. Modules toggled one by one would be missed.
4. **Skip is not implemented.** The "Skip" action on issues (server `…/issues/skip`, or the reserved `ResolveIssue`) has no agent method, so the Issues screen offers only Retry Skipped.
5. **Mode switching is not offered.** It needs `SetMode` plus UAC. The Mode screen is read-only and shows any pending server mode request.
6. **Forbidden steps.** An identity or repository step refused with `forbidden` shows "needs a Windows administrator". A UAC relaunch is not implemented.
7. **No Go unit tests.** `app_test.go` is not an owned path.

## Notes for dependent tasks / INT
- **Agent/IPC owner:** add these methods; the UI picks them up automatically.
  - `SaveBackupJob(ctx, wire.Job) (BackupJob, error)`: user access. It must force `profile_sid` from the caller (ignore the body), create or patch through `client.CreateJob`/`UpdateJob`, then re-sync the jobs.
  - `GiveConsent(ctx, proof []byte) error`: user access, using the caller SID from `ipc.CallerFromContext`, then `Manager.RecordConsent` and `Report`.
  - `GetMode` returning `modules.ModulesState`. Once it exists, `GetModeState` should call it before the mode-file fallback.
  - `ipc.Client` must implement the same signatures for the type assertions to succeed over the pipe.
- **`app.go` owner:** consider moving the `modules` and `backup` kinds into `bridgedKinds` and dropping the `uiWatch` subscription in `app_mode.go`.
- **Error format:** only the new bindings use the JSON error format. Old screens still show the plain message, which is verbatim for `access_denied` and `updates_managed`.
- **Built assets:** `frontend/dist` was rebuilt by `npm run build`.
- **Dependencies:** none added. `package.json`, `go.mod` and `go.sum` are unchanged.
