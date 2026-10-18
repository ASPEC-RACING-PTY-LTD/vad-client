# P0-16 (WEB-0) Split Admin.tsx + admin tabs registry + spinner fix
Status: completed

## Goal
- Split the 793-line `pages/admin/Admin.tsx` into one component per tab.
- Add an admin tabs registry that feature folders can extend without editing Admin.tsx.
- Extract `LocationBar`.
- Fix the Settings tab infinite spinner.
- Use `components/TransfersTable` and `lib/activity` directly (P0-17 request).

URLs and behaviour are unchanged. The admin tabs are path-based (`/admin`, `/admin/users`, ...), not query params, and the paths are identical to before.

## Changes (repo: vad-server)
- `web/src/pages/admin/Admin.tsx` (modified, 793 → 41 lines): now only the shell.
  - Renders the page header, the tab bar (same NavLink markup and classes) and the nested `<Routes>` from `enabledAdminTabs(adminTabs, ctx)`.
  - Routes are wrapped in `<Suspense fallback={<PageLoader/>}>` so feature tabs can be `lazy()`.
  - The catch-all `*` → `/admin` is kept.
- `web/src/pages/admin/LocationBar.tsx` (created): `LocationBar`, moved verbatim. This is the storage-location usage bar used by Overview, Storage and Health; Admin.tsx had no breadcrumb bar.
- `web/src/pages/admin/tabs/` (created). Each file is the tab moved verbatim as a default export, with imports adjusted:
  - `Overview.tsx` (`OverviewTab`)
  - `Users.tsx`
  - `Storage.tsx`
  - `Devices.tsx`
  - `Transfers.tsx`
  - `Settings.tsx`
  - `Health.tsx`
  - `Audit.tsx`
- `tabs/QuotaInput.tsx` (created): the shared `QuotaInput` and `GB` constant, used by Users, Settings and Storage.
- `tabs/registry.ts` (created): the `AdminTab` type, the built-in tabs, feature discovery and filtering. Its header comment documents how to add a tab.
- `tabs/registry.test.ts` (created): 3 tests.
  - Built-in order and paths are unchanged.
  - Ids and paths are unique.
  - Sorting, `superuserOnly` and `requiredFeatureFlag` filtering work.
- Updates tab: it stays in `pages/admin/Updates.tsx` (not owned), and the registry references `UpdatesTab` from there.
- Imports: `describe` now comes from `lib/activity` (Overview, Audit), and `TransfersTable` from `components/TransfersTable` (Transfers). Nothing in admin imports `pages/Sync` or `pages/Activity` any more.

### Spinner bug (research doc 03-web-app.md §1.9 / hotspot 12)
- **Cause:** the Settings tab checked `if (isLoading || !s) return <PageLoader/>` before the error check. `s` is only set from `data` in an effect, so when `/admin/settings` failed, `s` stayed `null` and the tab spun forever. The error state was never reached.
- **Fix:** `tabs/Settings.tsx` now checks `error` first.
- **Related fix in `tabs/Transfers.tsx`:** when the active-uploads query failed with no data, the tab showed a misleading "No active uploads". It now shows `ErrorState` with retry. This is the only visible change beyond the bug fix.

## Interfaces exposed
```ts
// web/src/pages/admin/tabs/registry.ts
export interface AdminTab {
  id: string                  // unique, kebab-case
  path?: string               // URL segment under /admin; defaults to id; '' = index (/admin)
  label: string
  icon: LucideIcon
  order: number               // lower first; built-ins 10..90
  component: ComponentType    // no props; may be React.lazy()
  superuserOnly?: boolean
  requiredFeatureFlag?: string
}
export interface AdminTabContext extends FeatureContext { isSuperuser: boolean }
export const coreAdminTabs: readonly AdminTab[]
export const featureAdminTabs: readonly AdminTab[]
export const adminTabs: readonly AdminTab[]          // core + feature
export function tabPath(t: AdminTab): string
export function isAdminTabEnabled(t: AdminTab, ctx: AdminTabContext): boolean
export function enabledAdminTabs(all: readonly AdminTab[], ctx: AdminTabContext): AdminTab[] // filtered, sorted (order, id); DEV logs duplicate ids/paths

// web/src/pages/admin/LocationBar.tsx
export function LocationBar(props: { l: any }): JSX.Element
// web/src/pages/admin/tabs/QuotaInput.tsx
export const GB: number
export function QuotaInput(props: { value: number; onChange: (v: number) => void }): JSX.Element
```
**Built-in orders:**

| Order | Tab | Path |
|---|---|---|
| 10 | Overview | `''` |
| 20 | Users | |
| 30 | Storage | |
| 40 | Devices | |
| 50 | Transfers | |
| 60 | Settings | |
| 70 | System health | `health` |
| 80 | Updates | |
| 90 | Audit log | `audit` |

**Registering a tab.** Both mechanisms are supported and mirror `DashboardWidget`; no edits to Admin.tsx or the registry are needed:
- `web/src/features/<x>/admin.tsx` with `export default <AdminTab | AdminTab[]>`
- `export const adminTabs: AdminTab[]` in `web/src/features/<x>/module.tsx`

Both are discovered with eager `import.meta.glob`; `module.tsx` is read as a namespace glob, so modules without `adminTabs` still build.

**`superuserOnly`:** there is no superuser role yet (roles are `user` | `admin`). Admin.tsx passes `isSuperuser: isAdmin`, so every admin is a superuser and the flag has no effect until a narrower admin role exists.

**`requiredFeatureFlag`:** uses P0-13's `featureFlags`, which is currently empty.

## Commands run and results
All commands ran in `vad-server/web`.

| Command | Result |
|---|---|
| `npm run build` | Passes (`✓ built in 312ms`). The Admin chunk is 39.3 kB, and `TransfersTable` is still its own shared chunk. |
| `npx vitest run` | 7 files, 19 tests passed, including the new `registry.test.ts`. |
| `npx eslint src/pages/admin` | 0 errors, 24 warnings, the same count as before the split (23 `no-explicit-any` plus the existing `exhaustive-deps` on `QuotaInput`). |
| `npm run lint` | 0 errors, 102 warnings. The baseline had 1 error, in `components/ScheduleEditor.tsx`, which is not mine and has since been fixed by its owner. No new errors. |

The app was not checked in the browser, because that needs the Go server plus an admin session. The registry test covers the tab order and URLs.

## Contract issues
None.

## Gaps / follow-ups
- The `QuotaInput` `useEffect` still omits `onChange` from its dependencies (an existing lint warning; behaviour was kept).
- The tabs are still typed with `any`, as before.
- Feature tabs only appear if their feature flag is active; flags are still an empty set (P0-13 gap).

## Notes for dependent tasks / INT
- The `export { TransfersTable }` in `pages/Sync.tsx` and `export { describe }` in `pages/Activity.tsx` are no longer used by admin and can be removed (owner: P0-17 / INT). Grep for other importers first.
- WS3 GC and Providers tabs:
  - add `features/storage/admin.tsx` exporting, for example, `{ id: 'gc', label: 'Storage GC', icon, order: 35, component: lazy(() => import('./admin/GcTab')) }`;
  - the URL becomes `/admin/gc`.
- Admin.tsx should now be treated as frozen (research doc: "frozen after WS0").
