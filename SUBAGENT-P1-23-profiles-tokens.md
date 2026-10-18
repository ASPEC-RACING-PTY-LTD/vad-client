# P1-23 (DSK-P) Profiles core: active user, consent record, session/S4U tokens, profile report
Status: completed

## Goal
Implement `desktop/internal/tokens` on Windows: `ForProfile(sid)` uses a session token or S4U, the token is filtered, and errors are typed. Add `desktop/internal/profiles`, which covers:
- active console user detection and ProfileList enumeration;
- the DPAPI consent proof (created in the UI, verified by the service under a session token only);
- the consent record store in the `profiles` table;
- the default policy (O6);
- the `PUT /device/profiles` report;
- the Phase 3 `SecuritySignal` hook.

## Changes (repo: path, created/modified/deleted, why)
All changes are in the client repo `VaultDock`, in `desktop/internal/`.

**`tokens/`**
- `tokens.go` (modified):
  - Adds `Source`, `Caps` and `CapsFor`.
  - The `Token` interface gains `Source`, `SessionID`, `Caps` and `AccessCheckPath`. P0-36's `Impersonate`, `SID` and `Close` are kept.
  - `Provider` gains `SessionOnly`; `ForProfile(sid)` keeps the P0-36 signature.
  - Adds the package-level `SessionOnly` and `IsUserSID`.
  - `ValidateSID` now allows a 14-digit identifier authority and caps sub-authorities, matching the server's `sidPattern`.
- `errors.go` (created):
  - the sentinels;
  - `*Error{Op,SID,Err,Cause}`, which unwraps to both the sentinel and the OS cause;
  - portable NTSTATUS and Win32 → sentinel mapping;
  - `PackageFor(domain, host)`.
- `lsa_windows.go` (created): adapted from the spike.
  - `LsaRegisterLogonProcess`, `LsaLookupAuthenticationPackage` and S4U `LsaLogonUser` (Network logon type).
  - `enablePrivilege` calls `AdjustTokenPrivileges` directly, so `ERROR_NOT_ALL_ASSIGNED` is captured on the same thread. This fixes a latent `GetLastError` race in the spike.
- `tokens_windows.go` (created):
  - `WindowsProvider`; `init` sets `Default`.
  - A fast "not LocalSystem → `ErrInsufficientPrivilege`" check, then SeTcb is enabled.
  - Session path: `WTSEnumerateSessions` + `WTSQueryUserToken`, preferring Active over Disconnected. S4U fallback: MSV1_0 for local and MSA accounts, Negotiate/Kerberos for domain accounts, and `ErrNeedsLogon` for AzureAD.
  - `normalise`: checks the SID, then `CreateRestrictedToken(DISABLE_MAX_PRIVILEGE, deny-only Administrators)`, then an impersonation duplicate.
  - `winToken`:
    - `Impersonate` on a locked OS thread; the thread stays locked if `RevertToSelf` fails;
    - `AccessCheck` and `AccessCheckPath`;
    - `PrimaryDup`;
    - `Close` is idempotent and mutex-guarded.
- `tokens_test.go` (rewritten): portable tests for SID validation, `IsUserSID`, the stub provider, caps, NTSTATUS and Win32 mapping, `PackageFor` and error formatting.
- `tokens_windows_test.go` (created):
  - `Default` is the Windows provider;
  - a real non-SYSTEM process gets `ErrInsufficientPrivilege` from `ForProfile` and `SessionOnly`;
  - the same with an injected `isSystem`;
  - closed-token behaviour.

**`profiles/`** (new)
- `profiles.go`: package doc and policy; `Record`; `Store` over the state.db `profiles` table (Get, List, Put, Update in an immediate tx, MarkReported); `Consented`; `Record.Allowed`; errors; local-only `StateDiscovered`.
- `consent.go`: `ConsentClaim`, `CreateConsentProof`, `VerifyConsentProof`, `CheckFresh`, `ProofDigest`, `NewConsentID`. Constant-time claim compares; the blob is capped at 16 KiB.
- `consent_windows.go` / `consent_other.go`: user-scope `CryptProtectData`/`CryptUnprotectData` with device-bound entropy (`"VaultDock.consent.v1\0"+device_id`). `NTE_BAD_KEY_STATE` maps to `ErrDPAPIKeyUnavailable`; anything else maps to `ErrConsentInvalid`.
- `system.go` and `system_windows.go` / `system_other.go`: `System` interface and `OSSystem`.
  - `WTSGetActiveConsoleSessionId` → `WTSQuerySessionInformationW` (user and domain names) → `LookupAccountName`, so it works unprivileged.
  - Read-only ProfileList walk: `S-1-5-21-*` and `S-1-12-1-*` only, `ProfileImagePath` expanded, and `.bak` / `State` temp flags reported as `Temporary`. No hive is ever loaded.
- `signals.go`: `SecuritySignal` (+`Validate` against the §6B enums), the `SignalSource` Phase 3 hook, `EventSender`, `ConsentSignal(err)` (the candidate mapping; `ErrNeedSession` never yields a signal).
- `report.go`: `ReportSender`, `BuildReport`.
- `manager.go`: `Manager` with these methods: `Refresh`, `Allowed`, `RecordConsent`, `VerifyStored`, `ApplyConsentRequest`, `Revoke`, `MarkCleared`, `HandleSignal`, `CheckSecurity` and `Report`.
- `profiles_test.go`: portable tests against a real migrated `state.db`, using a fake System, Provider and Token.
- `consent_windows_test.go`:
  - DPAPI round trip as the current user;
  - wrong device, tampered blob and other-SID cases;
  - `RecordConsent` → active_default or authorised;
  - renewal;
  - re-consent after clear;
  - a real `OSSystem` check.

## Interfaces exposed
```go
package tokens
type Source int; const (SourceSession Source = iota + 1; SourceS4U); func (Source) String() string
type Caps struct{ Files, DPAPI, EFS, Network bool }; func CapsFor(Source) Caps
type Token interface {
	SID() string; Source() Source; SessionID() (uint32, bool); Caps() Caps
	Impersonate(fn func() error) error
	AccessCheckPath(path string, mask uint32) (bool, error)
	Close() error
}
type Provider interface { ForProfile(sid string) (Token, error); SessionOnly(sid string) (Token, error) }
type Unsupported struct{}                    // non-Windows Default
var Default Provider                         // Windows: *WindowsProvider (init)
func ForProfile(sid string) (Token, error); func SessionOnly(sid string) (Token, error)
func ValidateSID(sid string) error; func IsUserSID(sid string) bool
func PackageFor(domain, host string) (string, error); const PackageMSV, PackageNegotiate
var ErrUnsupported, ErrInvalidSID, ErrInsufficientPrivilege, ErrNotLoggedOn, ErrNeedsLogon,
    ErrS4UUnavailable, ErrAccountNotFound, ErrNotUser, ErrSIDMismatch error
type Error struct{ Op, SID string; Err, Cause error }   // Unwrap() []error
// windows only:
type WindowsProvider struct{...}; func NewProvider() *WindowsProvider
type PrimaryToken interface{ PrimaryDup() (windows.Token, error) }
type SecurityChecker interface{ AccessCheck(sd *windows.SECURITY_DESCRIPTOR, mask uint32) (bool, error) }

package profiles
const StateDiscovered = "discovered"; const ScopeProfile = "profile"; const MaxProofSize = 16 << 10
var ErrNotFound, ErrBlacklisted, ErrInvalidSID, ErrBadTransition, ErrUnsupported, ErrNoActiveUser,
    ErrNeedSession, ErrDPAPIKeyUnavailable, ErrConsentInvalid, ErrConsentStale error
type Record struct{ SID, Username, ProfilePath, State, ConsentID string; ConsentAt int64; ConsentBlob []byte; ProofDigest,
	RequestID, RequestScope, RequestedBy string; BlacklistedAt int64; BlacklistReason string; LastSecurityCheck int64;
	LastSecuritySignal, LastSecurityEvidence string; ReportedAt, UpdatedAt int64 }
func (r *Record) Allowed() bool; func Consented(state string) bool
type Store struct{ DB *sql.DB }  // Get, List, Put, Update(ctx, sid, fn(r *Record, found bool) error), MarkReported
type ConsentClaim struct{ V int; SID, DeviceID, ConsentID string; Time int64 /*ms*/; Scope string }
func CreateConsentProof(deviceID, sid string) ([]byte, error)                                 // UI process
func VerifyConsentProof(tok tokens.Token, blob []byte, deviceID, sid string) (ConsentClaim, error) // service, session token only
func CheckFresh(c ConsentClaim, now time.Time) error; func ProofDigest(blob []byte) string; func NewConsentID() (string, error)
type LocalProfile struct{ SID, Username, ProfilePath string; Temporary bool }
type ActiveUser struct{ SessionID uint32; SID, Username string }
type System interface{ ActiveConsoleUser() (ActiveUser, error); LocalProfiles() ([]LocalProfile, error) }
type OSSystem struct{}
type SecuritySignal struct{ SID, Signal, Evidence string; ObservedAt time.Time; Detail string }
type SignalSource interface{ Name() string; Check(ctx context.Context, rec Record) ([]SecuritySignal, error) }
type EventSender func(ctx context.Context, sid string, ev wire.ProfileSecurityEventRequest) error
type ReportSender func(ctx context.Context, rep wire.ProfileReport) error
func ConsentSignal(err error) (signal, evidence string, ok bool)
func BuildReport(recs []Record) wire.ProfileReport
type Manager struct{ Store *Store; Sys System; Tokens tokens.Provider; DeviceID string;
	SendReport ReportSender; SendEvent EventSender; Signals []SignalSource; Log *slog.Logger; Now func() time.Time }
func (m *Manager) Refresh(ctx) error
func (m *Manager) Allowed(ctx, sid) bool
func (m *Manager) RecordConsent(ctx, callerSID string, blob []byte) (*Record, error)
func (m *Manager) VerifyStored(ctx, sid) error
func (m *Manager) ApplyConsentRequest(ctx, wire.TaskProfileConsentRequestPayload) error
func (m *Manager) Revoke(ctx, sid) error
func (m *Manager) MarkCleared(ctx, sid) error
func (m *Manager) HandleSignal(ctx, SecuritySignal) error
func (m *Manager) CheckSecurity(ctx) error
func (m *Manager) Report(ctx) (wire.ProfileReport, error)
```

**State machine (local).**

| Event | Transition / effect |
|---|---|
| New ProfileList entry | → `discovered` |
| Admin request | `discovered`/`revoked`/`requested` → `requested`; other states keep their state |
| Verified fresh consent | Any state except `blacklisted` → `active_default` if the caller is the active console user and no other profile holds active_default (renewal keeps active_default); otherwise → `authorised` |
| Signal | Any state → `blacklisted`; the local block is written first, then the event is posted |
| Server clear | `blacklisted` → `cleared_pending_consent` (proof dropped) |
| User revoke | consented/`requested` → `revoked` (proof dropped) |

`Allowed` requires a consenting state with a consent id, a digest and a blob.

## Commands run and results
From `D:/VaultDock/VaultDock/desktop`:
- `go build ./...`: ok.
- `go vet ./internal/profiles/ ./internal/tokens/`: ok.
- `go test -count=1 ./internal/profiles/ ./internal/tokens/`: ok / ok. All 12 profiles tests passed, including the real DPAPI round trip and `OSSystem` on this host. The not-SYSTEM → `ErrInsufficientPrivilege` test also ran.
- `GOOS=linux go vet ./internal/profiles/ ./internal/tokens/`: ok; `GOOS=linux go test -c ./internal/profiles/`: compiles.
- `gofmt -l`: clean.
- Nothing ran elevated, and no new dependencies were added (`golang.org/x/sys/windows` and `/registry` are already required).

## Contract issues
1. **No state for "discovered, never consented or requested".** api-v1 §6B's six states cannot express such a profile, and the server rejects unknown values. The agent therefore keeps it local-only (`discovered`) and **omits it from `PUT /device/profiles`**. Consequence: an admin cannot see those SIDs to make a `selected` request before one exists, though `all` works. Suggest an amendment that adds a reportable `discovered` (or `unconsented`) state.
2. The §6B evidence enum gaps found in P0-S4 still apply: there is no value for `account_disabled`, `profile_moved` or `eventlog_4726`. Phase 3 must use `sid_lookup` in the interim. `Validate` enforces the current enum.
3. The table has no consent-scope column (§8.2 / P0-22). The scope (`profile`) is sealed in the DPAPI claim and checked on verify. `request_scope` holds only the admin request scope.

## Gaps / follow-ups
- **VM test items.** Run as LocalSystem and record the results; numbering follows spike P0-S3 §5:
  - T1–T3: session, disconnected and S4U token sources, plus event 4624 type 3.
  - T4: EFS read fails under S4U.
  - T5: `VerifyConsentProof` under S4U returns `ErrNeedSession` without calling DPAPI. Also call `unprotect` directly under S4U and expect `NTE_BAD_KEY_STATE`.
  - T6: `VerifyStored` OK under a session token, with a stable digest.
  - T7/T8: admin reset → `ErrDPAPIKeyUnavailable`; a user change still passes.
  - T9/T10: MSA-linked account under S4U and after an online password change.
  - T11: an AzureAD account returns `ErrNeedsLogon` before any LSA call.
  - T12: a loc2 admin account under S4U is denied an Administrators-only file (deny-only filter).
  - T13: an INTERACTIVE-only ACE under a Network S4U logon.
  - T14: OneDrive placeholder.
  - T15: the service with only the C6 RequiredPrivileges allowlist.
  - T16: a non-SYSTEM admin → `ErrInsufficientPrivilege` (already covered unprivileged here).
  - T17: `AccessCheckPath` vs a real open.
  - T18: after `Impersonate`, `OpenThreadToken` returns `ERROR_NO_TOKEN`.
  - P1-23 specific: `OSSystem.ActiveConsoleUser` as SYSTEM with nobody signed in returns `ErrNoActiveUser`, and with an RDP-only session the console has no user. `LocalProfiles` sees a `.bak`/temp profile as `Temporary`. `ErrS4UUnavailable` for a domain account with the DC offline.
- `ActiveConsoleUser` ignores RDP sessions by design (console only).
- Phase 3:
  - the signal collectors (R1–R5) as `SignalSource` implementations;
  - the debounce for the DPAPI `password_reset` candidate (confirm on a second logon session);
  - scheduling `CheckSecurity` and `VerifyStored` at service start, every 6 h, at logon and before each job.
- How the agent learns of a server-side clear is not decided here. `MarkCleared` exists, but no task or summary field feeds it yet.
- `CheckFresh` rejects consent claims older than 24 h or more than 10 min in the future, at record time only.

## Notes for dependent tasks / INT
- **Agent/IPC wiring (P1-29 / agent):** add an IPC method such as `RecordProfileConsent(blob)`. Pass the caller SID from `ipc.CallerFromContext(ctx)` (never from the body) to `Manager.RecordConsent`. The UI calls `profiles.CreateConsentProof(deviceID, ownSID)`; `deviceID` must be the same string the agent's `Manager.DeviceID` uses (suggest the decimal device id). After a successful record, call `Manager.Report`.
- **Device task handler:** call `ApplyConsentRequest` for a `profile_consent_request` task, then send the report. `SendReport` and `SendEvent` should go through the outbox, so they are retried.
- **Walker (P1-24):**
  - Check `Manager.Allowed(sid)` before a job.
  - Use `tokens.ForProfile` (one token per job per profile) and do the I/O inside `tok.Impersonate`.
  - `Caps().EFS == false` → `efs_needs_logon`.
  - `ErrNeedsLogon`, `ErrNotLoggedOn` or `ErrS4UUnavailable` → `profile_needs_logon` skip.
  - `ErrInsufficientPrivilege` → the agent is not running as the service (dev mode).
  - For VSS reads, assert `tokens.SecurityChecker`.
- The agent's dev mode (running as a user) always gets `ErrInsufficientPrivilege` from the tokens provider. For dev, consent verification can use a fake provider, as the tests do.
