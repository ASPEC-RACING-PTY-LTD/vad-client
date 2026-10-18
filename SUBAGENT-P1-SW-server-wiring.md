# P1-SW Server cross-task wiring (SRV-8 + SRV-0 follow-ups)
Status: completed (one test in a file this task doesn't own now needs a one-line update by INT; see Notes)

## Goal
Wire the hooks the P1-17, P1-19, P1-20, P0-03 and P0-05 reports left for INT:
- the superuser toggle and last-superuser guards in the admin user routes;
- profile consent tasks sent through `CreateTask`;
- approval and lease expiry in the maintenance loop;
- `install_mode`, `modules_summary` and `can_delete` in the device lists.

## Changes (repo: vad-server)
- `internal/api/admin.go` (modified)
  - `PATCH /admin/users/{id}` accepts `is_superuser`.
    - The caller must be a superuser, checked before any field changes; otherwise 403 `not_superuser`.
    - Granting to someone who won't be an active admin after the request returns 400 `not_admin`, also checked before any field changes.
    - Disabling, demoting or revoking calls `guardLastSuperuser`, which returns 409 `last_superuser`.
    - The flag is applied through `s.setUserSuperuser` after the role and disabled update, so one request can promote and grant together. The reply's `user` includes `is_superuser`.
  - `DELETE /admin/users/{id}` calls `guardLastSuperuser` first.
  - `GET /admin/users` returns `is_superuser` on each user (new `adminUserJSON`, `adminUserViews`, `adminUserView`).
  - `GET /admin/devices` now goes through `deviceViews(ctx, 0, list)`, so it carries `can_delete`, `install_mode` and `modules_summary`.
- `internal/api/devices.go` (modified, device view only)
  - `deviceJSON` gains `install_mode` and `modules_summary`, both pointers with omitempty. They are set only by the list path, so single-device replies are unchanged.
  - `deviceViews` with userID 0 now means all users.
  - New `deviceModuleSummaries` reads `device_modules` in one query.
- `internal/api/wiring.go` (created)
  - An `init()` points `createProfileConsentTask` at `s.CreateTask`. The task uses kind `profile_consent_request`, `RequiredModules: ["profiles"]`, `UserID` = device owner and `CreatedBy` = the admin.
  - New exported `(*Server).ExpireApprovals(ctx)` and `(*Server).ExpireTaskLeases(ctx)`. The lease sweep wakes the long-polls for requeued tasks and runs result hooks for failed ones.
- `internal/api/profiles.go` (modified, 4 lines)
  - `handleProfileRequest` calls `s.requireModules(ctx, d.ID, "profiles")` before `CreateProfileRequest`.
  - Without this, the new 409 from `CreateTask` would leave a `profile_requests` row and changed `device_profiles` states behind with no task.
- `internal/app/app.go` (modified): `maintain` also calls `Server.ExpireApprovals` and `Server.ExpireTaskLeases`.
- `internal/api/wiring_test.go` (created)
  - `TestAdminSuperuserToggle`
  - `TestDeviceListModulesAndCanDelete`
  - `TestProfileConsentTaskWiring`
  - `TestMaintenanceSweeps`

## Interfaces exposed
- `func (s *Server) ExpireApprovals(ctx context.Context)`
- `func (s *Server) ExpireTaskLeases(ctx context.Context)`
- `PATCH /api/v1/admin/users/{id}` body adds `"is_superuser": bool`. The reply is `{"user": {...user, "is_superuser"}}`.
- `GET /api/v1/admin/users`: each user adds `is_superuser`.
- `GET /api/v1/devices` and `GET /api/v1/admin/devices`: each device has these fields:
  - `can_delete`
  - `install_mode`: `"simple"` when the device has never reported
  - `modules_summary`: `{"enabled":[ids],"failed":[ids],"updated_at":ms}`, where `updated_at` is 0 when the device has never reported
- `POST /devices/{id}/profiles/requests` now returns 409 `module_unavailable` when the device has not reported the `profiles` module as enabled.

## Commands run and results
- `go build ./...`: OK
- `go vet ./internal/...`: OK
- `gofmt -l` on the touched files: clean
- `go test -count=1 ./internal/...`: everything passes, including the 4 new tests, except `TestProfilesBlacklistClearReconsent` (`profiles_test.go:135`). That test asks for profile consent on a device that never reported the `profiles` module, so it now gets the intended 409 `module_unavailable`.
- `go test -count=1 -tags integration ./tests/...`: OK

## Contract issues
- api-v1 §6A says `GET /devices` gains `modules_summary` but gives no shape. I chose `{enabled[], failed[], updated_at}`; INT/web should confirm.

## Gaps / follow-ups
- `store.QueueProfileConsentTask` (P1-20) is now unused and can be deleted.
- The TODO comment on `queueDeviceTask` in `modules.go` is stale; P1-17 already rewired it.
- The `createProfileConsentTask` comment in `profiles.go` still says "INT may point it…".

## Notes for dependent tasks / INT
- **Required (`profiles_test.go`, P1-20 file):** before line 135 (`admin.must("POST", reqPath, ...201)`), the test must report the profiles module from the device:
  ```go
  dc.Call(ctx, "PUT", "/api/v1/device/modules", wire.ModuleReport{InstallMode: "advanced", CoreVersion: "1", Modules: []wire.ModuleStatus{{ID: "core", Version: "1", State: "enabled"}, {ID: "profiles", Version: "1", State: "enabled"}}}, nil)
  ```
  After that the suite is green. The 409 is intended: `CreateTask` checks required modules when the task is created.
- Web (P1-35 / admin users page) can now read `is_superuser` from `GET /admin/users` and toggle it with `PATCH`. Plain admins get 403 `not_superuser`.
