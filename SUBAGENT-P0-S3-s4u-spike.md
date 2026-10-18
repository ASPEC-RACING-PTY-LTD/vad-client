# P0-S3 S4U tokens for local/MSA accounts; file access; DPAPI behaviour
Status: completed (design and prototype; VM validation pending because LocalSystem paths were not run, per brief rule 6)

## Goal
Design `tokens.ForProfile(sid)` (session token, falling back to S4U), document the S4U limits (R-P2) and the DPAPI consent proof (§7.8), provide a compilable prototype, and produce a limitations matrix, a VM test plan and the interface for P1-23.

## Changes (repo: path, created/modified/deleted, why)
All files are in the VaultDock repository and were created by this task:
- `planning/spikes/s4u/README.md`: the design, call sequences, R-P2 consequences, consent-proof design, limitations matrix, the 18-case VM test plan, and the recommended `tokens` interface.
- `planning/spikes/s4u/go.mod`, `go.sum`: module `vdspike/s4u`, `golang.org/x/sys v0.49.0` (same version as `desktop`).
- `planning/spikes/s4u/doc.go`: package doc.
- `planning/spikes/s4u/lsa_windows.go`: `LsaRegisterLogonProcess`, `LsaLookupAuthenticationPackage` and `LsaLogonUser` with `MSV1_0_S4U_LOGON`/`KERB_S4U_LOGON` (same layout), plus privilege enabling.
- `planning/spikes/s4u/token_windows.go`:
  - `ForProfile`: WTS session lookup, then S4U package choice;
  - normalisation: SID assert, `CreateRestrictedToken` with Administrators deny-only and `DISABLE_MAX_PRIVILEGE`, impersonation duplicate;
  - `Do` (locked-thread impersonation), `AccessCheck`, `AccessCheckPath`, `PrimaryDup`.
- `planning/spikes/s4u/dpapi_windows.go`: `ProtectConsent` (UI), `VerifyConsent` (service, session token only) and `VerifyConsentSelf`; error classification into `ErrDPAPIKeyUnavailable` and `ErrConsentInvalid`.
- `planning/spikes/s4u/cmd/s4uspike/main_windows.go`: the CLI. `consent-selftest` needs no admin rights; `profile` needs LocalSystem and is for the VM.

## Interfaces exposed
Recommended for P1-23 (README §6):
- `tokens.Provider{ForProfile(ctx, sid) (Token, error); SessionOnly(ctx, sid) (Token, error)}`
- `Token{SID; Source; SessionID; Caps; Do(func() error) error; AccessCheck(sd, mask) (bool, error); PrimaryDup; Close}`
- Errors: `ErrNotLoggedOn`, `ErrS4UUnsupported`, `ErrNoPrivilege`, `ErrSIDMismatch`, `ErrNotUser`.

## Commands run and results
All run from `planning/spikes/s4u` with `GOWORK=off GOPROXY=off`:
- `gofmt -l -w .`: one file reformatted.
- `GOOS=windows go vet ./...`: clean.
- `GOOS=windows go build ./...`: OK.
- `go run ./cmd/s4uspike consent-selftest`, as a normal user with no admin rights:
  - DPAPI protect and unprotect round trip OK (blob 472 bytes);
  - a wrong `device_id` (entropy) was rejected;
  - a tampered blob was rejected (`ERROR_INVALID_DATA`).
- Not run: anything that needs SYSTEM or `SeTcb` (`profile` subcommand). See the VM plan T1–T18.

## Contract issues
None. `proof_digest` = SHA-256 of the blob, as api-v1 §6B specifies.

## Gaps / follow-ups
- Every S4U and DPAPI behaviour in the matrix is **expected, not yet verified**. The key unknowns:
  - MSA S4U when logged off (T9);
  - how an MSA online password change shows up in DPAPI (T10);
  - the exact Entra failure NTSTATUS (T11);
  - Network vs Batch logon type for INTERACTIVE-only ACEs (T13).
- **Entra ID-only accounts have no S4U path.** Their profiles can only be backed up while the user is signed in. The consent UI should say so.
- Option for P2+: raw EFS backup via `ReadEncryptedFileRaw`. It needs no key, but the backup is restorable only for the same user certificate.
- `CreateProcessAsUser` with an S4U token, for the Recycle Bin helper (`IFileOperation`) in session 0, is unverified.

## Notes for dependent tasks / INT
**P0-36 / P1-23:**
- `desktop/internal/tokens` did not exist when this spike ran. P1-23 should adopt the README §6 interface, with the real implementation behind `//go:build windows` and a fake `Provider` for tests.
- The consent protect/verify code belongs in `internal/profiles`.

**P1-23, DPAPI verification:**
- Verify only with a **session** token. Under S4U, unprotect always fails and must never raise a signal.
- Debounce `password_reset`: confirm it in a second, distinct logon session before blacklisting.
- On consent receipt, check that the pipe-client SID equals `claim.sid`.

**P1-24 (walker):**
- When `Caps().EFS == false`, skip `FILE_ATTRIBUTE_ENCRYPTED` files with an `efs_needs_logon` issue.
- **Always** skip cloud placeholders (`RECALL_ON_DATA_ACCESS` / `RECALL_ON_OPEN`); otherwise a session-token read causes a mass OneDrive download.
- Do batched file work inside `Token.Do`; never start goroutines inside it.
- Get one token per job, not per file: each S4U call is an LSA logon and a 4624 event.

**C6:** no privileges beyond the planned allowlist are needed. `SeTcb` covers WTS, LSA registration and impersonation-level S4U.

**Issue codes:** `efs_needs_logon`, `profile_needs_logon` and `cloud_placeholder` are proposed issue codes. INT should check them against the issue-code list in api-v1 §6 and amend it if they are missing.
