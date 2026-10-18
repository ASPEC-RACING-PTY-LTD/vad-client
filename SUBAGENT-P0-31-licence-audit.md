# P0-31 Independent licence audit, PolyForm LICENSE files, third-party notices, allowlist configs, README credits draft
Status: completed

## Goal
Add the verbatim PolyForm Noncommercial 1.0.0 `LICENSE` (with the Required Notice) to both repos; audit every direct and indirect dependency at its pinned version by reading its actual licence file; produce `THIRD_PARTY_NOTICES.md` per repo; add an allowlist config and a CI-usable checker in `.licenses/`; draft the README "Licence" and "Credits" sections.

## Changes (repo: path, created/modified/deleted, why)
- C: `LICENSE` (created): line 1 `Required Notice: Copyright 2026 ASPEC Racing Pty Ltd (https://github.com/ASPEC-RACING-PTY-LTD)`, a blank line, then the official plain-text licence fetched from https://polyformproject.org/licenses/noncommercial/1.0.0.txt, byte-for-byte (sha256 of the licence part `ffcca38841adb694b6f380647e15f17c446a4d1656fed51a1e2041d064c94cc8`). Its wording was cross-checked word for word against `PolyForm-Noncommercial-1.0.0.md` on branch `1.0.0` of github.com/polyformproject/polyform-licenses: identical apart from line wrapping.
- S: `LICENSE` (created): identical to C: `LICENSE`.
- C: `.licenses/check.mjs` and S: `.licenses/check.mjs` (created, identical): the Node checker (no npm deps, no network).
  - **Go:** it runs `go list -deps` for each configured module, GOOS and build-tag set (GOWORK=off, so each module's own pins are used). For each linked module it takes the directory from the module cache and reads the top-level LICENSE/COPYING/NOTICE/PATENTS files, plus any nested licence files that cover a linked package (for example the Wails `internal/*` subcomponents). It also adds the Go standard library (GOROOT LICENSE + PATENTS).
  - **npm:** it reads `package-lock.json` (production deps, plus build tools whose output is bundled, i.e. tailwindcss) and each package's LICENSE file.
  - **Classification:** licences are classified from their text (canonical wording plus `SPDX-License-Identifier` lines) and compared with `allowlist.json`.
  - **Modes:** check (exit 1 on a violation); `--report` (markdown audit table); `--notices` (generates THIRD_PARTY_NOTICES.md); `--all` (also audits the whole `go list -m all` graph and the npm dev deps, reported as warnings).
- C: `.licenses/check.sh` and S: `.licenses/check.sh` (created, identical): bash wrapper for CI. `check.sh` runs the gate; `check.sh --notices` regenerates THIRD_PARTY_NOTICES.md and exits 1 if it was stale; `check.sh --report [--all]` prints the audit table.
- C: `.licenses/allowlist.json` (created):
  - allowed SPDX ids: MIT, BSD-2-Clause, BSD-3-Clause, ISC, Apache-2.0, 0BSD, Zlib, Unlicense, CC0-1.0;
  - forbidden and needs-approval lists (informational);
  - Go builds: `sdk` (windows + linux) and `desktop` (windows; tags "", `desktop,production`, `desktop,production,bindings`); npm project `desktop/frontend`;
  - per-package exceptions: the Go PATENTS grant and the SQLite public-domain dedication;
  - excluded files: sqlite-vec's licence and sqlite's `LICENSE-3RD-PARTY.md`, which cover parts that are not linked;
  - display-only SPDX expressions and manual-review notes.
- S: `.licenses/allowlist.json` (created): same policy; Go module `.` (linux + windows), npm project `web`.
- C: `THIRD_PARTY_NOTICES.md` (created, generated): 36 incorporated components with the full text of every licence, NOTICE and PATENTS file (about 2,400 lines).
- S: `THIRD_PARTY_NOTICES.md` (created, generated): 40 incorporated components (about 1,800 lines).
- C: `planning/licences/README-credits-draft.md` (created): "Licence" and "Credits" sections for each README (client, server). Credits list only incorporated code, each with its specific contribution; no research-only projects.
- C: `planning/licences/audit-client.md`, `planning/licences/audit-server.md` (created): full `--all --report` audit tables (also reproduced below).

No README, go.mod/go.sum or package file was edited. The only side effect outside the owned files was `go mod download` of 79 graph-only modules into the user module cache (`C:\Users\Ali\go\pkg\mod`), run from an empty scratch directory so their LICENSE files could be read.

## Interfaces exposed
- CI: `bash .licenses/check.sh` (gate) and `bash .licenses/check.sh --notices` (freshness gate), run from each repo root.
  - Prerequisites: Go, Node 18 or later, `go mod download`, and `npm ci` in `web/` or `desktop/frontend`.
- `allowlist.json` keys: `allowed`, `go[] {module, goos[], tags[]}`, `npm[] {dir, bundledDev[]}`, `ignoreModulePrefixes`, `notBundled`, `exceptions{pkg:{allow[],reason}}`, `excludeFiles{pkg:[file]}`, `spdx{pkg:display}`, `manualReview{pkg:note}`.

## Commands run and results
- `curl` polyformproject.org `.../noncommercial/1.0.0.txt` (200) and the GitHub `.md` (branch `1.0.0`; `master` and `main` return 404). A word-level diff found them identical. `cmp` of LICENSE (minus the notice lines) against the fetched text: identical.
- `go list -m all` in sdk, desktop and vad-server (GOWORK=off); `go list -deps` per GOOS/tags to find what is actually linked.
- `go mod download <79 module@version>` from an empty scratch dir: OK.
- `node .licenses/check.mjs`:
  - client: `36 incorporated components checked, 0 violation(s).`
  - server: `40 incorporated components checked, 0 violation(s).`
- `bash .licenses/check.sh --notices`, run twice per repo: the first run generated the file and the second reported "up to date" (deterministic output).
- `node --check` and `bash -n check.sh`: OK. shellcheck isn't installed.

## Findings and flagged items
**No incorporated dependency violates the allowlist.** Items examined in detail:

1. **`git.sr.ht/~jackmordaunt/go-toast/v2` v2.0.3: OK.**
   - LICENSE has `SPDX-License-Identifier: Unlicense OR MIT`, with both texts reproduced; copyright (c) 2023 Jack Mordaunt.
   - Both options are on the allowlist; VaultDock uses it under MIT. No nested third-party licences.
2. **`modernc.org/sqlite` v1.60.1: OK, with an exception.**
   - BSD-3-Clause, plus `LICENSE-SQLITE`: the SQLite amalgamation (transpiled) is public domain. This is allowed through a per-package exception (`LicenseRef-Public-Domain`).
   - `LICENSE-SQLITE_VEC` (MIT) and `LICENSE-3RD-PARTY.md` are excluded from the notices. sqlite-vec and the vfs bridge are linked only when `modernc.org/sqlite/vec` or `/vfs` is imported, and neither repo does. The components that are linked are listed as their own entries.
3. **`modernc.org/libc` v1.77.1: OK.** BSD-3-Clause. Its `LICENSE-3RD-PARTY.md` bundles Go (BSD-3), musl (MIT), go-netdb (MIT) and NixOS/nixpkgs (MIT); it is reproduced in full because musl is linked.
4. **`github.com/wailsapp/go-webview2` v1.0.22: REVIEW (no action needed now).**
   - MIT; `webviewloader/` is ISC (a Go port of OpenWebView2Loader).
   - The module also contains Microsoft `WebView2Loader.dll` binaries (x86/x64/arm64). They are `go:embed`ded **only with build tag `native_webview2loader`**, which VaultDock does not set.
   - Keep that tag off. If it is ever enabled, Microsoft's WebView2 SDK licence notice must be added.
5. **`github.com/wailsapp/wails/v2` v2.16.0: OK.** MIT, plus three nested subcomponents that are linked. All are reproduced; there is no NOTICE file.
   - `internal/frontend/desktop/windows/winc`: MIT, winc Authors;
   - `internal/go-common-file-dialog`: MIT, Harry Phillips;
   - `internal/typescriptify`: Apache-2.0.
6. **Apache-2.0 incorporated code: `energye/systray`, `tkrajina/go-reflector` and Wails typescriptify.** None ships a NOTICE file, so only the full licence text is required, and it is included.
7. **Go standard library/runtime and `golang.org/x/*`: OK.** BSD-3-Clause plus the Go PATENTS grant, which is allowed through an exception because it is a grant rather than a restriction. Both texts are included.
8. **`lucide-react` 1.57.0: OK.** The LICENSE contains ISC (Lucide) and MIT (Feather-derived icons).
9. **The SDK currently links no third-party Go module.**
   - `klauspost/compress` and `golang.org/x/crypto` are in `sdk/go.mod` (P0-01) but not yet imported, so they are not in the notices yet.
   - Their licences were audited: klauspost/compress is BSD-3-Clause, with `gzhttp/*` Apache-2.0 and some s2/snappy files MIT; x/crypto is BSD-3-Clause.
   - The next `check.sh --notices` run adds them automatically.
10. **Not incorporated, but flagged by `--all` (informational only):**
    - `github.com/hashicorp/golang-lru/v2` v2.0.7: **MPL-2.0**. In the module graph of both desktop and server but not linked. If it is ever linked, owner approval is required (§7.15).
    - `github.com/cyphar/filepath-securejoin` v0.6.1: **BSD-3-Clause AND MPL-2.0**. In the desktop graph only (via the Wails CLI's go-git); not linked.
    - `lightningcss` / `lightningcss-win32-x64-msvc` 1.32.0, 1.33.0: **MPL-2.0**. A build-time CSS tool (Tailwind/Vite) in both frontends. Its code is not shipped, only the CSS it produces, so it is acceptable as a dev tool.
    - `caniuse-lite`: **CC-BY-4.0** (data). Server web dev deps only (browserslist).
    - `lru-cache` 11.5.3 and `minimatch` 10.2.6: **BlueOak-1.0.0** (permissive). Server web dev deps only (jsdom/eslint).
    - Six npm dev packages ship no licence file: `@rolldown/binding-win32-x64-msvc`, `@humanfs/types`, `esrecurse`, `imurmurhash`, `natural-compare` and `saxes`. Their licence (MIT, Apache-2.0, BSD-2 or ISC) was taken from package.json; all are dev-only.
    - Dev dependency summary:
      - client frontend: 24 MIT, 3 Apache-2.0, 2 ISC, 1 BSD-3, 1 mixed permissive, 4 MPL-2.0 (lightningcss);
      - server web: 181 MIT, 23 Apache-2.0, 12 ISC, 8 BSD-2, 3 BSD-3, 1 CC0, 2 mixed permissive, 2 BlueOak, 1 CC-BY-4.0, 4 MPL-2.0.
    - **No GPL, LGPL, AGPL, SSPL or noncommercial licence was found anywhere in either dependency graph.**

## Contract issues
None.

## Gaps / follow-ups
- MASTER_PLAN §7.15 names `go-licenses` and `license-checker` for CI. I used a self-contained checker instead (no new tools or dependencies). INT may still add go-licenses later; the allowlist would then need converting.
- `desktop/e2e` (test-only module) and `vad-server/testkit` are not audited as incorporated code.
- The Required Notice uses "Copyright 2026 ASPEC Racing Pty Ltd" as the task specified (Q10). §7.15 shows the form `Copyright © ASPEC Racing Pty Ltd`; the plan text may need aligning.
- The notices file is generated from the local module cache and node_modules, so CI must run `go mod download` and `npm ci` before `check.sh`.
- A Wails desktop build with `-tags dev` (development only, never shipped) links extra modules such as echo and gorilla/websocket. Those are not in the notices.

## Notes for dependent tasks / INT
- Paste the sections from `planning/licences/README-credits-draft.md` into `VaultDock/README.md` and `vad-server/README.md`. Add a Credits line for each planned dependency when it is actually imported: klauspost/compress, x/crypto in the SDK, go-winio, vdf, minio-go, volmgmt.
- CI wiring: `bash .licenses/check.sh && bash .licenses/check.sh --notices`.
- `desktop/wails.json` still has `"copyright": "VaultDock contributors"` and the author `vaultdock@example.invalid`. Consider aligning them with ASPEC Racing Pty Ltd (not my file).
- After every dependency change (including `go mod tidy`), run `bash .licenses/check.sh --notices` in each repo and commit the regenerated file.
- Never build the desktop app with `-tags native_webview2loader` unless Microsoft's WebView2 licence notice is added first.

## Full audit table: client (`VaultDock`: sdk + desktop + desktop/frontend; `--all`)

| Eco | Package | Version | Licence (from file text) | Declared | Files read | Copyright | Incorporated in | Status |
|---|---|---|---|---|---|---|---|---|
| go | atomicgo.dev/cursor | v0.2.0 | MIT |  | LICENSE | Copyright (c) 2020 Marvin Wendt (MarvinJWendt) | desktop (graph only) (not incorporated) | ok |
| go | atomicgo.dev/keyboard | v0.2.9 | MIT |  | LICENSE | Copyright (c) 2020 Marvin Wendt (MarvinJWendt) | desktop (graph only) (not incorporated) | ok |
| go | atomicgo.dev/schedule | v0.1.0 | MIT |  | LICENSE | Copyright (c) 2022 Marvin Wendt (MarvinJWendt) | desktop (graph only) (not incorporated) | ok |
| go | dario.cat/mergo | v1.0.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2013 Dario Castañé. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | git.sr.ht/~jackmordaunt/go-toast/v2 | v2.0.3 | MIT OR Unlicense |  | LICENSE | Copyright (c) 2023 Jack Mordaunt | desktop | ok |
| go | github.com/acarl005/stripansi | v0.0.0-20180116102854-5a71ef0e047d | MIT |  | LICENSE | Copyright (c) 2018 Andrew Carlson | desktop (graph only) (not incorporated) | ok |
| go | github.com/alecthomas/chroma/v2 | v2.14.0 | MIT |  | COPYING | Copyright (C) 2017 Alec Thomas | desktop (graph only) (not incorporated) | ok |
| go | github.com/aymanbagabas/go-osc52/v2 | v2.0.1 | MIT |  | LICENSE | Copyright (c) 2022 Ayman Bagabas | desktop (graph only) (not incorporated) | ok |
| go | github.com/aymerick/douceur | v0.2.0 | MIT |  | LICENSE | Copyright (c) 2015 Aymerick JEHANNE | desktop (graph only) (not incorporated) | ok |
| go | github.com/bep/debounce | v1.2.1 | MIT |  | LICENSE | Copyright (c) 2016 Bjørn Erik Pedersen | desktop | ok |
| go | github.com/bitfield/script | v0.24.0 | MIT |  | LICENSE | Copyright (c) 2019 John Arundel | desktop (graph only) (not incorporated) | ok |
| go | github.com/charmbracelet/glamour | v0.8.0 | MIT |  | LICENSE | Copyright (c) 2019-2023 Charmbracelet, Inc | desktop (graph only) (not incorporated) | ok |
| go | github.com/charmbracelet/lipgloss | v0.12.1 | MIT |  | LICENSE | Copyright (c) 2021-2023 Charmbracelet, Inc | desktop (graph only) (not incorporated) | ok |
| go | github.com/charmbracelet/x/ansi | v0.1.4 | MIT |  | LICENSE | Copyright (c) 2023 Charmbracelet, Inc. | desktop (graph only) (not incorporated) | ok |
| go | github.com/cloudflare/circl | v1.6.3 | BSD-3-Clause |  | LICENSE | Copyright (c) 2019 Cloudflare. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/containerd/console | v1.0.3 | Apache-2.0 |  | LICENSE | Copyright The containerd Authors | desktop (graph only) (not incorporated) | ok |
| go | github.com/cyphar/filepath-securejoin | v0.6.1 | BSD-3-Clause AND MPL-2.0 |  | COPYING.md, LICENSE.BSD, LICENSE.MPL-2.0 | Copyright (C) 2014-2015 Docker Inc & Go Authors. All rights reserved. | desktop (graph only) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| go | github.com/davecgh/go-spew | v1.1.1 | ISC |  | LICENSE | Copyright (c) 2012-2016 Dave Collins <dave@davec.name> | desktop (graph only) (not incorporated) | ok |
| go | github.com/dlclark/regexp2 | v1.11.0 | MIT |  | LICENSE | Copyright (c) Doug Clark | desktop (graph only) (not incorporated) | ok |
| go | github.com/dustin/go-humanize | v1.0.1 | MIT |  | LICENSE | Copyright (c) 2005-2008  Dustin Sallings <dustin@spy.net> | desktop | ok |
| go | github.com/emirpasic/gods | v1.18.1 | BSD-2-Clause AND ISC |  | LICENSE | Copyright (c) 2015, Emir Pasic | desktop (graph only) (not incorporated) | ok |
| go | github.com/energye/systray | v1.0.3 | Apache-2.0 |  | LICENSE | Copyright 2014 Brave New Software Project, Inc. | desktop | ok |
| go | github.com/flytam/filenamify | v1.2.0 | MIT |  | LICENSE | Copyright (c) 2020 tanjiahui | desktop (graph only) (not incorporated) | ok |
| go | github.com/fsnotify/fsnotify | v1.9.0 | BSD-3-Clause |  | LICENSE | Copyright © 2012 The Go Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/go-git/gcfg | v1.5.1-0.20230307220236-3a3c6141e376 | BSD-3-Clause |  | LICENSE | Copyright (c) 2012 Péter Surányi. Portions Copyright (c) 2009 The Go | desktop (graph only) (not incorporated) | ok |
| go | github.com/go-git/go-billy/v5 | v5.9.0 | Apache-2.0 |  | LICENSE | Copyright 2017 Sourced Technologies S.L. | desktop (graph only) (not incorporated) | ok |
| go | github.com/go-git/go-git/v5 | v5.19.2 | Apache-2.0 |  | LICENSE | Copyright 2018 Sourced Technologies, S.L. | desktop (graph only) (not incorporated) | ok |
| go | github.com/go-ole/go-ole | v1.3.0 | MIT |  | LICENSE | Copyright © 2013-2017 Yasuhiro Matsumoto, <mattn.jp@gmail.com> | desktop | ok |
| go | github.com/godbus/dbus/v5 | v5.1.0 | BSD-2-Clause |  | LICENSE | Copyright (c) 2013, Georg Reinke (<guelfey at gmail dot com>), Google | desktop (graph only) (not incorporated) | ok |
| go | github.com/golang/groupcache | v0.0.0-20241129210726-2c02b8208cf8 | Apache-2.0 |  | LICENSE |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/google/pprof | v0.0.0-20260802141513-ef3492d7dac3 | Apache-2.0 |  | LICENSE |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/google/shlex | v0.0.0-20191202100458-e7afc7fbc510 | Apache-2.0 |  | COPYING |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/google/uuid | v1.6.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2009,2014 Google Inc. All rights reserved. | desktop | ok |
| go | github.com/gookit/color | v1.5.4 | MIT |  | LICENSE | Copyright (c) 2016 inhere | desktop (graph only) (not incorporated) | ok |
| go | github.com/gorilla/css | v1.0.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2023 The Gorilla Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/gorilla/websocket | v1.5.3 | BSD-2-Clause |  | LICENSE | Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/hashicorp/golang-lru/v2 | v2.0.7 | MPL-2.0 |  | LICENSE | Copyright (c) 2014 HashiCorp, Inc. | desktop (graph only) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| go | github.com/itchyny/gojq | v0.12.13 | MIT |  | LICENSE | Copyright (c) 2019-2023 itchyny | desktop (graph only) (not incorporated) | ok |
| go | github.com/itchyny/timefmt-go | v0.1.5 | MIT |  | LICENSE | Copyright (c) 2020-2022 itchyny | desktop (graph only) (not incorporated) | ok |
| go | github.com/jackmordaunt/icns | v1.0.0 | MIT |  | LICENSE | Copyright (c) 2018 Jack Mordaunt | desktop (graph only) (not incorporated) | ok |
| go | github.com/jaypipes/ghw | v0.21.3 | Apache-2.0 |  | COPYING |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/jaypipes/pcidb | v1.1.1 | Apache-2.0 |  | COPYING, LICENSE |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/jbenet/go-context | v0.0.0-20150711004518-d14ea06fba99 | MIT |  | LICENSE | Copyright (c) 2014 Juan Batiz-Benet | desktop (graph only) (not incorporated) | ok |
| go | github.com/jchv/go-winloader | v0.0.0-20210711035445-715c2860da7e | ISC |  | LICENSE.md | Copyright © 2021, John Chadwick <john@jchw.io> | desktop (graph only) (not incorporated) | ok |
| go | github.com/kevinburke/ssh_config | v1.2.0 | MIT |  | LICENSE | Copyright (c) 2017 Kevin Burke. | desktop (graph only) (not incorporated) | ok |
| go | github.com/klauspost/compress | v1.20.3 | BSD-3-Clause (gzhttp: Apache-2.0; s2/snappy parts: BSD-3-Clause, MIT) |  | LICENSE | Copyright (c) 2012 The Go Authors. All rights reserved. | sdk (graph only), desktop (graph only) (not incorporated) | ok |
| go | github.com/klauspost/cpuid/v2 | v2.3.0 | MIT |  | LICENSE | Copyright (c) 2015 Klaus Post | desktop (graph only) (not incorporated) | ok |
| go | github.com/kr/text | v0.2.0 | MIT |  | License | Copyright 2012 Keith Rarick | desktop (graph only) (not incorporated) | ok |
| go | github.com/labstack/echo/v4 | v4.13.3 | MIT |  | LICENSE | Copyright (c) 2021 LabStack | desktop (graph only) (not incorporated) | ok |
| go | github.com/labstack/gommon | v0.4.2 | MIT |  | LICENSE | Copyright (c) 2018 labstack | desktop (graph only) (not incorporated) | ok |
| go | github.com/leaanthony/clir | v1.3.0 | MIT |  | LICENSE | Copyright (c) 2019 Lea Anthony | desktop (graph only) (not incorporated) | ok |
| go | github.com/leaanthony/debme | v1.2.1 | MIT |  | LICENSE | Copyright (c) 2021 Lea Anthony | desktop (graph only) (not incorporated) | ok |
| go | github.com/leaanthony/go-ansi-parser | v1.6.1 | MIT |  | LICENSE | Copyright (c) 2021-Present Lea Anthony | desktop | ok |
| go | github.com/leaanthony/gosod | v1.0.4 | MIT |  | LICENSE | Copyright (c) 2019 Lea Anthony | desktop | ok |
| go | github.com/leaanthony/slicer | v1.6.0 | MIT |  | LICENSE | Copyright (c) 2019 Lea Anthony | desktop | ok |
| go | github.com/leaanthony/u | v1.1.1 | MIT |  | LICENSE | Copyright (c) 2023-Present Lea Anthony | desktop | ok |
| go | github.com/leaanthony/winicon | v1.0.0 | MIT |  | LICENSE | Copyright (c) 2020 Lea Anthony | desktop (graph only) (not incorporated) | ok |
| go | github.com/lithammer/fuzzysearch | v1.1.8 | MIT |  | LICENSE | Copyright (c) 2018 Peter Lithammer | desktop (graph only) (not incorporated) | ok |
| go | github.com/lucasb-eyer/go-colorful | v1.2.0 | MIT |  | LICENSE | Copyright (c) 2013 Lucas Beyer | desktop (graph only) (not incorporated) | ok |
| go | github.com/Masterminds/semver | v1.5.0 | MIT |  | LICENSE.txt | Copyright (C) 2014-2019, Matt Butcher and Matt Farina | desktop (graph only) (not incorporated) | ok |
| go | github.com/matryer/is | v1.4.1 | MIT |  | LICENSE | Copyright (c) 2017-2018 Mat Ryer | desktop (graph only) (not incorporated) | ok |
| go | github.com/mattn/go-colorable | v0.1.13 | MIT |  | LICENSE | Copyright (c) 2016 Yasuhiro Matsumoto | desktop (graph only) (not incorporated) | ok |
| go | github.com/mattn/go-isatty | v0.0.24 | MIT |  | LICENSE | Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com> | desktop | ok |
| go | github.com/mattn/go-runewidth | v0.0.16 | MIT |  | LICENSE | Copyright (c) 2016 Yasuhiro Matsumoto | desktop (graph only) (not incorporated) | ok |
| go | github.com/microcosm-cc/bluemonday | v1.0.27 | BSD-3-Clause |  | LICENSE.md | Copyright (c) 2014, David Kitchen <david@buro9.com> | desktop (graph only) (not incorporated) | ok |
| go | github.com/Microsoft/go-winio | v0.6.2 | MIT |  | LICENSE | Copyright (c) 2015 Microsoft | desktop (graph only) (not incorporated) | ok |
| go | github.com/muesli/reflow | v0.3.0 | MIT |  | LICENSE | Copyright (c) 2019 Christian Muehlhaeuser | desktop (graph only) (not incorporated) | ok |
| go | github.com/muesli/termenv | v0.15.3-0.20240618155329-98d742f6907a | MIT |  | LICENSE | Copyright (c) 2019 Christian Muehlhaeuser | desktop (graph only) (not incorporated) | ok |
| go | github.com/ncruces/go-strftime | v1.0.0 | MIT |  | LICENSE | Copyright (c) 2022 Nuno Cruces | desktop | ok |
| go | github.com/nfnt/resize | v0.0.0-20180221191011-83c6a9932646 | ISC |  | LICENSE | Copyright (c) 2012, Jan Schlicht <jan.schlicht@gmail.com> | desktop (graph only) (not incorporated) | ok |
| go | github.com/niemeyer/pretty | v0.0.0-20200227124842-a10e7caefd8e | MIT |  | License | Copyright 2012 Keith Rarick | desktop (graph only) (not incorporated) | ok |
| go | github.com/pjbgf/sha1cd | v0.6.0 | Apache-2.0 |  | LICENSE | Copyright 2023 pjbgf | desktop (graph only) (not incorporated) | ok |
| go | github.com/pkg/browser | v0.0.0-20240102092130-5ac0b6a4141c | BSD-2-Clause |  | LICENSE | Copyright (c) 2014, Dave Cheney <dave@cheney.net> | desktop | ok |
| go | github.com/pkg/errors | v0.9.1 | BSD-2-Clause |  | LICENSE | Copyright (c) 2015, Dave Cheney <dave@cheney.net> | desktop | ok |
| go | github.com/pmezard/go-difflib | v1.0.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2013, Patrick Mezard | desktop (graph only) (not incorporated) | ok |
| go | github.com/ProtonMail/go-crypto | v1.1.6 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright (c) 2009 The Go Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/pterm/pterm | v0.12.80 | MIT |  | LICENSE | Copyright (c) 2020 pterm | desktop (graph only) (not incorporated) | ok |
| go | github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |  | LICENSE | Copyright (c) 2012 The Go Authors. All rights reserved. | desktop | ok |
| go | github.com/rivo/uniseg | v0.4.7 | MIT |  | LICENSE.txt | Copyright (c) 2019 Oliver Kuederle | desktop | ok |
| go | github.com/rjeczalik/notify | v0.9.3 | MIT |  | LICENSE | Copyright (c) 2014-2015 The Notify Authors | desktop | ok |
| go | github.com/sabhiram/go-gitignore | v0.0.0-20210923224102-525f6e181f06 | MIT |  | LICENSE | Copyright (c) 2015 Shaba Abhiram | desktop (graph only) (not incorporated) | ok |
| go | github.com/samber/lo | v1.49.1 | MIT |  | LICENSE | Copyright (c) 2022-2025 Samuel Berthe | desktop | ok |
| go | github.com/sergi/go-diff | v1.3.2-0.20230802210424-5b0b94c5c0d3 | MIT |  | LICENSE | Copyright (c) 2012-2016 The go-diff Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | github.com/skeema/knownhosts | v1.3.1 | Apache-2.0 |  | LICENSE, NOTICE |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/stretchr/objx | v0.5.3 | MIT |  | LICENSE | Copyright (c) 2014 Stretchr, Inc. | desktop (graph only) (not incorporated) | ok |
| go | github.com/stretchr/testify | v1.12.1 | MIT |  | LICENSE | Copyright (c) 2012-2020 Mat Ryer, Tyler Bunnell and contributors. | desktop (graph only) (not incorporated) | ok |
| go | github.com/tc-hib/winres | v0.3.1 | 0BSD |  | LICENSE | Copyright (c) 2021 Thomas Combeléran | desktop (graph only) (not incorporated) | ok |
| go | github.com/tidwall/gjson | v1.14.2 | MIT |  | LICENSE | Copyright (c) 2016 Josh Baker | desktop (graph only) (not incorporated) | ok |
| go | github.com/tidwall/match | v1.1.1 | MIT |  | LICENSE | Copyright (c) 2016 Josh Baker | desktop (graph only) (not incorporated) | ok |
| go | github.com/tidwall/pretty | v1.2.0 | MIT |  | LICENSE | Copyright (c) 2017 Josh Baker | desktop (graph only) (not incorporated) | ok |
| go | github.com/tidwall/sjson | v1.2.5 | MIT |  | LICENSE | Copyright (c) 2016 Josh Baker | desktop (graph only) (not incorporated) | ok |
| go | github.com/tkrajina/go-reflector | v0.5.8 | Apache-2.0 |  | LICENSE.txt |  | desktop | ok |
| go | github.com/valyala/bytebufferpool | v1.0.0 | MIT |  | LICENSE | Copyright (c) 2016 Aliaksandr Valialkin, VertaMedia | desktop (graph only) (not incorporated) | ok |
| go | github.com/valyala/fasttemplate | v1.2.2 | MIT |  | LICENSE | Copyright (c) 2015 Aliaksandr Valialkin | desktop (graph only) (not incorporated) | ok |
| go | github.com/wailsapp/go-webview2 | v1.0.22 | MIT (webviewloader: ISC) |  | LICENSE, webviewloader/LICENSE | Copyright (c) 2020 John Chadwick | desktop | REVIEW: webviewloader can go:embed Microsoft's WebView2Loader.dll, but only with the build tag native_webview2loader, which VaultDock builds do not set (the pure-Go loader port, ISC, is used instead). Keep that tag off; if it is ever enabled, add Microsoft's WebView2 SDK licence notice. |
| go | github.com/wailsapp/mimetype | v1.4.1 | MIT |  | LICENSE | Copyright (c) 2018-2020 Gabriel Vasile | desktop | ok |
| go | github.com/wailsapp/wails/v2 | v2.16.0 | MIT (bundled subcomponents: MIT, Apache-2.0) |  | LICENSE, internal/frontend/desktop/windows/winc/LICENSE, internal/go-common-file-dialog/LICENSE, internal/typescriptify/LICENSE.txt | Copyright (c) 2018-Present Lea Anthony | desktop | ok |
| go | github.com/wzshiming/ctc | v1.2.3 | MIT |  | LICENSE | Copyright (c) 2017-NOW  wzshiming <wzshiming@foxmail.com> | desktop (graph only) (not incorporated) | ok |
| go | github.com/wzshiming/winseq | v0.0.0-20200112104235-db357dc107ae | MIT |  | LICENSE | Copyright (c) 2018 wzshiming | desktop (graph only) (not incorporated) | ok |
| go | github.com/xanzy/ssh-agent | v0.3.3 | Apache-2.0 |  | LICENSE |  | desktop (graph only) (not incorporated) | ok |
| go | github.com/xo/terminfo | v0.0.0-20220910002029-abceb7e1c41e | MIT |  | LICENSE | Copyright (c) 2016 Anmol Sethi | desktop (graph only) (not incorporated) | ok |
| go | github.com/yuin/goldmark | v1.7.4 | MIT |  | LICENSE | Copyright (c) 2019 Yusuke Inuzuka | desktop (graph only) (not incorporated) | ok |
| go | github.com/yuin/goldmark-emoji | v1.0.3 | MIT |  | LICENSE | Copyright (c) 2020 Yusuke Inuzuka | desktop (graph only) (not incorporated) | ok |
| go | github.com/yusufpapurcu/wmi | v1.2.4 | MIT |  | LICENSE | Copyright (c) 2013 Stack Exchange | desktop (graph only) (not incorporated) | ok |
| go | Go standard library and runtime | (build toolchain) | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | sdk, desktop | ok |
| go | go.yaml.in/yaml/v3 | v3.0.5 | Apache-2.0 AND MIT |  | LICENSE, NOTICE | copyright staring in 2011 when the project was ported over: | desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/crypto | v0.58.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | sdk (graph only), desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/image | v0.41.0 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/mod | v0.41.0 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/net | v0.60.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop | ok |
| go | golang.org/x/sync | v0.24.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/sys | v0.49.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop | ok |
| go | golang.org/x/term | v0.47.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | sdk (graph only), desktop (graph only) (not incorporated) | ok |
| go | golang.org/x/text | v0.43.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop | ok |
| go | golang.org/x/time | v0.16.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop | ok |
| go | golang.org/x/tools | v0.51.0 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | desktop (graph only) (not incorporated) | ok |
| go | gopkg.in/check.v1 | v1.0.0-20200227125254-8fa46927fb4f | BSD-2-Clause |  | LICENSE | Copyright (c) 2010-2013 Gustavo Niemeyer <gustavo@niemeyer.net> | desktop (graph only) (not incorporated) | ok |
| go | gopkg.in/warnings.v0 | v0.1.2 | BSD-2-Clause |  | LICENSE | Copyright (c) 2016 Péter Surányi. | desktop (graph only) (not incorporated) | ok |
| go | gopkg.in/yaml.v3 | v3.0.1 | Apache-2.0 AND MIT |  | LICENSE, NOTICE | copyright staring in 2011 when the project was ported over: | desktop (graph only) (not incorporated) | ok |
| go | howett.net/plist | v1.0.2-0.20250314012144-ee69052608d9 | BSD-3-Clause |  | LICENSE | Copyright (c) 2013, Dustin L. Howett. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/cc/v4 | v4.29.7 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The CC Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/ccgo/v4 | v4.36.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2017 The CCGO Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/fileutil | v1.4.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The fileutil Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/gc/v2 | v2.6.5 | BSD-3-Clause |  | LICENSE | Copyright (c) 2016 The GC Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/gc/v3 | v3.1.5 | BSD-3-Clause |  | LICENSE | Copyright (c) 2016 The GC Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/goabi0 | v0.2.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2025 The goabi0 Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/libc | v1.77.1 | BSD-3-Clause (bundled: Go BSD-3-Clause, musl MIT, go-netdb MIT, nixpkgs MIT) (bundled notices: MIT, BSD-3-Clause) |  | LICENSE, LICENSE-3RD-PARTY.md | Copyright (c) 2017 The Libc Authors. All rights reserved. | desktop | ok |
| go | modernc.org/mathutil | v1.7.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The mathutil Authors. All rights reserved. | desktop | ok |
| go | modernc.org/memory | v1.12.1 | BSD-3-Clause |  | LICENSE, LICENSE-GO, LICENSE-LOGO, LICENSE-MMAP-GO | Copyright (c) 2017 The Memory Authors. All rights reserved. | desktop | ok |
| go | modernc.org/opt | v0.2.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2019 The Opt Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/sortutil | v1.2.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The sortutil Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/sqlite | v1.60.1 | BSD-3-Clause (embedded SQLite: public domain) |  | LICENSE, LICENSE-SQLITE | Copyright (c) 2017 The Sqlite Authors. All rights reserved. | desktop | REVIEW: LICENSE-SQLITE_VEC and LICENSE-3RD-PARTY.md excluded: sqlite-vec and the vfs bridge are only linked when modernc.org/sqlite/vec or /vfs is imported (not imported); the remaining linked components of its LICENSE-3RD-PARTY.md are listed here as their own entries. |
| go | modernc.org/strutil | v1.2.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The strutil Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | modernc.org/token | v1.1.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2009 The Go Authors. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| go | mvdan.cc/sh/v3 | v3.7.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2016, Daniel Martí. All rights reserved. | desktop (graph only) (not incorporated) | ok |
| npm | @jridgewell/gen-mapping | 0.3.13 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | desktop/frontend (dev) (not incorporated) | ok |
| npm | @jridgewell/remapping | 2.3.5 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | desktop/frontend (dev) (not incorporated) | ok |
| npm | @jridgewell/resolve-uri | 3.1.2 | MIT | MIT | LICENSE | Copyright 2019 Justin Ridgewell <jridgewell@google.com> | desktop/frontend (dev) (not incorporated) | ok |
| npm | @jridgewell/sourcemap-codec | 1.6.0 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | desktop/frontend (dev) (not incorporated) | ok |
| npm | @jridgewell/trace-mapping | 0.3.31 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | desktop/frontend (dev) (not incorporated) | ok |
| npm | @oxc-project/types | 0.154.0 | MIT | MIT | LICENSE | Copyright (c) 2024-present VoidZero Inc. & Contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | @rolldown/binding-win32-x64-msvc | 1.2.14 | MIT | MIT |  |  | desktop/frontend (dev) (not incorporated) | ok |
| npm | @rolldown/pluginutils | 1.0.1 | MIT | MIT | LICENSE | Copyright (c) 2026-present, rolldown/plugins repository contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | @tailwindcss/node | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | desktop/frontend (dev) (not incorporated) | ok |
| npm | @tailwindcss/oxide | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | desktop/frontend (dev) (not incorporated) | ok |
| npm | @tailwindcss/oxide-win32-x64-msvc | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | desktop/frontend (dev) (not incorporated) | ok |
| npm | @tailwindcss/vite | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | desktop/frontend (dev) (not incorporated) | ok |
| npm | @types/react | 19.3.0 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | desktop/frontend (not incorporated) | ok |
| npm | @types/react-dom | 19.3.0 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | desktop/frontend (dev) (not incorporated) | ok |
| npm | @typescript/typescript-win32-x64 | 7.0.2 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE.txt |  | desktop/frontend (dev) (not incorporated) | ok |
| npm | @vitejs/plugin-react | 6.1.2 | MIT | MIT | LICENSE | Copyright (c) 2019-present, Yuxi (Evan) You and Vite contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | csstype | 3.2.3 | MIT | MIT | LICENSE | Copyright (c) 2017-2018 Fredrik Nicol | desktop/frontend (not incorporated) | ok |
| npm | detect-libc | 2.1.2 | Apache-2.0 | Apache-2.0 | LICENSE |  | desktop/frontend (dev) (not incorporated) | ok |
| npm | enhanced-resolve | 5.26.0 | MIT | MIT | LICENSE | Copyright JS Foundation and other contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | fdir | 6.5.0 | MIT | MIT | LICENSE | Copyright 2023 Abdullah Atta | desktop/frontend (dev) (not incorporated) | ok |
| npm | graceful-fs | 4.2.11 | ISC | ISC | LICENSE | Copyright (c) 2011-2022 Isaac Z. Schlueter, Ben Noordhuis, and Contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | jiti | 2.7.0 | MIT | MIT | LICENSE | Copyright (c) Pooya Parsa <pooya@pi0.io> | desktop/frontend (dev) (not incorporated) | ok |
| npm | lightningcss | 1.32.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | desktop/frontend (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss | 1.33.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | desktop/frontend (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss-win32-x64-msvc | 1.32.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | desktop/frontend (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss-win32-x64-msvc | 1.33.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | desktop/frontend (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lucide-react | 1.57.0 | ISC (Feather-derived icons: MIT) | ISC | LICENSE | Copyright (c) 2026 Lucide Icons and Contributors | desktop/frontend | ok |
| npm | magic-string | 0.30.21 | MIT | MIT | LICENSE | Copyright 2018 Rich Harris | desktop/frontend (dev) (not incorporated) | ok |
| npm | nanoid | 3.3.20 | MIT | MIT | LICENSE | Copyright 2017 Andrey Sitnik <andrey@sitnik.ru> | desktop/frontend (dev) (not incorporated) | ok |
| npm | picocolors | 1.1.1 | ISC | ISC | LICENSE | Copyright (c) 2021-2024 Oleksii Raspopov, Kostiantyn Denysov, Anton Verinov | desktop/frontend (dev) (not incorporated) | ok |
| npm | picomatch | 4.0.7 | MIT | MIT | LICENSE | Copyright (c) 2017-present, Jon Schlinkert. | desktop/frontend (dev) (not incorporated) | ok |
| npm | postcss | 8.5.30 | MIT | MIT | LICENSE | Copyright 2013 Andrey Sitnik <andrey@sitnik.es> | desktop/frontend (dev) (not incorporated) | ok |
| npm | react | 19.3.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | desktop/frontend | ok |
| npm | react-dom | 19.3.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | desktop/frontend | ok |
| npm | rolldown | 1.2.14 | MIT | MIT | LICENSE | Copyright (c) 2024-present VoidZero Inc. & Contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | scheduler | 0.28.0 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | desktop/frontend | ok |
| npm | source-map-js | 1.2.2 | BSD-3-Clause | BSD-3-Clause | LICENSE | Copyright (c) 2009-2011, Mozilla Foundation and contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | tailwindcss | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | desktop/frontend (build, output bundled) | ok |
| npm | tapable | 2.3.3 | MIT | MIT | LICENSE | Copyright JS Foundation and other contributors | desktop/frontend (dev) (not incorporated) | ok |
| npm | tinyglobby | 0.2.17 | MIT | MIT | LICENSE | Copyright (c) 2024 Madeline Gurriarán | desktop/frontend (dev) (not incorporated) | ok |
| npm | typescript | 7.0.2 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE.txt |  | desktop/frontend (dev) (not incorporated) | ok |
| npm | vite | 8.3.4 | Apache-2.0 AND MIT AND BSD-2-Clause AND ISC | MIT | LICENSE.md | Copyright (c) 2019-present, VoidZero Inc. and Vite contributors | desktop/frontend (dev) (not incorporated) | ok |

## Full audit table: server (vad-server + web; --all)

| Eco | Package | Version | Licence (from file text) | Declared | Files read | Copyright | Incorporated in | Status |
|---|---|---|---|---|---|---|---|---|
| go | github.com/dustin/go-humanize | v1.0.1 | MIT |  | LICENSE | Copyright (c) 2005-2008  Dustin Sallings <dustin@spy.net> | . | ok |
| go | github.com/go-chi/chi/v5 | v5.5.0 | MIT |  | LICENSE | Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc. | . | ok |
| go | github.com/google/pprof | v0.0.0-20260802141513-ef3492d7dac3 | Apache-2.0 |  | LICENSE |  | . (graph only) (not incorporated) | ok |
| go | github.com/google/uuid | v1.6.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2009,2014 Google Inc. All rights reserved. | . | ok |
| go | github.com/hashicorp/golang-lru/v2 | v2.0.7 | MPL-2.0 |  | LICENSE | Copyright (c) 2014 HashiCorp, Inc. | . (graph only) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| go | github.com/klauspost/compress | v1.20.3 | BSD-3-Clause (gzhttp: Apache-2.0; s2/snappy parts: BSD-3-Clause, MIT) |  | LICENSE | Copyright (c) 2012 The Go Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | github.com/mattn/go-isatty | v0.0.24 | MIT |  | LICENSE | Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com> | . | ok |
| go | github.com/ncruces/go-strftime | v1.0.0 | MIT |  | LICENSE | Copyright (c) 2022 Nuno Cruces | . | ok |
| go | github.com/oschwald/maxminddb-golang/v2 | v2.8.0 | ISC |  | LICENSE | Copyright (c) 2015, Gregory J. Oschwald <oschwald@gmail.com> | . | ok |
| go | github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |  | LICENSE | Copyright (c) 2012 The Go Authors. All rights reserved. | . | ok |
| go | github.com/stretchr/testify | v1.12.1 | MIT |  | LICENSE | Copyright (c) 2012-2020 Mat Ryer, Tyler Bunnell and contributors. | . (graph only) (not incorporated) | ok |
| go | Go standard library and runtime | (build toolchain) | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . | ok |
| go | go.yaml.in/yaml/v3 | v3.0.5 | Apache-2.0 AND MIT |  | LICENSE, NOTICE | copyright staring in 2011 when the project was ported over: | . (graph only) (not incorporated) | ok |
| go | golang.org/x/crypto | v0.58.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . | ok |
| go | golang.org/x/mod | v0.41.0 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | golang.org/x/net | v0.60.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | golang.org/x/sync | v0.23.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | golang.org/x/sys | v0.49.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . | ok |
| go | golang.org/x/term | v0.47.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | golang.org/x/text | v0.43.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | golang.org/x/time | v0.16.0 | BSD-3-Clause (+ Go PATENTS grant) |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . | ok |
| go | golang.org/x/tools | v0.51.0 | BSD-3-Clause |  | LICENSE, PATENTS | Copyright 2009 The Go Authors. | . (graph only) (not incorporated) | ok |
| go | modernc.org/cc/v4 | v4.29.7 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The CC Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/ccgo/v4 | v4.36.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2017 The CCGO Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/fileutil | v1.4.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The fileutil Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/gc/v2 | v2.6.5 | BSD-3-Clause |  | LICENSE | Copyright (c) 2016 The GC Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/gc/v3 | v3.1.5 | BSD-3-Clause |  | LICENSE | Copyright (c) 2016 The GC Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/goabi0 | v0.2.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2025 The goabi0 Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/libc | v1.77.1 | BSD-3-Clause (bundled: Go BSD-3-Clause, musl MIT, go-netdb MIT, nixpkgs MIT) (bundled notices: MIT, BSD-3-Clause) |  | LICENSE, LICENSE-3RD-PARTY.md | Copyright (c) 2017 The Libc Authors. All rights reserved. | . | ok |
| go | modernc.org/mathutil | v1.7.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The mathutil Authors. All rights reserved. | . | ok |
| go | modernc.org/memory | v1.12.1 | BSD-3-Clause |  | LICENSE, LICENSE-GO, LICENSE-LOGO, LICENSE-MMAP-GO | Copyright (c) 2017 The Memory Authors. All rights reserved. | . | ok |
| go | modernc.org/opt | v0.2.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2019 The Opt Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/sortutil | v1.2.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The sortutil Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/sqlite | v1.60.1 | BSD-3-Clause (embedded SQLite: public domain) |  | LICENSE, LICENSE-SQLITE | Copyright (c) 2017 The Sqlite Authors. All rights reserved. | . | REVIEW: LICENSE-SQLITE_VEC and LICENSE-3RD-PARTY.md excluded: sqlite-vec and the vfs bridge are only linked when modernc.org/sqlite/vec or /vfs is imported (not imported); the remaining linked components of its LICENSE-3RD-PARTY.md are listed here as their own entries. |
| go | modernc.org/strutil | v1.2.1 | BSD-3-Clause |  | LICENSE | Copyright (c) 2014 The strutil Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| go | modernc.org/token | v1.1.0 | BSD-3-Clause |  | LICENSE | Copyright (c) 2009 The Go Authors. All rights reserved. | . (graph only) (not incorporated) | ok |
| npm | @adobe/css-tools | 4.5.0 | MIT | MIT | LICENSE | Copyright (c) 2012 TJ Holowaychuk <tj@vision-media.ca> | web (dev) (not incorporated) | ok |
| npm | @asamuzakjp/css-color | 7.1.7 | MIT | MIT | LICENSE | Copyright (c) 2024 asamuzaK (Kazz) | web (dev) (not incorporated) | ok |
| npm | @asamuzakjp/dom-selector | 10.0.4 | MIT | MIT | LICENSE | Copyright (c) 2023 asamuzaK (Kazz) | web (dev) (not incorporated) | ok |
| npm | @babel/code-frame | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/compat-data | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/core | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/generator | 7.29.8 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-compilation-targets | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-globals | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-module-imports | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-module-transforms | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-string-parser | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-validator-identifier | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helper-validator-option | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/helpers | 7.29.10 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/parser | 7.29.9 | MIT | MIT | LICENSE | Copyright (C) 2012-2014 by various contributors (see AUTHORS) | web (dev) (not incorporated) | ok |
| npm | @babel/runtime | 7.29.10 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/template | 7.29.7 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/traverse | 7.29.10 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @babel/types | 7.29.8 | MIT | MIT | LICENSE | Copyright (c) 2014-present Sebastian McKenzie and other contributors | web (dev) (not incorporated) | ok |
| npm | @bramus/specificity | 2.4.3 | MIT | MIT | LICENSE | Copyright (c) 2022 Bramus Van Damme - https://www.bram.us/ | web (dev) (not incorporated) | ok |
| npm | @cacheable/memory | 2.2.0 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @cacheable/utils | 2.5.0 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @csstools/color-helpers | 6.1.2 | MIT | MIT-0 | LICENSE.md | Copyright © CSSTools Contributors | web (dev) (not incorporated) | ok |
| npm | @csstools/css-calc | 3.4.3 | MIT | MIT | LICENSE.md | Copyright 2022 Romain Menke, Antonio Laguna <antonio@laguna.es> | web (dev) (not incorporated) | ok |
| npm | @csstools/css-color-parser | 4.2.6 | MIT | MIT | LICENSE.md | Copyright 2022 Romain Menke, Antonio Laguna <antonio@laguna.es> | web (dev) (not incorporated) | ok |
| npm | @csstools/css-parser-algorithms | 4.0.2 | MIT | MIT | LICENSE.md | Copyright 2022 Romain Menke, Antonio Laguna <antonio@laguna.es> | web (dev) (not incorporated) | ok |
| npm | @csstools/css-syntax-patches-for-csstree | 1.1.15 | MIT | MIT-0 | LICENSE.md | Copyright © CSSTools Contributors | web (dev) (not incorporated) | ok |
| npm | @csstools/css-tokenizer | 4.0.2 | MIT | MIT | LICENSE.md | Copyright 2022 Romain Menke, Antonio Laguna <antonio@laguna.es> | web (dev) (not incorporated) | ok |
| npm | @eslint-community/eslint-utils | 4.10.1 | MIT | MIT | LICENSE | Copyright (c) 2018 Toru Nagashima | web (dev) (not incorporated) | ok |
| npm | @eslint-community/regexpp | 4.12.2 | MIT | MIT | LICENSE | Copyright (c) 2018 Toru Nagashima | web (dev) (not incorporated) | ok |
| npm | @eslint/config-array | 0.23.5 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @eslint/config-helpers | 0.7.0 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @eslint/core | 1.2.1 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @eslint/js | 10.0.1 | MIT | MIT | LICENSE | Copyright OpenJS Foundation and other contributors, <www.openjsf.org> | web (dev) (not incorporated) | ok |
| npm | @eslint/object-schema | 3.0.5 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @eslint/plugin-kit | 0.7.3 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @exodus/bytes | 1.16.0 | MIT | MIT | LICENSE | Copyright (c) 2024-2025 Exodus Movement | web (dev) (not incorporated) | ok |
| npm | @humanfs/core | 0.19.2 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @humanfs/node | 0.16.8 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @humanfs/types | 0.15.0 | Apache-2.0 | Apache-2.0 |  |  | web (dev) (not incorporated) | ok |
| npm | @humanwhocodes/module-importer | 1.0.1 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @humanwhocodes/retry | 0.4.3 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | @jridgewell/gen-mapping | 0.3.13 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | web (dev) (not incorporated) | ok |
| npm | @jridgewell/remapping | 2.3.5 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | web (dev) (not incorporated) | ok |
| npm | @jridgewell/resolve-uri | 3.1.2 | MIT | MIT | LICENSE | Copyright 2019 Justin Ridgewell <jridgewell@google.com> | web (dev) (not incorporated) | ok |
| npm | @jridgewell/sourcemap-codec | 1.6.0 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | web (dev) (not incorporated) | ok |
| npm | @jridgewell/trace-mapping | 0.3.31 | MIT | MIT | LICENSE | Copyright 2024 Justin Ridgewell <justin@ridgewell.name> | web (dev) (not incorporated) | ok |
| npm | @keyv/bigmap | 1.3.1 | MIT | MIT | LICENSE | Copyright (c) Jared Wray | web (dev) (not incorporated) | ok |
| npm | @keyv/serialize | 1.1.1 | MIT | MIT | LICENSE | Copyright (c) 2017-2021 Luke Childs | web (dev) (not incorporated) | ok |
| npm | @oxc-project/types | 0.154.0 | MIT | MIT | LICENSE | Copyright (c) 2024-present VoidZero Inc. & Contributors | web (dev) (not incorporated) | ok |
| npm | @playwright/test | 1.64.1 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE |  | web (dev) (not incorporated) | ok |
| npm | @rolldown/binding-win32-x64-msvc | 1.2.14 | MIT | MIT |  |  | web (dev) (not incorporated) | ok |
| npm | @rolldown/pluginutils | 1.0.1 | MIT | MIT | LICENSE | Copyright (c) 2026-present, rolldown/plugins repository contributors | web (dev) (not incorporated) | ok |
| npm | @tailwindcss/node | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | web (dev) (not incorporated) | ok |
| npm | @tailwindcss/oxide | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | web (dev) (not incorporated) | ok |
| npm | @tailwindcss/oxide-win32-x64-msvc | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | web (dev) (not incorporated) | ok |
| npm | @tailwindcss/vite | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | web (dev) (not incorporated) | ok |
| npm | @tanstack/query-core | 5.104.3 | MIT | MIT | LICENSE | Copyright (c) 2021-present Tanner Linsley | web | ok |
| npm | @tanstack/react-query | 5.104.3 | MIT | MIT | LICENSE | Copyright (c) 2021-present Tanner Linsley | web | ok |
| npm | @testing-library/dom | 10.4.2 | MIT | MIT | LICENSE | Copyright (c) 2017 Kent C. Dodds | web (dev) (not incorporated) | ok |
| npm | @testing-library/jest-dom | 7.0.1 | MIT | MIT | LICENSE | Copyright (c) 2017 Kent C. Dodds | web (dev) (not incorporated) | ok |
| npm | @testing-library/react | 16.3.3 | MIT | MIT | LICENSE | Copyright (c) 2017-Present Kent C. Dodds | web (dev) (not incorporated) | ok |
| npm | @types/aria-query | 5.0.4 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/chai | 5.2.3 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/d3-color | 3.1.3 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/d3-drag | 3.0.7 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/d3-interpolate | 3.0.4 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/d3-selection | 3.0.12 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/d3-transition | 3.0.9 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/d3-zoom | 3.0.9 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/deep-eql | 4.0.2 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/esrecurse | 4.3.1 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/estree | 1.0.9 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/json-schema | 7.0.15 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (dev) (not incorporated) | ok |
| npm | @types/react | 19.3.0 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @types/react-dom | 19.3.0 | MIT | MIT | LICENSE | Copyright (c) Microsoft Corporation. | web (not incorporated) | ok |
| npm | @typescript-eslint/eslint-plugin | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/parser | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/project-service | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2025 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/scope-manager | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/tsconfig-utils | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2025 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/type-utils | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2021 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/types | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/typescript-estree | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/utils | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript-eslint/visitor-keys | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | @typescript/typescript-win32-x64 | 7.0.2 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE.txt |  | web (dev) (not incorporated) | ok |
| npm | @vitejs/plugin-react | 6.1.2 | MIT | MIT | LICENSE | Copyright (c) 2019-present, Yuxi (Evan) You and Vite contributors | web (dev) (not incorporated) | ok |
| npm | @vitest/mocker | 5.0.5 | MIT | MIT | LICENSE | Copyright (c) 2021-Present VoidZero Inc. and Vitest contributors | web (dev) (not incorporated) | ok |
| npm | @vitest/spy | 5.0.5 | MIT | MIT | LICENSE | Copyright (c) 2021-Present VoidZero Inc. and Vitest contributors | web (dev) (not incorporated) | ok |
| npm | @xyflow/react | 12.12.0 | MIT | MIT | LICENSE | Copyright (c) 2019-2025 webkid GmbH | web | ok |
| npm | @xyflow/system | 0.0.83 | MIT | MIT | LICENSE | Copyright (c) 2019-2025 webkid GmbH | web | ok |
| npm | acorn | 8.19.0 | MIT | MIT | LICENSE | Copyright (C) 2012-2022 by various contributors (see AUTHORS) | web (dev) (not incorporated) | ok |
| npm | acorn-jsx | 5.3.2 | MIT | MIT | LICENSE | Copyright (C) 2012-2017 by Ingvar Stepanyan | web (dev) (not incorporated) | ok |
| npm | ajv | 6.15.0 | MIT | MIT | LICENSE | Copyright (c) 2015-2017 Evgeny Poberezkin | web (dev) (not incorporated) | ok |
| npm | ansi-regex | 5.0.1 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | ansi-styles | 5.2.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | aria-query | 5.3.0 | Apache-2.0 | Apache-2.0 | LICENSE | Copyright 2020 A11yance | web (dev) (not incorporated) | ok |
| npm | assertion-error | 2.0.1 | MIT | MIT | LICENSE | Copyright (c) 2013 Jake Luer jake@qualiancy.com (http://qualiancy.com) | web (dev) (not incorporated) | ok |
| npm | balanced-match | 4.0.4 | MIT | MIT | LICENSE.md |  | web (dev) (not incorporated) | ok |
| npm | baseline-browser-mapping | 2.11.29 | Apache-2.0 | Apache-2.0 | LICENSE.txt |  | web (dev) (not incorporated) | ok |
| npm | bidi-js | 1.1.0 | MIT | MIT | LICENSE.txt | Copyright (c) 2021 Jason Johnston | web (dev) (not incorporated) | ok |
| npm | brace-expansion | 5.0.12 | MIT | MIT | LICENSE | Copyright Julian Gruber <julian@juliangruber.com> | web (dev) (not incorporated) | ok |
| npm | browserslist | 4.29.3 | MIT | MIT | LICENSE | Copyright 2014 Andrey Sitnik <andrey@sitnik.es> and other contributors | web (dev) (not incorporated) | ok |
| npm | cacheable | 2.5.0 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | caniuse-lite | 1.0.30001816 | CC-BY-4.0 | CC-BY-4.0 | LICENSE |  | web (dev) (not incorporated) | FLAG: not allowed: CC-BY-4.0 |
| npm | chai | 6.3.0 | MIT | MIT | LICENSE | Copyright (c) 2017 Chai.js Assertion Library | web (dev) (not incorporated) | ok |
| npm | classcat | 5.0.5 | MIT | MIT | LICENSE.md | Copyright © Jorge Bucaran <<https://jorgebucaran.com>> | web | ok |
| npm | convert-source-map | 2.0.0 | MIT | MIT | LICENSE | Copyright 2013 Thorsten Lorenz. | web (dev) (not incorporated) | ok |
| npm | cookie | 1.1.1 | MIT | MIT | LICENSE | Copyright (c) 2012-2014 Roman Shtylman <shtylman@gmail.com> | web | ok |
| npm | cross-spawn | 7.0.6 | MIT | MIT | LICENSE | Copyright (c) 2018 Made With MOXY Lda <hello@moxy.studio> | web (dev) (not incorporated) | ok |
| npm | css-tree | 3.2.1 | MIT | MIT | LICENSE | Copyright (C) 2016-2026 by Roman Dvornov | web (dev) (not incorporated) | ok |
| npm | css.escape | 1.5.1 | MIT | MIT | LICENSE-MIT.txt | Copyright Mathias Bynens <https://mathiasbynens.be/> | web (dev) (not incorporated) | ok |
| npm | csstype | 3.2.3 | MIT | MIT | LICENSE | Copyright (c) 2017-2018 Fredrik Nicol | web (not incorporated) | ok |
| npm | d3-color | 3.1.0 | ISC | ISC | LICENSE | Copyright 2010-2022 Mike Bostock | web | ok |
| npm | d3-dispatch | 3.0.1 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-drag | 3.0.0 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-ease | 3.0.1 | BSD-3-Clause | BSD-3-Clause | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-interpolate | 3.0.1 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-selection | 3.0.0 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-timer | 3.0.1 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-transition | 3.0.1 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | d3-zoom | 3.0.0 | ISC | ISC | LICENSE | Copyright 2010-2021 Mike Bostock | web | ok |
| npm | data-urls | 8.0.0 | MIT | MIT | LICENSE.txt | Copyright © Domenic Denicola <d@domenic.me> | web (dev) (not incorporated) | ok |
| npm | debug | 4.4.3 | MIT | MIT | LICENSE | Copyright (c) 2014-2017 TJ Holowaychuk <tj@vision-media.ca> | web (dev) (not incorporated) | ok |
| npm | decimal.js | 10.6.0 | MIT | MIT | LICENCE.md | Copyright (c) 2025 Michael Mclaughlin | web (dev) (not incorporated) | ok |
| npm | deep-is | 0.1.4 | MIT | MIT | LICENSE | Copyright (c) 2012, 2013 Thorsten Lorenz <thlorenz@gmx.de> | web (dev) (not incorporated) | ok |
| npm | dequal | 2.0.3 | MIT | MIT | license | Copyright (c) Luke Edwards <luke.edwards05@gmail.com> (lukeed.com) | web (dev) (not incorporated) | ok |
| npm | detect-libc | 2.1.2 | Apache-2.0 | Apache-2.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | dom-accessibility-api | 0.6.3 | MIT | MIT | LICENSE.md | Copyright (c) 2020 Sebastian Silbermann | web (dev) (not incorporated) | ok |
| npm | dom-accessibility-api | 0.5.16 | MIT | MIT | LICENSE.md | Copyright (c) 2020 Sebastian Silbermann | web (dev) (not incorporated) | ok |
| npm | electron-to-chromium | 1.5.459 | ISC | ISC | LICENSE | Copyright 2018 Kilian Valkhof | web (dev) (not incorporated) | ok |
| npm | enhanced-resolve | 5.26.0 | MIT | MIT | LICENSE | Copyright JS Foundation and other contributors | web (dev) (not incorporated) | ok |
| npm | entities | 8.1.0 | BSD-2-Clause | BSD-2-Clause | LICENSE | Copyright (c) Felix Böhm | web (dev) (not incorporated) | ok |
| npm | es-module-lexer | 2.3.2 | MIT | MIT | LICENSE | Copyright (C) 2018-2022 Guy Bedford | web (dev) (not incorporated) | ok |
| npm | escalade | 3.2.0 | MIT | MIT | license | Copyright (c) Luke Edwards <luke.edwards05@gmail.com> (lukeed.com) | web (dev) (not incorporated) | ok |
| npm | escape-string-regexp | 4.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | eslint | 10.13.0 | MIT | MIT | LICENSE | Copyright OpenJS Foundation and other contributors, <www.openjsf.org> | web (dev) (not incorporated) | ok |
| npm | eslint-plugin-react-hooks | 7.1.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web (dev) (not incorporated) | ok |
| npm | eslint-scope | 9.1.2 | BSD-2-Clause | BSD-2-Clause | LICENSE | Copyright JS Foundation and other contributors, https://js.foundation | web (dev) (not incorporated) | ok |
| npm | eslint-visitor-keys | 3.4.3 | Apache-2.0 | Apache-2.0 | LICENSE | Copyright contributors | web (dev) (not incorporated) | ok |
| npm | eslint-visitor-keys | 5.0.1 | Apache-2.0 | Apache-2.0 | LICENSE | Copyright contributors | web (dev) (not incorporated) | ok |
| npm | espree | 11.2.0 | BSD-2-Clause | BSD-2-Clause | LICENSE | Copyright (c) Open JS Foundation | web (dev) (not incorporated) | ok |
| npm | esquery | 1.7.0 | BSD-3-Clause | BSD-3-Clause | license.txt | Copyright (c) 2013, Joel Feenstra | web (dev) (not incorporated) | ok |
| npm | esrecurse | 4.3.0 | BSD-2-Clause | BSD-2-Clause |  |  | web (dev) (not incorporated) | ok |
| npm | estraverse | 5.3.0 | BSD-2-Clause | BSD-2-Clause | LICENSE.BSD |  | web (dev) (not incorporated) | ok |
| npm | estree-walker | 3.0.3 | MIT | MIT | LICENSE | Copyright (c) 2015-20 [these people](https://github.com/Rich-Harris/estree-walker/graphs/contributors) | web (dev) (not incorporated) | ok |
| npm | esutils | 2.0.3 | BSD-2-Clause | BSD-2-Clause | LICENSE.BSD |  | web (dev) (not incorporated) | ok |
| npm | expect-type | 1.4.0 | Apache-2.0 | Apache-2.0 | LICENSE | Copyright 2024 Misha Kaletsky | web (dev) (not incorporated) | ok |
| npm | fast-deep-equal | 3.1.3 | MIT | MIT | LICENSE | Copyright (c) 2017 Evgeny Poberezkin | web (dev) (not incorporated) | ok |
| npm | fast-json-stable-stringify | 2.1.0 | MIT | MIT | LICENSE | Copyright (c) 2017 Evgeny Poberezkin | web (dev) (not incorporated) | ok |
| npm | fast-levenshtein | 2.0.6 | MIT | MIT | LICENSE.md | Copyright (c) 2013 [Ramesh Nair](http://www.hiddentao.com/) | web (dev) (not incorporated) | ok |
| npm | fdir | 6.5.0 | MIT | MIT | LICENSE | Copyright 2023 Abdullah Atta | web (dev) (not incorporated) | ok |
| npm | file-entry-cache | 11.1.5 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | find-up | 5.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | flat-cache | 6.1.23 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | flatted | 3.4.4 | ISC | ISC | LICENSE | Copyright (c) 2018-2020, Andrea Giammarchi, @WebReflection | web (dev) (not incorporated) | ok |
| npm | gensync | 1.0.0-beta.2 | MIT | MIT | LICENSE | Copyright 2018 Logan Smyth <loganfsmyth@gmail.com> | web (dev) (not incorporated) | ok |
| npm | glob-parent | 6.0.2 | ISC | ISC | LICENSE | Copyright (c) 2015, 2019 Elan Shanker, 2021 Blaine Bublitz <blaine.bublitz@gmail.com>, Eric Schoffstall <yo@contra.io> and other contributors | web (dev) (not incorporated) | ok |
| npm | globals | 17.13.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | graceful-fs | 4.2.11 | ISC | ISC | LICENSE | Copyright (c) 2011-2022 Isaac Z. Schlueter, Ben Noordhuis, and Contributors | web (dev) (not incorporated) | ok |
| npm | hashery | 1.5.1 | MIT | MIT | LICENSE | Copyright (c) 2025 Jared Wray | web (dev) (not incorporated) | ok |
| npm | hermes-estree | 0.25.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web (dev) (not incorporated) | ok |
| npm | hermes-parser | 0.25.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web (dev) (not incorporated) | ok |
| npm | hookified | 1.15.1 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | hookified | 2.2.0 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | html-encoding-sniffer | 7.0.0 | MIT | MIT | LICENSE.txt | Copyright © Domenic Denicola <d@domenic.me> | web (dev) (not incorporated) | ok |
| npm | ignore | 7.0.12 | MIT | MIT | LICENSE-MIT | Copyright (c) 2013 Kael Zhang <i@kael.me>, contributors | web (dev) (not incorporated) | ok |
| npm | ignore | 5.3.2 | MIT | MIT | LICENSE-MIT | Copyright (c) 2013 Kael Zhang <i@kael.me>, contributors | web (dev) (not incorporated) | ok |
| npm | imurmurhash | 0.1.4 | MIT | MIT |  |  | web (dev) (not incorporated) | ok |
| npm | indent-string | 4.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | is-extglob | 2.1.1 | MIT | MIT | LICENSE | Copyright (c) 2014-2016, Jon Schlinkert | web (dev) (not incorporated) | ok |
| npm | is-glob | 4.0.3 | MIT | MIT | LICENSE | Copyright (c) 2014-2017, Jon Schlinkert. | web (dev) (not incorporated) | ok |
| npm | is-potential-custom-element-name | 1.0.1 | MIT | MIT | LICENSE-MIT.txt | Copyright Mathias Bynens <https://mathiasbynens.be/> | web (dev) (not incorporated) | ok |
| npm | isexe | 2.0.0 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | jiti | 2.7.0 | MIT | MIT | LICENSE | Copyright (c) Pooya Parsa <pooya@pi0.io> | web (dev) (not incorporated) | ok |
| npm | js-tokens | 4.0.0 | MIT | MIT | LICENSE | Copyright (c) 2014, 2015, 2016, 2017, 2018 Simon Lydell | web (dev) (not incorporated) | ok |
| npm | jsdom | 30.1.3 | MIT | MIT | LICENSE.txt | Copyright (c) 2010 Elijah Insua | web (dev) (not incorporated) | ok |
| npm | jsesc | 3.1.0 | MIT | MIT | LICENSE-MIT.txt | Copyright Mathias Bynens <https://mathiasbynens.be/> | web (dev) (not incorporated) | ok |
| npm | json-schema-traverse | 0.4.1 | MIT | MIT | LICENSE | Copyright (c) 2017 Evgeny Poberezkin | web (dev) (not incorporated) | ok |
| npm | json-stable-stringify-without-jsonify | 1.0.1 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | json5 | 2.2.3 | MIT | MIT | LICENSE.md | Copyright (c) 2012-2018 Aseem Kishore, and [others]. | web (dev) (not incorporated) | ok |
| npm | keyv | 5.6.0 | MIT | MIT | LICENSE | Copyright (c) 2017-2021 Luke Childs | web (dev) (not incorporated) | ok |
| npm | levn | 0.4.1 | MIT | MIT | LICENSE | Copyright (c) George Zahariev | web (dev) (not incorporated) | ok |
| npm | lightningcss | 1.32.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | web (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss | 1.33.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | web (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss-win32-x64-msvc | 1.32.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | web (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | lightningcss-win32-x64-msvc | 1.33.0 | MPL-2.0 | MPL-2.0 | LICENSE |  | web (dev) (not incorporated) | FLAG: not allowed: MPL-2.0 |
| npm | locate-path | 6.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | lru-cache | 11.5.3 | BlueOak-1.0.0 | BlueOak-1.0.0 | LICENSE.md | copyright in it. | web (dev) (not incorporated) | FLAG: not allowed: BlueOak-1.0.0 |
| npm | lru-cache | 5.1.1 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | lucide-react | 1.57.0 | ISC (Feather-derived icons: MIT) | ISC | LICENSE | Copyright (c) 2026 Lucide Icons and Contributors | web | ok |
| npm | lz-string | 1.5.0 | MIT | MIT | LICENSE | Copyright (c) 2013 pieroxy | web (dev) (not incorporated) | ok |
| npm | magic-string | 1.4.3 | MIT | MIT | LICENSE | Copyright 2018 Rich Harris | web (dev) (not incorporated) | ok |
| npm | magic-string | 0.30.21 | MIT | MIT | LICENSE | Copyright 2018 Rich Harris | web (dev) (not incorporated) | ok |
| npm | mdn-data | 2.27.1 | CC0-1.0 | CC0-1.0 | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | min-indent | 1.0.1 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com), James Kyle <me@thejameskyle.com> (thejameskyle.com) | web (dev) (not incorporated) | ok |
| npm | minimatch | 10.2.6 | BlueOak-1.0.0 | BlueOak-1.0.0 | LICENSE.md | copyright in it. | web (dev) (not incorporated) | FLAG: not allowed: BlueOak-1.0.0 |
| npm | ms | 2.1.3 | MIT | MIT | license.md | Copyright (c) 2020 Vercel, Inc. | web (dev) (not incorporated) | ok |
| npm | nanoid | 3.3.20 | MIT | MIT | LICENSE | Copyright 2017 Andrey Sitnik <andrey@sitnik.ru> | web (dev) (not incorporated) | ok |
| npm | natural-compare | 1.4.0 | MIT | MIT |  |  | web (dev) (not incorporated) | ok |
| npm | node-releases | 2.0.59 | MIT | MIT | LICENSE | Copyright (c) 2017 Sergey Rubanov (https://github.com/chicoxyzzy) | web (dev) (not incorporated) | ok |
| npm | obug | 3.0.0 | MIT | MIT | LICENSE | Copyright © 2025-PRESENT Kevin Deng (https://github.com/sxzz) | web (dev) (not incorporated) | ok |
| npm | optionator | 0.9.4 | MIT | MIT | LICENSE | Copyright (c) George Zahariev | web (dev) (not incorporated) | ok |
| npm | p-limit | 3.1.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | p-locate | 5.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | parse5 | 8.0.1 | MIT | MIT | LICENSE | Copyright (c) 2013-2019 Ivan Nikulin (ifaaan@gmail.com, https://github.com/inikulin) | web (dev) (not incorporated) | ok |
| npm | path-exists | 4.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | path-key | 3.1.1 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | picocolors | 1.1.1 | ISC | ISC | LICENSE | Copyright (c) 2021-2024 Oleksii Raspopov, Kostiantyn Denysov, Anton Verinov | web (dev) (not incorporated) | ok |
| npm | picomatch | 4.0.7 | MIT | MIT | LICENSE | Copyright (c) 2017-present, Jon Schlinkert. | web (dev) (not incorporated) | ok |
| npm | playwright | 1.64.1 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE |  | web (dev) (not incorporated) | ok |
| npm | playwright-core | 1.64.1 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE |  | web (dev) (not incorporated) | ok |
| npm | postcss | 8.5.30 | MIT | MIT | LICENSE | Copyright 2013 Andrey Sitnik <andrey@sitnik.es> | web (dev) (not incorporated) | ok |
| npm | prelude-ls | 1.2.1 | MIT | MIT | LICENSE | Copyright (c) George Zahariev | web (dev) (not incorporated) | ok |
| npm | pretty-format | 27.5.1 | MIT | MIT | LICENSE | Copyright (c) Facebook, Inc. and its affiliates. | web (dev) (not incorporated) | ok |
| npm | punycode | 2.3.1 | MIT | MIT | LICENSE-MIT.txt | Copyright Mathias Bynens <https://mathiasbynens.be/> | web (dev) (not incorporated) | ok |
| npm | qified | 0.10.1 | MIT | MIT | LICENSE |  | web (dev) (not incorporated) | ok |
| npm | react | 19.3.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web | ok |
| npm | react-dom | 19.3.1 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web | ok |
| npm | react-is | 17.0.2 | MIT | MIT | LICENSE | Copyright (c) Facebook, Inc. and its affiliates. | web (dev) (not incorporated) | ok |
| npm | react-router | 7.18.4 | MIT | MIT | LICENSE.md | Copyright (c) React Training LLC 2015-2019 | web | ok |
| npm | react-router-dom | 7.18.4 | MIT | MIT | LICENSE.md | Copyright (c) React Training LLC 2015-2019 | web | ok |
| npm | redent | 3.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | require-from-string | 2.0.2 | MIT | MIT | license | Copyright (c) Vsevolod Strukchinsky <floatdrop@gmail.com> (github.com/floatdrop) | web (dev) (not incorporated) | ok |
| npm | rolldown | 1.2.14 | MIT | MIT | LICENSE | Copyright (c) 2024-present VoidZero Inc. & Contributors | web (dev) (not incorporated) | ok |
| npm | saxes | 6.0.0 | ISC | ISC |  |  | web (dev) (not incorporated) | ok |
| npm | scheduler | 0.28.0 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web | ok |
| npm | semver | 7.8.5 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | semver | 6.3.1 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | set-cookie-parser | 2.7.2 | MIT | MIT | LICENSE | Copyright (c) 2015 Nathan Friedly <nathan@nfriedly.com> (http://nfriedly.com/) | web | ok |
| npm | shebang-command | 2.0.0 | MIT | MIT | license | Copyright (c) Kevin Mårtensson <kevinmartensson@gmail.com> (github.com/kevva) | web (dev) (not incorporated) | ok |
| npm | shebang-regex | 3.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | source-map-js | 1.2.2 | BSD-3-Clause | BSD-3-Clause | LICENSE | Copyright (c) 2009-2011, Mozilla Foundation and contributors | web (dev) (not incorporated) | ok |
| npm | std-env | 4.3.0 | MIT | MIT | LICENCE | Copyright (c) Pooya Parsa <pooya@pi0.io> | web (dev) (not incorporated) | ok |
| npm | strip-indent | 3.0.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | tailwindcss | 4.3.3 | MIT | MIT | LICENSE | Copyright (c) Tailwind Labs, Inc. | web (build, output bundled) | ok |
| npm | tapable | 2.3.3 | MIT | MIT | LICENSE | Copyright JS Foundation and other contributors | web (dev) (not incorporated) | ok |
| npm | tinybench | 6.2.2 | MIT | MIT | LICENSE | Copyright (c) 2022 Tinylibs | web (dev) (not incorporated) | ok |
| npm | tinyexec | 1.3.1 | MIT | MIT | LICENSE | Copyright (c) 2024 Tinylibs | web (dev) (not incorporated) | ok |
| npm | tinyglobby | 0.2.17 | MIT | MIT | LICENSE | Copyright (c) 2024 Madeline Gurriarán | web (dev) (not incorporated) | ok |
| npm | tldts | 7.4.18 | MIT | MIT | LICENSE | Copyright (c) 2017 Thomas Parisot, 2018 Rémi Berson | web (dev) (not incorporated) | ok |
| npm | tldts-core | 7.4.18 | MIT | MIT | LICENSE | Copyright (c) 2017 Thomas Parisot, 2018 Rémi Berson | web (dev) (not incorporated) | ok |
| npm | tough-cookie | 6.0.2 | BSD-3-Clause | BSD-3-Clause | LICENSE | Copyright (c) 2015, Salesforce.com, Inc. | web (dev) (not incorporated) | ok |
| npm | tr46 | 7.0.0 | MIT | MIT | LICENSE.md | Copyright (c) Sebastian Mayr | web (dev) (not incorporated) | ok |
| npm | ts-api-utils | 2.5.0 | MIT | MIT | LICENSE.md |  | web (dev) (not incorporated) | ok |
| npm | type-check | 0.4.0 | MIT | MIT | LICENSE | Copyright (c) George Zahariev | web (dev) (not incorporated) | ok |
| npm | typescript | 7.0.2 | Apache-2.0 | Apache-2.0 | LICENSE, NOTICE.txt |  | web (dev) (not incorporated) | ok |
| npm | typescript-eslint | 8.71.2 | MIT | MIT | LICENSE | Copyright (c) 2019 typescript-eslint and other contributors | web (dev) (not incorporated) | ok |
| npm | typescript6 | 6.0.3 | Apache-2.0 | Apache-2.0 | LICENSE.txt |  | web (dev) (not incorporated) | ok |
| npm | undici | 8.12.2 | MIT | MIT | LICENSE | Copyright (c) Matteo Collina and Undici contributors | web (dev) (not incorporated) | ok |
| npm | update-browserslist-db | 1.3.4 | MIT | MIT | LICENSE | Copyright 2022 Andrey Sitnik <andrey@sitnik.es> and other contributors | web (dev) (not incorporated) | ok |
| npm | uri-js | 4.4.1 | BSD-2-Clause | BSD-2-Clause | LICENSE | Copyright 2011 Gary Court. All rights reserved. | web (dev) (not incorporated) | ok |
| npm | use-sync-external-store | 1.7.0 | MIT | MIT | LICENSE | Copyright (c) Meta Platforms, Inc. and affiliates. | web | ok |
| npm | vite | 8.3.4 | Apache-2.0 AND MIT AND BSD-2-Clause AND ISC | MIT | LICENSE.md | Copyright (c) 2019-present, VoidZero Inc. and Vite contributors | web (dev) (not incorporated) | ok |
| npm | vitest | 5.0.5 | MIT AND BSD-3-Clause AND ISC | MIT | LICENSE.md | Copyright (c) 2021-Present VoidZero Inc. and Vitest contributors | web (dev) (not incorporated) | ok |
| npm | w3c-xmlserializer | 6.0.0 | MIT | MIT | LICENSE.md | Copyright © Sebastian Mayr | web (dev) (not incorporated) | ok |
| npm | webidl-conversions | 8.0.1 | BSD-2-Clause | BSD-2-Clause | LICENSE.md | Copyright (c) 2014, Domenic Denicola | web (dev) (not incorporated) | ok |
| npm | whatwg-mimetype | 5.0.0 | MIT | MIT | LICENSE.txt | Copyright © Domenic Denicola <d@domenic.me> | web (dev) (not incorporated) | ok |
| npm | whatwg-url | 17.3.0 | MIT | MIT | LICENSE.txt | Copyright (c) Sebastian Mayr | web (dev) (not incorporated) | ok |
| npm | which | 2.0.2 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | why-is-node-running | 3.2.1 | MIT | MIT | LICENSE | Copyright (c) 2016 Mathias Buus | web (dev) (not incorporated) | ok |
| npm | word-wrap | 1.2.5 | MIT | MIT | LICENSE | Copyright (c) 2014-2016, Jon Schlinkert | web (dev) (not incorporated) | ok |
| npm | xml-name-validator | 5.0.0 | Apache-2.0 | Apache-2.0 | LICENSE.txt |  | web (dev) (not incorporated) | ok |
| npm | xmlchars | 2.2.0 | MIT | MIT | LICENSE | Copyright Louis-Dominique Dubeau and contributors to xmlchars | web (dev) (not incorporated) | ok |
| npm | yallist | 3.1.1 | ISC | ISC | LICENSE | Copyright (c) Isaac Z. Schlueter and Contributors | web (dev) (not incorporated) | ok |
| npm | yocto-queue | 0.1.0 | MIT | MIT | license | Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com) | web (dev) (not incorporated) | ok |
| npm | zod | 4.6.7 | MIT | MIT | LICENSE | Copyright (c) 2025 Colin McDonnell | web (dev) (not incorporated) | ok |
| npm | zod-validation-error | 4.0.2 | MIT | MIT | LICENSE | Copyright 2022 Causaly, Inc <front-end@causaly.com> | web (dev) (not incorporated) | ok |
| npm | zustand | 4.5.7 | MIT | MIT | LICENSE | Copyright (c) 2019 Paul Henschel | web | ok |
