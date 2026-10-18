# P1-19 SRV-M: modules endpoints, gating helper, module catalog
Status: completed

## Goal
Implement api-v1 §6A in `vad-server`:
- `PUT /device/modules`;
- `GET /devices/{id}/modules`;
- `GET /modules/catalog`;
- `POST /devices/{id}/mode-request`.

Also: a `device_modules` store, the module-gating helper (`409 module_unavailable` with `details {module, device_id}`), and the `modules.v1` feature flag. MASTER_PLAN §7.7.

## Changes (repo: path, created/modified/deleted, why)
- **vad-server: `internal/store/device_modules.go` (created).** Adds the `DeviceModules` type, an upsert and a get.
- **vad-server: `internal/store/device_modules_test.go` (created).** Tests:
  - the unreported device;
  - upsert and replace;
  - enabled helpers;
  - nil modules;
  - the CHECK constraint on `install_mode`;
  - cascade on device delete.
- **vad-server: `internal/api/modules.go` (modified; it was the P0-03 stub).** Real handlers, the gating helpers, the static catalog, the mode-request task queue seam, and `init() { implementFeature(wire.FeatureModules) }`.
- **vad-server: `internal/api/modules_test.go` (created).** HTTP tests in package `api_test`.
- **vad-server: `internal/api/modules_internal_test.go` (created, outside the literal owned list).** It tests the unexported `requireModules` and `moduleErrorFrom` in package `api`. They cannot be reached from the external test package, and `export_test.go` is not mine. INT: keep it, or merge it into `export_test.go` with an exported shim.

## Interfaces exposed
**Store (`internal/store`):**
```go
type DeviceModules struct { DeviceID int64; InstallMode, CoreVersion string; Modules []wire.ModuleStatus; UpdatedAt int64 }
func (m *DeviceModules) Enabled(id string) bool   // nil-safe; state == enabled
func (m *DeviceModules) EnabledIDs() []string     // nil-safe, never nil, report order
func (s *Store) PutDeviceModules(ctx context.Context, m *DeviceModules) error   // upsert, sets UpdatedAt
func (s *Store) DeviceModules(ctx context.Context, deviceID int64) (*DeviceModules, error) // ErrNotFound if never reported
```

**API (`internal/api`, package-internal, for other feature areas):**
```go
func (s *Server) requireModules(ctx context.Context, deviceID int64, modules ...string) error // nil or *apiError 409 module_unavailable, details {module, device_id}; first missing module wins
func (s *Server) enabledModules(ctx context.Context, deviceID int64) ([]string, error)      // reported enabled ids; core always included
func (s *Server) checkAutomationSupport(ctx context.Context, deviceID int64, a *automation.Automation, version int) error // CheckSupport with the device's modules
func moduleErrorFrom(deviceID int64, err error) error  // *automation.UnsupportedError with Module != "" → 409 module_unavailable; else passthrough
func moduleUnavailable(deviceID int64, module string) *apiError
func (s *Server) ownerOrAdminDevice(r *http.Request) (*store.Device, error) // {id} for owner/admin, else ErrNotFound
var queueDeviceTask func(s *Server, ctx context.Context, t deviceTaskSpec) (int64, error) // seam, see below
type deviceTaskSpec struct { UserID, DeviceID, CreatedBy int64; Kind string; Payload any; RequiredModules []string }
```

**Gating rules:**
- A module counts as available only when the device's last report lists it with state `enabled`. `installing`, `failed`, `disabled` and unlisted modules are unavailable.
- `core` is always available, even before the first report. Nothing else is inferred from the mode.

**Routes:**

| Route | Auth | Behaviour |
|---|---|---|
| `PUT /device/modules` | dev | `wire.ModuleReport` → `204` |
| `GET /devices/{id}/modules` | sess; owner or admin, else 404 | `wire.ModuleDeviceState` |
| `GET /modules/catalog` | dev, sess | Bare array of `wire.ModuleCatalogEntry` in contract order |
| `POST /devices/{id}/mode-request` | sess; owner or admin, else 404 | `{"target_mode"}` → `201 wire.DeviceTaskCreated{task_id}` |

`PUT /device/modules` details:
- **Validation** (`422 validation`, `details.fields`):
  - `install_mode` must be simple or advanced;
  - each `state` must be one of the 4 wire states;
  - module ids must not repeat;
  - `version` and `core_version` are at most 64 chars.
- **Normalisation:**
  - `error` is cleaned and truncated to 500 chars;
  - unknown module ids are **dropped** for forward compatibility;
  - unknown JSON fields → `400 bad_json`.

`GET /devices/{id}/modules` before any report returns `{"install_mode":"simple","core_version":"","modules":[],"updated_at":0}`.

Catalog details:
- `core` is delivered as `builtin` and lists both modes.
- `sync` requires `sync_two_way.v1`; `profiles` requires `profiles.v1`. Every other `requires_features` is `[]`.

Mode request details:
- It creates a pending `mode_change_request` task with payload `{target_mode, requested_by: <username>}` and an audit entry `device.mode_request`.
- A non-approved device gets `409 device_not_approved`.

## Commands run and results
From `D:/VaultDock/vad-server`:
- `go build ./...`: OK
- `go vet ./internal/api/ ./internal/store/`: OK
- `gofmt -l` on the owned files: clean
- `go test -count=1 ./internal/store/`: ok
- `go test -count=1 -run 'TestModulesEndpoints|TestRequireModules' ./internal/api/`: PASS
- `go test -count=1 ./internal/api/`: **FAIL**, but only in two P0-03 tests in `devices_delete_test.go`, which I don't own:
  - `TestDeviceDeletePermission`, line 107;
  - `TestStubRoutesAndAuthGroups`, line 146.

  Both assert that `features` is empty. They now see `[modules.v1 deploy_env.v1]`; `deploy_env.v1` comes from another parallel task. This was expected once any feature is implemented.

## Contract issues
- §6A doesn't say what `GET /devices/{id}/modules` returns before the first report. I chose `200` with `install_mode` set to the column default `simple`, `modules: []` and `updated_at: 0`. No module is ever inferred.
- The success status of `POST /devices/{id}/mode-request` is not given (rev 1.0 gives only `202` for gated tasks). I used `201` with `{task_id}`. INT should align this with P1-17's `POST /device/tasks`.
- §6A doesn't say how to handle unknown module ids in a report. I dropped them silently for forward compatibility rather than rejecting the whole report.
- `409 device_not_approved` (mode request for a pending or revoked device) is a server-local code, not in §1.1.
- As noted by P0-38, the report states cannot express wanted or suspended; both arrive as `disabled`.

## Gaps / follow-ups
- `GET /devices` does not yet return `install_mode` or `modules_summary`, because `devices.go` is not mine. Use `s.st.DeviceModules` in `deviceView` (a batch query would be nicer for long lists).
- P1-17 task delivery must fail tasks with `module_unavailable` when their `required_modules` are missing. It can use `s.enabledModules` / `missingModule`.
- The mode-request route does not reject a target equal to the current mode; it always queues the task.

## Notes for dependent tasks / INT
- **Device-task seam:** `queueDeviceTask` in `modules.go` is a package-level function variable.
  - Its default writes a minimal `pending` row straight into `device_tasks`, using the P0-02 columns: user_id, device_id, kind, payload, required_modules, status, created_by, created_at.
  - When P1-17's store lands, INT should point it at the P1-17 create function, so that expiry, events and wake-ups apply. The comment on the variable shows the shape. P1-20's profile requests can reuse the same seam.
- **P1-18 jobs / automations:** call `s.requireModules(ctx, deviceID, job.RequiredModules...)` before creating or enabling jobs, and pass the error to `writeErr`. For automations, use `s.checkAutomationSupport(ctx, deviceID, a, version)`, or `moduleErrorFrom(deviceID, automation.CheckSupport(...))`.
- **INT:** update `devices_delete_test.go` lines 107 and 146, which expect empty `features`, now that the feature flags are being turned on.
- No dependencies were added. The new imports are `sdk/modules` and `sdk/automation`, which are already in the workspace and module requirements.
