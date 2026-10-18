# P1-17 (SRV-3) Device tasks (long-poll) + approvals
Status: completed (inventory routes `PUT /device/inventory` and `GET /devices/{id}/apps`, which live in the same stub file, are still 501; see Gaps)

## Goal
Implement api-v1 §6 device tasks (long-poll delivery with claim-on-return, leases, results, session create/list) and §8 approvals (inbox, approve/reject, `consumed`, resume through the P0-05 runner via a per-kind handler registry), with the flags `device_tasks.v1` and `approvals.v1`.

## Changes (repo vad-server)
| File | Change |
|---|---|
| `internal/store/device_tasks.go` | created: task store (create, claim, batch claim with module gate, complete, lease expiry, release/cancel/fail, list) |
| `internal/store/approvals.go` | created: approval store (create, get with lazy expiry, list, decide, expire, consume, link operation) |
| `internal/api/device_tasks.go` | replaced stub: poll/result/create/list routes, `CreateTask`, in-process notifier, task result hooks, `device_task` approval handler; wires `queueDeviceTask` (modules.go) to `CreateTask` from `init()` |
| `internal/api/approvals.go` | replaced stub: routes, `RequireApproval`/`acceptApproval`, handler registry, permission checks, helpers `decodeOptional`, `listParams` |
| `internal/store/device_tasks_test.go`, `internal/store/approvals_test.go` | created |
| `internal/api/device_tasks_test.go`, `internal/api/approvals_test.go` | created |

`server.go` not touched. The notifier lives in a package-level `sync.Map` keyed by `*Server`, because `Server` has no field for it.

## Interfaces exposed

### Routes
| Route | Auth | Behaviour |
|---|---|---|
| `GET /device/tasks?wait=0..25` | dev | Expires stale leases, then claims up to 20 claimable tasks (pending, approval null/approved/consumed, modules enabled). If none and wait > 0, it blocks until woken by a new/released task, the timeout, or client disconnect. Values > 25 are clamped; a negative/non-numeric value gives 400 `validation`. Returns `{"items":[DeviceTask]}`; every returned task is `claimed` with a 10 min lease |
| `POST /device/tasks/{id}/result` | dev | `{status: done\|failed, result: object\|null}` → `204`. Not claimed by this device (or already finished / requeued after the lease ended) → `409 task_not_claimable` |
| `POST /device/tasks` | sess | Device owner or admin. Payload validated with `wire.DecodeTaskPayload` (unknown kind/field → 400 `validation`, `details.fields.kind\|payload`). Unknown/foreign device → 400 `validation` (`device_id`); unapproved device → 409 `device_not_approved`; module missing → 409 `module_unavailable`. Normal → `201 {task_id}`; `restore` / `prune_compact` → `202 {task_id, approval_id}` (risk high, subject name = device name) |
| `GET /device/tasks/list?device=&status=&cursor=&limit=` | sess | Caller's tasks, newest first. `List[DeviceTask]`; cursor = last id |
| `GET /approvals?state=&cursor=&limit=` | sess | Own approvals, plus global/superuser ones for admins. `List[Approval]` |
| `GET /approvals/{id}` | sess | Invisible → 404 |
| `POST /approvals/{id}/approve` | sess+CSRF | `{confirm_text}` (body optional) → `200 {…Approval, "operation_id": N\|null}` |
| `POST /approvals/{id}/reject` | sess+CSRF | `{reason}` (optional, ≤1000 chars) → `200 {…Approval, "operation_id": null}` |

Errors on approve/reject:
- not allowed → `403 forbidden` (or `403 not_superuser` for superuser approvals);
- not pending → `409 approval_not_pending` (`details.state`);
- high risk and `confirm_text` ≠ subject name → `400 validation`;
- digest mismatch → the approval becomes `expired`, `409 plan_stale`.

Wire mapping:
- Task DB `awaiting_approval` is shown as `pending` with `approval_id` set.
- `created_by` is `user` when `device_tasks.created_by` (a user FK) is set, else `server`.
- Approval DB `executed` is shown as `consumed` (the P0-02 CHECK has no `consumed`).

### Go helpers for other features (package `api`)
```go
// Device tasks
type TaskRequest struct {
    UserID, DeviceID int64      // UserID = device owner
    Kind             string
    Payload          any        // typed wire payload or json.RawMessage; validated by wire.DecodeTaskPayload
    RequiredModules  []string   // nil → default per kind (automation_op→automation, profile_consent_request→profiles)
    CreatedBy        *int64     // creating user; nil = server
    Approval         *ApprovalRequest // non-nil gates the task (kind device_task, subject device_task/<id>, digest of {device_id,kind,payload} unless given)
}
func (s *Server) CreateTask(ctx context.Context, req TaskRequest) (*wire.DeviceTaskCreated, error) // checks modules (409 module_unavailable), wakes long-polls
type TaskResultHook func(ctx context.Context, s *Server, t *store.DeviceTask) error
func RegisterTaskResultHook(kind string, h TaskResultHook) // runs after a device result, and after a task fails from lease exhaustion

// Approvals
type ApprovalRequest struct {
    UserID int64; Kind, SubjectType string; SubjectID int64
    SubjectName, Summary, Risk string         // SubjectName required for risk high (= confirm_text)
    Plan any; PlanDigest string               // Plan is digested with store.PlanDigest; else PlanDigest as given
    Data any                                  // kept for the handler (store.Approval.Data JSON)
    RequestedBy, RequesterRef string; RequestedByUserID, RequestedByDeviceID *int64
    Global, Superuser bool                    // Global: any admin decides; Superuser: only superusers decide
    TTL time.Duration                         // 0 = 72 h
}
func (s *Server) RequireApproval(ctx context.Context, req ApprovalRequest) (*wire.ApprovalAccepted, error) // operation_id always null
func (s *Server) acceptApproval(w http.ResponseWriter, r *http.Request, req ApprovalRequest)              // fills requester from the principal, writes 202
type ApprovalHandler struct {
    OpKind   string // "" = Resume runs inline in the approve request (p nil); else a long operation of that kind owned by the approval owner
    Digest   func(ctx context.Context, s *Server, a *store.Approval) (string, error) // optional rev 1.0 §8 rule 4 check
    Resume   func(ctx context.Context, s *Server, a *store.Approval, p *OpProgress) (any, error)
    Rejected func(ctx context.Context, s *Server, a *store.Approval) error // optional, after reject/expiry/stale
}
func RegisterApprovalHandler(kind string, h ApprovalHandler) // call from init(); panics on empty kind / nil Resume
```
Resume semantics:
- **Inline handler (`OpKind` ""):** the approval becomes `consumed` when Resume returns nil.
- **Operation handler (`OpKind` set):** the approval becomes `consumed` when Resume returns, success or not. The operation records the outcome, and its id is stored in `approvals.operation_id`.
- **Kind with no handler:** the approval stays `approved`. The feature checks it itself and calls `Store.ConsumeApproval`.
- **Built in:** the `device_task` kind is registered. Approving releases the task to `pending` and wakes the device, which makes the approval `consumed`. Rejecting or expiring cancels the waiting task, in the same store transaction.
- **Notification:** `createApproval` also adds an in-app notification ("Approval required", source `approval`) for the owner.

### Store (package `store`)
```go
const TaskStatusAwaitingApproval = "awaiting_approval"; DefaultTaskLease = 10*time.Minute; MaxTaskAttempts = 3; TaskErrorLeaseExpired = "lease_expired"
var ErrTaskNotClaimable, ErrApprovalNotPending error
type DeviceTask struct{…}; type TaskFilter struct{UserID, DeviceID int64; Status string; BeforeID int64; Limit int}
func (s *Store) CreateTask(ctx, *DeviceTask) error
func (s *Store) Task(ctx, id) (*DeviceTask, error)
func (s *Store) SetTaskApproval(ctx, taskID, approvalID) error
func (s *Store) ReleaseTask / CancelTask(ctx, id, reason) / FailTask(ctx, id, code, msg)
func (s *Store) ClaimTask(ctx, id, deviceID, lease) (*DeviceTask, error)              // atomic conditional UPDATE
func (s *Store) ClaimTasks(ctx, deviceID, limit, lease, enabled map[string]bool) ([]DeviceTask, error) // missing module → task failed module_unavailable
func (s *Store) CompleteTask(ctx, id, deviceID, status, result) (*DeviceTask, error)
func (s *Store) ExpireTaskLeases(ctx) (requeued, failed []int64, err error)          // requeue, or fail after MaxTaskAttempts
func (s *Store) ListTasks(ctx, TaskFilter) ([]DeviceTask, error)
const DefaultApprovalTTL = 72*time.Hour
type Approval struct{…}; type ApprovalFilter struct{UserID int64; IncludeGlobal bool; State string; BeforeID int64; Limit int}
func (s *Store) CreateApproval(ctx, *Approval) error
func (s *Store) Approval(ctx, id) (*Approval, error)     // lazily expires an overdue pending approval
func (s *Store) ListApprovals(ctx, ApprovalFilter) ([]Approval, error)
func (s *Store) DecideApproval(ctx, id, deciderID, approve bool, reason) (*Approval, error)
func (s *Store) ExpireApproval(ctx, id) error; ExpireApprovals(ctx) ([]int64, error)
func (s *Store) ConsumeApproval(ctx, id) error; SetApprovalOperation(ctx, id, opID) error
```
Storage choices:
- `approvals.title` holds the subject name.
- `approvals.payload` holds `{plan_digest, requester_ref, global, superuser, data}`.
- `approvals.source` holds `requested_by`.
- `decision_note` holds the reason.
- `device_tasks.expires_at` holds the lease end.

## Commands run and results
- `cd D:/VaultDock/vad-server && go build ./... && go vet ./internal/... && go test -count=1 ./internal/store/ ./internal/api/`: all OK (store 0.8 s, api 4.7 s).
- `gofmt -l` on the 8 owned files: clean.
- `-race` is unavailable on this host (no cgo).

## Tests
- **store:**
  - claim/complete, foreign-device and double claim;
  - 4 concurrent `ClaimTasks` on 5 tasks: each task is claimed exactly once;
  - lease expiry: requeue twice, then fail with `lease_expired`; a late result is refused; a live lease is untouched;
  - module-gated failure, approval-gated release, list/filter/paging;
  - approval lifecycle: consume needs approved, second decision refused, `consumed` mapping, global listing, state filters;
  - reject, bulk expiry and lazy expiry cancel waiting tasks.
- **api:**
  - feature flags advertised; `wait=0` returns empty;
  - **the long-poll wakes up** when a task is created;
  - claim-on-return with no double delivery;
  - result auth and validation, second result → 409;
  - list, validation and module gate;
  - **lease expiry requeue** via the poll;
  - gated restore → 202, no delivery, inbox, summary count, isolation;
  - devices cannot approve, admins cannot decide a user's approval, `confirm_text`;
  - **approve wakes an in-flight long-poll** and returns `consumed`;
  - reject cancels the task;
  - **approve → resume through an operation** → `consumed`;
  - digest mismatch → `plan_stale` + `expired`;
  - global approvals (admin decides, user 404), superuser approvals (owner gets `not_superuser`, the superuser admin approves).

## Contract issues
1. Rev 1.0 does not define the approve/reject response. I return the Approval plus `operation_id` (an implementer choice; extra fields are allowed by §1).
2. There is no error code for deciding a non-pending approval. I used `409 approval_not_pending`.
3. The `POST /device/tasks` status for a non-gated task is unspecified. I used `201 {task_id}`, matching modules.go. The `/result` reply is unspecified. I used `204`.
4. P0-02 `approvals.status` has no `consumed` value; it is stored as `executed`. Its extra values `cancelled` and `failed` are never written; they would read as `rejected` and `consumed`.
5. P0-02 `device_tasks.created_by` is a user FK, so `automation` and `ai` creators cannot be represented (they show as `user` or `server`). A `created_by_kind` column would need a migration.
6. Approval visibility for "global subjects" and superuser-only approvals (§11B) is not specified. I chose: global and superuser approvals are visible to all admins. Global ones are decided by any admin, superuser ones only by superusers.
7. Leases are not in the contract. I chose a 10 min lease and 3 attempts, then `failed` with `error_code lease_expired`.

## Gaps / follow-ups
- `PUT /device/inventory` and `GET /devices/{id}/apps` (same stub file) are still 501. They are discovery-module features and need their own task. The `device_tasks.v1` flag is advertised anyway, because the task routes work.
- Expiry of approvals and leases is lazy: on approval list/get/approve and on every poll. Nothing sweeps them while idle. INT can add `ExpireApprovals` and `ExpireTaskLeases` to the app maintenance loop (`internal/app/app.go`), but `Rejected` hooks only run on the API path (`s.expireApprovals`).
- Tasks have no cancel route (none in the contract). `Store.CancelTask` exists.
- The notifier is in-process only, so multiple server replicas would need polling (not a current deployment shape).

## Notes for dependent tasks / INT
- **modules.go (SRV-M):** `device_tasks.go` `init()` reassigns `queueDeviceTask` to call `CreateTask`, which validates the payload, checks modules and wakes long-polls. This depends on the `deviceTaskSpec` fields `UserID, DeviceID, CreatedBy int64, Kind, Payload any, RequiredModules`. If SRV-M changes that struct, update the assignment. The TODO comment in modules.go can now be removed.
- **Restore / prune / GC / unprotect / escrow / two-way (SRV-2, SRV-3, SRV-4, SRV-5, SRV-K):**
  - In the execute handler, call `s.acceptApproval(w, r, api.ApprovalRequest{UserID, Kind: wire.ApprovalKind…, SubjectType, SubjectID, SubjectName, Summary, Risk, Plan: plan})`.
  - In `init()`, call `RegisterApprovalHandler(kind, ApprovalHandler{OpKind: wire.OperationKind…, Digest: recompute, Resume: run, Rejected: rollback})`.
  - A restore approved this way should create its `restore` device task with `s.CreateTask` (no Approval field; it is already approved).
- **Task result handling:** use `RegisterTaskResultHook(wire.TaskKindRestore, …)` to update restore/run state when the device reports.
- `listParams(r)` and `decodeOptional(r, v)` in approvals.go are generic helpers other files of package `api` may reuse.
