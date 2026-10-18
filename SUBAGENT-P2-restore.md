# P2 Restore (done directly, no agents)

Implemented: wire/client/sdk restore, desktop executor + task runner (T-G1 test), agent TaskKindRestore wiring, standalone `vaultdock-restore` CLI (T-R3 tool), server routes (vad-server), web Restore page (vad-server).

Audit: sdk/server/executor tests pass; agent wiring, CLI and web UI only build/vet/tsc/eslint-checked (usage limit). Never deletes user files (overwrite keeps the old file); all writes guard-checked; no passwords stored.

Remaining work and tests: see D:/VaultDock/remains.md.
