# P0-13 (WEB-0) Web feature registry; App/Layout from registry; logout clears query cache (B5)
Status: completed

## Goal
Add `web/src/app/` with a `FeatureModule` interface and registry of the existing pages; derive `App.tsx` routes and `Layout.tsx` sidebar from it with URLs and behaviour unchanged; clear the React Query cache on logout (defect B5).

## Changes (repo: vad-server)
- `web/src/app/types.ts` (created): `FeatureModule`, `FeatureRoute`, `FeatureNav`, `FeatureContext` types.
- `web/src/app/core.tsx` (created): the existing pages as modules (dashboard, files, uploads, sync, devices, automations, activity, trash, account [route only], admin [adminOnly, nav section 'admin', route `/admin/*`, nav to `/admin`]). Dashboard/Files stay eagerly imported, the rest are `lazy()` as before. Nav orders are 10, 20, ... 80.
- `web/src/app/registry.ts` (created): `modules` = core + auto-discovered `import.meta.glob('../features/*/module.tsx', { eager: true })` default exports; `featureFlags` (empty set for now); `isEnabled`, `enabledModules`, `navEntries(ctx, section)`; a DEV-only duplicate id/route check (console.error). The header comment documents how to add a module.
- `web/src/app/useFeatureContext.ts` (created): `useFeatureContext()` builds `{ isAdmin, flags }` from `useUser()`.
- `web/src/App.tsx` (modified): routes built from `enabledModules(ctx)`; catch-all `*` → `/` kept; admin route still only exists for admins.
- `web/src/components/Layout.tsx` (modified): the hard-coded `nav` array is removed. The main and "Administration" sections render from `navEntries(...)` with the same markup and classes; the Administration header shows only when admin entries exist, which matches the old admin-only behaviour. The user menu is unchanged.
- `web/src/lib/auth.tsx` (modified, B5): `AuthProvider` takes `useQueryClient()`. `logout` calls `queryClient.clear()` after the state becomes anonymous. The `onUnauthenticated` handler (session expired or revoked) also clears the cache, so a different user logging in after a 401 can't see stale data either.
- `web/src/main.tsx`: not changed. `AuthProvider` is already inside `QueryClientProvider`.

## Interfaces exposed
```ts
// web/src/app/types.ts
import type { ComponentType } from 'react'
import type { LucideIcon } from 'lucide-react'

export interface FeatureRoute {
  path: string
  component: ComponentType          // eager or React.lazy(); App wraps routes in Suspense
}

export interface FeatureNav {
  label: string
  icon: LucideIcon
  to?: string                       // defaults to routes[0].path
  order: number                     // lower first within section; core uses 10..80
  end?: boolean                     // NavLink exact match
  section?: 'main' | 'admin'        // default 'main'
}

export interface FeatureModule {
  id: string
  routes: FeatureRoute[]
  nav?: FeatureNav                  // omit for route-only modules
  adminOnly?: boolean
  requiredFeatureFlag?: string
}

export interface FeatureContext {
  isAdmin: boolean
  flags: ReadonlySet<string>
}
```
```ts
// web/src/app/registry.ts
export const modules: readonly FeatureModule[]
export const featureFlags: ReadonlySet<string>
export function isEnabled(m: FeatureModule, ctx: FeatureContext): boolean
export function enabledModules(ctx: FeatureContext): FeatureModule[]
export type NavEntry = Required<Pick<FeatureNav, 'label' | 'icon' | 'to' | 'order' | 'section'>> & { id: string; end: boolean }
export function navEntries(ctx: FeatureContext, section: 'main' | 'admin'): NavEntry[]
// web/src/app/useFeatureContext.ts
export function useFeatureContext(): FeatureContext
```
How to add a feature: create `web/src/features/<x>/module.tsx` with `export default { id, routes, nav?, adminOnly?, requiredFeatureFlag? } satisfies FeatureModule`, using `lazy()` for its pages. The glob picks it up, so App.tsx, Layout.tsx and the registry need no edits.

## Commands run and results
- `npm run build` (in `vad-server/web`): the first attempts failed with `TS6133` in `src/components/ui.tsx`, which another parallel task owns and I didn't touch. A retry passed (`✓ built in 327ms`), and the lazy chunks are still emitted separately (Sync, Devices, EditorPage, Admin, ...).
- ESLint: not run because the project has no `eslint.config.*`.

## Contract issues
None.

## Gaps / follow-ups
- There is no server-side feature-flag source yet. `featureFlags` is an empty set, so any module with `requiredFeatureFlag` stays hidden until a flag source (for example `/info` or a summary query) is wired into `registry.ts`/`useFeatureContext.ts`.
- The research doc's richer extension points (`adminTabs`, `dashboardWidgets`, `previewPanels`, nav `badge`, more nav sections) are not added yet. Extend `FeatureModule` when the first feature needs them.
- No registry contract test yet, because vitest isn't set up. Once it is, add tests for unique ids/paths and adminOnly filtering (`isEnabled`, `navEntries` are pure and easy to test).

## Notes for dependent tasks / INT
- Feature tasks should only create `web/src/features/<x>/module.tsx`. Don't edit `App.tsx` or `Layout.tsx` for routes or nav.
- Nav orders for main-section core pages are 10..80, so use in-between values (for example 25) to slot in. Admin console is order 10 in the 'admin' section.
- Route paths must be unique across modules; DEV builds log duplicates to the console.
