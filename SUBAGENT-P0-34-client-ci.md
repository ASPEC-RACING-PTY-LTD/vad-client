# P0-34 REL-C: client repo CI and release workflows; signing step gated on a secret
Status: completed

## Goal
Rewrite the pre-split `.github/workflows/{ci,release}.yml` for the client repository (sdk + desktop only). Add licence, bindings, e2e and NSIS checks, optional Authenticode signing (O4, through `scripts/sign.ps1`) and optional Ed25519 component-manifest signing (D29).

## Changes (repo: path, created/modified/deleted, why)
- C: `.github/workflows/ci.yml` (rewritten). Removed the server, web, installer (`install.sh`) and image jobs and the root `go.mod`/`go.sum` references. Jobs:
  - `sdk` (ubuntu): `gofmt -l` gate, `go mod tidy -diff`, `go vet ./...`, `GOOS=windows go vet ./...`, `go test ./...`.
  - `desktop` (windows): `npm ci` and `npm run build` for the frontend, then `go vet ./...` and `go test ./...`, then `GOOS=linux go vet ./internal/...`. That last step covers the `!windows` stubs. The Wails/systray `main` package is excluded because it is Windows-only.
  - `bindings-diff` (windows): installs the cached Wails CLI v2.16.0 and creates a placeholder `frontend/dist/.keep`. A placeholder is enough because `//go:embed all:frontend/dist` only needs the directory to exist, and `dist/.keep` is not tracked in git. It then runs `wails generate module` and fails if `git status --porcelain -- desktop/frontend/wailsjs` is non-empty. That check also catches untracked files and respects `.gitattributes` normalisation.
  - `e2e` (ubuntu, `continue-on-error: true`): checks out this repo into `VaultDock/` and `ASPEC-RACING-PTY-LTD/vad-server` into `vad-server/`, so `desktop/e2e`'s `../../../vad-server` resolves. Then it runs `go test ./...` in `VaultDock/desktop/e2e`. A comment says it may fail until vad-server is published on GitHub and that `continue-on-error` should be removed after that.
  - `licences` (ubuntu): `go mod download` in sdk and desktop, `npm ci` in `desktop/frontend`, then `bash .licenses/check.sh && bash .licenses/check.sh --notices`.
  - `nsis` (windows): runs the frontend build, then `wails build -s -skipbindings -clean -nsis -trimpath`, then checks that `*-amd64-setup.exe` exists. Nothing is uploaded or published.
- C: `.github/workflows/release.yml` (rewritten). The server-image job is gone. Triggers are tag `v*` (`v[0-9]+.[0-9]+.[0-9]+*`) or `workflow_dispatch` with a tag. Jobs:
  - `desktop` (windows): builds the installer as before.
    - When `WINDOWS_CERT_PFX` is set, the `HAS_CERT` job env is computed from `secrets.WINDOWS_CERT_PFX != ''`. The step signs `VaultDock.exe` via `scripts/sign.ps1`, re-runs `makensis -DARG_WAILS_AMD64_BINARY=<signed exe> project.nsi` so the installer contains the signed binary, then signs the installer.
    - Otherwise it emits a `::notice::` and the build stays unsigned.
  - `publish` (ubuntu): guarded by `github.repository == 'ASPEC-RACING-PTY-LTD/VaultDock'`.
    - Writes the placeholder component manifest `components-<v>.json` as `{"manifest_version":1,"release":"<v>","components":[]}`. It is valid per `sdk/modules`.
    - If `RELEASE_MANIFEST_KEY` is set, it writes a detached `components-<v>.json.sig`: a base64 Ed25519 signature over the exact file bytes, made with `openssl pkeyutl -rawin` and verified in-step. The key goes to a 0600 temp file that is removed on exit, and only the public key is printed.
    - Then it writes `SHA256SUMS` over all assets and creates or updates the GitHub release (`--prerelease` for `-` tags).
- C: `scripts/sign.ps1` (created).
  - **Arguments:** `-Path <files...>`, plus optional `-TimestampUrl` (default DigiCert, then Sectigo fallback) and `-Description`.
  - **Certificate:** reads `WINDOWS_CERT_PFX` (base64) and `WINDOWS_CERT_PASSWORD` (optional). If no cert is configured, it prints a notice and exits 0.
  - **Signing:** decodes the PFX to a random temp file that is always deleted, finds signtool (on PATH or in the newest Windows Kits x64), and runs `signtool sign /fd sha256 /td sha256 /tr <RFC3161>` with retry across timestamp servers, then `signtool verify /pa`.
  - **Secrets:** never echoed. An invalid base64 value reports a fixed message.

## Interfaces exposed
- Secrets (all optional):
  - `WINDOWS_CERT_PFX`: base64 .pfx.
  - `WINDOWS_CERT_PASSWORD`.
  - `RELEASE_MANIFEST_KEY`: Ed25519 PKCS#8 PEM, from `openssl genpkey -algorithm ed25519`. The matching public key, from `openssl pkey -pubout`, is what DSK-MOD embeds.
- Release assets:
  - `VaultDock-<v>-amd64-setup.exe`;
  - `components-<v>.json`;
  - `components-<v>.json.sig`, only when the key is set;
  - `SHA256SUMS`, which covers all of the above.
- `scripts/sign.ps1 <file> [<file>...]`, exit 0 when unconfigured.

## Commands run and results
- YAML parse with Ruby Psych (PyYAML is not installed): both files parse. ci.yml has jobs sdk, desktop, bindings-diff, e2e, licences and nsis; release.yml has desktop and publish. Every step has `uses` or `run`.
- `actionlint`: not installed. It was not fetched because the task grants no network access.
- `[System.Management.Automation.Language.Parser]::ParseFile` on `sign.ps1`: 0 errors.
- `sign.ps1` behaviour (Windows PowerShell 5.1, `-ExecutionPolicy Bypass` for that process only):
  - empty `WINDOWS_CERT_PFX`: notice, exit 0;
  - invalid base64: the fixed error message;
  - missing file: "File to sign not found".
- Manifest signing tested end to end locally: the same printf, openssl sign and verify, and base64 steps on a throwaway key, then verified with a scratch Go program using `modules.NewEd25519Verifier(...).VerifyManifest` and `modules.ParseManifest`. Result: `ok 1.2.3-rc.1 0 <nil>`.
- Local dry-runs of the CI commands (GOWORK=off):
  - `sdk`: `gofmt -l .` is clean. `go mod tidy -diff` **fails**: it would drop the indirect `klauspost/compress` and `golang.org/x/crypto` that P0-01 pre-added. `go build ./...` **fails** in `sdk/automation`, which other agents are editing right now (initialisation cycle; undefined `RuntimeDevice` and `ValidateDocument`).
  - `desktop`, `GOOS=linux go vet` package by package: `dbutil`, `guard`, `platform`, `tokens`, `transfer` and `updater` pass. The rest fail only because of the `sdk/automation` breakage above.
- Not run locally: Wails CLI, makensis and signtool signing with a real certificate (none of them are available), and the workflows themselves.

## Contract issues
None.

## Gaps / follow-ups
- **Secret name mismatch:** MASTER_PLAN §7.14 names the secret `WINDOWS_SIGNING_CERT`; this task (and the implementation) uses `WINDOWS_CERT_PFX` (+ `WINDOWS_CERT_PASSWORD`). INT should align §7.14.
- **Uninstaller signing:** this needs `!uninstfinalize` in `desktop/build/windows/installer/project.nsi`, which is commented out today and owned by P1-30. Until then only `VaultDock.exe` and the setup exe are signed.
- **Assumption about Wails' makensis call:** the signed-build repackaging assumes Wails v2 invokes makensis with only `-DARG_WAILS_AMD64_BINARY` and that it regenerates `wails_tools.nsh` from `wails.json` during `wails build -nsis`. This is unverified on CI; check it on the first signed release.
- **Assumption about `wails generate module`:** `bindings-diff` assumes the command does not run the frontend install or build. If it does, add Node and `npm ci` to that job.
- **e2e runner:** runs on ubuntu, using the engine's `!windows` paths. Switch it to `windows-latest` if the e2e tests come to depend on Windows behaviour.
- **Restore tool:** §7.1 lists a restore-tool release asset; none exists yet, so it is not built.
- **Real components:** later REL-C tasks add real `vss`/`discovery`/`reinstall` entries to the manifest.
- **Optional:** sign `SHA256SUMS` with the same Ed25519 key (one more openssl line). Not done, because it was not requested.

## Notes for dependent tasks / INT
- CI is red until two things happen:
  1. `sdk/automation` compiles again. This also affects desktop vet and test.
  2. `go mod tidy -diff` is clean in `sdk/`. The pre-added indirect requirements will be pruned by tidy until code imports them. Either run tidy once the deps are used, or drop the tidy step temporarily.
- The `licences` job runs `check.sh --notices`, so commit a regenerated `THIRD_PARTY_NOTICES.md` after every dependency change.
- The CI `desktop` job does not run `gofmt`, because the Windows checkout converts line endings to CRLF and gofmt would flag every file. Go formatting for the client is gated only in the `sdk` job.
- DSK-MOD: verify the detached `components-<v>.json.sig` with `VerifyManifest(raw, sig)`; the signature is base64 text.
