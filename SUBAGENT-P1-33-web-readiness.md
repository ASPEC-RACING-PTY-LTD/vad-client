# P1-33 Web readiness + dashboard widget
Status: completed

## Goal
Add the web Readiness feature: a `/readiness` page that mirrors `GET /readiness` (api-v1 §11, P1-21 shapes) and a dashboard widget showing the overall status and counts.

## Changes (repo: path, created/modified/deleted, why)
All files are in vad-server, under `web/src/features/readiness/`, and all were created:
- `types.ts`: mirrors `wire.ReadinessReport`, `ReadinessDevice`, `ReadinessCheck` and `ReadinessJob`. It also holds the check labels and these helpers:
  - severity, tone and labels;
  - `deviceStatus`: the worst scored check, or `unknown`, using the same rule as the server;
  - `sortChecks` and `countDevices`/`countChecks`;
  - `safeFixLink`: accepts only app-relative paths.
- `api.ts`: `readinessKeys`, `readinessPath` and `useReadiness`.
- `shared.tsx`: `StatusIcon` and `StatusBadge`. They are a separate file so the eager dashboard widget does not pull in the lazy page.
- `ReadinessPage.tsx`: the page:
  - overall score, status and device counts per status;
  - one card per device, worst device first;
  - checks sorted worst first, with message, severity badge and a "Fix" link (taken from `fix_link`) on non-ok checks;
  - `install_mode` shown as "Info";
  - unknown check ids (for example `server_key_custody`) get humanised labels;
  - device links to `/backups?device=N` and `/devices?device=N`;
  - a jobs table with links to `/backups/jobs/{id}` showing last success, open issues, verified, test restore and VSS.
  - It honours `?device=` and shows a "Show all devices" button when one is set.
  - Admins get an "All users" toggle that sets `?scope=all`.
  - Device and job names come from `useDevices`/`useAllJobs` in `features/backups/api`; when a name is missing it falls back to `Device #N` or `Job #N`.
- `dashboard.tsx`: `ReadinessWidget`: status badge, score, device counts (critical/warning/ok), the number of critical and warning checks, and a "View all" link. Widget id `readiness.overview`, order 31, flag `backup.v1`.
- `module.tsx`: route `/readiness`, nav entry "Readiness" (Gauge icon, order 47), `requiredFeatureFlag: 'backup.v1'`.
- `__tests__/readiness.test.tsx`: 13 vitest tests covering the helpers, the page and the widget.

## Interfaces exposed
- Route `/readiness`, with query params `device=<id>` and `scope=all` (admin only).
- `useReadiness(filter?: { device?: number; scope?: 'mine' | 'all' })` calls `GET /readiness`. It uses query key `['readiness','list',filter]` and refetches every 60 s.
- `readinessPath(filter)`: `device` wins over `scope`, matching the server.
- Exports from `types.ts`: `deviceStatus`, `countDevices`, `countChecks`, `sortChecks`, `checkLabel`, `safeFixLink`, `statusTone`, `statusLabel`.
- Dashboard widget id `readiness.overview`.

## Commands run and results
All run from `vad-server/web`:
- `npx vitest run src/features/readiness`: 13/13 passed.
- `npx vitest run src/test src/app`: 7/7 passed (registry, smoke).
- `npm run build`: OK.
- `npm run lint`: 0 errors. There are 102 warnings, none in `features/readiness`; `npx eslint src/features/readiness` is clean.

## Contract issues
- There is no readiness feature flag in api-v1 §1.3, so the module and widget are gated on `backup.v1`.
- `ReadinessReport` carries no device or job names, so the page makes extra calls to `/devices` and `/backup/jobs`. Under `scope=all`, devices of other users show as `Device #N` and their jobs as `Job #N`.

## Gaps / follow-ups
- There is no nav badge. Summary `readiness` is a status, not a count, and P1-21 notes it is still `unknown` until `summary.go` is wired.
- The Devices page does not read `?device=` yet, so the link opens the full devices list.

## Notes for dependent tasks / INT
- If a `readiness.v1` flag is ever added, change `requiredFeatureFlag` in `module.tsx` and `dashboard.tsx`.
- If the server adds `device_name` or `job.name` to the report, the page can drop the extra lookups.
