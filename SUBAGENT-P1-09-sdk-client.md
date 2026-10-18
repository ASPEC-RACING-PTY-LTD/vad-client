# P1-09 SDK-CLIENT SDK client methods
Status: completed

## Goal
Add device-side (VD1-signed) client methods in `sdk/client` built on the `sdk/wire` types. They cover capabilities and features, §5A keys with the SIK pin check, repos, objects (streamed PUT), locks, snapshots (streamed commit), jobs, runs, issues, Retry Skipped, device tasks, modules, profiles and backup-state. Also add the response helpers: 202 accepted, plus typed `*APIError` details for `module_unavailable` and `repo_locked`.

## Changes (repo VaultDock)
| File | Status | Why |
|---|---|---|
| `sdk/client/client.go` | modified | `APIError.Details` (raw `error.details`). Error decoding moved to `open`/`decodeAPIError`. New `sendStatus`/`doStatus` (status code, extra unsigned headers such as If-Match); an empty or 204 success body no longer fails decoding. §5A fields on `EnrollRequest` (`box_pub`, `install_mode`, `core_version`; `omitempty`, so existing callers are unchanged). `EnrollStatus.ServerIdentity`. `Me.Features`. Response helpers: `apiPath`, `listQuery`, `Accepted`, `ParseAccepted`, `CallAccepted`, `Operation`, `ModuleUnavailableDetails`, `DecodeDetails`, `ModuleUnavailable`, `RepoLocked` |
| `sdk/client/capabilities.go` | created | `/info`, `/device/me` features, `/backup/capabilities`, `PUT /device/backup-state` |
| `sdk/client/keys.go` | created | `GET /keys/server`, the SIK pin check, key status, key_share/rewrap task results |
| `sdk/client/repo.go` | created | repos, objects (streamed PUT with a verifying reader), missing objects, locks, streamed snapshot commit, snapshot list/get |
| `sdk/client/jobs.go` | created | jobs list/get/create/patch, runs start/heartbeat/finish, issues, retry-skipped |
| `sdk/client/tasks.go` | created | task long-poll (which also claims), task result |
| `sdk/client/modules.go` | created | `PUT /device/modules`, `GET /modules/catalog` |
| `sdk/client/profiles.go` | created | `PUT /device/profiles`, security event |
| `sdk/client/repo_test.go` | created | `httptest` fake server that verifies the VD1 signature with `protocol.Verify`, the body SHA-256 and the Content-Length. Tests: streamed PUT (plain `io.Reader`, no chunked encoding, replay 200, a tampered or short body aborts and is never accepted, config needs an explicit hash), HEAD/GET+Range, repos, object list, missing batching, locks + `repo_locked` details, streamed commit with a >2 MiB body + replay, `module_unavailable` details + Retry-After, 202 operation/approval |
| `sdk/client/{keys,jobs,tasks,capabilities,modules,profiles}_test.go` | created | per-file tests against the same fake |

`sdk/client/upload.go` and `sdk/protocol/*` are untouched. No protocol helper was needed: a streamed body's hash is known before sending (the oid, or a pre-pass), so the existing `protocol.Sign` is enough.

## Interfaces exposed
```go
// client.go
type APIError struct { Status int; Code string; Message string; Details json.RawMessage; RetryAfter time.Duration; Body json.RawMessage }
func (e *APIError) DecodeDetails(v any) bool
type ModuleUnavailableDetails struct { Module string `json:"module"`; DeviceID int64 `json:"device_id"` }
func ModuleUnavailable(err error) (*ModuleUnavailableDetails, bool)
func RepoLocked(err error) (*wire.LockedDetails, bool)
type Accepted struct { Operation *wire.OperationAccepted; Approval *wire.ApprovalAccepted }
func ParseAccepted(raw []byte) (*Accepted, error)
func (c *Client) CallAccepted(ctx context.Context, method, path string, in any) (*Accepted, error)
func (c *Client) Operation(ctx context.Context, id int64) (*wire.Operation, error)
// EnrollRequest += BoxPub, InstallMode, CoreVersion (omitempty); EnrollStatus += ServerIdentity *wire.ServerIdentity; Me += Features []string

// capabilities.go
type ServerInfo struct { Name, SiteName, Version string; RegistrationEnabled bool; ChunkSize, ServerTime int64; Features []string }
func (i *ServerInfo) HasFeature(f string) bool
func (m *Me) HasFeature(f string) bool
func (c *Client) Info(ctx context.Context) (*ServerInfo, error)               // unsigned
func (c *Client) Capabilities(ctx context.Context) (*wire.Capabilities, error)
func (c *Client) ReportBackupState(ctx context.Context, st wire.BackupState) error

// keys.go
func (c *Client) ServerIdentity(ctx context.Context) (*wire.ServerIdentity, error)
func (c *Client) VerifyServerIdentity(ctx context.Context, pinned string) (*wire.ServerIdentity, error)
var ErrNoSIKPin error
func CheckSIKPin(pinned string, id *wire.ServerIdentity) error   // 409 sik_mismatch *APIError on mismatch; constant-time; ignores case, ':' '-' ' '
func (c *Client) KeyStatus(ctx context.Context, repoID int64) (*wire.KeyStatus, error)
type KeyTaskResult struct { RepoID int64 `json:"repo_id"`; Epoch int64 `json:"epoch,omitempty"`; KeyFileOID string `json:"key_file_oid,omitempty"`; Error string `json:"error,omitempty"` }
func (c *Client) FinishKeyShare(ctx context.Context, taskID int64, res KeyTaskResult) error
func (c *Client) FinishRewrap(ctx context.Context, taskID int64, res KeyTaskResult) error

// repo.go
func (c *Client) CreateRepo(ctx context.Context, req wire.RepoCreateRequest) (*wire.Repo, error)
func (c *Client) Repos(ctx context.Context, cursor string, limit int) (*wire.List[wire.Repo], error)
func (c *Client) Repo(ctx context.Context, id int64) (*wire.Repo, error)
func (c *Client) HeadObject(ctx context.Context, repoID int64, kind, oid string) (exists bool, size int64, err error)
func (c *Client) PutObject(ctx context.Context, repoID int64, kind, oid string, body io.Reader, size int64, sha256Hex string) (created bool, err error)
var ErrBodyMismatch error
func (c *Client) GetObject(ctx context.Context, repoID int64, kind, oid string, offset, length int64) (io.ReadCloser, error)
func (c *Client) ListObjects(ctx context.Context, repoID int64, kind, cursor string, limit int) (*wire.List[wire.ObjectInfo], error)
func (c *Client) MissingObjects(ctx context.Context, repoID int64, kind string, oids []string) ([]string, error)
func (c *Client) AcquireLock(ctx context.Context, repoID int64, kind string, ttlSeconds int) (*wire.Lock, error)
func (c *Client) RefreshLock(ctx context.Context, repoID int64, lockID string, ttlSeconds int) (*wire.Lock, error)
func (c *Client) ReleaseLock(ctx context.Context, repoID int64, lockID string) error
func (c *Client) CommitSnapshot(ctx context.Context, repoID int64, req wire.SnapshotCommitRequest) (resp *wire.SnapshotCommitResponse, created bool, err error)
type SnapshotQuery struct { RepoID, JobID, DeviceID int64; Profile, Kind, Tag string; From, To int64; Protected *bool; Cursor string; Limit int }
func (c *Client) Snapshots(ctx context.Context, q SnapshotQuery) (*wire.List[wire.Snapshot], error)
func (c *Client) Snapshot(ctx context.Context, id int64) (*wire.Snapshot, error)

// jobs.go
func (c *Client) Jobs(ctx context.Context, cursor string, limit int) (*wire.List[wire.Job], error)   // ?device=<own id>
func (c *Client) Job(ctx context.Context, id int64) (*wire.Job, error)                              // pages Jobs
func (c *Client) CreateJob(ctx context.Context, req wire.JobCreateRequest) (*wire.Job, error)       // DeviceID defaults to own
func (c *Client) UpdateJob(ctx context.Context, id, revision int64, req wire.JobUpdateRequest) (*wire.Job, error) // If-Match
func (c *Client) StartRun(ctx context.Context, jobID int64, req wire.RunStartRequest) (int64, error)
func (c *Client) RunHeartbeat(ctx context.Context, runID int64, p wire.RunProgress) error
func (c *Client) FinishRun(ctx context.Context, runID int64, p wire.RunProgress) error              // terminal status only
func (c *Client) ReportIssues(ctx context.Context, runID int64, issues []wire.IssueReport) error    // batches of 500
func (c *Client) RetrySkipped(ctx context.Context, runID int64) (*wire.DeviceTaskCreated, error)

// tasks.go
const MaxTaskWait = 25 * time.Second
func (c *Client) PollTasks(ctx context.Context, wait time.Duration) ([]wire.DeviceTask, error)       // returned tasks are claimed
func (c *Client) TaskResult(ctx context.Context, taskID int64, status string, result json.RawMessage) error

// modules.go
func (c *Client) ReportModules(ctx context.Context, rep wire.ModuleReport) error
func (c *Client) ModuleCatalog(ctx context.Context) ([]wire.ModuleCatalogEntry, error)

// profiles.go
func (c *Client) ReportProfiles(ctx context.Context, rep wire.ProfileReport) error
func (c *Client) ReportSecurityEvent(ctx context.Context, sid string, ev wire.ProfileSecurityEventRequest) error
```

## Commands run and results
- `cd sdk && gofmt -l client protocol`: no output.
- `go vet ./client/ ./protocol/`: clean.
- `go test -count=1 ./client/ ./protocol/`: ok, both packages. `TestSelfSignedPinning` logs the expected TLS handshake errors.
- `cd vad-server && go build ./...`: ok. `go vet ./internal/api/`: ok (the frozen `api_test.go` still compiles against the extended `EnrollRequest`).
- `cd desktop && go build ./...`: ok. `go vet ./internal/transfer/ ./internal/engine/`: ok.

## Contract issues
1. **No `GET /backup/jobs/{id}`** exists in rev 1.0 or rev 1.1. `Job(id)` pages through the device's own job list and returns a synthetic 404 `not_found` `*APIError` when the job is absent.
2. **`POST /backup/runs/{id}/retry-skipped` is `sess` only.** The method is implemented as asked, but the server mounts the route as `rt.Sess`, so a device signature is rejected. Either INT makes the route dev-accessible, or the device should start a run with `trigger:"retry_skipped"` when it receives a `retry_skipped` task.
3. **No explicit claim route.** The contract says "returning a task claims it". `PollTasks` is the claim; there is no `ClaimTask`.
4. **Undefined shapes:**
   - The key_share/rewrap task `result` is not defined. `KeyTaskResult{repo_id, epoch?, key_file_oid?, error?}` is my choice.
   - The lock refresh (PUT) body and reply are not defined. `RefreshLock` sends `{"ttl_seconds"}` only when ttl > 0, and accepts an empty/204 reply or a `Lock`.
5. **`/snapshots` and `/snapshots/{id}` are `sess` in §5.4.** `Snapshots`/`Snapshot` are signed device calls, as asked. Devices can use them only if the server also mounts them for `dev`.
6. **`GET /repos` list shape not stated.** It is assumed to be `List[Repo]` (the §1 list convention).
7. **The SIK fingerprint format is not specified** (backup-format §4 only says it is pinned). `CheckSIKPin` compares case-insensitively and ignores `:`, `-` and spaces. It does not re-derive the fingerprint from `sik_pub`.

## Gaps / follow-ups
- There is no `ChunkBody`-style progress or bandwidth hook for `PutObject`. Callers can wrap the `io.Reader` they pass in.
- `sync.go` and `download.go` (Phase 5) are not part of this task.
- Admin/session-only routes (key rotate, escrow change, approvals) are not wrapped. `CallAccepted` is the generic helper for 202 routes.

## Notes for dependent tasks / INT
- **Streamed routes:**
  - `PutObject` takes the exact `size` and the body SHA-256. The SHA-256 defaults to the oid; `config` must pass it explicitly.
  - The body is hashed while it is sent. On a mismatch or a short body the request is aborted (no complete request reaches the server), and the error wraps `ErrBodyMismatch`.
  - A zero-length body is sent as `http.NoBody`.
  - `CommitSnapshot` encodes the JSON twice (hash pass, then a streamed pass through `io.Pipe`), so bodies over 2 MiB work.
- **Error handling:**
  - Use `IsCode(err, wire.Code…)` for codes.
  - `ModuleUnavailable(err)` and `RepoLocked(err)` give the typed details.
  - `(*APIError).DecodeDetails` handles any other details.
- **Changed behaviour of the existing `send`:** a 2xx response with an empty body no longer returns an EOF decode error. Existing callers are unaffected.
- **Enrolment:** desktop enrolment (DSK tasks) should now set `EnrollRequest.BoxPub/InstallMode/CoreVersion` and pin `EnrollStatus.ServerIdentity.SIKFingerprint`.
- **Server side:** `GET /device/me` already emits `features`. The P1-10 server backend adapter can build directly on the `repo.go` methods.
