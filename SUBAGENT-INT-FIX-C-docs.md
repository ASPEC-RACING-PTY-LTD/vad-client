# INT-FIX-C Text and docs fixes
Status: completed

## Goal
INT audit item 7: say in the install-mode texts that the server holds the backup keys by default (full escrow, C1). Update both READMEs from `planning/licences/README-credits-draft.md`. Fix the pre-split repository and image names in the docs. Align the MASTER_PLAN §7.14 secret names with the CI workflows.

## Changes (repo: path, created/modified/deleted, why)
- **VaultDock: `desktop/build/windows/installer/modes/simple.en.txt`** (modified)
  - New bullet under Permissions, after the encryption line. By default the server also holds the backup keys, so the server admin can browse and restore your files (for example onto a new PC).
  - It also says that anyone who takes over the server could read them too, and that the admin can choose a names-only mode (metadata escrow).
- **VaultDock: `.../modes/advanced.en.txt`** (modified): the same point as one bullet under Permissions.
- **VaultDock: `.../modes/compare.en.txt`** (modified): a "Both modes" bullet saying the server holds the keys by default, so the admin can browse and restore.
- All mode texts are still ASCII, LF only, no BOM, with lines of 74 characters or fewer. New word counts: simple 278, advanced 289, compare 159. Simple and Advanced are now above P1-31's 120-220 target; this is accepted for the escrow disclosure. `switching.en.txt` is unchanged.
- **VaultDock: `README.md`** (rewritten for the client repo)
  - Describes the desktop app and SDK, and points to `ASPEC-RACING-PTY-LTD/vad-server` for the server installer (`raw.githubusercontent.com/.../vad-server/main/install.sh`) and the image `ghcr.io/aspec-racing-pty-ltd/vad-server`.
  - Covers the admin installer with Simple/Advanced modes and a "Who can read your backups" note (full escrow by default, metadata escrow option, recovery key).
  - Release assets and the secret names `WINDOWS_CERT_PFX`, `WINDOWS_CERT_PASSWORD` and `RELEASE_MANIFEST_KEY`.
  - A docs table: links to server docs now point at the vad-server repo, because they no longer exist in this repo.
  - The new repository layout (`sdk/`, `desktop/`, `desktop/e2e`, `scripts/`, `assets/`), and Licence and Credits copied from draft section 1.
- **vad-server: `README.md`** (modified)
  - New title, and the install URLs changed to the `vad-server` repo.
  - The image name is stated, and the desktop section points to the VaultDock repo (admin install, modes, escrow note linking to `docs/security.md`).
  - The Updates and Releases text now matches the server release workflow assets.
  - The repository layout is updated (no `pkg/` or `desktop/`; adds `deploy/`, `testkit/` and `scripts/`, plus the SDK module note).
  - Licence and Credits are copied from draft section 2, replacing "License: Not chosen yet". CRLF line endings are preserved.
- **VaultDock: `docs/desktop.md`** (modified): the "Publishing a release" section now describes the client release only: installer, component manifest and `SHA256SUMS`. It names the three signing secrets and says the server image `ghcr.io/aspec-racing-pty-ltd/vad-server` comes from the vad-server repo. This replaces the old `ghcr.io/aspec-racing-pty-ltd/vaultdock` text.
- **vad-server: `docs/desktop.md`** (modified): the same section now says the desktop app is released from VaultDock and the server image and deploy assets from vad-server. The image is now `ghcr.io/aspec-racing-pty-ltd/vad-server`.
- **VaultDock: `docs/architecture.md`** (modified): `pkg/automation` changed to `sdk/automation`.
- **vad-server: `docs/architecture.md`** (modified): `pkg/automation` changed to `sdk/automation`, with a note on the module path and repo.
- **vad-server: `docs/operations.md`** (modified): the upgrade step now names the image `ghcr.io/aspec-racing-pty-ltd/vad-server`.
- **VaultDock: `MASTER_PLAN.md`** §7.14 (modified): `WINDOWS_SIGNING_CERT` becomes `WINDOWS_CERT_PFX`, plus the optional `WINDOWS_CERT_PASSWORD`. The manifest key is now named `RELEASE_MANIFEST_KEY` (Ed25519 PKCS#8 PEM; the `.sig` is written only when it is set).
- **VaultDock: `SUBAGENT-INT-FIX-C-docs.md`** (created): this report.

## Interfaces exposed
None. The image name was checked against `vad-server/.github/workflows/release.yml` (`IMAGE: ghcr.io/aspec-racing-pty-ltd/vad-server`) and `install.sh` (`DEFAULT_REPO=ASPEC-RACING-PTY-LTD/vad-server`). The secret names were checked against `VaultDock/.github/workflows/release.yml`.

## Commands run and results
- Mode texts: an awk line-length check (max 74), a CR count (0) and a non-ASCII grep (none). `wc -w` as above.
- After the edits, a grep of the in-scope `*.md` for `pkg/` and for `WINDOWS_SIGNING_CERT` finds nothing.
- The remaining `ASPEC-RACING-PTY-LTD/VaultDock` mentions are correct (client repo or SDK module).
- No code, workflow, `SUBAGENT-*`, `sdk/contracts/**` or `planning/**` files were touched. No git commands other than `git status`.

## Contract issues
None.

## Gaps / follow-ups
- `docs/development.md` (identical in both repos) is still the pre-split dev guide: server, web and desktop commands in one file, and root `go test ./...`. It contains no repo or image names, so it was left as is. It should be split per repo.
- `vad-server/docs/desktop.md` and `vad-server/docs/architecture.md` (desktop engine parts) still describe the client. Consider moving that content to VaultDock and replacing it with a link.
- The old desktop text in `vad-server/docs/desktop.md` (per-user install, no admin) is outdated relative to P1-30. It was not rewritten because that file is about to be moved or removed.
- MASTER_PLAN line 3 still names the Codehold origin as `.../vaultdock`. That is the historical baseline, so it was left.
- MASTER_PLAN §7.1 (around line 344) mentions the secrets without names. It could reference the §7.14 names.

## Notes for dependent tasks / INT
- The UI that embeds the mode texts (Settings > Mode, first-run screen) gets the escrow wording automatically if it embeds these files at build time.
- If the escrow default ever changes from `full`, update the mode texts and both READMEs ("Who can read your backups" in the client README, and the escrow paragraph in the server README).
