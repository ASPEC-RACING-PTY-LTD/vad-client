# Backup & Recovery Initiative: Requirements Brief

Shared input for all research/planning agents. Research and planning only: no implementation.

## Goal
Integrate advanced Windows backup, recovery and cloud storage management into the existing VaultDock project. Reuse existing infrastructure, preserve all current functionality, avoid duplicate subsystems, plan around the actual source code, and state technical limitations honestly.

## Requirements
1. **Backup engine**: discover and back up user files, AppData, applications, settings, registry data where appropriate, game saves, development projects and installed-software inventories. Per-application modes: full-data, data-only, reinstall-inventory.
2. **Independent processing**: every application/service/project/folder is a separate backup job with its own manifest, locally compressed data (prefer Zstd), progress, upload queue and retries. Parallel processing, resumable transfers, content-defined chunking, deduplication, incremental snapshots, minimal reuploads. Uploading must never wait for a whole backup run to finish.
3. **Recovery**: reinstall apps via package managers/installers, restore data and settings, detect changed Windows usernames/profile paths, automatic or granular manual destination mapping. Do not restore incompatible ACLs/SIDs. Handle encrypted/protected data where authorised and technically possible.
4. **Error handling**: actionable warnings, skip / skip-all, detailed completion summaries, Retry Skipped workflow. Never silently discard failures.
5. **Continuous sync**: keep existing VaultDock sync; add dedicated sync folders, arbitrary watched folders, custom rulesets, exclusions, schedules, conflict handling and version history. Keep sync and disaster-recovery snapshots logically separate.
6. **Storage integrations**: local disks, NAS/SMB, S3/R2/B2, extensible custom providers. Multiple destinations, replication, storage policies, user-managed credentials.
7. **Web management**: browse, preview, search, download, selectively restore and manage backed-up files, apps and snapshots. Advanced filtering, custom pruning/retention rules, dry-run previews, bulk operations, storage analytics, safe deduplication-aware garbage collection.
8. **Automations & AI**: extend the existing visual automation builder for backup schedules, retention, replication, verification and recovery tasks. Optional user-provided AI API keys for diagnostics/recommendations, with approval gates for destructive operations.
9. **Reliability/security**: client-side encryption, VSS snapshots, integrity verification, offline operation, recoverable upload queues, protected recovery points, resource limits, recovery testing, and restore without depending on the management server.
10. **UX**: simple first-time setup, automatic periodic backups, transparent progress, configurable notifications, recovery-readiness reports, guided restoration after reinstalling Windows.

## Open-source research rules
Read-only research; never copy external code. Focus on actively maintained repos. Record each project's licence and clearly separate **MIT** candidates from other licences (Apache-2.0, BSD, MPL, GPL/AGPL, etc.). Write down trade-offs and what is worth borrowing architecturally.

## Repo facts already established
- Go server (`cmd/vaultdock`, `internal/{api,store,storage,auth,config}`), SQLite (modernc, WAL), content-addressed SHA-256 blobs per user, `nodes` file tree.
- React/Vite/Tailwind/React Query/React Flow web UI in `web/`, embedded in the server binary.
- Wails v2 Windows client in `desktop/` (separate go.mod), one-way sync engine in `desktop/internal/engine`, local SQLite queue in `desktop/internal/state`, self-updater in `desktop/internal/updater`.
- Shared `pkg/{automation,client,protocol,release,version,pathutil,mimetypes}`.
- Upload protocol: create session → 8 MiB chunks (hash-verified, parallel, 503 back-pressure) → complete.
- Device auth: Ed25519 per-device keys (DPAPI), signed + replay-protected requests. Web: session cookie + CSRF, Argon2id.
- Tests: Go tests pass. No unit tests in `internal/store`, `internal/storage`, `internal/auth`, `desktop/internal/state`; no web tests.
