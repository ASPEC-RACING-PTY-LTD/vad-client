# Sub-agent brief (common to every implementation task)

You are one sub-agent implementing **one task** of `D:/VaultDock/VaultDock/MASTER_PLAN.md` (r3). The master agent (INT) gave you a task id, goal, owned files and dependencies. Read this brief fully, then only the plan/contract sections your task names.

## Layout (after Phase S, already done)

| Repo | Path | Go module |
|---|---|---|
| Client | `D:/VaultDock/VaultDock` | `sdk/` = `github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk`; `desktop/` = `github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop`; `desktop/e2e/` = test-only module (desktop engine vs real server) |
| Server | `D:/VaultDock/vad-server` | `github.com/ASPEC-RACING-PTY-LTD/vad-server` (React web app in `web/`) |

- `D:/VaultDock/go.work` (untracked) ties the modules together; Go commands run inside any module directory use it automatically.
- In the plan, `S:` means `vad-server/` and `C:` means `VaultDock/`. Old pre-split paths such as `pkg/x` are now `sdk/x`.
- Contracts, **LOCKED rev 1.1**:
  - `D:/VaultDock/VaultDock/sdk/contracts/api-v1.md`
  - `D:/VaultDock/VaultDock/sdk/contracts/backup-format-v1.md`
  - Implement them exactly and **never edit them**. Record any contract problem under "Contract issues" in your report.
- Host: Windows 11; Go toolchain 1.27 with `go 1.26.0` directives (don't change them); Node 24. Use the Bash tool (Git Bash) or PowerShell.

## Hard rules

1. **Edit only the files your task owns**, and create new files only under the paths it owns. If you need a change in someone else's file, don't make it; describe it in your report under "Notes for dependent tasks / INT".
2. **No state-changing git or `ch` commands:** no commit, push, checkout/switch, stash, reset, rebase, merge, `rm` or `mv`. Read-only `git status`, `git diff` and `git log` are fine.
3. **Dependencies:** only the dependency owner named in your task may run `go get` or `npm install <pkg>`. Nobody runs `go mod tidy`; INT does. Use the standard library and existing deps where possible. Every new third-party dependency must be MIT, BSD, ISC or Apache-2.0, with its licence checked; list it in your report. **Never copy code from other projects**; research-only references are fine.
4. **Network** only where your task says so: fetching licence texts, or a package install by the dependency owner.
5. **Size:** your task is sized for about 10 minutes. If it grows, stop at a point that builds and tests cleanly and report `partial` with the remainder clearly listed. Don't gold-plate.
6. **No long-running processes** left behind: no dev servers or watchers. Don't install global tools, change system settings or run anything that needs admin rights. Spikes document what they can't run.
7. **Match the surrounding code:** naming, comment density, error style, `slog` logging, table tests. Run `gofmt` on Go files. Server tests follow the existing `internal/api/api_test.go` patterns. Never edit frozen files: `vad-server/internal/api/api_test.go` and `vad-server/tests/integration/integration_test.go`.
8. **Security:**
   - no secrets in logs;
   - constant-time compares for MACs/tokens;
   - `crypto/rand` only;
   - validate every input from clients;
   - never weaken existing auth (VD1 device signing, session + CSRF).

## Verify (run only for the packages you touched)

- Go: `go build ./...`, `go vet ./<your pkgs>/...`, `go test ./<your pkgs>/...`, each from inside the module directory.
- Web (`vad-server/web`): `npx tsc -b --noEmit` or `npm run build`, plus `npx vitest run <your files>` once vitest exists.
- `install.sh`: `bash -n install.sh`, plus `shellcheck` if it is available.

## Report (mandatory; the task is not done without it)

Write `D:/VaultDock/VaultDock/SUBAGENT-<TASKID>-<short-kebab-title>.md` with these sections:

```
# <TASKID> <title>
Status: completed | partial | failed
## Goal
## Changes (repo: path, created/modified/deleted, why)
## Interfaces exposed (exact Go/TS signatures, routes, schema others depend on)
## Commands run and results
## Contract issues
## Gaps / follow-ups
## Notes for dependent tasks / INT
```

Keep it factual and concise. Your final chat reply should be just the status and the report path.
