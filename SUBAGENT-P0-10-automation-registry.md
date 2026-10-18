# P0-10 (SDK-AUTO) Automation registry + KindSpec (+ required_modules) + golden v1
Status: completed

## Goal
Introduce a kind registry in `sdk/automation`, extend `KindSpec` with `RequiredModules` and a minimum schema version, derive the catalogue from the registry without changing behaviour, and pin the v1 catalogue with a golden test (a guard for P0-11 and P0-12).

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `sdk/automation/registry.go` (created)
  - `KindSpec` moved here from catalog.go, with two new fields: `MinSchemaVersion` and `RequiredModules`.
  - Module id constants (api-v1 §6A).
  - `Registry` with Register, MustRegister, Lookup, Kinds and Catalog.
  - `DefaultRegistry`, built from `BuiltinKinds()`.
- VaultDock: `sdk/automation/catalog.go` (modified)
  - The KindSpec definition was removed (it moved to registry.go).
  - Every built-in kind literal now sets `RequiredModules`.
  - The hand-built `catalog` map was replaced by `BuiltinKinds()`.
  - `LookupKind`/`Catalog()` now delegate to `DefaultRegistry`.
  - `var catalog = DefaultRegistry.kinds` stays, because `validate.go:suggestKind` indexes it directly. It is the same live map, so kinds registered later are visible through it.
- VaultDock: `sdk/automation/registry_test.go` (created): golden test (with an `-update` flag), a check that the built-ins are registered with the expected metadata, and a table test of Register's defaults and errors.
- VaultDock: `sdk/automation/testdata/golden_v1.json` (created): indented JSON of `Catalog()` (27 kinds: 10 triggers, 9 conditions, 8 actions).

## Interfaces exposed
```go
const SchemaVersionV1 = 1
const ModuleCore, ModuleSync, ModuleAutomation, ModuleProfiles, ModuleVSS, ModuleDiscovery, ModuleReinstall = "core","sync","automation","profiles","vss","discovery","reinstall"
type KindSpec struct { Type, Kind, Title, Description, Category string; Params []ParamSpec; Context string; Contexts, Ports []string;
    MinSchemaVersion int `json:"min_schema_version,omitempty"`; RequiredModules []string `json:"required_modules,omitempty"` }
type Registry struct{ /* unexported */ }
func NewRegistry() *Registry
func (r *Registry) Register(spec KindSpec) error   // validates type/kind/module ids, rejects duplicates; defaults Ports by type and MinSchemaVersion=1
func (r *Registry) MustRegister(spec KindSpec)
func (r *Registry) Lookup(nodeType, kind string) *KindSpec
func (r *Registry) Kinds(nodeType string) []string // sorted
func (r *Registry) Catalog() map[string][]KindSpec  // sorted by kind
var DefaultRegistry *Registry
func BuiltinKinds() []KindSpec
// unchanged: LookupKind(nodeType, kind) *KindSpec, Catalog() map[string][]KindSpec
```
Field mapping to the task wording:
- category (trigger/condition/action) is the existing `Type` field;
- human label is `Title`;
- params schema is `Params`;
- the existing `Category` field remains the editor palette group.

The `required_modules` assignment for the v1 kinds:

| Kinds | required_modules |
|---|---|
| All file and sync triggers; actions sync.file, sync.skip, dest.set_folder, dest.rename, tag.apply, sync.start_task | `["automation","sync"]` |
| schedule.interval, all conditions, dest.create_folder, notify.send | `["automation"]` |

## Commands run and results
- Before the change, I dumped `Catalog()` to scratch. After the change, I checked that the JSON with the two new keys stripped is identical to that dump: True, so the change is behaviour-preserving apart from the additive fields.
- `cd sdk && go vet ./automation/ && go test -count=1 ./automation/`: OK.
- `gofmt -l`: clean.
- `cd vad-server && go build ./...`: OK. `internal/api/automations.go` uses `automation.Catalog()`, and that now also emits `required_modules` and `min_schema_version`.
- `cd desktop && go build ./...`: FAILS in `internal/engine/transfer.go:255,265` (`e.xfer undefined`).
  - That file was modified at 13:26 by a concurrently running task and does not involve the automation package.
  - Earlier in the run, `internal/state/migrate.go` (`undefined: schemaV1`) also failed transiently; it now builds.
  - `go build ./internal/config ./internal/filter` (the other desktop importers of sdk/automation) succeeds.

## Contract issues
None. Module ids match api-v1 §6A.

## Gaps / follow-ups
- The handlers registry (Match/Eval/Apply funcs, see research 02 §4.1) is not part of this task; it belongs to P0-11.
- `dest.create_folder` → `["automation"]` only is a judgement call: it is usable from schedule context and does storage I/O, not sync. INT may revise it; if so, update the golden file with `-update`.

## Notes for dependent tasks / INT
- P0-11: switch `validate.go:suggestKind` from `catalog[nodeType]` to `DefaultRegistry.Kinds(nodeType)`, then delete `var catalog` in catalog.go.
- P0-11: wire the engine.go switches to registry handlers.
- New kinds: register them via `DefaultRegistry.MustRegister` in `init()` of per-domain files. Register defaults the Ports and the MinSchemaVersion.
- After an intentional catalogue change, regenerate the golden file with `go test ./automation/ -run TestGoldenCatalogV1 -update`. `TestBuiltinKindsRegistered` hard-codes 27 built-ins; update that count when adding built-in kinds.
- Server delivery filtering (§7.11) can union `LookupKind(n.Type, n.Kind).RequiredModules` over the nodes of a document.
- INT: re-run the desktop build once the task that is editing `desktop/internal/engine/transfer.go` finishes.
