# P0-17 (WEB-0) TransfersTable, describe(), dashboard widget slots
Status: completed

## Goal
- Move `TransfersTable` out of `pages/Sync.tsx` into a shared component.
- Move `describe()` out of `pages/Activity.tsx` into `lib/activity.ts`.
- Make `pages/Dashboard.tsx` render registered dashboard widget slots, with the existing content as the first built-in widgets. Visuals and behaviour stay unchanged.

## Changes (repo: vad-server)
- `web/src/components/TransfersTable.tsx` (created): `TransfersTable` moved verbatim from Sync.tsx (same query key, URL, 10 s refetch, markup and classes). The Source cell now calls `transferSource()` and produces the same output.
- `web/src/lib/activity.ts` (created):
  - `describe()` with the action-label map moved from Activity.tsx. It also describes transfer rows that have no `action`, by `status`.
  - `transferSource()` helper.
- `web/src/lib/dashboardWidgets.ts` (created): the `DashboardWidget` type, glob discovery and filtering. The header comment documents how to add a widget.
- `web/src/pages/Sync.tsx` (modified): the table code is removed. It imports from `components/TransfersTable` and keeps `export { TransfersTable }` for existing importers.
- `web/src/pages/Activity.tsx` (modified): the map is removed. It imports `describe` from `lib/activity` and `TransfersTable` from `components/` (no longer from `./Sync`), and keeps `export { describe }`.
- `web/src/pages/Dashboard.tsx` (modified):
  - The page header stays in the page. The body is split into five built-in widgets:
    - `core.stats` (order 10, span 3)
    - `core.storage` (order 20, span 2)
    - `core.sync` (order 30)
    - `core.recent` (order 40, span 2)
    - `core.automation` (order 50)
  - They render into one `grid gap-6 xl:grid-cols-3`, which gives the same spacing as the old three row grids (gap-6, then mt-6).
  - Each slot is a `grid` wrapper, so cards still stretch to row height, with `xl:col-span-N`, Suspense and an error boundary.
  - The page still shows `PageLoader`/`ErrorState` until `/stats` loads. Widgets read the cached `['stats']` and `['sync-overview']` queries with the same keys and intervals.

## Interfaces exposed
```ts
// web/src/lib/dashboardWidgets.ts
export interface DashboardWidget {
  id: string                  // unique; DEV logs duplicates
  order: number               // lower first; built-ins use 10..50
  component: ComponentType    // no props; fetches its own data (may be React.lazy)
  span?: 1 | 2 | 3            // xl columns of the 3-col grid; default 1, 3 = full width
  adminOnly?: boolean
  requiredFeatureFlag?: string
}
export const featureWidgets: readonly DashboardWidget[]
export function isWidgetEnabled(w: DashboardWidget, ctx: FeatureContext): boolean
export function enabledWidgets(all: readonly DashboardWidget[], ctx: FeatureContext): DashboardWidget[] // filtered + sorted (order, id)

// web/src/lib/activity.ts
export type DescribableEvent = { action?: string | null; status?: string | null }
export function describe(e: DescribableEvent): string   // action label, else transfer-status label, else raw value
export function transferSource(t: { username?: string; device_name?: string; source?: string }, withUser = false): string

// web/src/components/TransfersTable.tsx
export function TransfersTable(props: { status: string; device?: number; admin?: boolean }): JSX.Element
```
**Registering a widget.** Use either of these, with no edits to Dashboard.tsx or the registry:
- `web/src/features/<x>/dashboard.tsx` with `export default <DashboardWidget | DashboardWidget[]>`
- `export const dashboardWidgets: DashboardWidget[]` in `web/src/features/<x>/module.tsx`

Both are discovered with eager `import.meta.glob`. `adminOnly` and `requiredFeatureFlag` are evaluated against `useFeatureContext()` (P0-13), so they follow the same rules as `FeatureModule`.

## Commands run and results
- `npm run build` (vad-server/web): passes (`✓ built in ~330ms`). `TransfersTable` is now its own chunk, shared by Sync, Activity and Admin.
- Discovery probe: I temporarily created `src/features/zztmp/{module.tsx,dashboard.tsx}` (one module without a `dashboardWidgets` export), and the build passed. I then deleted the probe. The first version used `import: 'dashboardWidgets'` in the glob, which would break the build for such modules, so it now uses a namespace glob.
- `npx vitest run`: 3 files, 9 tests passed.

## Contract issues
None.

## Gaps / follow-ups
- There are no unit tests for `describe`/`enabledWidgets` yet. Both are pure functions and easy to add to the vitest suite.
- The data typing is still `any`, as before. Typed `/stats` responses are left to a later task.

## Notes for dependent tasks / INT
- `pages/admin/Admin.tsx` (not owned here) still imports `TransfersTable` from `../Sync` and `describe` from `../Activity`. Both work through re-exports. Its owner should switch to `../../components/TransfersTable` and `../../lib/activity`, and the re-exports in Sync.tsx and Activity.tsx can then be removed.
- WS1 "Protection" card: add `features/backups/dashboard.tsx` with, for example, `{ id: 'backups.protection', order: 15, span: 3, component: lazy(...) }`. The value 15 puts it between stats and storage. Use span 1 or 2 to share a row.
- `describe()` now returns `''` for an event with neither `action` nor `status`. Previously it returned `undefined`, but no current caller passed such events.
