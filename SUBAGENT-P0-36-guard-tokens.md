# P0-36 (DSK-P) guard package (system-file protection) + tokens interface stubs
Status: completed

## Goal
Implement `desktop/internal/guard` (`guard.Check(op, path, profile)`, MASTER_PLAN §7.6 / §13.5, api-v1 §10.3) with canonicalisation, deny-list, reparse-point refusal and typed errors; add `desktop/internal/tokens` interface stubs (real session/S4U implementation is P1-23).

## Changes (repo: path, created/modified/deleted, why)
All in the client repo `D:/VaultDock/VaultDock`, all **created**:
- `desktop/internal/guard/guard.go`: package doc, `Op`, `Profile`, `Env`, `Guard`, sentinel errors, `BlockedError`, `New`, `Default`, `Check`, `Canonicalize`, deny rules.
- `desktop/internal/guard/path.go`: portable lexical Windows-path canonicaliser (drive/UNC/`\\?\`/`\\?\UNC\`, cleaning, trailing dots/spaces, ADS/device/reserved-name rejection).
- `desktop/internal/guard/guard_windows.go`: `SystemEnv` (known-folder API), 8.3 expansion (`GetLongPathName`), final-path resolution (`GetFinalPathNameByHandle`), reparse-point walk (`GetFileAttributes` + `FindFirstFile` reparse tag).
- `desktop/internal/guard/guard_other.go`: non-Windows fallbacks (lexical checks only; `SystemEnv` from env vars).
- `desktop/internal/guard/guard_test.go`: portable table tests (about 85 cases) for Check, lexical canonicalisation and Op strings.
- `desktop/internal/guard/guard_windows_test.go`: junction tests via `cmd /c mklink /J` (they skip if that fails), a junction into `%WINDIR%`, a symlink test (skips without the symlink privilege), an 8.3 short-name test (skips if 8.3 names are disabled) and a `SystemEnv` test.
- `desktop/internal/tokens/tokens.go`: the `Token` and `Provider` interfaces, the `Unsupported` stub, `ForProfile`, `ValidateSID`, `ErrUnsupported`, `ErrInvalidSID`.
- `desktop/internal/tokens/tokens_test.go`: stub table tests.

No new dependencies (uses the existing `golang.org/x/sys/windows`).

## Interfaces exposed (exact Go signatures)
```go
package guard

type Op uint8
const (
	OpRead Op = iota + 1 // backup reads
	OpWrite              // create/modify/rename, two-way sync downloads
	OpDelete             // delete (to Recycle Bin)
	OpRestoreWrite       // OpWrite + requires Profile.SID
)
func (o Op) String() string // "read" | "write" | "delete" | "restore-write"

type Profile struct {
	SID             string   // required for OpRestoreWrite
	Root            string   // e.g. C:\Users\alice
	AuthorisedRoots []string // other consented profile dirs (Advanced)
}

type Env struct {
	WinDir, ProgramFiles, ProgramFilesX86, ProgramFilesX64, ProgramData, ProfilesDir, SystemDrive string
}
func SystemEnv() Env

type Guard struct{ /* unexported */ }
func New(env Env) (*Guard, error)
func Default() (*Guard, error)                                   // New(SystemEnv()), built once
func (g *Guard) Check(op Op, path string, profile Profile) error
func Check(op Op, path string, profile Profile) error            // uses Default(); fails closed
func Canonicalize(path string) (string, error)

var ErrProtected, ErrReparse, ErrOutsideProfile, ErrInvalidPath error
type BlockedError struct { Op Op; Path string; Reason string; Err error } // Err is one sentinel
func (e *BlockedError) Error() string
func (e *BlockedError) Unwrap() error
```
```go
package tokens

var ErrUnsupported, ErrInvalidSID error
type Token interface {
	SID() string
	Impersonate(fn func() error) error
	Close() error
}
type Provider interface { ForProfile(sid string) (Token, error) }
type Unsupported struct{}                                  // validates the SID, then ErrUnsupported
func (Unsupported) ForProfile(sid string) (Token, error)
var Default Provider = Unsupported{}
func ForProfile(sid string) (Token, error)                 // Default.ForProfile
func ValidateSID(sid string) error                         // syntactic "S-1-…" check
```

### Guard semantics (summary)
- **Canonicalisation:**
  - Paths must be absolute: `C:\…`, `C:` or UNC `\\server\share\…`. A `\\?\` or `\\?\UNC\` prefix is accepted; `/` becomes `\`.
  - `.`, `..` and duplicate separators are cleaned; `..` above the root is rejected.
  - Trailing dots and spaces are stripped per component, but rejected inside `\\?\` verbatim paths.
  - Comparison is case-insensitive.
  - The following are refused with `ErrInvalidPath`: ADS (`:` in a component), wildcard and control characters, reserved device names (CON, NUL, COMn, LPTn …), `\\.\`, `\??\`, `\\?\GLOBALROOT` and `\\?\Volume{…}`.
  - On Windows, 8.3 names in the existing prefix are expanded.
  - The lexical form **and** the long-name form are both checked, followed by the final path after links and subst drives.
- **All ops:**
  - volume-root entries `System Volume Information`, `$Recycle.Bin`, `RECYCLER`, `pagefile.sys`, `hiberfil.sys` and `swapfile.sys` on any volume;
  - `%ProgramData%\Microsoft` and `%ProgramData%\VaultDock`;
  - administrative UNC shares (`\\host\C$`, `ADMIN$`, …);
  - `Default`, `Default User` and `All Users` under ProfilesDir.

  These return `ErrProtected`. Other users' profiles under ProfilesDir that are not in `Root`/`AuthorisedRoots` return `ErrOutsideProfile`. `Public` is allowed.
- **Write / delete / restore-write additionally:**
  - every volume root and share root, and the ProfilesDir itself;
  - `%WINDIR%`, `%ProgramFiles%`, `(x86)`, `X64` and **all of `%ProgramData%`**;
  - root boot/recovery entries (`boot`, `bootmgr`, `bootnxt`, `bootsect.bak`, `EFI`, `Recovery`, `$WinREAgent`, `$Windows.~BT/~WS`, `Config.Msi`, `DumpStack.log(.tmp)`).

  These return `ErrProtected`. Any existing component that is a **name-surrogate** reparse point (symlink, junction, mount point, WSL symlink) returns `ErrReparse`. Cloud-file (OneDrive) and dedup reparse tags are allowed, and a failure to read a tag fails closed.
- Reads deliberately allow `%WINDIR%`, Program Files and the rest of ProgramData (app-settings backups); ACLs are enforced by the user token.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `gofmt -l internal/guard internal/tokens`: clean.
- `go vet ./internal/guard/ ./internal/tokens/`: OK.
- `go test -count=1 ./internal/guard/ ./internal/tokens/`: ok / ok.
  - The junction, junction-to-Windows and 8.3 short-name tests ran and passed on this host.
  - `TestReparseSymlink` skipped: the symlink privilege is not held.
- `GOOS=linux go vet ./internal/guard/ ./internal/tokens/`: OK.
- `go build ./...` (whole desktop module) fails in `internal/agent/api.go` (`undefined: EventKind`, `Event`). That package belongs to another in-progress task; it is unrelated to this one.

## Contract issues
None. api-v1 §10.3 lists only `%ProgramData%\Microsoft`. This task's brief asked to deny all of ProgramData; I did that for mutating ops only. This is stricter than the contract and does not conflict with it.

## Gaps / follow-ups
- `\\?\Volume{GUID}\…` paths are rejected rather than resolved to a mount path (a follow-up could use `GetVolumePathNamesForVolumeName`).
- UNC: non-administrative shares that export a protected local directory are not detected, and `GetFinalPathNameByHandle` does not map a loopback share back to `C:`. Only `$` shares are refused.
- Profiles are detected only under `ProfilesDir`. Profiles relocated elsewhere are not treated as "other users" (ProfileList registry lookup could be added).
- Not implemented here; these belong to callers or P1 tasks: §7.6 item 4 (System attribute), item 5 (Recycle Bin via `IFileOperation`) and item 6 (registry rules).
- Check-then-use is inherently TOCTOU. Callers should open with `FILE_FLAG_OPEN_REPARSE_POINT` and re-verify where it matters.
- Non-Windows builds do lexical checks only; there are no FS or reparse checks.

## Notes for dependent tasks / INT
- Callers should map any `*BlockedError` to a `guard_blocked` issue. Use `errors.Is` with the sentinels; `Reason` is safe to log and contains no secrets.
- The agent's own `%ProgramData%\VaultDock` store code must not go through the guard (it is denied for all ops).
- Pass only consented/authorised profiles (§7.8). `OpRestoreWrite` requires `Profile.SID`.
- P1-23 should implement `tokens.Provider` on Windows and assign `tokens.Default` (or replace `Default` in `tokens_windows.go`). Keep `Unsupported` for non-Windows.
