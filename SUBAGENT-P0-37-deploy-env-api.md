# P0-37 Deploy env API (api-v1 §11A) + internal/deployenv
Status: completed

## Goal
SRV-0. Implement api-v1 §11A:
- `GET /admin/deploy/env`, `POST /admin/deploy/env/removals`, `DELETE /admin/deploy/env/removals/{name}` (all `super`);
- an `internal/deployenv` package that embeds and parses `deploy/env.schema.json`, computes the environment view (secrets masked) and reads/writes the admin approvals file that `install.sh upgrade` consumes;
- advertise `deploy_env.v1`.

## Changes (repo: path, created/modified/deleted, why)
- `vad-server: deploy/embed.go` (created). `package deploy` with `//go:embed env.schema.json` as `var EnvSchema []byte`. Go embed cannot reach parent directories from `internal/`, so the schema stays in one place: `deploy/env.schema.json`, which DEP owns.
- `vad-server: internal/deployenv/deployenv.go` (created). Schema types, `Parse`, `Embedded`, `Compute` (the §11A view) and `ValidName`.
- `vad-server: internal/deployenv/approvals.go` (created). The `Approvals` file store. Writes are atomic: temp file in the same directory, fsync, rename, then a best-effort directory sync. The file is removed when the list becomes empty.
- `vad-server: internal/deployenv/deployenv_test.go` (created). Covers:
  - parsing the embedded schema, and rejection of bad schemas;
  - classification and masking (no secret value or secret default in the JSON; empty lists are `[]`, never `null`);
  - an approvals round trip: on-disk format, an existing approval is kept, withdraw, the file is deleted after the last withdraw, no temp files are left;
  - reading the wrapped `{"approvals":[…]}` form, and rejecting a corrupt file.
- `vad-server: internal/api/deploy_env.go` (stub replaced). The three handlers on `rt.Super`, plus `init() { implementFeature(wire.FeatureDeployEnv) }`.
- `vad-server: internal/api/deploy_env_test.go` (created). Covers:
  - auth: a plain user, an admin without `is_superuser` (`403 not_superuser`) and a signed device are refused, and a refused request writes no file;
  - the GET contents, and that the setup token (secret) never appears in the reply;
  - validation cases;
  - `409 env_change_requires_approval`;
  - approve: the file is written in the state volume;
  - withdraw: 404 for a name that is not pending, 200 otherwise, and the file is removed;
  - `/info` advertises `deploy_env.v1`.

## Interfaces exposed
- `package deploy`: `var EnvSchema []byte`.
- `package deployenv`:
  - `type Variable struct{Name, Default string; Required, Secret, EnvFile, Managed bool; Scope, Since string; Obsolete bool; DeprecatedSince, Replacement, Description string}`
  - `type Schema struct{SchemaVersion int; Variables []Variable}`
  - `func Parse([]byte) (*Schema, error)`, `func Embedded() (*Schema, error)`, `func (*Schema) Lookup(name string) (Variable, bool)`
  - `type LookupFunc func(string) (string, bool)`
  - `type View struct{ wire.DeployEnvStatus; Missing []string }`, `func Compute(s *Schema, lookup LookupFunc, pending []wire.DeployEnvRemovalApproval) View`
  - `func ValidName(string) bool`
  - `const ApprovalsFile = "deploy/env-removal-approvals.json"`
  - `func NewApprovals(stateDir string) *Approvals`, and on `*Approvals`: `Path() string`, `List() ([]wire.DeployEnvRemovalApproval, error)`, `Approve(names []string, by, at int64) ([]wire.DeployEnvRemovalApproval, error)`, `Withdraw(name string) (bool, error)`
- **Approvals file:**
  - Path: `${cfg.StateDir}/deploy/env-removal-approvals.json`, i.e. `/state/...` in the container and `${VAULTDOCK_STATE_PATH}/deploy/...` on the host, as `install.sh` `env_admin_approvals_file` expects.
  - Format: a JSON array of flat objects `{"name","approved_by","approved_at"}` (ms). Mode 0600; the directory is created with 0750.
  - The reader also accepts `{"approvals":[…]}`.
- **Routes** (`super`):
  - `GET /admin/deploy/env` → `200 wire.DeployEnvStatus`.
  - `POST /admin/deploy/env/removals` (`wire.DeployEnvRemovalRequest`, unknown fields rejected) → `200` with the updated `DeployEnvStatus`.
    - An already approved name keeps its original approval.
    - Audited as `admin.env_removal_approved`.
  - `DELETE /admin/deploy/env/removals/{name}` → `200` with the updated status, or `404 not_found` when the name is not pending.
    - Audited as `admin.env_removal_withdrawn`.
  - Errors:
    - `400 validation_failed` with `details.fields.{names|confirm_text|name}`: empty or too many names, a malformed or duplicate name, a name not in the schema, or a wrong `confirm_text`.
    - `409 env_change_requires_approval` with `details.names`: a known but non-obsolete variable. Only `install.sh` can make that change.
- **Semantics of `Compute`:**
  - The process environment is checked for presence only; values are never read into the reply.
  - `added_by_upgrade`: non-obsolete variables with `env_file: true` and scope ≠ `installer` whose `since` is newer than the schema baseline (the lowest `since`, currently 1.0.0). The `default` of a secret variable is returned as `""`.
  - `obsolete_present`: obsolete variables set in the container environment. `note` = the schema `description`.
  - `pending_removals`: the approvals file, sorted by name.
  - `Missing` (internal only; not in the wire type): required, non-obsolete `scope: server` variables that are unset.

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./...`: OK.
- `go vet ./internal/deployenv/ ./internal/api/ ./deploy/`: OK.
- `gofmt -l` on the owned files: clean.
- `go test ./internal/deployenv/`: ok.
- `go test ./internal/api/ -run TestDeployEnv`: PASS.
- `go test ./internal/api/` (full): **2 failures, both in `devices_delete_test.go` (not owned)**. Details are under "Notes for dependent tasks / INT".

## Contract issues
- §11A does not define the error code for a malformed request. I used `400 validation_failed` with `details.fields`, following the §1 rule "Validation errors go in details.fields".
- §11A does not say when `env_change_requires_approval` (§1.1) applies. I return it for removal requests that name non-obsolete schema variables.
- `added_by_upgrade` is not defined precisely, and the server cannot see the host `.env` or the previous installed version. I approximated it with the schema `since` versus the schema baseline (see above).
- Like P0-32, I noted that the approvals file shape is only "(name, approved_by, approved_at)". I chose a bare array, which `install.sh` handles with both jq and awk.

## Gaps / follow-ups
- `obsolete_present` reflects the container environment. That matches the host `.env` only for variables `docker compose` passes through `env_file` (true for every current `env_file` variable).
- `since` values are still P0-32 placeholders (1.0.0 / 1.1.0). `added_by_upgrade` follows them automatically once INT aligns them.

## Notes for dependent tasks / INT
- **`Dockerfile` (not owned) — required, or the image build breaks:**
  - The build stage copies only `cmd`, `internal` and `web/embed.go`.
  - Add `COPY deploy/embed.go deploy/env.schema.json ./deploy/` before `go build`.
  - `.dockerignore` does not exclude `deploy/`. Its header comment could list it.
- **`internal/api/devices_delete_test.go` (P0-03's test, not owned) needs updating:**
  - `TestDeviceDeletePermission` and `TestStubRoutesAndAuthGroups` assert an empty `features` list. That was already failing before this task, because `modules.v1` (P0-38) is advertised.
  - `TestStubRoutesAndAuthGroups` also expects `501` from `GET /admin/deploy/env` for the superuser. It now returns 200.
  - Suggested fix: drop the empty-features assertions, or compare them against the implemented set. Point the super-group 501 probe at another super stub such as `POST /admin/keys/rotate-server` (it must still be a stub), or expect 200 here. The `not_superuser` checks for admin2/alice stay valid.
- **Web (admin UI):** POST and DELETE return the full updated `DeployEnvStatus`, so the UI can re-render without a second GET. The confirm text comes from `wire.DeployEnvRemovalConfirmText(n)`: `REMOVE <n> VARIABLES`.
- No new dependencies.
