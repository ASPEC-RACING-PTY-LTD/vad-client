# P0-11 (SDK-AUTO) Generic ops, merge, approval metadata
Status: completed

## Goal
- Finish the P0-10 registry wiring: `suggestKind` uses the registry, `var catalog` is removed, and engine dispatch goes through registry handler funcs.
- Add generic ops with deterministic param merge semantics (`ops.go`).
- Add kind approval metadata plus `RequiresApproval(def)` (`approval.go`, api-v1 §8).
- Backward compatible; the golden catalogue is unchanged.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `sdk/automation/validate.go` (modified): `suggestKind` now uses `DefaultRegistry.Kinds(nodeType)`, which is already sorted.
- VaultDock: `sdk/automation/catalog.go` (modified): removed `var catalog = DefaultRegistry.kinds` and its comment. That is the only change in this file.
- VaultDock: `sdk/automation/engine.go` (modified):
  - New handler model: `TriggerFunc`/`ConditionFunc`/`ActionFunc`, `Handlers`, and `RegisterHandlers`/`MustRegisterHandlers` with a mutex-guarded table keyed by type/kind.
  - The three kind `switch`es became per-kind funcs, registered in `init()`.
  - `matchTrigger` keeps the shared `configs` prefilter and then dispatches. Unknown kinds behave as before: a trigger doesn't match with an empty reason, a condition returns "unknown condition", an action returns an empty detail.
  - `applyAction` stamps ops that the handler emitted (`markApproval`).
  - `Outcome.Ops` and `Plan.Ops` were added (`json:"ops,omitempty"`). `EvaluateAll` folds the outcomes' ops with `MergeOps` in priority order.
  - Logic is otherwise moved verbatim. The one small dedup is that `isFileWrite` is shared by the size and extension triggers.
- VaultDock: `sdk/automation/ops.go` (created): `Op`, `OpApprovalPending`, `Outcome.EmitOp`, `MergeOps`, `MergeParams`, `NodeParams`, and deep-clone helpers.
- VaultDock: `sdk/automation/approval.go` (created): `ApprovalLevel` and `ApprovalPolicy`, `SetApprovalPolicy`/`MustSetApprovalPolicy`, the `KindSpec.ApprovalPolicy()` method, `RequiresApproval`, and `markApproval`.
- VaultDock: `sdk/automation/ops_test.go` (created): table tests for MergeParams (including non-aliasing), NodeParams and MergeOps; checks that every built-in has a handler and that registration errors are reported; an end-to-end test of a gated action (pending ops in Outcome and Plan, policy cannot be lowered); and a check that v1 templates need no approval.
- VaultDock: `sdk/automation/registry_test.go` (modified, **outside my ownership; required**): line 65 indexed the removed `catalog` var. It now reads `DefaultRegistry.kinds[TypeAction]["notify.send"]`, the same assertion. Without this change the instructed removal does not compile.

## Interfaces exposed
```go
// engine.go
type TriggerFunc func(n *Node, ev *Event, vars Vars) (bool, string)
type ConditionFunc func(p map[string]any, vars Vars, depth int) (bool, string)
type ActionFunc func(o *Outcome, n *Node, vars Vars, ev *Event) string
type Handlers struct { Match TriggerFunc; Eval ConditionFunc; Apply ActionFunc }
func RegisterHandlers(nodeType, kind string, h Handlers) error // kind must exist in DefaultRegistry; handler for its type required; no duplicates
func MustRegisterHandlers(nodeType, kind string, h Handlers)
// Outcome.Ops []Op `json:"ops,omitempty"`; Plan.Ops []Op `json:"ops,omitempty"`

// ops.go
type Op struct { Kind string `json:"kind"`; NodeID string `json:"node_id,omitempty"`; Target string `json:"target,omitempty"`;
    Params map[string]any `json:"params,omitempty"`; Approval string `json:"approval,omitempty"` }
const OpApprovalPending = "pending"
func (o *Outcome) EmitOp(op Op)
func MergeOps(a, b []Op) []Op
func MergeParams(defaults, overrides map[string]any) map[string]any
func NodeParams(spec *KindSpec, params map[string]any) map[string]any

// approval.go
type ApprovalLevel string // ApprovalNone "none" | ApprovalOptional "optional" | ApprovalRequired "required"
type ApprovalPolicy struct { Destructive bool `json:"destructive,omitempty"`; Approval ApprovalLevel `json:"approval,omitempty"` }
func (p ApprovalPolicy) Required() bool                     // Destructive || Approval == required
func SetApprovalPolicy(nodeType, kind string, p ApprovalPolicy) error // only tightens; Destructive forces required
func MustSetApprovalPolicy(nodeType, kind string, p ApprovalPolicy)
func (k KindSpec) ApprovalPolicy() ApprovalPolicy
func RequiresApproval(def *Automation) []string             // gated node ids in document order; nil if none
```

**Merge semantics:**
- `MergeParams`:
  - overrides win;
  - nested `map[string]any` values merge recursively;
  - lists and scalars are replaced;
  - a nil override deletes the key;
  - the result never aliases its inputs.
- `MergeOps`:
  - the identity is `(Kind, Target)`;
  - when ops collide, params merge with b overriding a and NodeID is taken from b;
  - `pending` approval is sticky;
  - order is first appearance.
- In `EvaluateAll`, outcomes are merged in ascending priority, so the higher-priority automation wins, matching the existing destination rules.

## Commands run and results
- `cd sdk && gofmt -l automation`: clean.
- `go vet ./automation/`: OK.
- `go test -count=1 ./automation/`: OK. `TestGoldenCatalogV1` passes, so the golden file was **not** regenerated and the catalogue output is unchanged.
- `go test -count=3 -shuffle=on ./automation/`: OK. The tests that swap a built-in's handler or policy restore it in `t.Cleanup` and never register new kinds, so the golden test isn't polluted.
- `cd vad-server && go build ./...`: OK.
- `cd desktop && go build ./...`: OK. `transfer.go` built cleanly at the time of the run.

## Contract issues
None.

## Gaps / follow-ups
- **Approval metadata is not in `KindSpec`/the catalogue JSON.** `KindSpec` lives in `registry.go`, which P0-10 owns and I don't, so the policy sits in a side table exposed through the `KindSpec.ApprovalPolicy()` method.
  - If INT wants it served by `/automations/reference`, add `Destructive bool` and `Approval ApprovalLevel` (omitempty) fields to `KindSpec`, populate them in `Registry.Register`/`Catalog` from `approvalPolicy`, then regenerate the golden file with `-update`.
  - With omitempty, the v1 output stays byte-identical, because no v1 kind is gated.
- **Handlers are tied to `DefaultRegistry` only** (a package-level table), not to arbitrary `Registry` instances. Moving them into `Registry` would need a `registry.go` edit.
- **No v1 kind emits ops.** Plans for v1 documents are byte-identical, with `ops` omitted.
- **The document-level "raise the bar" (`params.approval: "always"`) is not implemented.** The strict param validator would reject it; that belongs to v2 schema work (P0-12).
- **Not done here (later tasks):** the validator rules for destructive actions (research 02 §4.5), the `approval.request` kind/`EvaluateFrom`, and the server approval store.

## Notes for dependent tasks / INT
- **To add a kind:**
  1. `DefaultRegistry.MustRegister(spec)`.
  2. `MustRegisterHandlers(type, kind, Handlers{...})`.
  3. If it is destructive or gated, `MustSetApprovalPolicy(TypeAction, kind, ApprovalPolicy{Destructive: true})`.

  Do all three in `init()` of a per-domain file. Registering into `DefaultRegistry` changes the golden catalogue, so run `-update` and bump the 27 count in `TestBuiltinKindsRegistered`.
- **Action handlers for new kinds** call `o.EmitOp(Op{Kind, Target, Params})`. The engine fills `NodeID` and sets `Approval: "pending"` for gated kinds. Runners must not execute pending ops without a server approval record.
- **`NodeParams(LookupKind(n.Type, n.Kind), n.Params)`** gives the effective params with defaults. The validator already normalises defaults in place for validated docs.
- **INT:** `registry_test.go` was touched by one line, as explained above. Please accept it in the ownership audit.
