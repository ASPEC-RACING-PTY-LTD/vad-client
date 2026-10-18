# Spike P0-S4: credential-change signals

Status: done (time-boxed). Inputs: MASTER_PLAN §7.8, §4 C8, api-v1 §6B (LOCKED rev 1.1).
Constraints honoured: no passwords are handled or stored, nothing was changed, no elevation was used, and the Security log was not read (the unprivileged attempt was refused, as expected).

## 1. What was measured on this host

Host: Windows 11 Pro 26300, workgroup (not AD- or Entra-joined), `NgcSet: NO`. The user `ALPHA\Ali` (`S-1-5-21-…-1001`) is a **Microsoft-account-linked local account** and a member of Administrators. The process was not elevated.

| Probe (unprivileged) | Result |
|---|---|
| `net user Ali` | `Password last set` = **the current time on every call** (13:28:04, then 13:28:53) |
| `Get-LocalUser` | `PrincipalSource=MicrosoftAccount`, `PasswordLastSet` **empty** |
| `NetUserGetInfo` level 1 (prototype) | `usri1_password_age = 0` on every call |
| `NetUserGetInfo` level 24 (prototype) | `usri24_internet_identity=TRUE`, provider `MicrosoftAccount` |
| `WDAGUtilityAccount` (built-in, disabled) | `PasswordLastSet` changed today; Windows rotates this password automatically |
| ProfileList `HKLM\…\ProfileList\<SID>` | readable; `ProfileImagePath=C:\Users\Ali` |
| `CryptProtectData`/`CryptUnprotectData` round-trip (user scope, in memory) | OK; blob of 246 bytes |
| `%APPDATA%\Microsoft\Protect` | contains `<SID>\`, `CREDHIST` and `SYNCHIST` (the history chains DPAPI uses to re-wrap master keys after a password change or sync) |
| `Get-WinEvent -LogName Security` | `Attempted to perform an unauthorized operation` (needs SYSTEM, admin or *Event Log Readers*) |

**Key finding:** the planned signal "local account password age reset" (§7.8, first bullet) **fires constantly for every Microsoft-account-linked user** if it is applied naively, because SAM keeps no `PasswordLastSet` for them and reports an age of 0. The signal must be gated on `usri24_internet_identity == FALSE`.

Prototype: `main.go` (module `vdspike/credsignals`, stdlib plus `x/sys/windows` v0.49.0 only). It builds with `GOWORK=off go run .` and prints the SID, the password age, the MSA link, the profile path and the DPAPI round-trip. It is read-only.

## 2. Signal evaluation

Privilege notes: the agent runs as **SYSTEM** (§7.8). "In user session" means code running with the user's token (`WTSQueryUserToken` plus impersonation, or the VaultDock UI process) **with the profile loaded**. An S4U token does not work for this.

### S1. Local SAM password last-set (`NetUserGetInfo` level 1/3/4, `usriN_password_age`)

- **API:** `NetUserGetInfo(nil, name, 3, …)`. Derive `last_set = now − password_age` and store **`last_set`**, not the age. Use level 3 for `usri3_flags` (`UF_ACCOUNTDISABLE`, `UF_LOCKOUT`) and level 24 for the MSA link. `x/sys/windows` exposes `NetUserGetInfo` but only the `UserInfo10` struct; define the level 3 and level 24 structs locally (the prototype does this for levels 1 and 24).
- **Privilege:** none; any local user can query local accounts, and SYSTEM always can.
- **Reliability:**
  - **High** for pure local accounts. Both a user change (Ctrl-Alt-Del) and an admin reset (`net user`, lusrmgr) move `last_set`.
  - **Unusable** for MSA-linked accounts: the age is always 0, as measured above.
  - **Not applicable** to Entra (`AzureAD\…`) accounts, which are not in SAM (`NERR_UserNotFound`).
  - For domain accounts it does not apply locally; see S1d.
- **Rule:** fire `password_changed` when `last_set_new − last_set_baseline > 120 s`, and only when `internet_identity == FALSE` and `age > 0`. The 120 s tolerance absorbs clock steps and NTP.
- **False positives:**
  - A forced change at expiry, or "must change at next logon". Both are real changes, so blacklisting is correct.
  - Large wall-clock jumps (mitigated by the tolerance).
  - Built-in accounts such as `WDAGUtilityAccount` rotate automatically, so track only profile SIDs that are consented.
- **Cannot tell** a change from a reset. Classify with S2 or S3.

### S1d. Domain `pwdLastSet` (LDAP) or `NetUserGetInfo(\\DC, …, 3)`

- **API:** LDAP to the DC (`wldap32`, not in `x/sys/windows`), or `NetUserGetInfo` with `servername` set to the DC from `DsGetDcName`.
- **Privilege:** the computer account (SYSTEM) can read `pwdLastSet` for users under the default AD ACLs.
- **Reliability:** high when the DC is reachable; nothing when offline. Skip with no event when the DC is unreachable.
- **False positives:** replication lag between DCs can show an older value, so only fire when the value moves **forward**.
- **Evidence:** `ldap_pwdlastset`. Not measurable here because the host is not domain-joined.

### S2. DPAPI consent proof fails to decrypt

- **API:** in the user session, call `CryptUnprotectData(consent_blob, …, CRYPTPROTECT_UI_FORBIDDEN)`. Fire only on the specific failures `NTE_BAD_KEY_STATE` (0x8009000B) or `NTE_BAD_DATA`/`ERROR_INVALID_DATA` (0x8007000D) **with a successful same-session `CryptProtectData` canary**. The canary proves DPAPI itself works in that session.
- **Privilege:** must run as the user with the profile loaded. SYSTEM and S4U tokens cannot do it. The check therefore runs only at logon or unlock, or when VaultDock UI starts, never for logged-off users.
- **Reliability by account type:**
  - **Local account, admin reset:** high. The master keys are wrapped with the old password and there is no domain backup key, so the blob becomes undecryptable. This is the only case where DPAPI distinguishes a *reset*.
  - **Local account, user change:** no signal. Windows re-wraps the master keys and records the change in `CREDHIST`. S1 covers this case.
  - **MSA-linked:** expected to keep decrypting after an online password change, because Windows re-wraps through the sync path (`SYNCHIST` is present). **Unverified:** changing the password was out of scope.
  - **Domain:** a reset is still decryptable through the domain DPAPI backup key, so there is no signal.
  - **Entra:** unverified; assume no signal.
- **False positives:**
  - a profile copied or restored from another machine;
  - a corrupted `Protect\` folder;
  - a temporary profile (`.bak` in ProfileList);
  - a roaming profile not yet synchronised;
  - the profile not loaded when the check runs.

  Mitigations: require the canary to succeed, require the failure to **repeat at the next session** for any error code other than the two above, and check S5 first, because a temporary or moved profile explains the failure.
- **Maps to:** `password_reset` when S1 or S3 also shows a change or reset, otherwise `consent_unverifiable`. Evidence: `dpapi_unprotect_failed`.

### S3. Security event log 4723 / 4724 / 4738 / 4725 / 4726 / 4740 / 4767

- **API:** `EvtSubscribe` with a persisted `EvtCreateBookmark`, plus `EvtQuery` at service start for catch-up. These are in `wevtapi.dll`; `x/sys/windows` has no wrapper, so a `LazyDLL` is needed. Example XPath: `*[System[(EventID=4723 or EventID=4724 or EventID=4738 or EventID=4725 or EventID=4726) and band(Keywords,0x20000000000000)]]`, which is audit success only. Then match `EventData/TargetSid` against the consented SIDs.
- **Privilege:** SYSTEM, or SeSecurityPrivilege / *Event Log Readers*. Unprivileged access was confirmed to be refused.
- **Reliability:** medium, used as corroboration.
  - The required audit category, *Account Management / User Account Management: Success*, is normally on by default on Windows 11 clients. Policy can disable it, and VaultDock must **not** change audit policy because that is a system setting.
  - The log can roll over or be cleared (1102 or 1104). If the bookmark is lost, fall back to S1.
  - Only local SAM operations are logged here. MSA, Entra and domain password changes are logged in the cloud or on the DC.
- **Event meanings:**
  - **4723:** a user changed their own password → `password_changed`, `eventlog_4723`.
  - **4724:** an admin reset the password → `password_reset`, `eventlog_4724`. It also fires during account creation after 4720 for a brand-new account; such an account is never consented, so it is filtered out by SID.
  - **4738:** account changed. It accompanies 4723 and 4724 and also fires for unrelated attribute edits, so use it only for `UserAccountControl` changes (disabled), not alone.
  - **4725** (disabled) → `account_disabled`. **4726** (deleted) → `account_removed`.
  - **4740** (lockout) and **4767** (unlocked) are **not** credential changes. A lockout is often a typo or a stale cached credential. **Recommend: do not blacklist on them.** At most, log them locally; §6B has no signal for them.
- **False positives:** failed attempts (filtered by the success keyword), and automatic rotation of the built-in `WDAGUtilityAccount` (filtered by SID).

### S4. Microsoft account (MSA) and Entra ID

- **MSA:**
  - A password changed at account.microsoft.com is **not observable locally** until the user next signs in online with the password, and a user who signs in only with a Hello PIN may never do so.
  - SAM keeps no last-set time for MSA-linked accounts (S1 measured), DPAPI is expected to keep working (S2), and no local 4723 is written.
  - Nothing documented and unprivileged reveals it. **Accepted limitation (C8/Q7).**
  - Undocumented stores (`HKCU\Software\Microsoft\IdentityCRL`, the TokenBroker cache) must not be used.
- **Entra:**
  - Not in SAM, and changes happen in the cloud.
  - Locally, only undocumented PRT timestamps exist (`dsregcmd /status` `AzureAdPrtUpdateTime`), and they are rejected as a signal.
  - **The only reliable source is server-side Microsoft Graph** (`user.lastPasswordChangeDateTime`, `signInActivity`), which needs tenant consent. This is a possible future server feature outside this contract. Recorded as a follow-up.
- **What *is* observable locally for both:**
  - the SID and profile signals (S6);
  - account disabled or removed, when the local shadow account is removed;
  - S2 failures if master-key protection is broken by a reset.

### S5. Windows Hello and PIN changes

- The PIN and biometrics are device-bound gestures that unlock a TPM key (NGC). They are not the account password.
- The NGC store (`…\ServiceProfiles\LocalService\AppData\Local\Microsoft\Ngc`) is ACL-restricted to LocalService. Reading it would mean working around access controls, which is **forbidden**.
- `Microsoft-Windows-HelloForBusiness/Operational` events exist only for WHfB (domain or Entra) and are not documented as change notifications.
- **Recommendation: not a signal.** A PIN change does not alter the password or the DPAPI master-key wrapping, and §6B has no enum value for it.

### S6. SID change, removed or moved profile, disabled account

- **APIs:**
  - `LookupAccountSid(stored SID)`: `ERROR_NONE_MAPPED` (1332) from the **local** authority means the account was removed.
  - `LookupAccountName(stored username)`: a different SID means the account was deleted and re-created with the same name, so `sid_changed`.
  - `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\<SID>`: compare `ProfileImagePath` with the baseline (`profile_moved`). A missing key means `account_removed`. A `<SID>.bak` key or the `State` temp-profile flags mean `profile_moved`.
  - `NetUserGetInfo` level 3 `UF_ACCOUNTDISABLE` → `account_disabled`.
- **Privilege:** none; HKLM ProfileList is readable by Users and SYSTEM.
- **Reliability:** high for local accounts.
- **False positives:**
  - A **rename** keeps the same SID, so update `username` and do **not** blacklist.
  - For domain or Entra SIDs while offline, the lookup can fail with RPC or trust errors. Treat anything other than 1332 from the local SAM as transient and retry.
  - A temporary profile after a profile-load failure is real enough to stop work, so `profile_moved` is right.

## 3. Recommended signal set

The agent keeps a per-SID baseline in the agent's protected state (SYSTEM-only ACL): `{sid, username, account_kind: local|msa|entra|domain, profile_path, pwd_last_set (local or domain only), evtlog_bookmark, consent_blob, proof_digest}`. `account_kind` comes from level 24 (`internet_identity`), the `AzureAD\` prefix or `NetGetJoinInformation`/`DsGetDcName`.

| # | Signal | Applies to | When it runs | Blacklists? |
|---|---|---|---|---|
| R1 | S1 SAM `last_set` moved forward >120 s | `local` only (never `msa`) | start, every 6 h, logon, before job | yes |
| R2 | S3 4723/4724/4725/4726 success for a consented SID | `local` (and `msa` shadow for 4725/4726) | live subscription plus catch-up from bookmark | yes |
| R3 | S6 SID, profile or disabled checks | all | start, every 6 h, logon, before job | yes (rename → update only) |
| R4 | S2 DPAPI proof fails, canary OK | all; meaningful mainly for `local` | each logon, unlock, UI start | yes |
| R5 | S1d domain `pwdLastSet` forward | `domain`, DC reachable | every 6 h, logon | yes |
| — | 4738 alone, 4740, 4767, Hello/PIN, PRT timestamps | — | — | **no** |

Order of evaluation per check: R3, then R1/R5, then R2, then R4. R3 runs first because it explains R4 failures.

## 4. Event payloads (api-v1 §6B `POST /device/profiles/{sid}/security-event`)

`observed_at` is the RFC 3339 UTC time the agent observed the change (for R2, the event's `TimeCreated`). The agent first stops all work for the SID locally, then posts the event. It retries with backoff if the server is offline, and the local stop holds in the meantime.

| Trigger | `signal` | `evidence` |
|---|---|---|
| R1 local last-set moved, no 4724 seen | `password_changed` | `local_password_age` |
| R1 plus a matching 4724, or R4 plus R1 | `password_reset` | `eventlog_4724` / `dpapi_unprotect_failed` |
| R2 4723 | `password_changed` | `eventlog_4723` |
| R2 4724 | `password_reset` | `eventlog_4724` |
| R4 alone | `consent_unverifiable` | `dpapi_unprotect_failed` |
| R5 | `password_changed` | `ldap_pwdlastset` |
| R3 name maps to a different SID | `sid_changed` | `sid_lookup` |
| R3 SID maps to nothing (1332, local) or 4726 | `account_removed` | `sid_lookup` (4726: see the contract gap) |
| R3 `UF_ACCOUNTDISABLE`, or 4725 | `account_disabled` | *(gap; interim `sid_lookup`)* |
| R3 ProfileImagePath changed, or temp/.bak profile | `profile_moved` | *(gap; interim `sid_lookup`)* |

Example:
```json
{"signal":"password_reset","observed_at":"2026-10-18T02:28:47Z","evidence":"eventlog_4724"}
```

## 5. Contract gaps (for INT; the contract is not edited)

1. The `evidence` enum has no value for `account_disabled` (`netuser_flags` / `eventlog_4725`), `profile_moved` (`profile_list`) or `eventlog_4726`. The interim mapping above uses `sid_lookup`, which loses precision.
2. There is no optional free-text or `detail` field, for example the Win32 error code for `consent_unverifiable`. The agent should keep that locally instead (never secrets).
3. §7.8 cites `usri4_password_age`. Level 3 is sufficient, and level 4 adds nothing. More importantly, §7.8 does not say the signal must be **disabled for MSA-linked accounts**.

## 6. Limitations (accepted per C8 / Q7)

- MSA and Entra password changes made elsewhere are generally **undetected locally**. Entra could be covered by a future server-side Graph integration.
- DPAPI distinguishes a reset only for pure local accounts, and only when the user's session is present.
- Event-log coverage depends on the audit policy, which VaultDock must not change.
- Not exercised in this spike, because each would need a password change, an admin, a domain or Entra: actual 4723/4724 payloads, DPAPI behaviour after an MSA online change, and domain LDAP reads.
