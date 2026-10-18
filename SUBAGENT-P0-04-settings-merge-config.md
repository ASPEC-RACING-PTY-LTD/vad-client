# P0-04 Settings merge (B14) + config additions
Status: partial (merge logic, config and tests are done; wiring the handler needs a short edit in `internal/api/admin.go`, which belongs to P0-07)

## Goal
1. B14: the settings PUT should merge instead of overwriting. Fields absent from the body keep their stored values; present fields are validated and applied; unknown fields are rejected; the response shape stays the same.
2. Add config fields `VAULTDOCK_KEK_FILE`, `VAULTDOCK_AI_ALLOW_PRIVATE_ENDPOINTS` and `VAULTDOCK_SYNC_TOMBSTONE_DAYS`.

## Changes (repo: path, created/modified/deleted, why)
- vad-server: `internal/api/settings.go` (modified)
  - Added `(*SettingsCache).Merge`. It strictly decodes the body onto a copy of the current settings, validates the result, then persists and caches it.
  - Added a write mutex `wmu` so concurrent read-merge-write cycles cannot lose each other's updates. `Save` takes the same lock.
  - Validation errors are now wrapped in `invalidSettingsError`. Its `Error()` text is unchanged, so the existing handler output is byte-identical.
- vad-server: `internal/config/config.go` (modified)
  - Added the `KEKFile`, `AIAllowPrivateEndpoints` and `SyncTombstoneDays` fields.
  - Added the constant `MinSyncTombstoneDays = 90`.
- vad-server: `internal/api/settings_merge_test.go` (created, package `api` internal test)
  - A table test of `Merge`: empty object, single field, explicit false and zero applied, unknown field, wrong type, `null`, array, invalid values, and persistence across `LoadSettings`.
  - A test that the `Save` validation message is unchanged.

**Why the fix is not wired yet:** the PUT handler `handleAdminSaveSettings` is in `internal/api/admin.go`, not in `settings.go` as the task assumed. `admin.go` is owned by P0-07, so I did not edit it. Until it changes, the live PUT still replaces the whole struct.

## Interfaces exposed
```go
// internal/api
func (c *SettingsCache) Merge(ctx context.Context, body []byte) (Settings, error)
// errors: *apiError 400 "bad_json" (not an object, unknown field, wrong type),
//         *apiError 400 "invalid_settings" (validation), plain error for store failures.

// internal/config
type Config struct {
    // ...
    KEKFile                 string // VAULTDOCK_KEK_FILE, default filepath.Join(StateDir, "keys", "kek")
    AIAllowPrivateEndpoints bool   // VAULTDOCK_AI_ALLOW_PRIVATE_ENDPOINTS, default false
    SyncTombstoneDays       int    // VAULTDOCK_SYNC_TOMBSTONE_DAYS, default 90, clamped to [90, 3650]
}
const MinSyncTombstoneDays = 90
```
- A JSON `null` for a single field, as in `{"site_name": null}`, leaves that field unchanged. This is standard `encoding/json` behaviour for non-pointer fields.

## Commands run and results
- `cd D:/VaultDock/vad-server && gofmt -w <3 files> && go build ./... && go vet ./internal/api/ ./internal/config/ && go test ./internal/api/ ./internal/config/`
- Result: build and vet clean. `internal/api` passed (`ok`, 4.7s). `internal/config` has no test files.

## Contract issues
1. **Tombstone default.** api-v1 §10.2 says "Tombstones are retained ≥ 90 days". The task asked for a default of 30, which breaks that rule. I followed the locked contract: the default is **90**, and values below 90 are raised to 90. P0-32 (deploy schema) should document a default of 90. INT should decide whether to amend the contract or the task.
2. **Admin setting or env variable?** The contract calls the tombstone window an "admin setting" (§10.2) and says an admin "enables `ai_allow_private_endpoints`" (§11). The task asked for env-based config, which is what I added. If these must be changeable at runtime by an admin, they belong in `Settings` or `/admin/backup-settings` later, with the env value as the default.
3. **KEK path.** MASTER_PLAN §7.2 (line 357) mentions `/state/keys/master.key`, but the task specifies `<state dir>/keys/kek`. I used the task value. P0-32 and `install.sh` need to use the same default.

## Gaps / follow-ups
- **Required:** wire `Merge` into `handleAdminSaveSettings` in `internal/api/admin.go` (owner P0-07, or INT). Replacement:
  ```go
  func (s *Server) handleAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
  	var body json.RawMessage
  	if err := decode(r, &body); err != nil {
  		writeErr(w, r, err)
  		return
  	}
  	v, err := s.settings.Merge(r.Context(), body)
  	if err != nil {
  		writeErr(w, r, err)
  		return
  	}
  	s.blobs.GlobalMinFree = v.MinFreeBytes
  	s.activity(r, 0, "admin.settings_updated", "", fmt.Sprintf("registration=%v default_quota=%s", v.RegistrationEnabled, humanBytes(v.DefaultQuotaBytes)), true)
  	writeJSON(w, 200, map[string]any{"settings": v})
  }
  ```
  - `admin.go` may need `encoding/json` in its imports.
  - The response shape `{"settings": {...}}` and the error codes (`bad_json`, `invalid_settings`) stay the same.
  - After this change, an HTTP-level test (PUT a partial body, then GET) could go in `settings_merge_test.go` or an `api_test`-package file. It is not possible now because `api_test.go` is frozen.
- The new config fields are not used anywhere yet (keys, AI and sync tasks will use them). `Load` does not create the KEK file.

## Notes for dependent tasks / INT
- Keys/escrow tasks read the KEK path from `cfg.KEKFile`. AI endpoint validation reads `cfg.AIAllowPrivateEndpoints`. The sync feed reads `cfg.SyncTombstoneDays`.
- Data race R7 on `GlobalMinFree`, which the handler writes, is still open and belongs to P0-07.
- Line endings: my edits wrote LF. With `core.autocrlf=true`, `git diff` shows only the real changes.
