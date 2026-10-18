# P0-12 (SDK-AUTO) Automation schema v2, runtime, migrate, KindsSupportedBy, fuzz, reference prose
Status: completed

## Goal
Add automation document schema v2 (`schema_version`, `runtime`, per-node approval override), a lossless v1→v2 `Migrate`, runtime capability on `KindSpec` with server-runtime validation, `KindsSupportedBy(version, modules)` for module/version gating, fuzz tests, and v2 reference prose. v1 documents stay valid and byte-identical.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `sdk/automation/schema.go` (modified)
  - Added `SchemaIDV2`, `SchemaVersionV2`, `CurrentSchemaVersion`, `SchemaVersionOf`, `SchemaIDFor`.
  - `SchemaID` stays the v1 id and the default write version, so older desktops keep working.
  - New `Automation` fields, both omitempty: `SchemaVersion int` (`schema_version`) and `Runtime string` (`runtime`).
  - New `Node.Approval ApprovalLevel` (`approval`, omitempty).
  - Added `type Definition = Automation`, plus `(*Automation).Version()` and `EffectiveRuntime()`.
  - `Parse` now decodes strictly through `decodeStrict` and validates with `ValidateDocument`, so it accepts v1 and v2.
- VaultDock: `sdk/automation/runtime.go` (created)
  - `RuntimeDevice`/`RuntimeServer`.
  - `ValidateDocument`, the version-aware wrapper over `Validate`.
  - `NodeApproval`, `RequiredModules`, `CheckSupport`, `UnsupportedError`.
  - A rule walker that covers nested group rules.
- VaultDock: `sdk/automation/migrate.go` (created): `Migrate` and `Upgrade`.
- VaultDock: `sdk/automation/registry.go` (modified)
  - New `KindSpec` fields `Runtimes`, `Destructive` and `Approval`, all omitempty, plus a `SupportsRuntime` method.
  - `Register` validates runtimes, approval level and min_schema_version. It defaults `Runtimes` to `["device"]`, normalises a destructive kind to `required`, and records the policies of DefaultRegistry kinds in the shared approval table (`recordPolicy`).
  - `Catalog()` fills Destructive/Approval from the effective policy.
  - The built-ins `schedule.interval`, `variable.compare`, `group` and `notify.send` are marked device+server. All other built-ins are device-only.
  - Added `KindsSupportedBy` (package-level and Registry method).
- VaultDock: `sdk/automation/reference.go` (modified)
  - `JSONSchema()` covers v1 and v2:
    - `schema` enum, `schema_version`, `runtime`;
    - action `approval`;
    - `x-runtimes` per kind;
    - an if/then that forbids the v2 top-level fields in v1 documents;
    - `$id` changed to automation-v2.json.
  - `AIGuide()` documents both versions and the v2 fields, tags kinds `[runs on server]`, `[needs …v2]`, `[destructive]` and `[approval required]`, and replaces the false "Actions never delete" claim.
- VaultDock: `sdk/automation/registry_test.go` (modified)
  - Golden handling is split. `TestGoldenCatalogV1` now compares the catalogue, with the v2 fields stripped, against the **frozen** `golden_v1.json`, which is never regenerated.
  - New `TestGoldenCatalogV2` covers the full catalogue. `-update` now rewrites only `golden_v2.json`.
- VaultDock: `sdk/automation/testdata/golden_v2.json` (created). `golden_v1.json` is unchanged and still passes.
- VaultDock: `sdk/automation/automation_test.go` (modified). New tests:
  - templates stay v1;
  - the old-client contract (v1 `Validate` rejects v2);
  - a v2 validation table (runtime, server kinds including nested group rules, approval tighten/lower/on trigger, v2 fields in v1, schema_version mismatch, unknown schema message);
  - NodeApproval;
  - KindsSupportedBy;
  - CheckSupport/RequiredModules;
  - Register v2 metadata;
  - JSONSchema/AIGuide v2.
- VaultDock: `sdk/automation/migrate_test.go` (created): templates migrate losslessly, idempotently and deterministically; error cases; Upgrade does not mutate its input.
- VaultDock: `sdk/automation/fuzz_test.go` (created): `FuzzParse`, `FuzzValidate` (lenient decode followed by ValidateDocument, RequiredModules, CheckSupport and NodeApproval) and `FuzzMigrate`. Each checks for no panic and a stable round trip; Migrate is also checked for idempotence and losslessness against Parse.

## Interfaces exposed
```go
const SchemaIDV2 = "vaultdock.automation/v2"; const SchemaVersionV2 = 2; const CurrentSchemaVersion = SchemaVersionV2
func SchemaVersionOf(id string) int; func SchemaIDFor(version int) string
type Definition = Automation
// Automation: SchemaVersion int `json:"schema_version,omitempty"`; Runtime string `json:"runtime,omitempty"`
// Node: Approval ApprovalLevel `json:"approval,omitempty"`
func (a *Automation) Version() int; func (a *Automation) EffectiveRuntime() string
const RuntimeDevice = "device"; const RuntimeServer = "server"
func ValidateDocument(a *Automation) *ValidationResult   // what Parse uses; v1+v2
func Migrate(doc []byte) (*Definition, int /*fromVersion*/, error) // error is *ValidationResult for parse/validation failures
func Upgrade(a *Automation) *Definition                   // v2 copy, no validation
func NodeApproval(n *Node) ApprovalPolicy                 // kind policy tightened by node override
func RequiredModules(a *Automation) []string              // sorted union incl. nested rule kinds
type UnsupportedError struct{ NodeID, Kind, Module string; SchemaVersion int; Runtime string }
func CheckSupport(a *Automation, version int, modules []string) error // nil | *UnsupportedError
// KindSpec: Runtimes []string `json:"runtimes,omitempty"`; Destructive bool `json:"destructive,omitempty"`; Approval ApprovalLevel `json:"approval,omitempty"`
func (k *KindSpec) SupportsRuntime(rt string) bool
func KindsSupportedBy(version int, modules []string) []KindSpec           // MinSchemaVersion<=version, all modules present, device runtime; sorted type then kind
func (r *Registry) KindsSupportedBy(version int, modules []string) []KindSpec
```

**v2 document rules (`ValidateDocument`):**
- `schema` must be the v1 or the v2 id.
- `schema_version` is optional. In v2 it must be 2; it is not allowed in v1.
- `runtime` is `device` (default) or `server`, and is v2 only. Every node kind and every group rule kind must list that runtime in `Runtimes`.
- `approval` is v2 only and only allowed on actions. Its value is none, optional or required, and it must not be lower than the kind's policy.
- A kind whose `MinSchemaVersion` is above the document version is an error.

**Migrate:**
- Sets `schema = v2`, `schema_version = 2`, and `runtime = "device"` if it was empty.
- Every other field is kept exactly as `Parse` would return it.
- Migrating a v2 document again gives the same bytes (idempotent).

**Golden files:** `go test ./automation/ -run TestGoldenCatalog -update` rewrites only `golden_v2.json`.

## Commands run and results
- `cd sdk && gofmt -l automation`: clean.
- `go vet ./automation/`: OK.
- `go test -count=1 ./automation/`: OK.
- `go test -count=2 -shuffle=on ./automation/`: OK.
- 15s fuzz runs (`go test ./automation/ -run='^$' -fuzz=^<target>$ -fuzztime=15s`), no failures and no corpus written:

  | Target | Executions |
  |---|---|
  | FuzzParse | about 5.5M |
  | FuzzValidate | about 6.0M |
  | FuzzMigrate | about 6.1M |
- `cd vad-server && go build ./... && go test ./internal/api/`: OK.
- `cd desktop && go build ./...`: OK.

## Contract issues
None. api-v1 does not define the automation document format. Module gating (§6A, `409 module_unavailable` + `details.module`) maps to `UnsupportedError.Module`.

## Gaps / follow-ups
- **The per-node approval override is not yet enforced by the engine.** `approval.go` (P0-11, not mine) computes gating with `approvalPolicy(n.Type, n.Kind)` in `RequiresApproval` and `markApproval`. Both should call `NodeApproval(&n)` / `NodeApproval(n)`, so that a node `approval: "required"` marks its ops pending.
  - These are one-line changes for INT.
  - Likewise, `KindSpec.ApprovalPolicy()` should return `k.policy()`, so policies set via `KindSpec.Destructive`/`Approval` on non-default registries are honoured.
  - Validation already enforces "tighten only".
- **`Validate` (validate.go, not mine) still accepts only `SchemaID`.** `ValidateDocument` works around this by validating v2 documents with the id temporarily swapped. Callers must use `Parse`/`ValidateDocument`, not `Validate`, for v2.
  - INT may later make `Validate` version-aware and drop the swap.
  - The vad-server and desktop only call `Parse`, so both are fine.
- Server-capable built-ins are a judgement call: schedule.interval, variable.compare, group, notify.send. No server runner exists yet (later task).
- `KindsSupportedBy` also excludes kinds that can't run on a device. The `version` parameter is a schema version; mapping a desktop `client_version` string to it is server work.

## Notes for dependent tasks / INT
- **Server (automations CRUD and delivery):**
  - Store `a.Version()` and `a.EffectiveRuntime()` in the `automations.schema_version`/`runtime` columns.
  - In `devices.go` delivery, skip documents where `CheckSupport(a, deviceSchemaVersion, deviceModules) != nil`.
  - For job gating, return `409 module_unavailable` with `details.module = ue.Module` when `ue.Module != ""`.
  - Server-runtime documents return `UnsupportedError{Runtime:"server"}` and must never be delivered.
- **Desktop:** desktop rebuilds automatically parse v2 documents. If a v2 server-runtime document ever reaches the desktop, it should skip it (`EffectiveRuntime() != RuntimeDevice`); that check belongs in `desktop/internal/engine/automations.go`.
- **Web/reference endpoint:** the catalogue now carries `runtimes` (on every kind) and `destructive`/`approval` (only when set). The JSON schema `$id` is now automation-v2.json. `schema_id` in `/automations/reference` remains the v1 write id; consider also exposing `SchemaIDV2`.
- **New kinds:** register with `MinSchemaVersion: 2` when they are v2-only, set `Runtimes` explicitly for server kinds, and `Destructive`/`Approval` in the spec, which is recorded in the policy table for DefaultRegistry. Then run `-run TestGoldenCatalog -update` and bump the counts in `TestBuiltinKindsRegistered`/`TestKindsSupportedBy` only for built-ins.
