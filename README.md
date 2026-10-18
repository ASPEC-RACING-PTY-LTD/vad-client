# VaultDock

The Windows client of VaultDock, a self-hosted backup, file storage and synchronisation system. This repository holds the Windows desktop app and the shared Go SDK. The server and web app live in [ASPEC-RACING-PTY-LTD/vad-server](https://github.com/ASPEC-RACING-PTY-LTD/vad-server).

- **Windows desktop app**: tray app and Windows service that back up the folders you select, browse and restore older versions, and (in Advanced mode) sync folders, run automations, discover and reinstall apps, and back up app settings.
- **Resumable, verified transfers**: background uploads with adaptively parallel chunk transfers.
- **Secure by design**: device approval, Ed25519 per-device keys protected with Windows DPAPI, signed and replay-protected requests. Backups are encrypted on the PC before upload.
- **SDK** (`sdk/`, Go module `github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk`): automation engine, API client, request signing, wire types and the backup format, shared with the server.

## Install the server first

The server runs in Docker on Linux. Its installer is in the server repository:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/ASPEC-RACING-PTY-LTD/vad-server/main/install.sh
less install.sh
bash install.sh
```

The image is `ghcr.io/aspec-racing-pty-ltd/vad-server`. See the [vad-server README](https://github.com/ASPEC-RACING-PTY-LTD/vad-server#readme) for details.

## Windows desktop app

Build the installer (see [docs/desktop.md](docs/desktop.md)) or download `VaultDock-<version>-amd64-setup.exe` from a release. Installing needs administrator rights once. The installer offers two modes, each with an (i) info text:

- **Simple Folder Backups**: back up folders of your own Windows profile on a schedule, then browse and restore them.
- **Full Access (Advanced)**: adds sync, automations, app discovery and reinstall, settings backup, snapshots and (with their consent) other profiles on the PC.

You can switch modes later in **Settings > Mode** without reinstalling.

1. Enter your server URL, username and password.
2. The app shows a verification code. In the web interface open **Devices**, check the code, and approve.
3. Pick the folders to back up, preview what will upload, and you're done.

The password is used once to request access and is never stored. Revoking a device in the web interface stops it immediately.

**Who can read your backups:** by default the server also holds the backup keys (full escrow), so the server administrator can browse and restore your files, for example onto a new PC. An administrator can instead set a repository to metadata escrow, where the server sees file names but never file contents. A recovery key lets you restore even without the server.

## Updates and releases

- The desktop app updates itself from the GitHub releases of `ASPEC-RACING-PTY-LTD/VaultDock`. Installers are verified against their published SHA-256 checksum.
- Push a `vX.Y.Z` tag and the Release workflow publishes the Windows installer, the component manifest `components-<version>.json` (with a detached Ed25519 signature when `RELEASE_MANIFEST_KEY` is set) and `SHA256SUMS`. The installer and app are Authenticode-signed when `WINDOWS_CERT_PFX` (and `WINDOWS_CERT_PASSWORD`) are set.

## Documentation

| Topic | |
|---|---|
| [Desktop app](docs/desktop.md) | Building, installer, updates, logs |
| [Architecture](docs/architecture.md) | Components, sync engine, adaptive concurrency, automations |
| [Development](docs/development.md) | Running locally, tests |
| [Server documentation](https://github.com/ASPEC-RACING-PTY-LTD/vad-server/tree/main/docs) | Deployment, operations, security, automations |

## Repository layout

```
sdk/                 shared Go SDK (automation, client, protocol, wire, backup format, ...)
sdk/contracts        API and backup-format contracts
desktop/             Wails Windows app: UI, agent service, sync and backup engine, NSIS installer
desktop/e2e          end-to-end tests of the desktop engine against a real server
scripts/             build helpers (icons, code signing)
assets/              brand assets
```

## Licence

VaultDock is source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE).

- **Noncommercial use is permitted**: personal use, hobby projects, research, and use by
  charities, educational institutions, public research, public safety or health
  organisations and government institutions.
- **Commercial use requires a separate licence.** Contact ASPEC Racing Pty Ltd via
  <https://github.com/ASPEC-RACING-PTY-LTD> to arrange one.
- Redistribution must keep the `LICENSE` file (or its URL) and the line
  `Required Notice: Copyright 2026 ASPEC Racing Pty Ltd (https://github.com/ASPEC-RACING-PTY-LTD)`.

Third-party components keep their own licences; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Credits

VaultDock is built on these open-source projects. Thank you to their authors and contributors.

| Project | Licence | What it does in VaultDock |
|---|---|---|
| [Wails](https://github.com/wailsapp/wails) (incl. go-webview2) | MIT | Desktop application shell: WebView2 window, Go/JS bindings, native dialogs |
| [energye/systray](https://github.com/energye/systray) | Apache-2.0 | System-tray icon and menu |
| [go-toast](https://git.sr.ht/~jackmordaunt/go-toast) | MIT OR Unlicense | Windows toast notifications |
| [rjeczalik/notify](https://github.com/rjeczalik/notify) | MIT | File-system change notifications for sync folders |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) (with modernc.org/libc) | BSD-3-Clause (SQLite: public domain) | Local state database without cgo |
| [Go `x/` libraries](https://go.dev/) (`x/sys`, `x/time`, `x/net`, `x/text`) | BSD-3-Clause | Windows system APIs and registry access; transfer bandwidth limiting |
| [go-ole](https://github.com/go-ole/go-ole) | MIT | COM interop on Windows (via Wails) |
| [React](https://react.dev/) | MIT | Desktop user interface |
| [Lucide](https://lucide.dev/) | ISC | Icons |
| [Tailwind CSS](https://tailwindcss.com/) | MIT | Styling of the desktop interface |

The full list of incorporated components, including indirect ones, with their licence
texts is in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
