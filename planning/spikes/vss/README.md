# P0-S1 spike: VSS from a Go LocalSystem service + AccessCheck-gated reads

Status: the design is settled and the prototype compiles (`GOOS=windows go build`, `go vet` clean).
The token gate works unelevated against live files. The shadow lifecycle and gated snapshot reads are **untested** because they need an elevated VM (see "VM test plan").
Dependencies: `github.com/go-ole/go-ole` v1.3.0 (MIT) and `golang.org/x/sys` v0.49.0 (BSD-3, already used by `desktop`). go-vss (MPL-2.0) is not used. No code was copied: the prototype is written from the Microsoft API docs, and restic and go-vss were used for research only.

## Files

| File | Purpose |
|---|---|
| `wmi_windows.go` | `Win32_ShadowCopy` create, DeviceObject lookup and delete through go-ole (`SWbemLocator`, then `ExecMethod_`) |
| `gate_windows.go` | `Gate`: duplicates the user token to identification level, then runs `AccessCheck` on live and snapshot security descriptors; `withBackupPrivilege` (thread-scoped SeBackup); `OpenSnapshotFile` |
| `main_windows.go` | CLI: `check`, `create`, `delete`, `read`, `demo` |

Build: `cd planning/spikes/vss && GOWORK=off go build -o vss-spike.exe .` (`GOWORK=off` because the module isn't part of `D:/VaultDock/go.work`).

## Recommended approach

1. **Production (DSK-F, `vd-vss.exe`): the VSS COM API (`IVssBackupComponents`) through raw vtable calls with go-ole and x/sys**, written clean-room from the MS docs. Use `VSS_CTX_BACKUP` (non-persistent, so no auto-release flag) and `VSS_BT_COPY`. Reasons:
   - A non-persistent shadow is **deleted automatically when the requestor process exits or crashes**. This meets the "always delete" requirement without relying on reaping.
   - It adds writer coordination (`PrepareForBackup`/`BackupComplete`), which gives application-consistent snapshots for writer-aware apps.
   - It doesn't create user-visible "Previous Versions" entries and doesn't consume persistent shadow storage between runs.
   - `VSS_BT_COPY` leaves other backup tools' backup history untouched.
2. **Spike/fallback route: WMI `Win32_ShadowCopy.Create(Volume, "ClientAccessible")`** (prototyped here). It is simpler and needs only IDispatch, but:
   - the shadow is **persistent**: a crash leaks it until it is reaped;
   - it shows up in Previous Versions;
   - it has no writer coordination (crash-consistent only).
   It is acceptable for M3 if the COM binding slips, provided orphan reaping is in place.

Either way, the shadow work lives in the short-lived `vd-vss.exe` component (SYSTEM). The agent does the **gated reads**: it receives the device path over the pipe, or `vd-vss` streams the file. Gate logic is the same in both cases.

### COM call sequence (production)

`CreateVssBackupComponentsInternal` (vssapi.dll; the documented `CreateVssBackupComponents` is a C++-mangled export), then:
1. `InitializeForBackup(NULL)`
2. `SetContext(VSS_CTX_BACKUP)`
3. `SetBackupState(selectComponents=false, bootableSystemState=false, VSS_BT_COPY, partialFile=false)`
4. `GatherWriterMetadata` → wait on the `IVssAsync` (needed for writers to participate, even with no component selection)
5. `StartSnapshotSet(&setID)`
6. `AddToSnapshotSet(L"C:\\", GUID_NULL, &snapID)` for each needed volume (one set per backup run)
7. `PrepareForBackup` → wait
8. `DoSnapshotSet` → wait, then check `QueryStatus` HRESULT
9. `GetSnapshotProperties(snapID)` → `m_pwszSnapshotDeviceObject` (`\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN`); `VssFreeSnapshotProperties`
10. … run the gated reads …
11. `BackupComplete` → wait
12. `DeleteSnapshots(setID, VSS_OBJECT_SNAPSHOT_SET, TRUE, …)`
13. `Release()`

Run it all on one `runtime.LockOSThread`'d goroutine after `CoInitializeEx(COINIT_MULTITHREADED)`, plus `CoInitializeSecurity(RPC_C_AUTHN_LEVEL_PKT_PRIVACY, RPC_C_IMP_LEVEL_IMPERSONATE)`. The latter is required: VSS calls back into the requestor. Build for the native architecture only (amd64/arm64): a WOW64 requestor fails with `VSS_E_WOW64_UNSUPPORTED`.

### WMI call sequence (prototype)

1. `CoInitializeEx(MTA)`
2. `SWbemLocator` → `Security_.Privileges.AddAsString("SeBackupPrivilege")` → `ConnectServer(".", "root\cimv2")`
3. `Get("Win32_ShadowCopy")` → `Methods_("Create").InParameters.SpawnInstance_()` with `Volume="C:\"`, `Context="ClientAccessible"`
4. `ExecMethod_("Create", in)` → `ReturnValue`, `ShadowID`
5. **Persist `ShadowID`** to `%ProgramData%\VaultDock\state\shadows.json`
6. `Get("Win32_ShadowCopy.ID=\"{…}\"").DeviceObject`
7. … gated reads …
8. `Delete_()`

Shadow IDs are validated against a GUID regex before they go into an object path. Only IDs that VaultDock recorded are ever deleted (§7.6 uninstaller rule). Reaping on start: delete every recorded ID that still exists. Remaining gap: a crash between `Create` returning and the ID being persisted. To close it, list `Win32_ShadowCopy` before and after creation and record the new IDs as "pending".

## Privilege requirements

| Operation | Needs | Notes |
|---|---|---|
| Create/delete a shadow (COM or WMI) | Administrators or Backup Operators; native bitness | LocalSystem qualifies. The VSS service does the work; the requestor's token must hold **SeBackupPrivilege** (it is checked during `InitializeForBackup`/writer callbacks). |
| Open files on the shadow device | **SeBackupPrivilege enabled** + `FILE_FLAG_BACKUP_SEMANTICS` | The shadow keeps the original ACLs; SYSTEM isn't granted on every profile file. |
| Read SDs (`READ_CONTROL`) of live/shadow files | SeBackupPrivilege enabled (covers objects that don't grant SYSTEM `READ_CONTROL`) | Unelevated, the spike gets `Access is denied` on such objects; the gate treats that as **deny** (fail-closed). |
| Get the user token | **SeTcbPrivilege** (`WTSQueryUserToken`, S4U `LsaLogonUser`) | Validated by P0-S3. |
| `AccessCheck` | an impersonation token (identification level is enough) | no privilege needed |

SCM `RequiredPrivileges` (C6) must therefore include SeBackupPrivilege and SeTcbPrivilege; SeRestore is not needed. **SeBackup is enabled only on a thread impersonation token** (`ImpersonateSelf` → `AdjustTokenPrivileges` on the thread token → `RevertToSelf`), never process-wide. The prototype calls `AdjustTokenPrivileges` directly so it can detect `ERROR_NOT_ALL_ASSIGNED` (privilege not held), which x/sys's wrapper swallows.

## AccessCheck design (C7)

For each file `C:\rel` read from the shadow `DEV`, on a locked OS thread with thread-scoped SeBackup:
1. Take the user's primary token (from `WTSQueryUserToken`, or from S4U for logged-off users), then `DuplicateTokenEx(…, SecurityIdentification, TokenImpersonation)`. Do this once per user per run.
2. **Live check:** open `C:\rel` with `READ_CONTROL|FILE_READ_ATTRIBUTES`, `FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT`, sharing all, and **reject reparse points**. Then call `GetSecurityInfo(handle, OWNER|GROUP|DACL|LABEL)`. Two details matter:
   - The SD is read by handle, not with `GetNamedSecurityInfo`, so junctions and symlinks are never followed.
   - `LABEL` is included so mandatory-integrity no-read-up labels are honoured.
3. `MapGenericMask(GENERIC_READ, file mapping)` → `FILE_GENERIC_READ` (covers `FILE_READ_DATA`), then `AccessCheck(sd, impToken, …)`. The file passes only if the status is TRUE and all requested bits are granted.
4. **Snapshot check:** repeat steps 2 and 3 on `DEV\rel`. Checking the snapshot-time SD as well is stricter than C7, which requires only the live SD. It covers files that were protected at snapshot time and loosened later, and it decides alone when the live file has since been deleted.
5. Only after both checks pass, open `DEV\rel` with `GENERIC_READ` and `BACKUP_SEMANTICS|OPEN_REPARSE_POINT|SEQUENTIAL_SCAN`. Then verify the file index matches the checked object (the snapshot is immutable, so this is a sanity check).
6. Directories: before enumerating `DEV\dir`, run `CheckDir` (`FILE_LIST_DIRECTORY` on the live and snapshot directory), because file names are data too.
7. Fail-closed: any error (`ErrDenied`, `ErrReparse`, open/SD failure) skips the file with an `access_denied`/`snapshot_read_failed` issue. Only "live file absent" is tolerated.

Why this matches "what the user could read":
- AccessCheck evaluates the user's groups, deny ACEs, `OWNER RIGHTS`, and the integrity label.
- It does **not** grant access through privileges, so the user's own SeBackup (for example as a Backup Operator) is ignored.
- `WTSQueryUserToken` returns the UAC-*filtered* token for admins, so Administrators is deny-only. The gate therefore mirrors unelevated Explorer access, which is the right policy.
- Hard links share one SD per file object, so they can't be used to launder access.

## Risks

- **Traverse/parent ACLs:** AccessCheck on the file ignores parent directories. Users normally hold SeChangeNotify (bypass traverse), so this matches Windows semantics. The `CheckDir` gate prevents name disclosure for unlistable directories.
- **EFS-encrypted files:** reading them through backup semantics returns an error or ciphertext. Detect `FILE_ATTRIBUTE_ENCRYPTED` and skip with an issue; live reads under the user token cover them instead.
- **Cloud placeholders / `FilesNotToSnapshot`** (OST etc.): these are missing or offline in the shadow. Skip them as today.
- **Restricted-privilege service + WMI:** WmiPrvSE impersonates the caller, so a `RequiredPrivileges`-trimmed token must still carry SeBackup. Test this in the VM (T3 below). COM VSS avoids the WMI hop.
- **TOCTOU:** the snapshot side is immutable. On the live side, permissions can change after the check; that is accepted because the snapshot SD is checked too.
- **Persistent WMI shadows** leak on crash, fill shadow storage, and can be pruned by Windows. Reaping and the COM route mitigate this.
- **Performance:** two extra opens + `GetSecurityInfo` + `AccessCheck` per file. Cache SDs per file index if profiling demands it. A directory-level cache is wrong because files can have explicit ACEs.

## VM test plan (elevated; not run on the dev machine)

Prepare a Windows 11 VM with users `alice` (standard, signed in at the console) and `bob` (standard), plus the Sysinternals `psexec`. Copy in `vss-spike.exe`.

| # | Steps (as Administrator unless noted) | Pass |
|---|---|---|
| T1 | `vss-spike create C:\` then `vss-spike delete <id>`; check with `vssadmin list shadows` | ID and device printed; shadow gone afterwards |
| T2 | `psexec -s vss-spike.exe demo C:\Users\alice\Documents\a.txt C:\out\a.txt` | Copy identical; shadow deleted |
| T3 | Install the spike as a service with `sc privs` = SeBackup/SeTcb/SeImpersonate/SeChangeNotify/SeIncreaseQuota/SeAssignPrimaryToken; run `demo` | Same as T2 |
| T4 | `demo C:\Users\bob\Documents\secret.txt …` (alice is the console user; bob's profile ACL excludes alice) | Logged "skipped", **no output file** |
| T5 | In alice's profile: a file with an explicit Deny Read for alice; a file owned by an admin with alice removed | Both skipped |
| T6 | Alice's Outlook PST open (or `type`-locked via a handle-holding process) | Copied from the snapshot |
| T7 | Junction in alice's Documents → `C:\Users\bob` | `ErrReparse`, skipped |
| T8 | A file with a High-integrity no-read-up label | Skipped |
| T9 | Change a file ACL to deny alice **after** creating the shadow, then `read <id>` | Skipped (live check) |
| T10 | Kill the process (`taskkill /f`) mid-demo; restart | WMI: the shadow is listed in state and reaped. COM: the shadow has disappeared on its own |
| T11 | Alice is a UAC admin; target a file readable only by elevated Administrators | Skipped (filtered token) |
| T12 | As SYSTEM, impersonate alice's token (no SeBackup) and open `DEV\Users\alice\…` and `DEV\Users\bob\…` without backup semantics | alice's file opens; bob's is denied by Windows itself |

## C7 pass/fail criteria

**Pass**, which keeps AccessCheck-gated VSS for all authorised profiles: T2–T5 and T7–T9 and T11 all behave as stated, **and** no file the user can't open from an unelevated Explorer/`type` session is ever produced. The cross-check is a script that tries `type` as the user on every file that the gate allowed and on every file it skipped; there must be 0 mismatches over a seeded profile tree.

**Fail:** any allowed file the user cannot read, or an object class where the gate can't decide reliably (for example the SD can't be read). In that case, apply the fallback below.

## Fallback (C7 limitation, Q6)

VSS is restricted to the **active console user's own profile**.
- The snapshot is read only for paths under that user's profile root, and still goes through the gate.
- All other profiles, and anything outside the profile, are read live under the owning user's token only. Locked files there produce a "locked files skipped" issue.
- In the fallback, the agent can additionally open shadow files while **impersonating the active user** (thread token = user token + no SeBackup). Windows then enforces the ACL itself and no AccessCheck emulation is needed. This is **expected but unverified**: Previous Versions lets unelevated users open `ClientAccessible` shadow files subject to the file ACLs, which suggests the shadow device is reachable by a user token. Verify this in the VM as T12.
- If T12 holds, impersonated reads would also work for S4U tokens, which may make it a simpler primary design. Worth testing first in DSK-F.
