# P0-S4 Credential-change signals spike
Status: completed

## Goal
Evaluate secure, password-free signals that a Windows profile's credentials or security changed, so the profile can be blacklisted (§7.8, C8, api-v1 §6B). Recommend a signal set and payload mapping.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `planning/spikes/credsignals/README.md` (created). The deliverable: measured results, per-signal evaluation, recommended set and payload mapping.
- VaultDock: `planning/spikes/credsignals/go.mod`, `go.sum`, `main.go` (created). A read-only prototype in module `vdspike/credsignals` using stdlib plus `golang.org/x/sys` v0.49.0 (already used by sdk; BSD-3). It prints the SID, the password age, the MSA link (USER_INFO_24), ProfileList and a DPAPI round-trip.

## Interfaces exposed
None. This is a spike. It does propose a per-SID baseline schema and a trigger-to-`{signal, evidence}` mapping (README §3–4).

## Commands run and results
- `net user Ali`, `whoami /user`, `Get-LocalUser`, `dsregcmd /status`, ProfileList read, a listing of `%APPDATA%\Microsoft\Protect`: OK.
- `Get-WinEvent -LogName Security`: refused with "unauthorized", as expected. It was not retried with elevation.
- `GOWORK=off GOPROXY=off go vet .`, `go build`, `go run .`, `gofmt -l`: all clean. Output: `password_age=0s`, `internet_identity=true provider=MicrosoftAccount`, `dpapi_roundtrip_ok=true`.

## Key findings
- **The §7.8 "password age reset" signal false-fires constantly on Microsoft-account-linked accounts.** SAM keeps no PasswordLastSet for them, so `usri_password_age` is always 0 (`net user` shows "now"). It must be gated on `usri24_internet_identity == FALSE`.
- DPAPI decrypt failure detects an admin **reset** of pure local accounts only. A normal change re-wraps through CREDHIST, and domain resets are covered by the domain backup key. MSA behaviour is expected to keep decrypting (SYNCHIST is present) but is unverified. The check needs the user's session, not SYSTEM or S4U.
- The Security log (4723/4724/4725/4726) needs SYSTEM and default auditing; use it as corroboration. 4740, 4767, 4738 alone, Hello/PIN changes and PRT timestamps should not blacklist.
- MSA and Entra cloud password changes are not locally observable (accepted, C8/Q7). Entra could be covered by a future server-side Graph integration.

## Contract issues
1. api-v1 §6B `evidence` enum has no value for `account_disabled` (netuser flags / 4725), `profile_moved` (ProfileList) or `eventlog_4726`. The interim mapping uses `sid_lookup`.
2. There is no optional `detail` field (for example a Win32 error code).
3. MASTER_PLAN §7.8 cites `usri4_password_age` (level 3 suffices) and omits the MSA gating.

## Gaps / follow-ups
- Not exercised: real 4723/4724 payloads, DPAPI after an MSA online change or local reset, domain LDAP `pwdLastSet`. Each needs a password change, an admin, or a domain.
- `wevtapi` (EvtSubscribe/bookmark) and LDAP have no `x/sys/windows` wrappers; DSK-P needs a LazyDLL.

## Notes for dependent tasks / INT
- DSK-P: follow README §3 (evaluation order R3 → R1/R5 → R2 → R4, the 120 s tolerance, SID filtering, the DPAPI canary, and retry on any error other than 1332 or the two named DPAPI errors).
- INT: consider a contract amendment for the evidence-enum gaps.
