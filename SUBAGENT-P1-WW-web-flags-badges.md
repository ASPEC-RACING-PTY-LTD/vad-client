# P1-WW (WEB-0 follow-up) Server feature flags and nav badges in the web registry
Status: completed

## Goal
- Load feature flags from `GET /api/v1/info` (`features`, api-v1 §1.3) into `FeatureContext.flags`.
- Add nav count badges and wire approvals to `/summary.pending_approvals`.
- Add tests for both.

## Changes (repo: vad-server, all under `web/src/`)
- `app/types.ts` (modified)
  - `FeatureNav` gets an optional `useBadge?: () => number | undefined`. It is a React hook, called unconditionally inside a per-entry component.
  - Doc comment added for `FeatureContext.flags`.
- `app/registry.ts` (modified)
  - Removed the static empty `featureFlags`.
  - Added `noFeatureFlags`, `FEATURE_FLAGS_REFRESH_MS` (5 min), `featureFlagsKey(userId)` (`['info','features',userId]`) and `fetchFeatureFlags()`. `fetchFeatureFlags()` calls GET `/info` and keeps only the string entries of `features`, as a Set.
  - `NavEntry` now carries `useBadge`.
- `app/useFeatureContext.ts` (modified)
  - New `useFeatureFlags()`: a React Query query with `staleTime` and `refetchInterval` of 5 min. The key is scoped to the user id, so every login fetches fresh. Logout already clears the cache (B5).
  - `useFeatureContext()` now returns the real flags.
- `app/summary.ts` (created)
  - `Summary` type for api-v1 §3 and a shared `useSummary()` on `queryKeys.summary`, refreshing every 30 s. Approvals mutations already invalidate that key, so the badge updates right after a decision.
  - `summaryBadge(field)`, a factory that returns a badge hook.
- `components/Layout.tsx` (modified)
  - `NavBadge` count pill: rose, shows `99+` above 99, hidden when 0 or undefined, `aria-label="N pending"`. It renders in both the main and admin nav sections.
  - While the flags are loading, `<main>` shows `PageLoader` instead of the page. Without this, a deep link to a flag-gated route such as `/approvals/7` would fall through to the catch-all `*`, which redirects to `/` before `/info` answers.
  - If `/info` fails, the page renders with no flags.
- `features/approvals/module.tsx` (modified)
  - Added `useBadge: summaryBadge('pending_approvals')` to `nav`, plus its import line. That is two lines rather than the one allowed, because the import can't be avoided.
- `app/__tests__/flags-badges.test.tsx` (created): 6 tests.
  - Registry gating by `requiredFeatureFlag`.
  - Layout hides Approvals and holds page content while `/info` is pending, then shows the entry once `approvals.v1` arrives.
  - Approvals stays hidden when the flag is absent, and when `/info` returns 500 (the page still renders).
  - The badge renders "3" with label "3 pending" inside the `/approvals` link.
  - The badge is hidden when the count is 0.

## Interfaces exposed
```ts
// app/types.ts (FeatureNav)
useBadge?: () => number | undefined
// app/registry.ts
export const noFeatureFlags: ReadonlySet<string>
export const FEATURE_FLAGS_REFRESH_MS: number
export const featureFlagsKey: (userId: number) => readonly ['info', 'features', number]
export function fetchFeatureFlags(): Promise<ReadonlySet<string>>
export type NavEntry = Required<Pick<FeatureNav,'label'|'icon'|'to'|'order'|'section'>> & Pick<FeatureNav,'useBadge'> & { id: string; end: boolean }
// app/useFeatureContext.ts
export function useFeatureFlags(): { flags: ReadonlySet<string>; ready: boolean }
export function useFeatureContext(): FeatureContext
// app/summary.ts
export type Summary = { pending_devices; pending_approvals; running_jobs; failed_runs_24h; unread_notifications; open_conflicts; blacklisted_profiles: number; readiness: string }
export type SummaryCounter = Exclude<keyof Summary, 'readiness'>
export function useSummary(): UseQueryResult<Summary, ApiError>
export function summaryBadge(field: SummaryCounter): () => number | undefined
```
Usage in a feature: `nav: { label, icon, order, useBadge: summaryBadge('open_conflicts') }`. A custom hook works too, as long as it is a stable function reference defined at module level.

## Commands run and results
- `/info` is mounted outside the session group (`internal/api/server.go:770`, `handleInfo` carries `features`), so it is public. `/summary` needs a session (`internal/api/summary.go`).
- `npm run build`: OK (`✓ built in 335ms`).
- `npx vitest run`: 9 files, 37/37 tests passed (6 of them new).
- `npm run lint`: 0 errors, 102 warnings. The warning count is unchanged and they are all pre-existing `no-explicit-any`. The touched files have no new warnings.
- I hit and fixed one problem of my own: a Python edit saved `§` as cp1252 in `types.ts` and `registry.ts`, which broke the rolldown build. Both files are UTF-8 again.

## Contract issues
None.

## Gaps / follow-ups
- Flags refresh on login (per-user key) and every 5 min. They are not pushed when the server is upgraded mid-session.
- `AuthProvider` also calls `/info` separately (for site name and registration). That costs one extra fetch per login. It could share the query, but `lib/auth.tsx` is not owned here.
- The badge's aria-label is the generic "N pending". If a future badge doesn't mean "pending", add an optional badge label to `FeatureNav`.

## Notes for dependent tasks / INT
- Features: gate modules and dashboard widgets with `requiredFeatureFlag: '<flag>.v1'`. They appear automatically once the server advertises the flag. Approvals now appears whenever `/info` lists `approvals.v1`.
- Other components that need `/summary` should reuse `useSummary()` from `app/summary.ts`, which keeps a single cache entry under `queryKeys.summary`.
