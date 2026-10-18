# P0-03 (SRV-0) Mount hooks, stub routes, streamed routes, feature flags, can_delete (B1)
Status: completed

## Goal
In `vad-server/internal/api/`:
- restructure route registration so each feature area mounts its own routes through a hook;
- add 501 stub files for every api-v1 rev 1.1 route, each under its auth group, including a `super` group;
- extend the streamed-route list (§1.2);
- add a feature-flag registry (§1.3);
- fix B1: devices can no longer purge trash, and `devices.can_delete` is honoured, exposed and toggleable.

## Changes (repo vad-server)
- `internal/api/server.go` (modified):
  - Added the `routes` struct (one `chi.Router` per auth label), `mountFeatures`, and the `Handler` restructure.
  - Admin routes moved from `r.Route("/admin")` into a group with full `/admin/...` paths, so feature files can add `/admin/...` and `/devices/{id}/profiles/requests` routes in the admin group.
  - Trash purge and empty (`DELETE /trash/{id}`, `DELETE /trash`) moved to the session-only group (B1).
  - `requireDeviceCanDelete` applied to `DELETE /files/{id}` and `POST /files/delete`.
  - Added `requireSuperuser`, `isSuperuser`, `notImplemented`, `isStreamedBody` (extended), the `streamedBody` helper, the feature registry, and `"features"` in `/info`.
  - Imports were re-sorted by gofmt.
- `internal/api/devices.go` (modified):
  - Added `deviceJSON` (store device + `can_delete`), `deviceCanDelete`, `setDeviceCanDelete`, `deviceView` and `deviceViews`.
  - `GET /devices`, approve/reject and `/device/me` now return `can_delete`. `/device/me` also returns `"features"`.
  - `handleDeviceRename` was replaced by `handleDevicePatch`.
  - Uses raw SQL on `s.st.DB`, because `internal/store/devices.go` is unowned.
- Created 25 stub files (see the table below) and `internal/api/devices_delete_test.go`.

### Stub files and owners
Every route returns `501 {"error":{"code":"not_implemented"}}`. Each file has a header naming its owner; ownership passes to that owner at creation (§13.6).

| File | Owner (task) | Routes (group) |
|---|---|---|
| summary.go | SRV-0 (P0-05) | GET /summary (Sess) |
| operations.go | SRV-0 (P0-05) | GET /operations/{id} (User) |
| deploy_env.go | SRV-0 (P0-37) | GET /admin/deploy/env, POST /admin/deploy/env/removals, DELETE …/removals/{name} (Super) |
| destinations.go | SRV-1 | GET /destinations (User); POST /destinations, /destinations/test, GET /destinations/providers, PATCH/DELETE /destinations/{id}, POST /destinations/{id}/test (Sess); /admin/destinations CRUD (Admin) |
| keys.go | SRV-K (P1-12) | GET /keys/server, /repos/{id}/keys/status (User); POST /repos/{id}/keys/rotate, PATCH /repos/{id} (Sess); POST /admin/keys/rotate-server (Super) |
| backup_objects.go | SRV-2 (P1-14) | GET /backup/capabilities, GET /repos, /repos/{id}, GET objects/{kind}/{oid} (User); POST /repos, GET /repos/{id}/objects, POST …/objects/missing, HEAD/PUT …/objects/{kind}/{oid} (Dev, PUT streamed) |
| snapshots.go | SRV-2 (P1-15) | locks POST/PUT/DELETE, POST /repos/{id}/snapshots (Dev, streamed); /snapshots, /snapshots/diff, /snapshots/{id} GET/PATCH, /tree, /search, POST /catalog/rebuild (Sess) |
| snapshot_download.go | SRV-2 | GET /snapshots/{id}/content, POST /snapshots/{id}/download (Sess) |
| backup_jobs.go | SRV-3 (P1-18) | /backup/jobs[/{id}] (GET User, writes Sess); POST /backup/jobs/{id}/runs, PATCH /backup/runs/{id}, POST …/issues (Dev); GET runs/issues, issues/skip, retry-skipped (Sess) |
| device_tasks.go | SRV-3 (P1-17) | GET /device/tasks, POST /device/tasks/{id}/result, PUT /device/inventory (Dev, streamed); POST /device/tasks, GET /device/tasks/list, GET /devices/{id}/apps (Sess) |
| approvals.go | SRV-3 (P1-17) | GET /approvals, /approvals/{id}, POST …/approve, …/reject (Sess) |
| restore.go | SRV-3 | POST /restore/plan, /restore/execute, GET /restore/{id}, /restore/{id}/items (Sess) |
| modules.go | SRV-M (P1-19) | PUT /device/modules (Dev); GET /modules/catalog (User); GET /devices/{id}/modules, POST /devices/{id}/mode-request (Sess) |
| profiles.go | SRV-M (P1-20) | PUT /device/profiles, POST /device/profiles/{sid}/security-event (Dev); GET /devices/{id}/profiles (Sess); POST /devices/{id}/profiles/requests (Admin); POST /admin/device-profiles/{id}/clear (Super) |
| superuser.go | SRV-M (P1-20) | empty hook (see Notes) |
| prune.go | SRV-4 | /retention-policies CRUD, POST /repos/{id}/prune/plan, …/prune/execute, /repos/{id}/verify (Sess); POST /admin/gc/plan, /admin/gc/execute (Admin) |
| replication.go | SRV-6 | /replication CRUD (Sess) |
| analytics.go | SRV-6 | GET /repos/{id}/analytics (Sess) |
| versions.go | SRV-5 | GET /files/{id}/versions, GET …/versions/{vid}/content (User); POST …/restore, PUT …/pin (Sess) |
| sync_folders.go | SRV-5 | GET /sync-folders, GET/PATCH /sync-folders/{id} (User); POST, DELETE (Sess) |
| sync_changes.go | SRV-5 | GET /sync/changes (Dev); POST /sync/folders/{id}/ops (Dev + requireDeviceCanDelete) |
| conflicts.go | SRV-5 | GET /sync/conflicts, POST /sync/conflicts/resolve (Sess) |
| readiness.go | SRV-7 (P1-21) | GET /readiness (Sess); PUT /device/backup-state (Dev) |
| notify_prefs.go | SRV-7 | GET/PUT /account/notification-prefs (Sess) |
| ai.go | SRV-7 | GET/PUT /account/ai, POST /ai/diagnose (Sess) |

## Interfaces exposed

### Mount-hook pattern
```go
type routes struct {
    User  chi.Router // "dev, sess" (CSRF on session writes)
    Dev   chi.Router // "dev"
    Sess  chi.Router // "sess"
    Admin chi.Router // "admin" (full paths, e.g. "/admin/destinations")
    Super chi.Router // "super" (requireSuperuser)
}
func (s *Server) mountX(rt routes)   // defined in the feature's own file
```
- `mountFeatures(rt)` in `server.go` calls all 25 hooks.
- Paths are absolute below `/api/v1`.
- To implement a route, replace `notImplemented` with the real handler, keeping the route in its group.
- Per-route middleware: `rt.Dev.With(mw).Post(...)`.
- A new feature area (a new file) needs one added line in `mountFeatures` (INT/SRV-0).

### Middleware and helpers (`server.go`)
- `func (s *Server) requireSuperuser(next http.Handler) http.Handler`: session + `IsAdmin()` + `users.is_superuser = 1`. Otherwise `403 not_superuser`.
- `func (s *Server) isSuperuser(ctx context.Context, userID int64) (bool, error)`
- `func (s *Server) requireDeviceCanDelete(next http.Handler) http.Handler`: sessions pass; a device with `can_delete = 0` gets `403 device_cannot_delete`.
- `func notImplemented(w http.ResponseWriter, r *http.Request)`

### Streamed routes
- `isStreamedBody` covers:
  - PUT `/uploads/{id}/chunks/{n}`
  - PUT `/repos/{id}/objects/{kind}/{oid}`
  - POST `/repos/{id}/snapshots`
  - PUT `/device/inventory`
- For these routes `authDevice` neither buffers the body nor applies the 2 MiB cap. It sets `Principal.DeclaredBodySHA`.
- Helper:
  ```go
  func newStreamedBody(r *http.Request, limit int64) (*streamedBody, error) // io.Reader; 400 invalid_hash if no signed hash
  func (b *streamedBody) Len() int64
  func (b *streamedBody) Verify() error // drains the rest, constant-time compare, 400 body_mismatch
  ```
- Requirement (documented on `isStreamedBody`): read the whole body through it, spool only to a temporary location, and call `Verify()` BEFORE committing any state.

### Feature flags
```go
func implementFeature(flag string) // call from the feature file's init(): func init() { implementFeature(wire.FeatureBackup) }
func features() []string            // contract order, never nil
```
- `implementFeature` panics on a flag not in api-v1 §1.3.
- `/info` and `/device/me` return `"features": features()`. All flags are currently off, so both return `[]`.

### Device JSON and PATCH
- Device JSON from `GET /devices`, approve/reject and `/device/me` gains `"can_delete": bool`.
- `PATCH /devices/{id}` accepts `{"name"?, "can_delete"?}`:
  - **Owner:** may change both.
  - **Admin (non-owner):** may change only `can_delete`; a name change gets `403 forbidden`.
  - **Other users:** `404`.
  - **Empty body:** `400 bad_name` (as before).
- A `can_delete` change is audit-logged as `device.can_delete`.

### Behaviour changes
- `DELETE /trash/{id}` and `DELETE /trash` are session-only. A device gets `403 session_required`.
- An unknown `/api/v1/admin/...` path now returns 404 instead of the old 403 for non-admins (the admin subrouter became a group).

## Commands run and results
- `go build ./...`: OK. A first attempt failed on in-flight `sdk/automation` edits by another task; a retry was clean.
- `go vet ./internal/api/`: OK
- `go test -count=1 ./internal/...`: api, storage, store all ok
- `go test -count=1 -v -run 'TestDeviceDeletePermission|TestStubRoutesAndAuthGroups' ./internal/api/`: both PASS
- `go test -count=1 -tags integration ./tests/...`: ok
- `gofmt -l`: all owned files are clean. The remaining hits are pre-existing unowned files, whose imports are unsorted after the module rename.

## Contract issues
1. Rev 1.0 route paths are referenced but not defined anywhere ("as rev 1.0"). These stub paths were taken from `planning/research/01` §3 and the `sdk/wire` doc comments, and are **assumed**:
   - approvals: `/approvals[/{id}]`, `/approvals/{id}/approve`, `/approvals/{id}/reject` (reject is not named anywhere);
   - restore: `/restore/plan`, `/restore/execute`, `/restore/{id}`, `/restore/{id}/items`;
   - prune: `/repos/{id}/prune/execute` (research had `/prune-plans/{id}/approve|execute`);
   - GC: `/admin/gc/plan`, `/admin/gc/execute` (no path anywhere);
   - `/device/tasks/list` auth (sess assumed);
   - versions content path `/files/{id}/versions/{vid}/content`.
   Owners may change these; INT should pin them.
2. The `POST /snapshots/{id}/download` result `download_url` implies a token download route whose path is unspecified. No stub was created.
3. `/ai/recommend` (in research rev 1.0) is not in rev 1.1 §11. Only `/ai/diagnose` is stubbed.

## Gaps / follow-ups
- `GET /admin/devices` (admin.go, not owned) does not include `can_delete`. Admins can toggle it via `PATCH /devices/{id}` but cannot see it in the admin list.
- `PUT /uploads/...` replacing a file and `POST /files/move` are not gated by `can_delete`. Only true deletes are.

## Notes for dependent tasks / INT
- **SRV-K (devices.go hand-off after P0-03):** `handleDeviceRename` is now `handleDevicePatch`. Device responses go through `deviceView`/`deviceViews`; add enrolment fields there or in `handleEnroll`/`handleEnrollStatus`.
- **SRV-M (superuser.go):** granting or revoking `is_superuser` belongs in `PATCH /admin/users/{id}` in `admin.go` (SRV-8 owns it). That handler must call `s.isSuperuser` itself, because the route stays in the admin group. If you prefer a separate route, register it in `mountSuperuser` on `rt.Super`.
- **SRV-5:** `POST /sync/folders/{id}/ops` already carries `requireDeviceCanDelete`. Add the two-way-folder check in the handler.
- **SRV-2 / SRV-3:** streamed handlers must use `newStreamedBody` + `Verify()` before any DB write or final object rename.
- **SRV-C:** catalog tree/search/rebuild routes are in `snapshots.go` (SRV-2 file), because SRV-C has no API file in §13.2.
- **SRV-8 / INT:** consider showing `can_delete` in `GET /admin/devices`.

## Follow-up: rev 1.0 route reconciliation
Source: `sdk/contracts/history/api-v1-rev1.0.md` (Rev 1.1a amendment), with rev 1.1 changes applied on top.

### Assumed routes, checked against rev 1.0
| Item | Rev 1.0 | Result |
|---|---|---|
| Approvals: GET `/approvals`, `/approvals/{id}`; POST `…/approve`, `…/reject` (sess) | §8, identical | Unchanged; no longer assumed |
| Restore: POST `/restore/plan`, `/restore/execute`; GET `/restore/{id}`, `/restore/{id}/items` (sess) | §7, identical | Unchanged |
| POST `/repos/{id}/prune/execute` (sess) | §9, identical | Unchanged |
| POST `/admin/gc/plan`, `/admin/gc/execute` (admin) | §9, identical | Unchanged; "assumed" comments removed |
| GET `/device/tasks/list` auth | §6: sess | Unchanged (Sess) |
| GET `/files/{id}/versions/{vid}/content` | §10: defined (Range); rev 1.1 §10.4 lists it as "sess; devices read-only" | Unchanged (User group, so devices can read) |
| Snapshot `download_url` route | Not in rev 1.0 (snapshot download is new in rev 1.1, which gives no path) | No stub added; still an implementer choice for SRV-2 |
| POST `/ai/recommend` | Not in rev 1.0 (only research/01 had it) | Not added |

### Other differences found and fixed
- `backup_jobs.go`:
  - POST `/backup/jobs` and PATCH `/backup/jobs/{id}` moved from Sess to User. Rev 1.0 §6: "dev may POST/PATCH jobs for its own device", and rev 1.1 says "as rev 1.0". The handler must restrict devices to their own device.
  - Removed GET `/backup/jobs/{id}` and GET `/backup/jobs/{id}/runs`, which neither revision defines. Runs are listed through GET `/backup/runs?job=`.
- `prune.go`: added GET/PUT `/admin/backup-settings` (admin, rev 1.0 §9).
- `analytics.go`: added GET `/storage/analytics?group=&range=` (sess, rev 1.0 §9).
- Not added: PUT `/repos/{id}/snapshots/{sid}/listing`. Rev 1.1 §14 removes the managed-metadata listing upload.
- Kept on the rev 1.1 labels where rev 1.1 changes rev 1.0: POST `/repos` stays dev only (rev 1.0 said dev, sess), and GET `/destinations` for devices returns names and ids only.
- Header comments of `approvals.go`, `restore.go`, `prune.go` and `replication.go` now cite the rev 1.0 history file instead of `sdk/wire`.

Contract issues 1–3 above are resolved by this reconciliation, except the `download_url` path, which remains unspecified in both revisions.

### Commands (follow-up)
- `go build ./...`: OK
- `go vet ./internal/api/`: OK
- `go test -count=1 ./internal/...`: api, storage, store ok
- `go test -count=1 -tags integration ./tests/...`: ok
- gofmt: all owned files clean
