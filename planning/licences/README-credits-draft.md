# README "Licence" and "Credits" drafts (P0-31)

Drafts for INT to paste into the two READMEs. Each section lists only code that is
**incorporated** (linked into a binary or bundled into the web assets) at the pinned
versions audited on 2026-10-18. Research-only projects (restic, kopia, borg, duplicacy,
Syncthing, rclone, gocloud, USMT, Velociraptor, ludusavi, etc.) are deliberately not
listed. Runtime tools that VaultDock only invokes or detects (winget, choco, scoop, git,
cloudflared) are not credited either.

When a planned dependency is actually imported (e.g. `klauspost/compress`,
`golang.org/x/crypto` in the SDK, `microsoft/go-winio`, `go-ole` directly, `andygrunwald/vdf`,
`minio/minio-go`), `.licenses/check.sh` will pick it up; add its Credits line then and
regenerate `THIRD_PARTY_NOTICES.md` with `.licenses/check.sh --notices`.

---

## 1. Client repository (`VaultDock/README.md`)

### Licence

VaultDock is source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE).

- **Noncommercial use is permitted**: personal use, hobby projects, research, and use by
  charities, educational institutions, public research, public safety or health
  organisations and government institutions.
- **Commercial use requires a separate licence.** Contact ASPEC Racing Pty Ltd via
  <https://github.com/ASPEC-RACING-PTY-LTD> to arrange one.
- Redistribution must keep the `LICENSE` file (or its URL) and the line
  `Required Notice: Copyright 2026 ASPEC Racing Pty Ltd (https://github.com/ASPEC-RACING-PTY-LTD)`.

Third-party components keep their own licences; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

### Credits

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

---

## 2. Server repository (`vad-server/README.md`)

### Licence

vad-server is source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE).

- **Noncommercial use is permitted**: personal use, hobby projects, research, and use by
  charities, educational institutions, public research, public safety or health
  organisations and government institutions.
- **Commercial use requires a separate licence.** Contact ASPEC Racing Pty Ltd via
  <https://github.com/ASPEC-RACING-PTY-LTD> to arrange one.
- Redistribution must keep the `LICENSE` file (or its URL) and the line
  `Required Notice: Copyright 2026 ASPEC Racing Pty Ltd (https://github.com/ASPEC-RACING-PTY-LTD)`.

Third-party components keep their own licences; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

### Credits

vad-server is built on these open-source projects. Thank you to their authors and contributors.

| Project | Licence | What it does in vad-server |
|---|---|---|
| [go-chi/chi](https://github.com/go-chi/chi) | MIT | HTTP routing and middleware |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) (with modernc.org/libc) | BSD-3-Clause (SQLite: public domain) | Server database without cgo |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | BSD-3-Clause | Argon2id password hashing |
| [golang.org/x/time](https://pkg.go.dev/golang.org/x/time) | BSD-3-Clause | Request rate limiting |
| [oschwald/maxminddb-golang](https://github.com/oschwald/maxminddb-golang) | ISC | Approximate location of sign-in IP addresses (GeoIP database reader) |
| [React](https://react.dev/) and [React Router](https://reactrouter.com/) | MIT | Web interface and routing |
| [TanStack Query](https://tanstack.com/query) | MIT | Data fetching and caching in the web interface |
| [xyflow (React Flow)](https://xyflow.com/) (with d3 modules) | MIT (d3: ISC, BSD-3-Clause) | Node editor for automation workflows |
| [Lucide](https://lucide.dev/) | ISC | Icons |
| [Tailwind CSS](https://tailwindcss.com/) | MIT | Styling of the web interface |

The full list of incorporated components, including indirect ones, with their licence
texts is in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
