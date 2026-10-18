# P0-33 install.sh follow-up: repo move, image-only upgrades, managed files, KEK, cloudflared, test harness
Status: completed

## Goal
DEP follow-up to P0-32 (DR1–DR6, C21, C24, §7.13):
- the repository URL moves to vad-server;
- upgrades touch only the images and managed files they need;
- every managed file gets `.new`/`.local` handling;
- a KEK is generated with a backup prompt;
- rollback is kept and extended;
- opt-in cloudflared detection and integration;
- trusted proxies are added only when missing;
- a test harness in `scripts/test-install/`.

## Changes (repo `vad-server`)

### `install.sh` (modified)

**Repository**
- `DEFAULT_REPO=ASPEC-RACING-PTY-LTD/vad-server` and `LEGACY_REPO=ASPEC-RACING-PTY-LTD/VaultDock`. The header `curl` URL and `--help` are updated.
- The release image is now `ghcr.io/aspec-racing-pty-ltd/vad-server:<ver>`, from `release.yml`'s `ghcr.io/${GITHUB_REPOSITORY,,}`. Before this change it was `ghcr.io/aspec-racing-pty-ltd/vaultdock`.
- Backward compatibility:
  - `build_image` tries the legacy image of the same version (`legacy_release_image`) when the vad-server image cannot be pulled. It does this only for the default repo, a release version, and when `VAULTDOCK_IMAGE` is unset.
  - An existing `VAULTDOCK_IMAGE=ghcr.io/.../vaultdock:x` in `.env` stays valid for rollback.
  - An existing `VAULTDOCK_UPDATE_REPO=ASPEC-RACING-PTY-LTD/vad-client` is **not** changed, because it is a user value. The upgrade warns and says how to change it.

**Managed files**
- `runtime_files DIR` builds the file list from the `replace`/`keep-modified` entries of `managed-files.json`, dropping unsafe paths. `RUNTIME_FILES` is only the fallback, and the harness checks that the two stay in sync.
- `fetch_app` downloads `managed-files.json` first, then the files it lists. `record_shipped` uses the same list.
- `apply_managed_files` now covers every managed file:
  - `keep-modified`: a locally modified copy is kept and the shipped version is written as `<path>.new`. Previously this applied only to the Caddyfile.
  - `replace` (installer internals): the shipped version is installed and the modified copy is saved as `<path>.local`.
  - Unmodified means the file matches the recorded checksum or a `known_sha256`.
  - `MODIFIED_FILES` is reported at the end of the upgrade.
- `integration_file NAME` maps `cloudflared-net` to `deploy/integrations/cloudflared/compose.cloudflared-net.yml`. The family is the part of the name before the first `-`.

**Image-only upgrades**
- `build_image` reuses an image that is already present (the existing behaviour), and `compose up -d` recreates only services that changed. Volumes and bind mounts are never touched.
- The file part of an upgrade is extracted into `upgrade_files`, and that of a fresh install into `install_files`. Neither makes Docker calls. The harness runs them, and `do_upgrade`/`do_install` call them.

**KEK**
- `kek_host_path`: maps `VAULTDOCK_KEK_FILE` (default `/state/keys/kek`) to `$(state_path)/keys/kek`. Paths outside `/state` (Docker secrets) or containing `..` are not managed.
- `kek_generate`: 32 bytes from `openssl rand`, falling back to `head -c 32 /dev/urandom`. It writes to a temp file under `umask 077`, checks the size, sets mode 0600 and then moves the file into place. The key directory is 0700. Both are chowned to `VAULTDOCK_UID:GID` when running as root. The key is never printed.
- `kek_ensure` runs on a fresh install (after `prepare_dirs`) and on upgrade (before the backup and before any change):
  - an existing KEK is never regenerated;
  - when the KEK is missing **and** `db_has_wrapped_keys`, it dies with restore and data-loss instructions and changes nothing;
  - `db_has_wrapped_keys` counts rows in `server_keys`, `catalog_keys` and `secrets` with `sqlite3` when it is installed. Without `sqlite3` it treats any trace of those tables in the DB or WAL as "has keys", which is conservative;
  - an install from before the KEK existed gets a KEK on upgrade.
- `kek_backup_prompt`: explains how to copy the key off the server (scp, password manager, encrypted USB). It warns that losing the KEK **and** every recovery key (VDRK1-…) means data loss, and asks for confirmation (default No). The confirmation is remembered in `$DIR/.kek-backup-confirmed`, which is deleted whenever a new KEK is generated. Until the admin confirms, every successful upgrade shows the reminder again.
- New command `kek-status` and menu item: path, size and mode, never the content.
- `backup_now` records the KEK's presence in `<backup>/kek-status`. The key itself is never copied.

**Rollback**
- `compose up` failing during an upgrade now also triggers the rollback. Before, it aborted through the ERR trap with the new `app/` already swapped in.
- `integration_change WHAT CMD…`: tars `app/` (including `.env`) to `backups/integration-<ts>/app.tar`, applies the change, then runs `compose up -d` and the health check. On failure it restores the tar, runs `compose up -d --remove-orphans` and reports.
- The cloudflared ingress edit has its own backup and rollback (see below).

**cloudflared (opt-in; no container is ever added)**
- `cf_detect`: runs the shipped `detect.sh` as its own `sh` process and parses the `key=value` lines into `CF[]` (`declare -gA`).
- `cf_pick`: chooses the instance. The default is an active, locally managed one.
- `cf_action`: decides how to proceed.
  - It is `dashboard` when the logs show a remote config (`remote_config_seen=yes`).
  - It is `manual` for `kind=process` or an unreadable config. A process is never restarted.
- **Locally managed config** (`cf_edit_ingress`):
  1. `cf_ingress_candidate` inserts the rule before the catch-all. It refuses YAML anchors and aliases, more than one `ingress:`, and lists without a catch-all.
  2. `cf_validate` runs `cloudflared tunnel --config <candidate> ingress validate` and then `ingress rule https://<host>`, which must name our service. It uses the host binary, or `docker exec` for containers.
  3. Backups go to `backups/cloudflared-<ts>/` and to `config.yml.vaultdock-<ts>`.
  4. The config is installed atomically, keeping owner and mode. It is written in place for Docker file mounts.
  5. It **asks before restarting** (default No, so `--yes` never restarts), then checks the service is active and runs an HTTPS `/readyz` probe.
  6. When cloudflared does not come back, it rolls back: restore, restart, report. When only the probe fails, it shows DNS hints and asks whether to keep the rule.
- **Token or dashboard-managed tunnel**: prints the dashboard steps (hostname, HTTP and service URL), with optional probing afterwards.
- **None found**: prints how to install cloudflared outside VaultDock's Compose project.
- **Docker bridge network** (`cf_join_network`):
  - checks for an alias conflict on `vaultdock`;
  - asks before joining;
  - sets `VAULTDOCK_TUNNEL_NETWORK` and adds `cloudflared-net` to `VAULTDOCK_INTEGRATIONS` through `integration_change`, which provides the rollback;
  - the service URL becomes `http://vaultdock:8080`.

  On the default bridge it prints both options instead.
- Self-signed TLS together with a tunnel is refused, with an explanation.
- `trusted_proxies_add`: sets `VAULTDOCK_TRUSTED_PROXIES=127.0.0.1/32,::1/128,<subnet>` **only when the variable is missing**. An existing value, even an empty one, is never changed; the missing subnet is reported instead.
- `do_integrations [cloudflared]` is new, along with the `integrations` command and menu item 3. A fresh install with a 127.0.0.1 bind offers to run it interactively.

**Other**
- `MANAGED_KEYS` gains `VAULTDOCK_TUNNEL_NETWORK`.
- The menu follows the §7.13 order: Install, Upgrade, Integrations, Environment review, Automatic updates, Uninstall, plus Server key status.
- `VAULTDOCK_INSTALL_LIB=1` defines the functions only, without running `main`. The harness uses it.

### Deploy files (all in `vad-server`)
- **`deploy/env.schema.json`** (modified):
  - new `VAULTDOCK_TUNNEL_NETWORK` (managed, scope compose, since 1.1.0);
  - `VAULTDOCK_REPO` default is now vad-server;
  - descriptions updated for `VAULTDOCK_INTEGRATIONS` (`cloudflared-net`) and `VAULTDOCK_KEK_FILE` (generation and backup).
- **`deploy/managed-files.json`** (modified):
  - `docker-compose.yml` is now `keep-modified`, with `known_sha256` for both historic shipped versions (`9eae2e6`, `01f1a1d`), so legacy installs are replaced rather than flagged as modified;
  - `compose.caddy.yml` is now `keep-modified`;
  - new entries `cloudflared/detect.sh` (`replace`) and `cloudflared/compose.cloudflared-net.yml` (`keep-modified`);
  - `$comment` updated.
- **`deploy/integrations/cloudflared/detect.sh`** (created): adapted from the P0-S6 spike, which we wrote. The header documents the output schema, and `install.sh` processes are excluded from the `/proc` scan.
- **`deploy/integrations/cloudflared/compose.cloudflared-net.yml`** (created): the `vaultdock` service joins `default` plus the external network `${VAULTDOCK_TUNNEL_NETWORK:?…}` with alias `vaultdock`. It adds no cloudflared service.

### Test harness: `scripts/test-install/` (all created)
- **`run.sh`**: `[--dry|--docker] [scenario…]`, with `--dry` as the default.
- **`dry.sh`**: no Docker. It sources `install.sh` in lib mode and runs 8 scenarios against temporary directories:

  | Scenario | What it covers |
  |---|---|
  | sync | RUNTIME_FILES vs the JSON list, MANAGED_KEYS vs the schema, image names, detect.sh run |
  | fresh | Fresh install |
  | upgrade | Upgrade |
  | legacy-caddy | Legacy Caddy migration, unmodified and modified |
  | modified | Locally modified managed files |
  | obsolete | Obsolete-variable approval: `--yes`, flag, admin JSON, approvals file |
  | kek | KEK handling, including the refusal when the KEK is lost |
  | cloudflared | Ingress candidate, a validation stub that passes or fails, rollback of the live file, no restart under `--yes`, trusted proxies, network enable |

- **`docker.sh`**: runs the real `install.sh install` and `upgrade` inside a throw-away privileged `docker:27.3.1-dind` container per scenario (fresh, upgrade, legacy-caddy, modified, obsolete). The host's Docker state is never touched.
- **`fixtures/legacy-docker-compose.yml`** and **`fixtures/legacy-Caddyfile`**: shipped versions from `9eae2e6`.

## Interfaces exposed
- CLI:
  - `install.sh integrations [cloudflared]`;
  - `install.sh kek-status`;
  - menu items 3 (Integrations) and 7 (Server key status). The menu numbers changed.
- `.env`:
  - `VAULTDOCK_TUNNEL_NETWORK` (managed);
  - `VAULTDOCK_INTEGRATIONS` can contain `cloudflared-net`.
- Host files:
  - `${VAULTDOCK_STATE_PATH}/keys/kek` (32 raw bytes, 0600, owner `UID:GID`; directory 0700);
  - `$DIR/.kek-backup-confirmed`;
  - `backups/{upgrade,integration,cloudflared}-<ts>/`.
- `install.sh` lib mode: `VAULTDOCK_INSTALL_LIB=1 . install.sh` defines the functions without running `main`. The functions are `install_files`, `upgrade_files`, `kek_ensure`, `kek_host_path`, `db_has_wrapped_keys`, `runtime_files`, `apply_managed_files`, `cf_ingress_candidate`, `cf_edit_ingress`, `trusted_proxies_add` and `integration_change`.
- `detect.sh`: `detect_cloudflared [PORT] [NET]`, schema `vaultdock-cloudflared-detect/1`, unchanged from the spike.

## Commands run and results
- `bash -n install.sh`: OK.
- `sh -n` on `detect.sh`, `run.sh` and `docker.sh`, and `bash -n dry.sh`: OK.
- `python -I -m json.tool` on `env.schema.json` and `managed-files.json`: both valid.
- `sh scripts/test-install/run.sh --dry` (Git Bash on Windows): **all 8 scenarios pass**, about 80 checks. The checks that don't run on this host:
  - the KEK mode check runs only on Linux;
  - the sqlite3 exact-count branch was skipped because sqlite3 is not installed. The grep fallback was tested.
- Mutation check: removing the add-only guard in `env_merge_missing` makes the upgrade scenario fail 3 checks, so the harness detects regressions. The first mutation (in `env_add`) wasn't caught, because the merge never passes an existing key to it.
- `--docker` mode: **not run**. The Docker daemon is not available on this host.
- `shellcheck`: **not installed** on this host, so not run.
- Real cloudflared, systemd and Docker paths were not exercised; they need T-E1 VM fixtures, as the spike noted.

## Contract issues
- backup-format-v1 §4.6 names the KEK file `VAULTDOCK_MASTER_KEY_FILE` with default `/state/keys/master.key`. The schema (P0-32), `config.go` and this task use `VAULTDOCK_KEK_FILE` with `/state/keys/kek`. `install.sh` follows `VAULTDOCK_KEK_FILE`. INT should amend the contract or alias the variable.
- §4.6 says `install.sh` "prints a one-time server key backup instruction". It prints instructions and asks for confirmation, and keeps reminding until confirmed. The key itself is never printed.

## Gaps / follow-ups
- Not done:
  - nginx/traefik/Caddy detection or snippets;
  - offering `cloudflared tunnel route dns` (only printed as a hint);
  - digest-aware pulls;
  - a preflight step (disk space, versions).
- Without `sqlite3` on the host, a new-schema DB whose key tables are empty but whose KEK is missing is refused, which is conservative. A server CLI subcommand (for example `vaultdock kek-check`) would make this exact without sqlite3.
- The cloudflared detection, validation and restart paths need Linux VM tests (T-E1). The `--docker` harness needs a CI runner with privileged Docker.

## Notes for dependent tasks / INT
- **`.gitattributes`** (not owned): add `deploy/** text eol=lf`. The `known_sha256` values in `managed-files.json` assume LF content, for example in `compose.*.yml`.
- **Docs** (not owned): update the old repo and image names.
  - `docs/desktop.md:60` still names `ghcr.io/aspec-racing-pty-ltd/vaultdock`; the vad-server package must be made public.
  - `README.md`/`docs/deployment.md` should show the vad-server `curl` URL, `install.sh integrations cloudflared` and the KEK backup.
- **C21 stub:** the old repo's stub must `exec` this `install.sh`. Old installs keep `VAULTDOCK_UPDATE_REPO=ASPEC-RACING-PTY-LTD/vad-client` in `.env`, and `install.sh` only warns about it. If INT wants an automatic change, it needs an explicit owner decision, because of DR4.
- **Server (config/escrow owner):**
  - `install.sh` creates the KEK with the container uid. The server should **refuse to start** rather than create a new KEK when sealed keys exist and the file is missing.
  - A `vaultdock kek-check` CLI command would help (see Gaps).
- **CI:** run `sh scripts/test-install/run.sh --dry` and shellcheck on `install.sh`, `detect.sh` and `scripts/test-install/*.sh`. Run `--docker` on a privileged runner.
