# P1-32 (WEB-1) Web Backups feature
Status: completed

## Goal
Finish `vad-server/web/src/features/backups/`:
- the module (nav entry "Backups" gated on `backup.v1`; routes `/backups`, `/backups/jobs/:id` and `/backups/runs/:id`);
- the jobs list, job editor (Stepper), job detail and run detail pages;
- the dashboard widget;
- tests.

This resumes from the existing `types.ts` and `api.ts`.

## Changes (repo: vad-server)
All files are under `web/src/features/backups/`.

- `api.ts` (modified): added `RepoLite` and `useRepos()` (`GET /repos`, all pages, `retry: false`) for the editor's repository picker. Nothing else changed.
- `module.tsx` (created): `FeatureModule` with id `backups` and nav "Backups" (icon `HardDriveDownload`, order 45), `requiredFeatureFlag: 'backup.v1'`. It has three lazy routes:
  - `/backups` → `JobsPage`;
  - `/backups/jobs/:id` → `JobPage` (`:id = new` opens the create wizard, with an optional `?device=`);
  - `/backups/runs/:id` → `RunPage`.
- `shared.tsx` (created):
  - `JobStateBadge`, `suspensionReason` (for mode suspensions it lists `required_modules`; for profile suspensions it shows `profile_sid`), `RunStatusBadge`, `RunProgress` (JobProgress fed from a Run), `canRunNow`;
  - `JobErrorBanner`: shows the `jobErrorView` message, an "Open Devices" link for gating errors and Reload on conflict;
  - `useJobActions`: pause/resume and re-check via PATCH `enabled` with If-Match, and run-now via the `backup_now` device task;
  - `JobActions`.
- `JobEditor.tsx` (created): a Stepper with four steps.
  - **Basics:** name, device, repository (a select from `/repos`, falling back to a numeric ID input), kind, mode, and a profile SID picker. Only `active_default` or `authorised` profiles can be picked.
  - **Sources:** ListInput for paths and excludes, plus `use_vss`.
  - **Schedule:** ScheduleEditor plus `validateSchedule`.
  - **Options:** `retention_policy_id`, priority, enabled.

  Creating sends POST `/backup/jobs`. Editing sends PATCH with `If-Match` and only the fields that can be updated; device, repository and kind are locked. Errors appear in `JobErrorBanner`.
- `IssuesPanel.tsx` (created): the run's issues in a DataTable.
  - A class filter maps to `?class=`.
  - Open rows can be selected, and "Skip selected" sends `{ids}`.
  - "Skip all <class>" sends `{class}`.
  - "Retry skipped" calls `/retry-skipped`.
  - Errors go through `jobErrorView`, which covers `nothing_to_retry`.
- `JobsPage.tsx` (created): one DataTable per device, showing schedule, state badge, suspension reason and actions:
  - Pause/Resume for active jobs, Re-check for suspended ones;
  - Run now only when the job is enabled and `active`;
  - a row click opens the detail page.
- `JobPage.tsx` (created): the create wizard (`new`) or the job detail. The detail page has:
  - stats: state and reason, schedule, last run, last successful run, paths, profile, required modules, revision;
  - actions: Run now, Pause, Re-check, Edit (a modal editor keyed by revision, with Reload that refetches the job) and Delete (with a confirm);
  - a recent runs table (running rows show JobProgress, which polls through `useRuns`);
  - an issues panel for the latest finished run.
- `RunPage.tsx` (created): run stats, live progress while the run is running, and the issues panel.
- `dashboard.tsx` (created): widget `backups.status` (order 32, gated on `backup.v1`). It shows the last run status for each job, from `GET /backup/runs?limit=200`, and an "N failing" / "all OK" badge.
- `__tests__/backups.test.tsx` (created): 11 tests:
  - the list, including suspension reasons and when actions are visible;
  - PATCH with `If-Match: 3`, and run-now as a `backup_now` task;
  - the conflict flow: 409 conflict, then the banner, then Reload refetches;
  - four gating messages with the Devices link;
  - a gating error during create;
  - skip by ids, skip by class, and Retry Skipped;
  - `nothing_to_retry`;
  - the dashboard widget.

No registry edit was needed: `app/registry.ts` and `lib/dashboardWidgets.ts` find `features/*/module.tsx` and `features/*/dashboard.tsx` automatically.

## Interfaces exposed
- Routes: `/backups`, `/backups/jobs/new[?device=<id>]`, `/backups/jobs/:id`, `/backups/runs/:id`.
- Dashboard widget id: `backups.status`.
- `api.ts` adds `export type RepoLite = { id: number; name: string }` and `export function useRepos()`.
- `shared.tsx`: `suspensionReason(job): string | null`, `useJobActions(onError?)`, `JobErrorBanner({view, onReload?})`, `RunProgress({run, title?})`.

## Commands run and results
All were run from `vad-server/web`:
- `npx tsc -b --noEmit`: exit 0.
- `npx vitest run src/features/backups`: 11/11 passed.
- `npm run build`: succeeded.
- `npm run lint`: 0 errors, 102 warnings, all in files outside this feature. `npx eslint src/features/backups` is clean (exit 0).

## Contract issues
These are unchanged from the earlier partial report, and the code follows the INT decisions:
- There is no `GET /backup/jobs/{id}`, so `fetchJob` pages through `GET /backup/jobs`.
- There is no session route to resolve an issue, so that action is omitted.
- Run-now uses the `backup_now` device task.

`GET /repos` (sess) is still `notImplemented` on the server. The editor assumes it returns a `{items,next_cursor}` page. If the call fails or returns nothing, the editor falls back to a numeric "Repository ID" input.

## Gaps / follow-ups
- Retention policy is a numeric ID input. There is no policy picker yet, because the §9 listing route is not wired in the web app.
- The dashboard widget reads only the newest 200 runs, so a job whose last run is older than that shows "never run".
- I did not check the pages in a browser. The tests run in jsdom only.

## Notes for dependent tasks / INT
- When `GET /repos` is implemented with a different response shape, adjust `useRepos` in `api.ts`.
- If a single-job GET is added, swap `fetchJob` for a direct GET. Callers don't need to change.
