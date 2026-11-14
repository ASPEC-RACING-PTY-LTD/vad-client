# Windows desktop app

## Building

Requirements (build machine only; end users need nothing): Go 1.26+, Node.js 22+, the Wails CLI v2 and NSIS.

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
winget install NSIS.NSIS
cd desktop
wails build -clean -nsis -trimpath -ldflags "-X main.version=0.1.0"
```

Outputs in `desktop/build/bin/`:

- `VaultDock.exe`: the application.
- `VaultDock-<version>-amd64-setup.exe`: per-user installer (no admin rights). It installs WebView2 if missing, creates Start menu and desktop shortcuts, and registers an uninstaller.

Version: set `info.productVersion` in `desktop/wails.json` and `-X main.version`. For releases, sign both executables (hooks are in `build/windows/installer/project.nsi`).

## Behaviour

- Runs in the notification area; closing the window keeps it syncing (configurable). Tray menu: open, sync all now, pause/resume, open web interface, quit.
- **Start with Windows** (default on after connecting) adds `HKCU\...\Run\VaultDock` with `--minimized`.
- Single instance: launching it again shows the existing window.
- Settings: parallel transfers (automatic with an upper limit, or fixed), bandwidth limit, notifications.
- **App lock**: the device key is sealed with a key unlocked by a 6-digit PIN (Argon2id; after 5 wrong tries each further one doubles a wait, up to 30 minutes) or, optionally, Windows Hello (an RSA key in the user's Hello container; the agent only ever gets its public key). VaultDock starts locked after every Windows start, restart and update and syncs nothing until it is unlocked. Existing installs are asked to set a PIN once. Tray: **Lock VaultDock**; Settings: change PIN, Windows Hello, Lock now.
- **Pause while playing games** (on by default): while a game from Steam, Epic Games, Ubisoft Connect, EA, GOG, Battle.net, Xbox, Riot, Rockstar, Amazon Games or itch runs (its program is inside a launcher's game library), transfers stop and go back to the queue and scheduled backups wait; everything continues when the game closes.
- **Background service**: with the per-machine install the app always uses the VaultDockAgent service. While it is still starting (after Windows starts) or stopped, the app waits for it and shows why (stopped, or its program missing, for example quarantined by antivirus) instead of starting a separate agent with its own settings.
- **Antivirus**: the agent program carries publisher and version information, and every downloaded update installer is checked by the PC's antivirus through the Windows Antimalware Scan Interface (AMSI: Microsoft Defender, Malwarebytes and other registered products) before it runs.

## Data on the PC

| Path | Contents |
|---|---|
| `%APPDATA%\VaultDock\settings.json` | Server URL, pinned certificate, device id, preferences (no secrets) |
| `%APPDATA%\VaultDock\device.key` | Ed25519 private key encrypted with DPAPI for the current Windows user |
| `%APPDATA%\VaultDock\state.db` | Sync configurations, file index, persistent upload queue, history |
| `%LOCALAPPDATA%\VaultDock\logs\` | JSON logs, rotated at 10 MB (3 kept) |

Uninstalling asks whether to remove these; your own files and everything on the server are never touched.

## Updates

The app keeps itself up to date from the GitHub releases of `ASPEC-RACING-PTY-LTD/vad-client` (override at build time with `-X main.UpdateRepo=owner/name`; an empty value disables updates).

- It checks two minutes after start and then every six hours. **Settings → Updates → Check now** checks immediately.
- A newer installer is downloaded only over HTTPS from `github.com` (redirects are limited to GitHub's asset hosts), into `%LOCALAPPDATA%\VaultDock\updates`.
- The download is verified against the SHA-256 digest GitHub publishes for the asset, or the release's `SHA256SUMS` file. A release without a checksum is never installed. The file is hashed again immediately before it runs.
- With **Install updates automatically** on (the default), the update is installed once nothing is uploading. Otherwise Settings shows **Install and restart**.
- With the VaultDockAgent service, the service checks for, downloads and verifies updates itself. While nobody uses the app it installs them silently; while the app is open it shows **VaultDock x.y.z is ready to install**, and **Install update** runs the installer with one administrator approval and reopens the app.
- Installing stops the sync engine cleanly, then runs the installer silently (`/S /RELAUNCH /D=<current folder>`). It replaces the app in place and starts it again in the notification area. Settings, the device key and sync state are kept.

Development builds (version `dev`) never update themselves.

## Publishing a release

Push a version tag in this repository (`ASPEC-RACING-PTY-LTD/vad-client`). The **Release** workflow builds the Windows installer, then publishes the GitHub release with the component manifest and `SHA256SUMS`. Signing is optional and driven by repository secrets: `WINDOWS_CERT_PFX` (base64 .pfx) and `WINDOWS_CERT_PASSWORD` for Authenticode, `RELEASE_MANIFEST_KEY` (Ed25519 PEM) for the component manifest signature.

```bash
ch tag v1.2.0
ch push origin v1.2.0
```

Desktop apps and the download page on vaultdock.dev use the latest release, so nothing else needs updating. The server image (`ghcr.io/aspec-racing-pty-ltd/vad-server`) is released separately from the `ASPEC-RACING-PTY-LTD/vad-server` repository.

## Connecting to different servers

The server URL is entered on first run; nothing is hardcoded. Disconnect (Settings) removes the device key; connecting to a different server or account resets the local upload index so files are evaluated again for the new destination.
