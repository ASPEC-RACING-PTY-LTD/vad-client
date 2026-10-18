# P0-30 SDK-MOD: sdk/modules (module ids, component manifest types, verification interface)
Status: completed

## Goal
Create `sdk/modules` implementing MASTER_PLAN §7.7 (install modes / module system), §7.14 (release integrity) and api-v1 §6A module ids: ids, kinds, modes, the component manifest, `ParseManifest`, and a `Verifier` with an Ed25519 + SHA-256 implementation. Stdlib only.

## Changes
- VaultDock: `sdk/modules/modules.go` (created): ids, `Kind`, `Mode`, `State`, mode sets, kind helpers.
- VaultDock: `sdk/modules/manifest.go` (created): `Manifest`/`Component`/`Authenticode` types, `ParseManifest`, `Validate`, `SigningPayload`, `Find`, compatibility and dependency checks.
- VaultDock: `sdk/modules/verify.go` (created): `Verifier` interface, `Ed25519Verifier`, `VerifyFile`, `SignManifest`.
- VaultDock: `sdk/modules/modules_test.go` (created): table tests.
- No dependencies added. The only in-module import is `sdk/version` (for `Parse`/`Compare`).

## Interfaces exposed
```go
type ID string
const Core, Sync, Automation, Profiles, VSS, Discovery, Reinstall ID = "core","sync","automation","profiles","vss","discovery","reinstall"
var All []ID // contract table order

type Kind string; const KindCore="core", KindBuiltin="builtin", KindComponent="component"
type Mode string; const ModeSimple="simple", ModeAdvanced="advanced"; func (Mode) Valid() bool
type State string; const StateEnabled, StateDisabled, StateInstalling, StateFailed; func (State) Valid() bool

func Known(id ID) bool
func KindOf(id ID) (Kind, bool)
func IsBuiltin(id ID) bool
func IsComponent(id ID) bool
func ModulesForMode(mode Mode) []ID   // simple: [core]; advanced: all 7; unknown: nil (fresh slice)
func ComponentsForMode(mode Mode) []ID // advanced: [vss discovery reinstall]
func InMode(id ID, mode Mode) bool

const ManifestVersion = 1
var ErrInvalidManifest, ErrIncompatible, ErrMissingDeps error
type Manifest struct { ManifestVersion int; Release string; Components []Component; Signature string /*optional base64 Ed25519*/ }
type Component struct { ID ID; Version, MinCore string; Modes []Mode; OS, Arch, URL, SHA256 string; Size int64; Depends []ID; Entry string; Authenticode *Authenticode }
type Authenticode struct { Subject, Thumbprint string }
func ParseManifest(raw []byte) (*Manifest, error)
func (m *Manifest) Validate() error
func (m *Manifest) SigningPayload() ([]byte, error) // JSON with Signature cleared
func (m *Manifest) Find(id ID, goos, goarch string) (Component, bool)
func CheckCompatible(c Component, coreVersion, goos, goarch string) error
func MissingDependencies(c Component, available []ID) []ID
func CheckDependencies(c Component, available []ID) error

var ErrBadSignature, ErrNoSignature, ErrChecksum, ErrSizeMismatch, ErrNoTrustedKeys, ErrInvalidKeySize error
type Verifier interface {
    VerifyManifest(raw, sig []byte) error
    VerifyComponent(c Component, path string) error
}
func NewEd25519Verifier(keys ...ed25519.PublicKey) (*Ed25519Verifier, error) // any key may match (rotation)
func (v *Ed25519Verifier) VerifyManifest(raw, sig []byte) error  // sig: raw 64 bytes or base64 text
func (v *Ed25519Verifier) VerifyEmbedded(raw []byte) (*Manifest, error)
func (v *Ed25519Verifier) VerifyComponent(c Component, path string) error
func VerifyFile(c Component, path string) error // size + SHA-256, constant-time compare
func SignManifest(priv ed25519.PrivateKey, payload []byte) string // base64; release tooling/tests
```

### Manifest JSON shape (`components-<v>.json`)
```json
{
  "manifest_version": 1,
  "release": "1.4.0",
  "components": [{
    "id": "vss", "version": "1.4.0", "min_core": "1.3.0",
    "modes": ["advanced"], "os": "windows", "arch": "amd64",
    "url": "https://github.com/<org>/<repo>/releases/download/v1.4.0/vd-vss-1.4.0.zip",
    "sha256": "<64 lowercase hex>", "size": 123456,
    "depends": ["profiles"],
    "entry": "vd-vss.exe",
    "authenticode": {"subject": "ASPEC Racing Pty Ltd", "thumbprint": "<optional sha1 hex>"}
  }],
  "signature": "<optional base64 Ed25519 over SigningPayload>"
}
```
Validation rules: `manifest_version` must be 1; release, version and min_core must parse as versions; the id must be a downloadable component (`vss`/`discovery`/`reinstall`); (id, os, arch) is unique; modes are non-empty and known; the url is absolute `https`; sha256 is 32 bytes of hex; size > 0; entry is required; depends are known ids other than the component itself; authenticode.subject is required when the block is present.

Signing has two supported forms (D29):
1. a detached `components-<v>.json.sig` (base64 or raw) over the raw file bytes, checked with `VerifyManifest`;
2. an embedded `signature` over `SigningPayload()`, which is the manifest re-encoded by `encoding/json` with `signature` cleared, checked with `VerifyEmbedded`.

## Commands run and results
- `cd sdk && gofmt -l modules` printed nothing (clean).
- `go vet ./modules/` passed.
- `go test -count=1 ./modules/` returned `ok`.
- `go build ./...` passed.

## Contract issues
None. The ids, modes and states match api-v1 §6A exactly.

## Gaps / follow-ups
- This package doesn't check Authenticode. That's platform specific and belongs to `desktop/internal/modules` (WinVerifyTrust). The manifest only carries the expectation.
- `CheckCompatible` treats an unparseable core version (such as `dev`) as incompatible. Note that `version.Parse("v1.3.0-3-gabc")` yields pre-release `3-gabc`, so a git-describe build after a tag sorts *below* the tag, and a component with `min_core` equal to that tag is rejected. The agent may want to override this for development builds.
- D29 (Q4) and the embedded public key are owned by REL-C / DSK-MOD. The tests cover key rotation, since multiple trusted keys are supported.

## Notes for dependent tasks / INT
- REL-C: generate `components-<v>.json` in the shape above. If you sign, prefer the detached `.sig` over the exact file bytes; that is the simplest and does not depend on re-encoding. With an embedded signature, sign `SigningPayload()` from this package, because a Go encoding of the struct is required.
- DSK-MOD: use `ParseManifest` (or `VerifyEmbedded`), then `Find(id, runtime.GOOS, runtime.GOARCH)`, `CheckCompatible`, `CheckDependencies`, and finally `VerifyComponent` on the downloaded archive before extracting it.
- SRV-M: for `/modules/catalog` and validating `PUT /device/modules`, use `All`, `KindOf`, `Mode.Valid` and `State.Valid`. `KindBuiltin`/`KindComponent` map directly to the catalog's `delivery` (core → `builtin` or treat separately).
