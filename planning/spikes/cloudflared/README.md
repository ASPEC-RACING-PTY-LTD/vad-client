# P0-S6 spike: cloudflared detection matrix

Scope: how `vad-server/install.sh` (Integrations step, MASTER_PLAN §7.13, DR1–DR3, DR6, C24) finds an existing cloudflared on a Linux host and wires VaultDock in without breaking it. The default Compose project never contains cloudflared (DR2).

Files:
- `detect.sh`: POSIX-sh prototype `detect_cloudflared [PORT] [VAULTDOCK_NETWORK]`. It is read-only and prints `key=value` lines.

## 1. Detection matrix

| # | Case | How to detect (read-only) | Mode signal | Config location | VaultDock exposure | Ingress service URL | install.sh action |
|---|---|---|---|---|---|---|---|
| 1 | systemd, **locally managed** (`cloudflared service install` without a token, or a hand-written unit) | `systemctl list-units --all` and `list-unit-files` for `cloudflared*` (includes `cloudflared@<name>` instances and custom names starting `cloudflared-`). `systemctl show -p ExecStart` gives the argv | no token; config has `ingress:` (and usually `tunnel:` plus `credentials-file:`) | `--config X` from ExecStart, otherwise the default search order: `/etc/cloudflared/config.yml`, `/usr/local/etc/cloudflared/`, `~root/.cloudflared/` | `VAULTDOCK_BIND=127.0.0.1`, host port `VAULTDOCK_PORT` | `http://127.0.0.1:<port>` | **edit-ingress** (§3) |
| 2 | systemd, **token / remotely managed** (`cloudflared service install <TOKEN>`) | as 1 | `--token`, `--token-file` in ExecStart, or `TUNNEL_TOKEN` in `Environment=` or an `EnvironmentFile=` | may still exist, but **remote config overrides local ingress** | `127.0.0.1:<port>` | `http://127.0.0.1:<port>` (set in the dashboard) | **dashboard** (§4) |
| 3 | snap / other package | `snap list cloudflared`, `dpkg-query`, `rpm -q`, `apk info`. The service is a systemd unit `snap.cloudflared.*` | as 1 or 2 | from ExecStart. Snap confinement may hide `/etc`, so the config is usually under `/var/snap/cloudflared/...` | `127.0.0.1:<port>` | `http://127.0.0.1:<port>` | as 1 or 2. Restart via `snap restart` / systemctl |
| 4 | Docker, `--network host` | `docker ps -a --no-trunc` where the image or command contains `cloudflared` (official `cloudflare/cloudflared`, or custom wrappers). Then `docker inspect` for `HostConfig.NetworkMode`, `Args`, `Config.Env`, `Mounts` and compose labels | `--token` in Args or `TUNNEL_TOKEN`/`TUNNEL_TOKEN_FILE` in Env means token; otherwise a mounted config with `ingress:` means local | host path mapped from the in-container `--config` (default `/etc/cloudflared/config.yml`) through `Mounts` | `127.0.0.1:<port>` | `http://127.0.0.1:<port>` | edit-ingress or dashboard |
| 5 | Docker on a **user-defined bridge** (typical compose project, for example `cloudflared_default` or a shared `proxy` network) | as 4; `.NetworkSettings.Networks` gives the names | as 4 | as 4 | **`127.0.0.1` does not work**: inside the container it is cloudflared itself. VaultDock joins cloudflared's network via an opt-in override (§5). The host port stays on `127.0.0.1` for health checks | `http://vaultdock:8080` (container port; the service name resolves on the shared network) | join-network, then edit-ingress or dashboard |
| 6 | Docker on the **default `bridge`** (`docker run` without `--network`) | as 4, `networks=bridge` | as 4 | as 4 | The default bridge has no DNS. Preferred: ask the user to attach cloudflared to a user-defined network (`docker network connect vaultdock_tunnel <cf>`; it survives restart, but not recreate). Fallback: bind VaultDock to the docker0 gateway (`VAULTDOCK_BIND=172.17.0.1`) | `http://vaultdock:8080` (preferred) or `http://172.17.0.1:<port>` | manual (print both options) |
| 7 | Process outside systemd and Docker (nohup, screen, supervisord, OpenRC) | `/proc/*/cmdline` contains `cloudflared`; the cgroup is neither Docker nor a unit already listed | `--token` / `TUNNEL_TOKEN` in `/proc/<pid>/environ` (root only) | `--config` from the cmdline | `127.0.0.1:<port>` | `http://127.0.0.1:<port>` | manual: print the rule. Never restart a process we do not manage |
| 8 | Quick tunnel (`cloudflared tunnel --url X`, TryCloudflare) | as 1, 4 or 7 | `--url` and no named tunnel | none | n/a | n/a | manual: it cannot route a fixed hostname; recommend a named tunnel |
| 9 | Kubernetes / remote host | not visible from this host (kube cgroups are skipped) | — | — | VaultDock must listen on a reachable interface | user-chosen | manual |
| 10 | None found | `count=0` | — | — | `127.0.0.1:<port>` | — | print instructions to install cloudflared **outside** the VaultDock Compose project (package or its own compose project), then rerun Integrations |

Several instances can exist at once (for example a stale systemd unit plus a container). install.sh lists them and lets the user pick. Default pick: active, then `mode=local`.

**Trusted proxies.** Today install.sh sets `VAULTDOCK_TRUSTED_PROXIES=127.0.0.1/32,::1/128,172.16.0.0/12`. Docker can allocate user-defined networks from `192.168.0.0/16` (default address pools) or from custom pools. For case 5 or 6, install.sh should add the actual subnet (`instance.N.network_subnet`) instead of assuming `172.16/12`.

## 2. `detect.sh` output (schema `vaultdock-cloudflared-detect/1`)

Global keys:
- `schema`, `vaultdock_port`, `vaultdock_network`;
- `root` (`yes`/`no`; without root, configs and environment files may read as `denied`);
- `binary`, `binary_version`;
- `package` (`deb`, `rpm`, `snap`, `apk`, `binary` or `none`);
- `systemd` (`yes`/`no`);
- `docker` (`yes`, `no` or `denied`), `vaultdock_network_exists`;
- `count`, and `action=install-outside-compose` when `count=0`.

Per instance `instance.N.*`:
- `kind` (`systemd`, `snap`, `docker` or `process`), `id` (unit, container name or pid);
- `active`, `enabled` or `restart`;
- `mode` (`token`, `local`, `quick` or `unknown`);
- `config` (host path), `config_readable` (`yes`, `no`, `denied` or `missing`);
- `ingress`, `ingress_hostnames`, `ingress_catchall`, `has_vaultdock_rule`, `credentials_file`, `tunnel_named`;
- `remote_config_seen` (`yes` if the logs show "Updated to new configuration", which only remotely managed tunnels log);
- `network` (`host`, a network name, or `bridge-default`), `network_mode`, `networks`, `network_subnet`;
- `compose_project`, `compose_file`, `image`, `config_in_container`;
- `vaultdock_bind`, `vaultdock_service` (the URL to put in the ingress rule or the dashboard);
- `action` (`edit-ingress`, `dashboard` or `manual`).

Values are reduced to `[A-Za-z0-9._:/@,+=-]`; anything else becomes `_`. Parse with `while IFS='=' read -r k v`. **Tokens are never printed**: only their presence is tested (`grep -q`), and argv or `docker inspect` env is never echoed.

Consumption in install.sh (bash): `DETECT_CLOUDFLARED_LIB=1 . deploy/integrations/cloudflared/detect.sh`, then `detect_cloudflared "$port" vaultdock_default > "$tmp"`. The body runs in a subshell, so nothing leaks into install.sh variables. Run it with sudo for full results; without root, `config_readable=denied` and `docker=denied` are reported rather than guessed.

A `mode=local` with `remote_config_seen=yes` is contradictory, because a dashboard config overrides local ingress. install.sh must treat it as **dashboard** and say why.

## 3. Locally managed config: safe ingress edit (DR6, C24)

Preconditions:
- `mode=local`, `config_readable=yes`;
- the user confirmed the hostname;
- `has_vaultdock_rule=no` (if `yes`, only verify: step 5 and step 8);
- the YAML has a simple block-style `ingress:` list ending in a catch-all `- service: http_status:404`.

Otherwise (flow style, anchors, no catch-all, includes) **do not edit**: print the snippet and stop.

1. **Backup:**
   - `cp -p config.yml backups/<ts>/cloudflared/config.yml`;
   - also `config.yml.vaultdock-<ts>` next to the original, so a root user finds it without knowing VaultDock.
2. **Candidate:** write to a temp file **in the same directory**, so the same mount is visible inside a container. Insert the rule immediately **before the catch-all** (rules match first-to-last), keeping the indentation of the existing items:
   ```yaml
     - hostname: vault.example.com
       service: http://127.0.0.1:3000        # or http://vaultdock:8080 (case 5/6)
   ```
   Do not add `originRequest` options. VaultDock speaks plain HTTP behind the tunnel. If it runs with built-in self-signed TLS, the rule would need `noTLSVerify`; install.sh should refuse that combination and switch to HTTP-behind-tunnel instead.
3. **Validate the candidate:**
   - host binary: `cloudflared tunnel --config <candidate> ingress validate`;
   - Docker: `docker exec <cf> cloudflared tunnel --config <in-container candidate> ingress validate`.
   
   Then confirm the match with `cloudflared tunnel --config <candidate> ingress rule https://vault.example.com`; it must print our rule, not an earlier wildcard.
4. **Install atomically:** `cat candidate > config.yml.tmp && mv config.yml.tmp config.yml`, preserving owner and mode (`chown --reference`, `chmod --reference`). `mv` on the same filesystem is atomic. Docker bind mounts of a *single file* keep the old inode, so for those write in place with `cat candidate > config.yml` instead.
5. **Restart.** cloudflared does not hot-reload a local config. Warn first that other hostnames on the tunnel drop for a few seconds, and ask.
   - systemd: `systemctl restart <unit>`;
   - snap: `snap restart cloudflared`;
   - Docker: `docker restart <cf>` (or `docker compose -p <project> restart <svc>`).
   - kind=process: never restart; print instructions.
6. **Health:**
   - `systemctl is-active` or `docker inspect .State.Running`, after a 20-second grace period;
   - cloudflared's readiness `/ready` on its metrics address if `--metrics` is set (default `127.0.0.1:2000-2004` on recent versions);
   - **then** an HTTPS probe `https://<hostname>/readyz` (expect 200, retry about 30 s; DNS may lag on new hostnames).
7. **Rollback** on any failure in steps 3–6:
   - restore the backup (same write method as step 4);
   - restart;
   - re-check `is-active`;
   - report both outcomes.
   
   The candidate failing validation never touches the live file.
8. **DNS:** a locally managed tunnel also needs a DNS route. If `cert.pem` exists (`~/.cloudflared/cert.pem` or `TUNNEL_ORIGIN_CERT`), offer `cloudflared tunnel route dns <tunnel> <hostname>`. This is not destructive, but it fails if a record exists; never pass `--overwrite-dns` without asking. Otherwise print the CNAME `<hostname> → <tunnel-uuid>.cfargotunnel.com` for the user to add.

Then set `VAULTDOCK_BIND=127.0.0.1` and `VAULTDOCK_PUBLIC_URL=https://<hostname>`. These are add-only `.env` rules (DR5): an existing differing value is shown and changed only with confirmation.

## 4. Token / remotely managed tunnels

Why there is no local edit:
- When a tunnel is created in the dashboard (Zero Trust → Networks → Tunnels), its ingress lives in Cloudflare and is pushed to the connector ("Updated to new configuration").
- Any local `ingress:` is ignored.
- The token only authorizes *running* the connector. It cannot change the configuration, which needs an API token with Tunnel edit scope that install.sh does not have and should not ask for.

What install.sh prints:
```
Your Cloudflare Tunnel is managed in the Cloudflare dashboard (token-based, <unit|container>).
Add a public hostname for VaultDock there:
  1. Cloudflare dashboard → Zero Trust → Networks → Tunnels → <your tunnel> → Configure
  2. Public Hostname → Add a public hostname
       Subdomain/Domain:  vault / example.com
       Service type:      HTTP
       URL:               127.0.0.1:3000          (case 2/3/4/7)
                          vaultdock:8080          (case 5: after the network step below)
  3. Save. Cloudflare creates the DNS record automatically.
Leave TLS settings at their defaults; do not enable "No TLS Verify" (VaultDock speaks HTTP behind the tunnel).
Press Enter when done and install.sh will check https://vault.example.com/readyz ...
```
Then probe with retries. If it fails, show the common causes: wrong service URL; 127.0.0.1 used from a bridge container; DNS not propagated; Access policy in front of the hostname (the desktop client needs direct API access, so an Access app must bypass `/api/*` or use a service token).

## 5. Docker network integration (case 5/6)

- Never modify the user's cloudflared compose project.
- VaultDock instead gets an opt-in override `deploy/integrations/cloudflared/compose.cloudflared-net.yml`, which joins the `vaultdock` service to the external network named in `.env`:
  ```yaml
  services:
    vaultdock:
      networks: [default, tunnel]
  networks:
    tunnel:
      external: true
      name: ${VAULTDOCK_TUNNEL_NETWORK}
  ```
- install.sh adds it to `COMPOSE_FILE` and runs `docker compose up -d vaultdock` (only VaultDock is recreated).
- The service name `vaultdock` (container_name `vaultdock`) resolves on that network, so the ingress URL is `http://vaultdock:8080`.
- Check first that no other container on that network already uses the alias `vaultdock`.
- Case 6 (default bridge): create `vaultdock_tunnel` only after the user agrees to `docker network connect vaultdock_tunnel <cf>`. Otherwise use the `172.17.0.1` bind fallback, which exposes the port to every container on docker0.

## 6. Verified vs not verified

- Checked here:
  - `bash -n`, `sh -n` (Git Bash);
  - busybox ash `sh -n` in the docker-desktop WSL VM;
  - a real run on both, which showed the empty-host path (`systemd=no`, `docker=denied`, `count=0`);
  - unit checks of the `--config` argv parser and the mount-path mapping.
- `shellcheck` is not installed on this host, so it was not run.
- **Not exercised** (no Linux host with systemd or cloudflared available, and installs are forbidden):
  - the systemd, Docker and /proc branches;
  - `systemctl show -p ExecStart` argv parsing on real units;
  - snap unit and config paths (`/var/snap/...` is an assumption);
  - `cloudflared tunnel ingress validate`/`rule` exit codes;
  - metrics `/ready` default ports.
  
  These belong in T-E1 (Debian VM with a systemd tunnel, a compose tunnel on a user-defined network, a `docker run` default-bridge tunnel, and a token tunnel).
- Known heuristics:
  - `remote_config_seen` depends on log retention;
  - Docker matching uses image or command text containing "cloudflared";
  - a YAML catch-all check with grep/awk is shape-based, which is why §3 refuses unusual files.
