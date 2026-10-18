# P0-35 REL-S Server repo CI and release (GHCR image, SDK via sibling checkout or proxy, licence check)
Status: completed

## Goal
Rewrite the pre-split `vad-server` workflows for the server repository only:
- `ci.yml`: server, web, installer, image and licence jobs;
- `release.yml`: a multi-arch GHCR image plus a GitHub Release with the deploy assets.

Remove the desktop jobs. Fix the Dockerfile and `.dockerignore` where needed, and keep the `sdk` build-context mechanism.

## Changes (repo: path, created/modified/deleted, why)
All changes are in repo `vad-server`.

### `.github/workflows/ci.yml` (rewritten)
The desktop job is removed.

**Sibling SDK checkout:**
- Jobs that compile Go check out this repo to `$GITHUB_WORKSPACE/vad-server`.
- A "Detect local SDK replace" step greps `go.mod` for `replace …/VaultDock/sdk => ../VaultDock/sdk`. Only when that replace is present does the job check out `ASPEC-RACING-PTY-LTD/VaultDock` (sparse `sdk/`) to `$GITHUB_WORKSPACE/VaultDock`.
- The ref is `vars.VAULTDOCK_SDK_REF`, defaulting to `main`.
- The token is `secrets.VAULTDOCK_SDK_TOKEN`, falling back to `github.token`.
- Once the replace is removed, the sibling checkout is skipped and the SDK comes from the proxy automatically.

**Jobs:**
- **server:**
  - creates a placeholder `web/dist/index.html` (see Notes);
  - `go mod download`;
  - gofmt gate over `git ls-files '*.go'`;
  - `go mod tidy -diff`;
  - `go vet ./...` and `go test ./...`;
  - `go test -tags integration ./tests/...`. Without `VAULTDOCK_URL` this only compiles and skips.
- **web:** `npm ci`, `npm run build`, `npm test`, `npm run lint`.
- **installer:**
  - `bash -n install.sh` and `shellcheck --severity=error install.sh`;
  - if `scripts/test-install/` exists, it runs `bash -n` and shellcheck on its `*.sh` files, then `bash scripts/test-install/run.sh --dry-run` if that file exists. Otherwise the step is skipped with a message.
- **image:**
  - `docker buildx build --check` (Dockerfile lint);
  - then `docker/build-push-action` with `build-contexts: sdk=$GITHUB_WORKSPACE/VaultDock/sdk` (only while the replace exists), amd64, `push: false`, `load: true`, gha cache;
  - then a smoke run: start the container, poll `/healthz` for 30 s, run `vaultdock version`, and dump the logs on failure.
- **licences:** sibling checkout, Go and Node setup, the `web/dist` placeholder, `go mod download`, `npm ci --ignore-scripts` in `web`, then `bash .licenses/check.sh && bash .licenses/check.sh --notices`.

### `.github/workflows/release.yml` (rewritten)
The Windows/desktop job is removed.
- **Trigger:** tags `v*`, plus `workflow_dispatch` with a `tag` input. The tag is validated as `vMAJOR.MINOR.PATCH[-pre]`.
- **server-image:**
  - uses the same conditional sibling checkout and writes the SDK commit used to the step summary;
  - builds and pushes `linux/amd64,linux/arm64` to `ghcr.io/aspec-racing-pty-ltd/vad-server:<version>`, plus `:latest` only for tags without `-`;
  - `docker/metadata-action` sets the OCI labels and annotations (manifest and index levels): title, description, vendor, licences `PolyForm-Noncommercial-1.0.0`, source, url and version, with created and revision added automatically;
  - `provenance: false` is kept, as before.
- **publish** (needs server-image) checks out the tag and builds `assets/`:
  - `install.sh`;
  - `docker-compose.yml`;
  - `env.example`, which is `.env.example` renamed because GitHub renames release assets that start with a dot;
  - `deploy.tar.gz`: reproducible (sorted, owner 0, commit mtime, `gzip -n`), unpacking to `deploy/…`;
  - `SHA256SUMS` over those four files.

  It then runs `gh release create` (or `upload --clobber` if the release exists) on `ASPEC-RACING-PTY-LTD/vad-server`. It uses generated notes with the image reference prepended, and `--prerelease` for tags containing `-`.

### `Dockerfile` (modified)
- **Bug fix:** the replace/dropreplace `RUN` contained a literal `\n` instead of a line continuation, so the shell saw `… ; n else …`. The sdk-context branch would have run a non-existent command `n`, and the else branch never ran. It is now a proper multi-line `if/else/fi`.
- The `FROM scratch AS sdk` stage is unchanged. Its comment now documents `docker buildx build --build-context sdk=../VaultDock/sdk .`.
- The final stage gains `ARG VERSION` and OCI labels: title, description, vendor, licences, version and source.
- It now copies `LICENSE` and `THIRD_PARTY_NOTICES.md` to `/usr/share/doc/vaultdock/`, because the BSD and Apache notice terms apply to binary redistribution.

### `.dockerignore` (rewritten)
- It still keeps every file the build needs: go.mod/sum, `cmd/`, `internal/`, the `web/` sources, `LICENSE` and `THIRD_PARTY_NOTICES.md`.
- It now also excludes: `.github`, `.env.*` (but not `.env.example`), `*.db*`, `web/test-results`, `web/playwright-report`, `web/blob-report` and `web/tsconfig.tsbuildinfo`.

## Interfaces exposed
- **Release asset names**, stable and version-free, so that `releases/latest/download/<name>` and `releases/download/<tag>/<name>` work: `install.sh`, `docker-compose.yml`, `env.example`, `deploy.tar.gz` (top-level dir `deploy/`), `SHA256SUMS`. The SHA256SUMS format is `sha256sum` output.
- **Image:** `ghcr.io/aspec-racing-pty-ltd/vad-server:<version>` and `:latest`, with no `v` prefix (e.g. `1.2.0`).
- **Repository settings** (all optional):
  - `vars.VAULTDOCK_SDK_REF`;
  - `secrets.VAULTDOCK_SDK_TOKEN`, a read token for VaultDock if that repo is private (`github.token` cannot read another private repo).
- **Harness hook for P0-33:** CI runs `bash scripts/test-install/run.sh --dry-run` if that file exists.

## Commands run and results
- **YAML:** both workflows parse with `gopkg.in/yaml.v3` (scratch program, offline module cache).
  - Every step has exactly one of `uses`/`run`, and the `${{ }}` delimiters are balanced.
  - Jobs: ci = server, web, installer, image, licences; release = server-image, publish (needs server-image).
- **Embedded shell:** all 29 `run:` blocks were extracted (with `${{…}}` replaced by a placeholder) and pass `bash -n`.
- **Release tarball:** the `deploy.tar.gz` command was run locally into the scratchpad and produces `deploy/env.schema.json`, `deploy/integrations/**` and `deploy/managed-files.json` with owner 0/0.
- **gofmt, tidy and Docker (read-only checks):**
  - `go mod tidy -diff` (GOWORK=off): the only difference is CRLF line endings in the Windows working copy. The content is identical, so it passes on a Linux checkout.
  - gofmt with CRLF stripped flags 3 files whose import order changed after the module rename (see Notes); the CI gofmt step will fail until they are fixed.
  - `actionlint`, `hadolint` and `shellcheck` are not installed, and fetching them is not allowed (no network for this task).
  - Docker is installed but the **engine is not running**, so `docker buildx build --check` and the image build were **not** run. The Dockerfile was reviewed by hand instead.

## Contract issues
None.

## Gaps / follow-ups
- **Proxy mode is not usable yet.** Without an `sdk` context the Dockerfile drops the replace, but go.mod still requires the pseudo-version `v0.0.0-00010101000000-000000000000`, which does not exist on the proxy, so the image build fails. That is expected until `sdk/v0.1.0` is tagged and go.mod requires it (with the replace removed); after that, the workflows skip the sibling automatically.
- **Release pinning:** while the replace exists, releases build against `VAULTDOCK_SDK_REF` (default `main`). Set that variable to an SDK tag or commit before tagging a release so the build is reproducible. The commit used is written to the job summary.
- **Integration tests:** they only compile in CI. A follow-up could run Phase1, then restart the container, then Phase2 against the smoke container in the image job, using a setup token.
- **Supply-chain extras:** arm64 is built only in the release job, not in CI. `provenance`/SBOM and keyless signing are off; D29/§7.14 may want cosign or attestations later.
- The `vendor` fallback mentioned in §7.1 step 5 is not implemented; the sibling-context approach replaces it while the SDK is unpublished.

## Notes for dependent tasks / INT
- **gofmt will fail CI** on `internal/api/automations.go` and `internal/testserver/testserver.go`, plus the **frozen** `internal/api/api_test.go`: the `vad-server` and `VaultDock/sdk` import groups are out of order after the rename. INT must run `gofmt -w` on them; I did not touch them (not owned, and one is frozen).
- **`web/dist`:** `web/embed.go` embeds `all:dist`, but `web/dist` (including the `.keep` that `.gitignore` whitelists) is **not tracked**, so `go vet`/`go test`/`go list` fail on a fresh checkout. CI works around this with a placeholder. INT should consider committing `web/dist/.keep` so plain checkouts build.
- **P0-33 (install.sh):**
  - download release assets by the names above; note `env.example`, not `.env.example`;
  - unpack `deploy.tar.gz` and verify against `SHA256SUMS`;
  - point `REPO` at `ASPEC-RACING-PTY-LTD/vad-server` and the image at `ghcr.io/aspec-racing-pty-ltd/vad-server`;
  - expose `scripts/test-install/run.sh --dry-run` (or INT adjusts the installer job to the real entry point).
- **P0-32 note:** `/admin/deploy/env` needs `deploy/env.schema.json` at runtime. The image does not ship it yet. Whoever implements that endpoint should `go:embed` it (which needs a copy under a Go package dir) or have the Dockerfile copy it; the latter is a one-line change to this Dockerfile.
- **Line endings:** git warns that LF will become CRLF for the workflow files and `.dockerignore`, which is harmless for the YAML. `THIRD_PARTY_NOTICES.md` must stay LF in commits so `check.sh --notices` (`cmp`) passes on Linux.
- No new dependencies. No git or `ch` state changes.
