# Phase 2: Restore (Simple mode complete), task breakdown

Decomposed by INT on 2026-10-18 from MASTER_PLAN §12 (Phase 2), §13 ownership and §18.4. On the owner's instruction, INT implements these directly, without sub-agents. Each task still gets a `SUBAGENT-P2-xx-*.md` report, and the work is committed locally only.

**Goal:**
- file restore, both server-driven and local;
- system-file protection (guard) enforced on every restore write;
- the standalone `vaultdock-restore` tool;
- web snapshot browse, search and download, plus a restore wizard.

**Exit tests:**
- **T-G1:** a restore into `%WINDIR%`, Program Files, another profile or through a junction is blocked and recorded.
- **T-R3:** a server-independent restore. The standalone tool restores files from a repository copy (a local or UNC directory) using only the recovery key, and the restored content matches the backed-up hashes. The r2 text of T-R3 was not carried into r3; this definition is INT's (decision P2-D1).

| Task | WS | Depends on | Scope (files) |
|---|---|---|---|
| P2-00 | INT | — | Contract api-v1 rev 1.1c (§14): device restore routes, restore cancel, download token route, restore item statuses. Wire types in `sdk/wire/restore.go` |
| P2-01 | SDK-RESTORE | — | `sdk/restore/walk.go`: resolve snapshot paths, walk a selection (include/exclude) over trees, continuation trees |
| P2-02 | SDK-RESTORE | — | `sdk/restore/reader.go`: stream a file's chunks from a `repo.Repo` and verify the keyed whole-file hash |
| P2-03 | SDK-RESTORE | P2-01 | `sdk/restore/plan.go`: token mapping (source tokens to target tokens, plus overrides), target paths, conflict actions from a stat function |
| P2-04 | SDK-CLIENT | P2-00 | `sdk/client/restore.go`: plan, execute, get, items, cancel; the device restore fetch and progress routes; snapshot content and download |
| P2-05 | SRV-3 | P2-00 | `internal/store/restores.go`: restores and items |
| P2-06 | SRV-3 | P2-05 | `internal/api/restore.go`: plan, execute (approval kind `restore` creates the device task), get, items, cancel, device fetch and progress |
| P2-07 | SRV-2 | P2-02 | `internal/api/snapshot_download.go` and `internal/backup/reader.go`: full-escrow file content (Range) and the zip download operation with a single-use token |
| P2-08 | SRV-ITEST | P2-06, P2-07 | Server unit tests; `tests/integration/restore_test.go` |
| P2-09 | DSK-G | P2-01..03 | `desktop/internal/restore`: executor. Guard on every target (recorded as `guard_blocked`), files created under the target user's token, conflict policies, never a permanent delete |
| P2-10 | DSK-G | P2-09, P2-04 | `desktop/internal/restore/task.go`: `restore` device-task handler, progress and items reporting, cancel |
| P2-11 | DSK-A | P2-09 | Agent API `ListSnapshots`, `BrowseSnapshot`, `PlanRestore`, `StartRestore`, `CancelRestore`, `ListRestores` (Host, InProc, IPC, authz) |
| P2-12 | DSK-I | P2-11 | Desktop Restore screen and Wails bindings |
| P2-13 | DSK-G | P2-02, P2-09 | `desktop/cmd/vaultdock-restore`: list, browse and restore from a directory repository copy with the recovery key or passphrase |
| P2-14 | WEB-2 | P2-07 | Web snapshots: list, browse, search, file download and zip download |
| P2-15 | WEB-2 | P2-06 | Web restore wizard (plan, execute, approval) and restore status |
| P2-16 | INT | all | T-G1 and T-R3 tests, the desktop e2e restore through the server, full verification, reports and commit |

## Decisions (INT)

- **P2-D1:** the T-R3 definition above.
- **P2-D2: overwrite never deletes.** The previous file is kept beside the target as `<name> (replaced <yyyy-mm-dd hhmmss>)<ext>`.
  - The Recycle Bin is not used. `SHFileOperation`/`IFileOperation` without UI can delete permanently when a file does not fit the bin or the volume has none, and the agent cannot confirm in session 0.
  - This narrows MASTER_PLAN §7.6 rule 5 ("deletes go to the Recycle Bin") to "restore never deletes".
- **P2-D3: device restores report through dedicated routes,** not through the task result. The `restore` task is acknowledged as soon as the device starts it, because a restore can outlive the task lease. Progress then goes to `POST /device/restores/{id}/progress`.
- **P2-D4: the standalone tool reads only directory repository copies in this phase.** Server mode needs a session login flow for a CLI and is deferred. The tool prints how to copy a repository for offline restore.
