# P0-S6 cloudflared detection matrix (systemd, Docker, token tunnels)
Status: completed

## Goal
Design how `install.sh` detects an existing cloudflared on a Linux host and integrates VaultDock without breaking it (DR1–DR3, DR6, C24, §7.13). The deliverables are a detection matrix and a read-only POSIX-sh `detect_cloudflared` prototype.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `planning/spikes/cloudflared/README.md`, created. It contains:
  - the detection matrix (10 cases: systemd local/token, snap and other packages, Docker host network, Docker user-defined bridge, Docker default bridge, stray process, quick tunnel, remote/k8s, none);
  - the output schema;
  - the safe ingress-edit procedure (back up, write a candidate, validate, check `ingress rule`, install atomically, restart, health check and HTTPS probe, roll back, set up the DNS route);
  - the token-tunnel dashboard text;
  - the Docker network-join design;
  - what was and was not verified.
- VaultDock: `planning/spikes/cloudflared/detect.sh`, created. It contains the POSIX-sh `detect_cloudflared [PORT] [VAULTDOCK_NETWORK]`, which prints `key=value` (schema `vaultdock-cloudflared-detect/1`) and changes nothing. It can be sourced with `DETECT_CLOUDFLARED_LIB=1`.

No changes to `vad-server/install.sh` (P0-32 owns it; read only).

## Interfaces exposed
- `detect_cloudflared [port=3000] [vaultdock_net=vaultdock_default]`. The function body is a subshell, so nothing leaks into the caller.
- Global keys:
  - `schema`, `vaultdock_port`, `vaultdock_network`, `root`;
  - `binary`, `binary_version`, `package`;
  - `systemd`, `docker` (`yes`/`no`/`denied`), `vaultdock_network_exists`;
  - `count`, and `action=install-outside-compose` when `count=0`.
- Per-instance keys `instance.N.*`:
  - `kind`, `id`, `active`, `mode` (`token`/`local`/`quick`/`unknown`);
  - `config`, `config_readable`, `ingress*`, `has_vaultdock_rule`;
  - `remote_config_seen`;
  - `network`, `networks`, `network_mode`, `network_subnet`;
  - `compose_project`, `compose_file`, `image`;
  - `vaultdock_bind`, `vaultdock_service`, `action` (`edit-ingress`/`dashboard`/`manual`).
- Tokens are only tested for presence and never printed.

## Commands run and results
- `bash -n detect.sh`: OK.
- `sh -n detect.sh` (Git Bash): OK.
- busybox ash `sh -n` in the docker-desktop WSL VM: OK.
- Real runs on Git Bash and busybox: exit 0 with the expected empty-host output (`systemd=no`, `docker=denied`, `count=0`).
- Standalone checks of the `--config` argv parser and the Docker mount-path mapping: correct.
- `shellcheck`: not available on this host, so not run.

## Contract issues
None. The API and backup-format contracts are not touched.

## Gaps / follow-ups
- These branches were not exercised, because no Linux/systemd host or cloudflared is available and installs are forbidden:
  - systemd, Docker and `/proc`;
  - snap config paths (assumed under `/var/snap`);
  - `cloudflared tunnel ingress validate`/`rule` exit codes;
  - metrics `/ready` ports.
  
  They need T-E1 VM fixtures: systemd local tunnel, systemd token tunnel, compose tunnel on a user-defined network, and `docker run` tunnel on the default bridge.
- Run shellcheck in CI.
- The YAML ingress edit is shape-based (insert before the catch-all). Unusual YAML is refused, and the snippet is printed instead.

## Notes for dependent tasks / INT
- **Key finding:** when cloudflared runs in a Docker bridge network, `http://127.0.0.1:<port>` from the plan (§7.13) is wrong, because 127.0.0.1 is the cloudflared container itself. For that case VaultDock should join cloudflared's network through a new opt-in override `deploy/integrations/cloudflared/compose.cloudflared-net.yml`, using a new env var `VAULTDOCK_TUNNEL_NETWORK`. The ingress or dashboard URL then becomes `http://vaultdock:8080`. Suggest INT adds this file and the env var to the §7.13 integration list, `env.schema.json` and `managed-files.json`.
- `VAULTDOCK_TRUSTED_PROXIES` currently assumes `172.16.0.0/12`. Docker may allocate networks from `192.168.0.0/16`, so install.sh should add the detected `network_subnet`.
- If `mode=local` and `remote_config_seen=yes`, treat the tunnel as dashboard-managed, because remote config overrides local ingress.
- Restarting cloudflared briefly drops every hostname on that tunnel. install.sh must ask before it restarts, and must never restart a `kind=process` instance.
- Suggested shipping location for the function: `vad-server/deploy/integrations/cloudflared/detect.sh`, sourced by install.sh (owned by DEP / P0-32 follow-up).
