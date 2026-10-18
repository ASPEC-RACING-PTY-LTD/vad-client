# VaultDock implementation status: Phase 1 complete (2026-10-18)

This file consolidates every `SUBAGENT-*.md` report in this folder. Each report has the full details: changed files, interfaces, tests run, contract issues and follow-ups.

**Commits.** Phase 0 and Phase 1 are committed locally in both repositories; nothing is pushed. The Phase S commits are `VaultDock` `01df74e` and `vad-server` `c177302`.

## Layout

- `D:/VaultDock/VaultDock` contains:
  - `sdk/`: Go module `github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk`;
  - `desktop/`;
  - `desktop/e2e/`: a test-only module (the desktop engine against a real test server);
  - `install.sh`: the C21 redirect stub to vad-server.

  The contracts are in `sdk/contracts/`: api-v1 rev 1.1b and backup-format-v1 rev 1.2. The rev 1.0 text is in `sdk/contracts/history/`.
- `D:/VaultDock/vad-server` is the server and web app. It builds against `../VaultDock/sdk` through a temporary `replace` until `sdk/v0.1.0` is tagged.
- `D:/VaultDock/go.work` is untracked.
- Pre-split backups are in `D:/VaultDock/backups/`.

## Phase 0: complete (38 tasks and 6 spikes)

See the P0 reports. The spikes still need validation on an elevated VM (VSS, S4U, service install, installer modes, cloudflared).

## Phase 1: complete (36 tasks plus follow-ups)

| Area | Tasks |
|---|---|
| SDK | P1-01 vdcrypt (rev 1.2), P1-02 compress, P1-03 chunker, P1-04 pack, P1-05 snapshot, P1-07 repo, P1-08 schedule, P1-09 client, P1-10 repo server backend |
| Server | P1-06 objstore, P1-11, P1-12 key custody, P1-13 enrolment/key exchange/escrow, P1-14 repos/objects, P1-15 locks/snapshots (`backup.v1`), P1-16 catalog ingestion/search, P1-17 device tasks/approvals, P1-18 jobs/runs/issues, P1-19 modules, P1-20 profiles/superuser, P1-21 readiness, P1-SW wiring |
| Desktop | P1-22 keystore, P1-23 profiles/tokens, P1-24 (+24b dirs, +24c run snapshot) backup store/walker, P1-25 pipeline, P1-26 (+26b service wiring) runner/task loop, P1-27 scheduler, P1-28 modules, P1-29 (+29b agent API) first-run and Simple screens |
| Web | P1-08w, P1-32 backups, P1-33 readiness, P1-34 approvals, P1-35 devices/profiles/keys, P1-WW flags/badges |
| Installer | P1-30 admin NSIS installer (not compiled), P1-31 mode texts |
| Tests | P1-36 end to end: enrol with SIK pin, first-device keys, backup, incremental, second-device key share, catalog browse (`vad-server/tests/integration/{backup,keys}_test.go`, `desktop/e2e/backup_e2e_test.go`) |

INT fixes:
- **INT-FIX-A:** caller-identity path checks; `InstallUpdate` refused in service mode; upgrade defaults to Advanced when sync folders exist.
- **INT-FIX-B:** approvals policy; outbox moved onto the engine.
- **INT-FIX-C:** docs and mode texts.
- **INT-FIX-D:** host wiring.
- **INT-FIX-E:** the server rejects key files of another repository; the keystore requires a repo id; `testkit.Password`.
- **INT-SEC (after Phase 1):** sync folders are read as their owner, and configurations are scoped per user.
- **Done directly by INT:**
  - `go mod tidy` and `gofmt` in all modules;
  - `web/dist/.keep`, `.gitattributes` (`deploy/**`), and `test-results` ignored;
  - the lock sweeper started in `internal/app`;
  - stale test assertions updated (job revision after a module report; stub routes; an approval kind without a handler);
  - the issue routes accept device signatures;
  - deterministic run ordering in `backup.db` (a flaky parent-snapshot test).

## Verification (last run 2026-10-18)

All of these pass:
- sdk: `go vet ./...` and `go test ./...`;
- desktop: `go build ./...`, `GOOS=linux go build ./...`, `go vet ./...` and `go test ./...`;
- desktop/e2e: `go vet ./...` and `go test ./...`;
- desktop/frontend: `npm run build`;
- vad-server: `go vet ./...`, `go test ./...` and `go test -tags integration ./tests/...`;
- vad-server/web: `npm run build`, `vitest` (80 of 80) and `eslint` (0 errors, 102 old warnings).

**Not verifiable on this machine:**
- Docker image build;
- `makensis` (the installer is not compiled);
- elevated or SYSTEM behaviour (real tokens, machine DPAPI, guard under impersonation, VSS, service install);
- shellcheck, actionlint and `-race` (no cgo);
- the Wails app in a real window.

## Decisions taken by INT (for owner review)

- **Format rev 1.2:** the chain is sealed under `K_chain`, so that metadata escrow cannot reach file contents.
- **api-v1 rev 1.1b** (additive):
  - a built-in server destination;
  - atomic config replace under an exclusive lock;
  - `lock_not_found`;
  - SIK fingerprint = the first 16 bytes of SHA-256, as 8 groups of 4 hex characters;
  - `rotate-server` rotates the SEK only;
  - `key_share.target_sign_pub` = the device's VD1 key;
  - rewrap semantics;
  - issue-class mapping;
  - device-signed issue list, skip and retry-skipped.
- **Earlier decisions:**
  - KEK file `VAULTDOCK_KEK_FILE` = `/state/keys/kek`;
  - 90-day tombstones;
  - ISO weekdays;
  - mode file `config\mode.json`;
  - service privileges (Simple: SeChangeNotify, SeImpersonate, SeTcb, SeCreateGlobal; Advanced adds SeBackup and SeManageVolume);
  - the P1-30 installer security choices.
- **Key files over the PUT route:** capped at 64 KiB and checked against the repository id.

## Open follow-ups (from the reports)

**Security:** both Phase 1 items are fixed by INT-SEC (see `SUBAGENT-INT-SEC-owner-identity.md`):
- sync configurations have an owner;
- in the service every scan, stat and open runs with the owner's token;
- pipe clients only see and act on their own folders, queue, history and events.

What remains:
- Validate with real session and S4U tokens on an elevated VM.
- The C5 marker import should set `OwnerSID`.

**Backup engine:**
- Per-file memory for the whole tree.
- No 30 s idle pack seal.
- ADS, placeholder extensions and inventory are not produced.
- Names that are not UTF-8 are skipped.
- `CommitRefs` re-reads trees (`Result.ReferencedPacks` is optional).
- Directory entries are emitted only behind `Walker.EmitDirs`, which the pipeline turns on.

**Keys:**
- SIK rotation is deferred.
- The audit row of a SEK rotation is only logged on failure.
- Readiness does not yet surface degraded custody (no KEK).

**Server:**
- Migration 9 for run/issue columns stored as JSON.
- No single-job GET; no manual "resolve issue" route.
- The snapshot `download_url` route is chosen by P1-16 (see its report).

**Desktop UI:**
- Bridge `modules`/`backup` events in `bridgedKinds`.
- Simplify the optional-interface assertions now that the methods exist.

**Contract:**
- §6B `discovered` state and evidence values.
- The §6A `modules_summary` shape.
- The §8 approval response fields.
- Tree authentication under metadata escrow (P1-07 note).

**Release:**
- Tag `sdk/v0.1.0` and drop the server's `replace`.
- Compile the installer.
- Validate the spikes on an elevated VM.

## Next

Phase 2. MASTER_PLAN §18.4 says the master decomposes it at its start from §12/§13; about 16 tasks are expected. Work then continues per [the execution rules](planning/AGENT_BRIEF.md).

## Phase 2 (Restore) — in progress (2026-10-18)
Decomposed in planning/PHASE2_TASKS.md. Core restore is implemented across sdk, desktop (executor, agent task, `vaultdock-restore` CLI) and vad-server (routes, downloads, web Restore page). The local in-app restore API, the desktop Restore screen, e2e and full verification remain; see D:/VaultDock/remains.md.
