# P1-20 (SRV-M) Profiles + superuser
Status: completed

## Goal
Implement api-v1 §6B (Windows profiles, consent, security events → blacklist, admin consent requests, superuser clear → re-consent) and §11B (superuser flag management) in `vad-server`. Advertise `profiles.v1`.

## Changes (repo vad-server)
- `internal/store/device_profiles.go` (created):
  - `DeviceProfile` record;
  - report apply with server-side transition rules;
  - security-event blacklist, which suspends jobs;
  - superuser clear;
  - admin profile requests;
  - an interim device-task writer.
- `internal/store/superuser.go` (created): `is_superuser` read, set and guard (the last active superuser is kept).
- `internal/api/profiles.go` (stub replaced):
  - the 5 §6B routes;
  - `requireProfileAuthorised` and `requireProfileConsent` gating helpers;
  - the `createProfileConsentTask` and `notifyProfileBlacklisted` function variables;
  - `init()` → `implementFeature(wire.FeatureProfiles)`.
- `internal/api/superuser.go` (stub replaced): `setUserSuperuser` and `guardLastSuperuser` helpers for `admin.go`. There are no own routes (see Notes).
- `internal/store/device_profiles_test.go` (created): blacklist → clear → re-consent with job suspend/resume, requests and task row, superuser store rules.
- `internal/api/profiles_test.go` (created): end-to-end HTTP covering report validation, views, the security event, 403 `not_superuser` on clear for a non-superuser admin and for a user, confirm text, 409 on a second clear, stale consent, re-consent, requests, audit entries, the superuser notice and the feature flag.
- `internal/api/superuser_test.go` (created, package `api`): `setUserSuperuser` (403, 409, 400, audit) and helper gating (403, 423, 403 after clear).

## Interfaces exposed
Routes:
- `PUT /device/profiles` (dev):
  - Body: `wire.ProfileReport`.
  - Reply `200 {"items":[wire.Profile…],"next_cursor":""}`: the server's view, so the agent learns about blacklisting and clears.
  - Errors: `422 validation` (`details.fields`); `400 invalid_profile_state` when active_default or authorised is claimed with no consent record at all.
- `POST /device/profiles/{sid}/security-event` (dev):
  - Body: `wire.ProfileSecurityEventRequest`.
  - Reply `200 wire.Profile` (blacklisted). Errors: `422 validation`.
  - Idempotent: the first reason is kept.
  - An unknown SID is recorded as a new blacklisted profile.
  - Jobs of `(device, profile_sid)` change from `active` to `suspended_by_profile`.
- `GET /devices/{id}/profiles` (sess, owner or admin; others get 404): list with `cursor`/`limit`.
- `POST /devices/{id}/profiles/requests` (admin):
  - Body: `{"sids":[…]}` | `{"all":true}`. Both, or neither, gives 422.
  - Reply `201 wire.DeviceTaskCreated{task_id}`.
  - Inserts a `profile_requests` row (`task_id` linked).
  - Unknown selected SIDs become `requested` rows, and `revoked` rows become `requested`.
  - Task payload `{request_id: "<profile_requests.id>", sids, requested_by: <admin username>}`, with `required_modules ["profiles"]`.
- `POST /admin/device-profiles/{id}/clear` (super):
  - Body: `{"confirm_text"}`, which must equal the profile's SID or Windows username (otherwise 422).
  - Unknown profile: 404 `profile_not_found`. A profile that isn't blacklisted: 409 `not_blacklisted`.
  - Success: `200 wire.Profile` in state `cleared_pending_consent`. Jobs stay suspended.

Transition rules (store `ApplyProfileReport`):
- A server `blacklisted` profile never changes from a device report.
- A reported `blacklisted` is honoured.
- A reported `cleared_pending_consent` is ignored.
- `cleared_pending_consent` → `active_default` or `authorised` needs a NEW `consent_id` with `consent_at > cleared_at`; then jobs resume.
- `revoked` → consented needs a new `consent_id`.
- Downgrades to `requested` or `revoked` are always accepted.
- `vaultdock_user_id` is set to the device owner for `active_default` (C10 default).

Go helpers (package `api`):
```go
func (s *Server) requireProfileAuthorised(ctx context.Context, deviceID int64, sid string) error // nil | 423 profile_blacklisted | 403 profile_not_authorised
func (s *Server) requireProfileConsent(ctx context.Context, deviceID int64, sid, consentID string) error // + consent_id must be the active one (snapshot commit §5.2)
func (s *Server) setUserSuperuser(r *http.Request, target *store.User, on bool) error // 403 not_superuser | 409 last_superuser | 400 not_admin; audited
func (s *Server) guardLastSuperuser(ctx context.Context, userID int64) error           // 409 last_superuser
var createProfileConsentTask func(ctx context.Context, s *Server, deviceID, createdBy int64, payload wire.TaskProfileConsentRequestPayload) (int64, error)
var notifyProfileBlacklisted func(r *http.Request, s *Server, p *store.DeviceProfile)
```

Store (package `store`):
- `DeviceProfile`, `(*DeviceProfile).Consented()`, `ProfileTransition`, `ProfileRequest`, `ErrProfileState`
- `DeviceProfile(ctx,id)`, `DeviceProfileBySID(ctx,deviceID,sid)`, `ListDeviceProfiles(ctx,deviceID,afterID,limit)`
- `ApplyProfileReport(ctx,deviceID,ownerID,wire.ProfileReport) ([]ProfileTransition, error)`
- `BlacklistProfile(ctx,deviceID,sid,reason,evidence) (*DeviceProfile, already bool, error)`
- `ClearProfileBlacklist(ctx,id,by) (*DeviceProfile, error)`
- `CreateProfileRequest(ctx,deviceID,by,scope,sids)`, `SetProfileRequestTask(ctx,reqID,taskID)`, `QueueProfileConsentTask(ctx,deviceID,createdBy,payload) (int64, error)`
- `IsSuperuser`, `SuperuserIDs`, `IsLastSuperuser`, `SetSuperuser(ctx,id,on) (changed bool, err)`, `ErrLastSuperuser`, `ErrNotAdmin`

Audit actions (all `audit=true`, written to the device owner's log): `profile.consented`, `profile.state_changed`, `profile.security_event`, `profile.consent_requested`, `profile.blacklist_cleared`, `admin.superuser_granted` and `admin.superuser_revoked`. Superusers additionally get a `profile.blacklisted_notice` activity entry.

## Commands run and results
- `go build ./...`: OK
- `go vet ./internal/...`: OK
- `go test -count=1 ./internal/store/`: ok
- `go test -count=1 ./internal/api/`: all of my tests pass. **One failure that is not mine:** `TestStubRoutesAndAuthGroups` (`devices_delete_test.go:150`) expects `GET /approvals` to return 501, but P1-17 has now implemented that route. `go test -skip TestStubRoutesAndAuthGroups ./internal/api/` is ok.
- `gofmt -l` on all owned files: clean.

## Contract issues
1. The task brief says "pending_consent"; the contract and wire use `cleared_pending_consent` (also `requested`). The contract was implemented as written.
2. §6B gives no reply body for `PUT /device/profiles`, the security event or the clear. I chose 200 with the profile list or profile (above). Requests reply `201 DeviceTaskCreated`.
3. `confirm_text` has no defined value. I chose: the profile's SID or Windows username.
4. The spike (§4) shows `observed_at` as RFC 3339, but the contract and wire use int64 ms. I implemented int64 ms (the spike's caveat stands). The spike's evidence gaps for `account_disabled` and `profile_moved` remain; `sid_lookup` is accepted for them.
5. The validation status is 422 `validation` (the same as modules.go and api-v1 §10.3). §1 does not pin the status code.

## Gaps / follow-ups
- **No "last security check" column** exists in `device_profiles` (migration 8). It is not stored. A migration 9 `device_profiles.last_checked_at` is needed if it is wanted. The security-event `observed_at` is kept only in the audit details.
- `blacklist_evidence` and `proof_digest` are stored but are not in `wire.Profile`, so they are not exposed in views.
- Admin requests do not check the device's `profiles` module (P1-19 gating helper). The task carries `required_modules ["profiles"]`, so P1-17 delivery should fail it with `module_unavailable`.
- Notification of superusers is an activity-feed entry only, until SRV-7 replaces `notifyProfileBlacklisted`.

## Notes for dependent tasks / INT
- **admin.go (SRV-8 / INT), §11B:** `PATCH /admin/users/{id}` must accept `"is_superuser": bool` and call `s.setUserSuperuser(r, u, *req.IsSuperuser)` (it checks the caller is a superuser itself). The route stays in the admin group; a duplicate route in `mountSuperuser` would clash with the admin route at the same path. The same handler, plus `DELETE /admin/users/{id}` (and disable/demote to `user`), should call `s.guardLastSuperuser(ctx, u.ID)` first. `GET /admin/users` should expose `is_superuser` (`store.IsSuperuser` / `SuperuserIDs`).
- **INT wiring with P1-17:** point `createProfileConsentTask` at the device-task store's create function. The default `store.QueueProfileConsentTask` inserts a raw `device_tasks` row (status `pending`, owner = device owner, `created_by` = admin). It can be deleted afterwards.
- **P1-15 (snapshot commit) and SRV-5 (sync), SRV-3 (restore into a profile):** call `s.requireProfileConsent(ctx, deviceID, user_sid, profile_consent_id)` or `s.requireProfileAuthorised(ctx, deviceID, sid)`. They return ready `*apiError` values (423 or 403).
- **P1-18:** jobs are suspended and resumed by profile state directly in `backup_jobs.state` (`active` ↔ `suspended_by_profile`, matched on `device_id` + `profile_sid`).
- **`devices_delete_test.go` (P0-03 file):** `TestStubRoutesAndAuthGroups` must stop asserting 501 on `/approvals` (P1-17 change). It does not depend on this task.
- **Web (P1-35):** clear needs the username or SID typed as `confirm_text`. The list reply is `{items,next_cursor}`.
