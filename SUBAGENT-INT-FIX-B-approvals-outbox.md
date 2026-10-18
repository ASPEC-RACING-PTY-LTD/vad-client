# INT-FIX-B SDK approvals and desktop outbox cleanup
Status: completed

## Goal
INT audit items 4–5:
- Enforce the per-node approval override (P0-12) and make `KindSpec.ApprovalPolicy()` honour spec-level policies.
- Move the desktop report outbox from a package-level `sync.Map` into an `Engine` field (P0-25).
- Remove the unused `Engine.scheduleLast` (P0-23).

## Changes (repo: path, created/modified/deleted, why)
All in client repo `VaultDock`:
- `sdk/automation/approval.go` (modified):
  - `RequiresApproval` and `markApproval` now gate on `NodeApproval(n).Required()` instead of `approvalPolicy(n.Type, n.Kind)`. A node with `approval: "required"` is now listed by `RequiresApproval`, and its ops are marked `OpApprovalPending`.
  - `KindSpec.ApprovalPolicy()` now returns `k.policy()`: the shared table tightened by the spec's `Destructive`/`Approval` fields.
- `sdk/automation/automation_test.go` (modified; added the `reflect` import):
  - `TestNodeApprovalOverrideGates`: a node override gates only that node, in both `RequiresApproval` and `Evaluate` ops.
  - `TestKindSpecApprovalPolicyUsesSpec`: `Destructive`/`Approval` on a non-default registry are honoured.
- `desktop/internal/engine/engine.go` (modified):
  - New field `outbox *outbox.Outbox`, guarded by `e.mu`.
  - `Start` initialises it through `e.reportOutbox()`.
  - Removed the `scheduleLast` field and its initialiser in `New`.
- `desktop/internal/engine/report.go` (modified):
  - Removed `var outboxes sync.Map` and the `sync` import.
  - `SetOutbox` sets `e.outbox` under `e.mu`.
  - `reportOutbox()` creates the outbox lazily under `e.mu` when it is unset and a store DB exists. This keeps tests that never call `Start` working.

## Interfaces exposed
No signature changes. `func (e *Engine) SetOutbox(o *outbox.Outbox)` and `func (k KindSpec) ApprovalPolicy() ApprovalPolicy` are unchanged. One semantic change: `RequiresApproval` and the op marking now include node-level overrides.

## Commands run and results
- `sdk`: `go build ./...` ok; `go vet ./automation/...` ok; `go test -count=1 ./automation/...` ok.
- `desktop`:
  - `go build ./...`: ok. The first attempt failed in `internal/keystore` (`undefined: pinRecord`/`loadPin`). That is another agent's file, which was mid-edit; the rerun passed.
  - `go vet ./internal/engine/ ./internal/outbox/`: ok.
  - `go test -count=1 ./internal/engine/ ./internal/outbox/`: ok.
  - `-race`: not available (cgo disabled).
- `desktop/e2e`: `go vet ./...` ok.
- `vad-server` (it imports `sdk/automation`):
  - `go build ./...`: ok.
  - `go test ./internal/...`: all pass except `internal/api` `TestBackupJobsCRUDGatingOwnDevice` (`backup_jobs_test.go:122: re-enable without module: 409 conflict`). That test belongs to the backup-jobs work and doesn't use automation/approval code, so it is unrelated to this change.
- `gofmt -l sdk/automation desktop/internal/engine`: clean.

## Contract issues
None.

## Gaps / follow-ups
- **`outbox.Run(e.ctx)` is deliberately not started in `Start`.** Delivery still runs from `timerLoop` → `flushReports` every 10s, which starts in `Start` and stops with `Stop` (ctx cancel + wg). That keeps behaviour identical.
  - `Run` would also flush while offline. `DeliverReport` returns a transient "not connected" there, so each offline flush would add an attempt and push `next_at` back by up to 15m, delaying delivery after reconnect.
  - `Run` would also race `flushReports`' own `Flush` (the TryLock makes one of them a no-op).
  - To start `Run` later, first make the sender stop without counting an attempt when no client is set.
- The failing `vad-server` backup-jobs test above belongs to its owner.

## Notes for dependent tasks / INT
- Audit items 4 and 5 are done. The P0-12 "Validate is v1-only" follow-up is untouched.
- No new dependencies.
