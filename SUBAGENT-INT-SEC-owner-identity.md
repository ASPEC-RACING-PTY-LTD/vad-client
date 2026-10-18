# INT-SEC Sync folders read as their owner, not SYSTEM
Status: completed (by INT, directly)

## Problem
Two security follow-ups from Phase 1 (`IMPLEMENTATION_STATUS.md`):
- In the agent service the sync engine scanned, stat-ed and opened files as SYSTEM. A subfolder with tighter ACLs under a folder the user could read was still read and uploaded with SYSTEM's rights (MASTER_PLAN §7.8, C7).
- Sync configurations were machine-global: any pipe user could list, edit, delete, trigger and watch every user's folders, queue and history.

## Design
- **Owner per configuration.** `config.SyncConfig.OwnerSID` (`owner_sid`). The agent sets it:
  - **pipe client:** always the caller's SID; whatever owner the client sends is ignored;
  - **in-process or SYSTEM caller:** the requested owner if it is a user SID, otherwise the stored owner on update;
  - **per-user app:** the current user, so the folder keeps its owner when moved to the service.
- **Engine owner identity** (`engine/identity.go`).
  - `Engine.SetTokens(p)` turns enforcement on; the host does this only in service mode.
  - Every file-system read for a configuration runs inside `asOwner`, impersonating `OwnerSID` with a `tokens.Token`.
  - One token per owner is cached for 5 minutes (`ownerTokenTTL`), so an S4U token is not minted per file and a session token replaces it soon after logon. Retired tokens close when their last user releases them; `Stop` closes them all.
  - With enforcement on, a configuration without an owner is never read (`ErrNoOwner`). A missing token gives `*OwnerError`. Both show in the folder's status text.
- **Only file-system work under the token.** Impersonated code must not log, touch `state.db` or do network I/O (`tokens.Token.Impersonate`).
  - **Scan:** split into `collect` (walk, filter, index compare, read probe, deleted-file check; under the token) and the processing step (issues, logging, automations, queueing; agent identity).
  - **Preview:** takes a `run` callback for the walk only. Automations and the `state.db` index are evaluated afterwards; before this, the host ran the whole `Preview`, including `state.db` reads, while impersonating.
  - **Upload:** both `Stat` calls and the open run as the owner. Hashing and uploading read the already-open handle without the token. With no owner token, the item is deferred one minute and the file is not opened.
  - **Watcher:** each changed path is stat-ed as the owner. A path the owner cannot see is ignored, and so is a deletion check.
- **Automation chains:** "start task" only starts configurations with the same owner. Names are now unique per owner, not machine-wide, so the duplicate-name error no longer reveals other users' folders.
- **Owner assignment at service start** (`Host.assignOwners`).
  - A configuration with no owner gets the user whose profile folder contains all of its paths.
  - Anything else stays unowned and is not synchronised until a user saves it again.
  - Nothing consumes the C5 migration marker yet, so unowned configurations only exist in development installs.
- **Pipe scope** (`agent/host_owner.go`). A pipe client sees and acts on only the configurations it owns; in-process and SYSTEM callers see all.
  - `ListConfigs`, `ListQueue`, `ListHistory` and `RetryFailed` are filtered. They use new state queries `ItemsIn`, `HistoryIn` and `RetryFailedIn`, which run `config_id IN (...)` in SQL.
  - `SaveConfig`, `DeleteConfig`, `SyncNow` and `PreviewConfig` give `not_found` for a configuration the caller does not own.
  - `SyncNow` still runs guard and the caller-token read check as defence in depth.
  - `Snapshot` and snapshot events drop active transfers of other users' configurations. `engine.ActiveView` gains `config_id`.
  - Event subscriptions are filtered per subscriber:
    - sync issues of other users' configurations are dropped;
    - automation notifications go only to their folder's owner (`Notification.owner_sid`);
    - the agent log (it names files) reaches only administrators.

## Changes (VaultDock `desktop/`)
- `internal/config/config.go`: `OwnerSID`.
- `internal/engine/identity.go` (new):
  - `SetTokens`, `EnforcesOwner`, `asOwner`;
  - the token cache;
  - `ErrNoOwner`, `OwnerError`, `IsOwnerError`.
- `internal/engine/scan.go`: `collect`/`walkResult`, `ownerMessage`, and `Preview(c, limit, run)`.
- `internal/engine/transfer.go`, `watch.go`: owner identity for stat and open.
- `internal/engine/automations.go`: same-owner task chains and the `NotifyOwner` routing.
- `internal/engine/engine.go`:
  - the `id` field; tokens closed on `Stop`;
  - `ActiveView.ConfigID`;
  - `Items`/`History` take a config filter, plus `RetryFailedIn`;
  - per-owner name uniqueness and the `NotifyOwner` hook.
- `internal/state/state.go`: `ItemsIn`, `HistoryIn`, `RetryFailedIn`, `inConfigs`.
- `internal/agent/host_owner.go` (new):
  - `configScope`, `visibleConfig`, `setOwner`, `assignOwners`;
  - `filterSnapshot`, `scopedEvents`/`filteredSub`.
- `internal/agent/host.go`:
  - service start runs `assignOwners` then `SetTokens`;
  - the scoped config, queue, history, retry, snapshot and subscribe methods;
  - `NotifyOwner` wiring.
- `internal/agent/events.go`: `Notification.OwnerSID`.
- `frontend/wailsjs/go/models.ts`: `owner_sid` on `SyncConfig` and `ConfigView`, `config_id` on `ActiveView`.

## Tests
- **New `engine/identity_test.go`:**
  - an unowned folder is refused, and no token is requested;
  - probes run only inside the owner token;
  - the token is reused, refreshed after the TTL, and closed;
  - a denied owner queues nothing, and a token error is an owner error;
  - the upload open runs inside the token, and with no token nothing is opened and the item waits;
  - automation chains are limited to the same owner.
- **New `agent/host_owner_test.go`:**
  - a pipe client cannot choose the owner;
  - foreign configurations give `not_found` on save, delete and preview;
  - queue, history and retry are scoped;
  - names are unique per owner;
  - owners are assigned at service start, and the unowned folder is not scanned;
  - the per-user host records the current user;
  - events: log only for admins, issues and notifications by owner, and snapshot filtering.
- **Updated `pathaccess_test.go`:** `SyncNow` now covers ownership plus the guard and token checks.

## Commands run and results
All of these pass:
- desktop: `go build ./...`, `GOOS=linux go build ./...`, `go vet ./...`, `go test -count=1 ./...`;
- desktop/e2e: `go vet ./...`, `go test ./...`;
- frontend: `npm run build`;
- `gofmt -l`: clean.

## Not verified here
- No real tokens: the tests use fake tokens, and this machine cannot run as SYSTEM. Real impersonation of the walk and open needs the elevated VM run, as for the other spikes.
- The change watcher's directory handle is still opened by SYSTEM (the notify library opens it on its own threads). It only yields path names; each one is stat-ed as the owner before anything happens.

## Follow-ups
- Validate on an elevated VM, with a session token and with S4U (owner logged off).
- `AdoptUserDevice` / C5 marker import should set `OwnerSID` from the marker's SID.
