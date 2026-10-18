# Spike P0-S3: S4U tokens, file access, DPAPI behaviour

Status: **design and prototype done; VM validation pending** (anything that needs LocalSystem was not run, per AGENT_BRIEF rule 6).
Scope: MASTER_PLAN §7.6 (least privilege, C6), §7.8 (profiles, consent), R-P2, C6–C10, api-v1 §6B.

Prototype: Go module `vdspike/s4u`, stdlib plus `golang.org/x/sys/windows` v0.49.0 (BSD-3, already used by `desktop`).

| File | Content |
|---|---|
| `lsa_windows.go` | `LsaRegisterLogonProcess` → `LsaLookupAuthenticationPackage` → `LsaLogonUser(S4U)`; privilege enable |
| `token_windows.go` | `ForProfile`, session lookup, package choice, token normalisation, `Do` (impersonation), `AccessCheck` |
| `dpapi_windows.go` | Consent proof: `ProtectConsent` (UI), `VerifyConsent` (service), error classification |
| `cmd/s4uspike` | `consent-selftest` (any user) and `profile -sid … -path … -dpapi …` (LocalSystem only, for the VM) |

Verified on the host: `GOOS=windows go vet ./...` and `go build ./...` are clean (GOWORK=off). `go run ./cmd/s4uspike consent-selftest` as a normal user passed: round trip OK, a wrong `device_id` (entropy) was rejected, and a tampered blob was rejected with `ERROR_INVALID_DATA`.

## 1. Design: `ForProfile(sid)`

```
ForProfile(sid):
  0. StringToSid; enable SeTcbPrivilege in the process token (it must be in the SCM RequiredPrivileges allowlist)
  1. Session path. For each session from WTSEnumerateSessions with state Active or Disconnected:
       WTSQueryUserToken(id) → primary token; keep it if TokenUser == sid (prefer Active)
     → Source=session
  2. Otherwise, S4U path:
       LookupAccountSid(sid) → (name, domain, SidTypeUser required)
       domain == ComputerName → MSV1_0 package, MSV1_0_S4U_LOGON{UPN=name, Domain=computer}
       domain == "AzureAD"    → ErrS4UUnsupported (skip the profile while it is logged off)
       otherwise (AD)         → Negotiate package, KERB_S4U_LOGON{ClientUpn=name, ClientRealm=domain}
       LsaRegisterLogonProcess("VaultDockAgent")      (needs SeTcb)
       LsaLogonUser(LogonType=Network, TOKEN_SOURCE "VaultDck"+LUID) → primary token, LogonId
       LsaFreeReturnBuffer(profile); LsaDeregisterLogonProcess
     → Source=s4u
  3. Normalise both kinds:
       assert TokenUser == sid  (otherwise ErrSIDMismatch)
       CreateRestrictedToken(DISABLE_MAX_PRIVILEGE, SidsToDisable=[BUILTIN\Administrators])
         → behaves like a UAC-filtered standard token whether the source was full, filtered or UAC-off
       DuplicateTokenEx(SecurityImpersonation, TokenImpersonation) → impersonation token
```

**Using the token.**
- **File I/O (walker, restore writes):** `tok.Do(fn)`:
  - `runtime.LockOSThread`, then `SetThreadToken(nil, imp)`;
  - `fn` runs synchronous `CreateFile` / `ReadFile`;
  - `RevertToSelf`, then unlock.
  - If `RevertToSelf` fails, the thread stays locked, so the Go runtime discards it instead of reusing a thread that still has the user's identity.
  - `fn` must not start goroutines and must not do network I/O.
  - Batch work inside `Do`, for example one directory listing or one file's open plus a bounded read, rather than one `Do` call per job.
- **VSS snapshot reads (C7):** `tok.AccessCheck(sd, FILE_READ_DATA)` with the file `GENERIC_MAPPING`.
  - The SD must include OWNER, GROUP and DACL.
  - Only after this check does the SYSTEM/SeBackup reader open the snapshot path.
  - It needs only the impersonation token, with no thread switch.
- **Impersonation vs primary token:**
  - Use **impersonation** for everything in-process: files, `AccessCheck`, and DPAPI verification.
  - Use the **primary** token (`PrimaryDup`) only for `CreateProcessAsUser` of a per-user helper (for example, the Recycle Bin delete via `IFileOperation`, §7.6 guard rule 5).
  - That needs `SeAssignPrimaryTokenPrivilege` and `SeIncreaseQuotaPrivilege`, both in the C6 allowlist.
  - A helper started on an S4U token runs in session 0 with no desktop. That is fine for `IFileOperation` with `FOF_NO_UI`, but the VM test plan still lists it as unverified.
- **Lifetime:**
  - Get one token per job per profile and `Close` it at job end.
  - Each S4U call creates an LSA logon session and a Security event 4624 (logon type 3), so tokens must not be minted per file.
  - Session tokens can go stale at logoff; re-query on the WTS `SESSION_LOGOFF` notification.

**Required service privileges** (all are already in the C6 allowlist; nothing new):

| Privilege | Used for |
|---|---|
| `SeTcbPrivilege` | `WTSQueryUserToken`, `LsaRegisterLogonProcess`, and an impersonation-level S4U token (without it S4U returns an *identification* token only) |
| `SeImpersonatePrivilege` | `SetThreadToken` with the user token |
| `SeAssignPrimaryTokenPrivilege`, `SeIncreaseQuotaPrivilege` | `CreateProcessAsUser` helpers only |
| `SeChangeNotifyPrivilege` | traverse checking |

`SeBackupPrivilege` is **not** used for the user-token paths; it is only for the VSS reader.

**Logon type.** Use `Network` (3), because "Access this computer from the network" is granted to Users by default. `Batch` (4), which Task Scheduler uses, needs `SeBatchLogonRight`, and that is not granted to standard users by default. A Network S4U token carries the NETWORK SID, not INTERACTIVE, so files whose ACL grants only INTERACTIVE are denied. That is rare in profiles, where the explicit ACE is the user SID, and the VM test plan includes it.

**Profile and hive.** No `LoadUserProfile`, ever (§7.8 "never load unloaded hives"). S4U gives the token only. HKCU of a logged-off user is unavailable, so registry capture for that profile is skipped with an issue.

## 2. What S4U tokens cannot do (R-P2) and backup consequences

An S4U logon session has **no credentials**: no password-derived keys and no Kerberos TGT.

| Capability | S4U token | Effect on backup |
|---|---|---|
| Files (NTFS ACL) | Works: the identity and groups are real | Logged-off profiles are backed up normally |
| DPAPI user-scope (`CryptUnprotectData`) | **Fails** (`NTE_BAD_KEY_STATE`): the master key in `%APPDATA%\Microsoft\Protect\<SID>` is wrapped with password-derived material the LSA does not have | VaultDock never decrypts user DPAPI data for backup; files are copied as opaque bytes, so this only affects the consent check (below). Browser/credential stores are still copied as ciphertext and are only usable after restore to the same user with the same master keys |
| EFS-encrypted files (transparent read) | **Fails** (access denied / decryption failed): the EFS private key is DPAPI-protected | Skip with an issue `efs_needs_logon` and retry at next logon (R-P2 as planned). **Option for P2+:** `OpenEncryptedFileRaw` / `ReadEncryptedFileRaw` exports the ciphertext with SeBackup and needs no key; it can only be restored with `WriteEncryptedFileRaw` and is only readable by a holder of the user's EFS cert. Not in the MVP |
| Network (SMB, mapped drives, proxies with NTLM/Kerberos) | Fails; mapped drives are per logon session and do not exist | Network locations in a logged-off profile are skipped. VaultDock's own server traffic uses the device key as SYSTEM and is unaffected |
| Cloud placeholders (OneDrive Files On-Demand) | Reading a dehydrated placeholder fails (provider not running) | **Walker rule for every token:** skip `FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS` / `RECALL_ON_OPEN` files (`cloud_placeholder` issue). With a session token a read would trigger a mass download |
| HKCU / NTUSER.DAT | Hive not loaded | Skip; never load it |

With a **session token** (user logged on, including a disconnected fast-user-switch session), the logon session has the credentials, so DPAPI and EFS work under impersonation. The UAC filtering applied in `normalise` does not affect DPAPI or EFS.

## 3. DPAPI consent proof (§7.8, C8)

**UI process** (the user's own session, no impersonation):
- `ProtectConsent({v:1, sid, device_id, consent_id, time, scope})`;
- uses `CryptProtectData`, user scope (no `LOCAL_MACHINE` flag), `CRYPTPROTECT_UI_FORBIDDEN`;
- **entropy** = `"VaultDock.consent.v1\0" + device_id`;
- sends the blob over the pipe.

**Service**, on receipt:
1. Impersonate the pipe client and check that the client SID == `claim.sid`; reject otherwise.
2. Store the blob under `%ProgramData%\VaultDock` (SYSTEM-only ACL).
3. Run `ForProfile(sid)` (a session token, because the user is logged on right now) and `VerifyConsent` (`CryptUnprotectData` inside `tok.Do`, compare claim fields in constant time).
4. Report `proof_digest = SHA-256(blob)` (api-v1 §6B; the blob itself never leaves the device).

**Re-verification** (service start, every 6 h, logon notification, before each job):
- Only when a **session** token exists. With an S4U token `VerifyConsent` returns `ErrNeedSession`, which means "not checked" and never a signal.

**Signal mapping:**

| Result (session token) | Meaning | Action |
|---|---|---|
| OK | Same master-key lineage | Continue |
| `ErrDPAPIKeyUnavailable` (`NTE_BAD_KEY_STATE`) | Master key unwrappable: typically an **admin password reset** of a local account (a user-initiated *change* re-wraps the keys and still passes) | Candidate `password_reset`, evidence `dpapi_unprotect_failed`. **Debounce:** confirm on a second, distinct logon session before blacklisting, to avoid a false blacklist from a transient LSA state |
| `ErrConsentInvalid` (`ERROR_INVALID_DATA`, claim mismatch) | Tampered or foreign blob | `consent_unverifiable` |
| `ErrNeedSession` | No session | Not evaluated |

**Limits.**
- The proof shows that *a process running as the user* sealed this claim on *this device*; it does not prove a human clicked. That is acceptable under the plan.
- For MSA and Entra accounts, how an online password change propagates into DPAPI is undocumented, so the plan treats it as best effort (R-P1). It is in the VM test plan.
- Windows Hello and PIN sign-in still yield a session with DPAPI access (to be verified).

## 4. Limitations matrix (expected; ✔ works, ✘ fails, ? verify on VM)

Logged-on = `WTSQueryUserToken` (Active or Disconnected session). Logged-off = S4U.

| Account | State | Files | DPAPI (user) | EFS |
|---|---|---|---|---|
| Local (SAM) | logged-on | ✔ | ✔ | ✔ |
| Local (SAM) | logged-off | ✔ (MSV1_0 S4U) | ✘ `NTE_BAD_KEY_STATE` | ✘ skip + retry at logon |
| Microsoft account (MSA-linked local) | logged-on | ✔ | ✔ | ✔ |
| Microsoft account (MSA-linked local) | logged-off | ? expected ✔: SAM name from `LookupAccountSid` with MSV1_0 S4U; this is how Task Scheduler "run whether logged on or not, without a password" works | ✘ | ✘ |
| Entra ID (`AzureAD\…`, cloud-only join) | logged-on | ✔ | ✔ | ✔ |
| Entra ID (`AzureAD\…`) | logged-off | ✘ **no S4U path**: MSV1_0 has no SAM account and Kerberos S4U2Self has no on-prem KDC. `ErrS4UUnsupported` → skip the profile while logged off (`profile_needs_logon` issue) | ✘ | ✘ |
| (Extra) AD / hybrid domain | logged-off | ? KERB_S4U via Negotiate; needs a reachable DC (machine account performs S4U2Self); offline → skip | ✘ | ✘ |

Network credentials are ✘ for every logged-off row. Logged-on rows have them, but VaultDock never uses them.

**Consequences for the product:**
- An Entra-only device backs up other users' profiles only while those users are signed in (including disconnected).
- The UI should state this on the consent screen for Entra accounts.

## 5. VM test plan (Windows 11 Pro, snapshot before each run; `psexec -s` or a test service as LocalSystem)

Accounts: `loc1` (local standard), `loc2` (local admin), `msa1` (MSA-linked), `aad1` (Entra-joined VM, separate snapshot). Each has `Documents\t.txt`, an EFS file `Documents\efs.txt` (`cipher /e`), a file with an INTERACTIVE-only ACE, a OneDrive placeholder (msa1/aad1), and a consent blob created with the UI path (`consent-selftest` variant that writes the blob to a file).

| # | Case | Expect |
|---|---|---|
| T1 | `profile -sid loc1 -path t.txt` while loc1 is signed in | source=session, read OK |
| T2 | Same, loc1 disconnected (switch user) | source=session |
| T3 | Same, loc1 signed out | source=s4u, read OK, 4624 type 3 logged |
| T4 | T3 for `efs.txt` | read fails; error code recorded |
| T5 | T3 with `-dpapi blob` | `ErrNeedSession` (no attempt); also call `unprotectConsent` directly under S4U → expect `NTE_BAD_KEY_STATE` |
| T6 | T1 with `-dpapi blob` | verify OK, digest stable |
| T7 | `net user loc1 NewPass` (admin reset), sign in, T6 | `ErrDPAPIKeyUnavailable` → password_reset |
| T8 | loc1 changes own password (Ctrl+Alt+Del), T6 | still OK |
| T9 | msa1 signed out: T3/T4/T5 | S4U works? record the exact status if not |
| T10 | msa1 password changed on account.microsoft.com, sign in with new password, T6 | record whether DPAPI changes (R-P1) |
| T11 | aad1 signed out | `ErrS4UUnsupported` before any LSA call; also force MSV1_0 and Negotiate to record the real NTSTATUS |
| T12 | loc2 (admin) signed out, read a file ACL'd Administrators-only | denied (deny-only filter works); `whoami /groups` equivalent dump shows Administrators deny-only |
| T13 | INTERACTIVE-only ACE file, S4U Network vs Batch logon | record; decide whether Batch is ever needed |
| T14 | OneDrive placeholder, session and S4U | confirm the placeholder attribute is detectable without hydration; S4U read fails |
| T15 | Service with the C6 RequiredPrivileges allowlist only (not psexec) | all of T1–T3 still work, so nothing outside the allowlist is needed |
| T16 | Run as a non-SYSTEM admin | `ErrNoPrivilege` (no SeTcb) and a clean failure |
| T17 | `AccessCheckPath` vs real open for 50 random profile files | same verdicts (feeds the C7 decision, Q6) |
| T18 | `RevertToSelf` hygiene: after `Do`, `OpenThreadToken` on that thread → `ERROR_NO_TOKEN` | — |

## 6. Recommended `tokens` interface for P1-23 (`desktop/internal/tokens`)

P0-36's stub was not present when this spike ran; if it differs, P1-23 should converge on this.

```go
package tokens

type Source int
const (
    SourceSession Source = iota + 1 // WTSQueryUserToken
    SourceS4U                       // LsaLogonUser S4U, no credentials
)

type Caps struct{ Files, DPAPI, EFS, Network bool }

var (
    ErrNotLoggedOn    = errors.New("tokens: user has no interactive session")
    ErrS4UUnsupported = errors.New("tokens: S4U not supported for this account type") // Entra ID
    ErrNoPrivilege    = errors.New("tokens: required privilege not held")
    ErrSIDMismatch    = errors.New("tokens: token user does not match requested SID")
    ErrNotUser        = errors.New("tokens: SID is not a user account")
)

// Token is a filtered (Administrators deny-only, no privileges) user token.
type Token interface {
    SID() string
    Source() Source
    SessionID() (uint32, bool)
    Caps() Caps
    // Do runs fn on a locked OS thread impersonating the user.
    Do(fn func() error) error
    // AccessCheck: would the user be granted mask on sd (file generic mapping)?
    AccessCheck(sd *windows.SECURITY_DESCRIPTOR, mask uint32) (bool, error)
    // PrimaryDup returns a caller-owned primary token for CreateProcessAsUser.
    PrimaryDup() (windows.Token, error)
    Close() error
}

type Provider interface {
    // ForProfile prefers a session token, then S4U. The caller closes the token.
    ForProfile(ctx context.Context, sid string) (Token, error)
    // SessionOnly never falls back to S4U (consent verification, DPAPI, EFS).
    SessionOnly(ctx context.Context, sid string) (Token, error)
}
```

**Package split for P1-23:**
- Consent protect and verify belong in `desktop/internal/profiles`, built on `Token.Do`.
- `ProtectConsent` runs in the UI binary.
- The `Provider` interface allows a fake in tests (non-Windows CI and unit tests); the real implementation is `//go:build windows`.

**Walker contract (P1-24):**
- `Caps().EFS == false` → skip EFS files (`FILE_ATTRIBUTE_ENCRYPTED`) with issue `efs_needs_logon`.
- Always skip cloud placeholders.
- `ErrS4UUnsupported` / `ErrNotLoggedOn` → skip the profile with `profile_needs_logon`.
