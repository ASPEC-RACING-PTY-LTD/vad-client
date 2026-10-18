# P0-32 Deploy env schema, add-only .env merge, Caddy de-bundling
Status: completed

## Goal
DEP task for B15, B16 and C20 (DR2, DR4, DR5, DR6):
- add `deploy/env.schema.json` and `deploy/managed-files.json`;
- make the `install.sh` `.env` handling add-only, with obsolete variables removed only after per-variable approval;
- take Caddy out of the default Compose file and make it the opt-in integration `deploy/integrations/caddy`, with automatic migration of existing installs.

## Changes (repo `vad-server`)
- **`deploy/env.schema.json`** (created). `{schema_version:1, variables:[…]}` with 47 flat entries.
  - Fields:
    - `name`, `type`, `default`, `required`, `secret`;
    - `generate` (`hex24`): generate a random value instead of using the default;
    - `env_file`: install.sh keeps the variable in `.env`;
    - `managed`: install.sh owns the value;
    - `scope` (`server`/`compose`/`installer`);
    - `since`, `obsolete`, `deprecated_since`, `replacement`, `description`.
  - Sources: `config.go`, `cmd/vaultdock/main.go` (`VAULTDOCK_TLS_HOSTS`), `.env.example`, `install.sh` and `docker-compose.yml`.
  - New variables:
    - `VAULTDOCK_KEK_FILE` (`/state/keys/kek`);
    - `VAULTDOCK_AI_ALLOW_PRIVATE_ENDPOINTS` (false);
    - `VAULTDOCK_SYNC_TOMBSTONE_DAYS` (**90**, per INT's correction; range 90–3650);
    - `VAULTDOCK_INTEGRATIONS` (managed);
    - `VAULTDOCK_ENV_APPROVE_REMOVE` (installer only).
  - `COMPOSE_PROFILES` is marked `obsolete`, `deprecated_since 1.1.0`, `replacement VAULTDOCK_INTEGRATIONS`.
- **`deploy/managed-files.json`** (created). One entry per file, with a policy:
  - `replace`: compose, install.sh, both JSON files, `compose.caddy.yml`;
  - `keep-modified`: the Caddyfile. It has `legacy_path: deploy/Caddyfile` and `known_sha256` set to the old shipped hash;
  - `merge`: `.env`;
  - `user`: `docker-compose.override.yml` and `deploy/env-approvals`.
- **`deploy/integrations/caddy/compose.caddy.yml`** (created). The former `caddy` service, with no profile.
  - It keeps the same service name and the same `${VAULTDOCK_STATE_PATH}/caddy-data` and `caddy-config` volumes, so certificates are kept.
  - The Caddyfile is mounted from `./deploy/integrations/caddy/Caddyfile`.
- **`deploy/integrations/caddy/Caddyfile`** (created). A byte-identical copy of the old file, so unmodified installs match the known hash.
- **`deploy/Caddyfile`** (deleted). Moved to the integration directory.
- **`docker-compose.yml`** (modified). The `caddy` service is removed: only `vaultdock` remains. The header comment points to `deploy/integrations/`.
- **`.env.example`** (modified):
  - points to the schema;
  - the Caddy option now uses `VAULTDOCK_INTEGRATIONS=caddy` instead of `COMPOSE_PROFILES`;
  - adds `KEK_FILE`, `SYNC_TOMBSTONE_DAYS=90` and `AI_ALLOW_PRIVATE_ENDPOINTS`.
- **`install.sh`** (modified):
  - `set_env` is replaced by these modular functions:

    | Function | What it does |
    |---|---|
    | `json_records` | Reads the JSON files: jq if installed, otherwise a POSIX awk reader for flat objects (documented in the script and in both JSON `$comment`s) |
    | `env_has`, `env_add` | `env_add` writes only when the key is missing |
    | `env_set_managed` | Refuses (dies on) any key outside `MANAGED_KEYS` = ENV_FILE, IMAGE, VERSION, AUTO_UPDATE, INTEGRATIONS |
    | `env_remove`, `env_backup` | `env_backup` writes `backups/env-<ts>/.env` once per run, before the first change |
    | `env_merge_missing` | Appends missing schema variables under a dated comment block; `generate: hex24` gets a random token |
    | `env_review_obsolete` | Lists obsolete variables that are present and removes each one only with approval (see below) |
    | `env_merge`, `do_env_review` | `env_merge` = merge plus obsolete review; `do_env_review` runs it on an installed `.env` |
    | `state_path` | Host state directory as an absolute path |
    | `file_sha256`, `record_shipped`, `apply_managed_files` | `record_shipped` writes `app/.managed-files.sha256` |
    | `integrations`, `integration_enabled`, `integration_file` | Enabled opt-in integrations and their override file paths |
    | `legacy_caddy_profile`, `migrate_caddy_profile` | Detect and migrate the old bundled Caddy profile |

  - Removal approval for obsolete variables comes from any of:
    - an interactive per-variable y/N prompt (default No);
    - `--approve-env-removal NAME[,NAME]`;
    - `VAULTDOCK_ENV_APPROVE_REMOVE`;
    - `app/deploy/env-approvals`;
    - the admin console's `${STATE}/deploy/env-removal-approvals.json` (api-v1 §11A), which is cleared after it is applied.
  - `--yes`/`VAULTDOCK_YES=1` (the auto-update cron) never approves a removal. Only schema-obsolete names can be removed.
  - `compose()` adds `deploy/integrations/<name>/compose.<name>.yml` for each entry in `VAULTDOCK_INTEGRATIONS`.
  - `RUNTIME_FILES` and `KEEP_FILES` are updated, and `fetch_app` creates subdirectories.
  - The upgrade now runs: `fetch_app` → `apply_managed_files` → swap → `migrate_caddy_profile` → `env_merge` → build. The existing backup (`backup_now` tar of `app/` plus the DB) and the `$APP.old` rollback cover `.env` and the migration. A failed migration restores `$APP.old` and aborts.
  - `VAULTDOCK_UPDATE_REPO` is now add-only (`env_add`).
  - `write_env` (fresh install):
    - writes `VAULTDOCK_INTEGRATIONS=caddy` instead of `COMPOSE_PROFILES`;
    - backs up a stray existing `.env`;
    - then runs `env_merge_missing`, so schema-only variables get their defaults.
  - New `env-review` command and menu item 5.
  - The header and `--help` are updated; `--help` now prints up to the `set -E` line.

## Interfaces exposed
- Schema format: see the `$comment` in `deploy/env.schema.json`. Entries must stay flat (no nested objects or arrays) for the no-jq parser.
- Approvals:
  - the server writes `${VAULTDOCK_STATE_PATH}/deploy/env-removal-approvals.json`. `install.sh` reads the `name` field of every flat object in it (both an array of objects and `{"approvals":[…]}` work);
  - `app/deploy/env-approvals`: one name per line, `#` comments allowed.
- CLI: `install.sh env-review`, `--approve-env-removal NAME[,NAME]` (also `=` form).
- `.env`: `VAULTDOCK_INTEGRATIONS` (comma list; currently `caddy`).
- `app/.managed-files.sha256`: sha256sum format of the shipped runtime files.

## Commands run and results
- `bash -n install.sh`: OK.
- `shellcheck`: not installed on this host; not run.
- `python -I -m json.tool` on both JSON files: valid.
- Fallback awk parser vs Python `json` on both files: identical output (47 and 9 records). The jq path was not executed because jq is not installed.
- Scenario script (scratchpad; install.sh functions sourced, no Docker): legacy install with `COMPOSE_PROFILES=caddy`, the old compose and old `deploy/Caddyfile`, then the upgrade steps.
  - Existing values were unchanged and 23 missing variables were appended.
  - `VAULTDOCK_INTEGRATIONS=caddy` was set, and `compose()` then includes `compose.caddy.yml`.
  - With `YES=1`, `COMPOSE_PROFILES` was kept. With approval it was removed.
  - An admin approval for a non-obsolete variable was ignored and the approvals file was cleared.
  - `env_set_managed` on a user key was refused.
  - Unmodified legacy Caddyfile: replaced. Modified one: kept, with `Caddyfile.new` written next to it.
- Fresh-install `write_env` (HTTPS=none): 31 variables, including the new ones.
- `docker compose config` (works offline; the Docker daemon is not running):
  - the default compose has the services `vaultdock` only;
  - with the caddy override: `vaultdock` and `caddy`. The Caddyfile and caddy-data/config paths resolve against the project directory.
  - No `up` was run.

## Contract issues
- api-v1 §11A does not specify the JSON shape of `env-removal-approvals.json` beyond "(name, approved_by, approved_at)". install.sh accepts any flat objects that have `name`.
- The plan's schema field list (`deprecated_since`) and the task's (`obsolete`) differ. Both are included.

## Gaps / follow-ups
- `since` and `deprecated_since` values (`1.0.0` = present before the schema, `1.1.0` = new) are placeholders. INT should align them with the real release number.
- `.env.example` is still hand-maintained, not generated from the schema.
- Integrations menu (nginx/traefik/cloudflared examples, cloudflared detection), preflight, and digest-aware image pulls: P0-33 or later.
- The fresh install makes a harmless extra backup of the just-written `.env`.

## Notes for dependent tasks / INT
- **`.gitattributes`** (not owned): add `deploy/integrations/caddy/Caddyfile text eol=lf`, and ideally `deploy/** text eol=lf`. The old `deploy/Caddyfile` rule is now stale. The `known_sha256` in `managed-files.json` assumes LF content.
- **`docs/deployment.md` line 63** (not owned) still documents `COMPOSE_PROFILES=caddy`. It should say `VAULTDOCK_INTEGRATIONS=caddy` (or `install.sh`), plus `docker compose -f docker-compose.yml -f deploy/integrations/caddy/compose.caddy.yml` for manual use.
- **C21 redirect stub / transitional release:**
  - An *old* install.sh run against these new files would download a missing `deploy/Caddyfile` (it fails safe, before swapping). Or, if a stub file is provided, it would run `--remove-orphans` and drop Caddy until the next new-script run.
  - So the stub must download the vad-server `install.sh` and `exec` it for upgrades, rather than run the old upgrade logic against new files.
- **P0-33:**
  - `REPO` default is still `ASPEC-RACING-PTY-LTD/vad-client` (the repo URL transition is not part of this task).
  - Keep `RUNTIME_FILES` in sync with `managed-files.json`.
  - Reuse `env_*`, `json_records` and `integration_*`.
  - Never reintroduce an overwriting setter.
- **Server side (`/admin/deploy/env`):** read the same schema. It needs shipping in the image or the state volume; today it lives only in `app/deploy/`.
- **P0-04 / config owner:** `config.go` should read the three new variables with the schema defaults: KEK `/state/keys/kek`, tombstone days 90, AI private endpoints false.
