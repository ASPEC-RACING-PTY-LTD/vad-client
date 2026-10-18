# 03 — Web App (React, `web/`) Audit and Plan

Scope: `web/` excluding `web/src/automation/*` and `node_modules`; `internal/api/web.go` and related security headers for serving/CSP only.
Method: every file in scope was read in full. Line citations are `file:line` against the current tree. Confidence tags: **[H]** verified directly in code, **[M]** inferred from code plus known platform behaviour, **[L]** needs a spike to confirm.

---

## 1. Current state

### 1.1 Stack and build

| Item | Value | Evidence |
|---|---|---|
| React / React DOM | ^19.3.1 | `web/package.json:15-16` |
| Router | react-router-dom ^7.18.4 (declarative `<Routes>`, not the data router) | `web/package.json:17`, `web/src/App.tsx:36-51` |
| Server state | @tanstack/react-query ^5.104.3 | `web/package.json:12` |
| Graph editor | @xyflow/react ^12.12.0 (automation only, lazy loaded) | `web/package.json:13`, `web/src/App.tsx:14` |
| Icons | lucide-react ^1.57.0 | `web/package.json:14` |
| Styling | Tailwind 4 via `@tailwindcss/vite` ^4.3.3, CSS-first `@theme` | `web/package.json:20,24`, `web/src/index.css:1-18` |
| Build | Vite ^8.3.4, TypeScript ^7.0.2, `tsc -b && vite build` | `web/package.json:7-9,25-26` |
| Dev proxy | `/api` and `/healthz` → `localhost:8080` | `web/vite.config.ts:7-10` |
| Bundle | `dist/`, no sourcemaps, chunk warning raised to 1200 kB | `web/vite.config.ts:11-16` |
| TS strictness | `strict`, `noUnusedLocals`, `noUnusedParameters` | `web/tsconfig.json:8-11` |
| Embedding | `//go:embed all:dist`, `web.Dist()` | `web/embed.go:9-19`; `dist/*` gitignored except `.keep` (`web/.gitignore`) |
| CI | `npm ci && npm run build` only. No lint, no tests | `.github/workflows/ci.yml:31-46` |
| Lint/format config | none (no eslint/prettier files in `web/`) | file search |
| Web tests | none | file search |

There are no chart, table, virtualization, form or date libraries. Everything is hand-built with Tailwind.

### 1.2 Bootstrap, routing and auth

- `main.tsx` sets the theme from `localStorage['vd-theme']` or the system preference (`web/src/main.tsx:13-19`). It creates one `QueryClient` with `staleTime: 10s`, `refetchOnWindowFocus`, and retries only on 5xx or network errors, at most twice (`main.tsx:21-29`). Provider order: Query → Router → UI → Auth → Upload (`main.tsx:31-45`).
- `App.tsx` gates on auth state. It shows a loader, then Setup/Login, then the authenticated `Layout` (`App.tsx:21-33`). Eager pages: Dashboard, Files, Auth. Lazy pages: Uploads, Sync, Devices, Automations, Editor, Activity, Trash, Account, Admin (`App.tsx:10-18`). Routes are hard-coded (`App.tsx:36-51`). `/admin/*` is registered only for `role === 'admin'` (`App.tsx:49`). Unknown routes redirect to `/`.
- `lib/auth.tsx`: `AuthProvider` calls `/info`, then `/auth/me`, and on a 401 calls `/setup/status` (`auth.tsx:43-65`). The CSRF token comes from `me.user.csrf_token` (`auth.tsx:55`). A global 401 listener drops the user to anonymous (`auth.tsx:71-77`). `useUser()` throws when not authenticated (`auth.tsx:34-38`).
- **[H] Defect:** `logout()` never clears the React Query cache (`auth.tsx:84-92`; the `QueryClient` lives in `main.tsx:21` and is not reachable from auth). When another user signs in on the same tab, they can briefly see the previous user's cached data until it refetches. This becomes more serious once snapshot and credential data is cached.
- **[H] Minor:** the forced-logout path sets `registration: false` (`auth.tsx:74`), so the register option disappears until the page is reloaded.

### 1.3 API wrapper (`lib/api.ts`)

- `api(method, path, body, init)` prefixes `/api/v1`, sends JSON, and uses `credentials: 'same-origin'`. It adds `X-CSRF-Token` for every non-GET/HEAD request (`api.ts:31-41`). The server enforces this for session principals (`internal/api/server.go:463-464`).
- Errors become `ApiError{status, code, message, details, body}` parsed from `{error:{code,message,details}}` (`api.ts:3-15,51-58`). A 401 outside login/setup notifies unauth listeners (`api.ts:54-56`).
- Helpers: `get/post/put/patch/del` (`api.ts:62-66`; `post`/`put`/`patch` default the body to `{}`), `csrfHeader()` for raw `fetch` (`api.ts:68-70`), and `errorMessage()` (`api.ts:72-76`).
- Gaps: everything is `any`-typed (`api<T = any>`). There are no typed endpoint modules and no query-key factory. Query keys are ad-hoc strings scattered across pages (for example `['files', parent, ...]` at `Files.tsx:73`, `['admin-storage']` at `Admin.tsx:114,330`, `['devices']` at both `Layout.tsx:110` and `Devices.tsx:95`). There are 66 `any` usages outside `automation/` (grep).

### 1.4 React Query patterns

- Reads use `useQuery` with inline `queryFn: () => get(...)`. There is **no `useMutation` anywhere** (grep). Writes are imperative `try { await post(); toast(); qc.invalidateQueries() } catch { toast('error') }` (for example `Files.tsx:143-150`, `Admin.tsx:122-135`, `Trash.tsx:18-26`).
- Live data is polled. Nothing uses SSE or WebSocket (grep). Intervals: sync overview 5 s (`Sync.tsx:151`), devices 5 s (`Devices.tsx:95`), pending-device banner 10 s on **every page** (`Layout.tsx:110`), admin uploads 5 s (`Admin.tsx:518`), transfers 10 s (`Sync.tsx:14`), notifications 30 s (`Layout.tsx:45`), stats 60 s (`Layout.tsx:134`).
- Pagination: only Activity uses `useInfiniteQuery` with an id cursor `before=` (`Activity.tsx:53-59`). Files loads up to the server cap (store default 5000 rows, `internal/store/nodes.go:120-123`) and only shows "Showing X of Y" with no paging (`Files.tsx:486`). Search is capped at `limit=500` (`Files.tsx:76`).

### 1.5 Upload manager (`lib/uploads.tsx`)

- It is a context provider that survives navigation. It uses the server's session protocol: `POST /uploads` → `PUT /uploads/{id}/chunks/{idx}` → `POST /uploads/{id}/complete` (`uploads.tsx:72,97,130`). A deduplicated create short-circuits on `status === 'complete'` (`uploads.tsx:73-76`).
- Concurrency is 3 files × 3 chunks (`uploads.tsx:40-41`). Each chunk is retried up to 8 times with exponential backoff capped at 30 s on 5xx, 429, 422 and network errors. A 507 (insufficient storage) is not retried (`uploads.tsx:94-114`). Speed uses an EMA (`uploads.tsx:116-125`). Pause aborts in-flight chunks (`uploads.tsx:166-169`). Cancel deletes the server session (`uploads.tsx:171-177`). A `beforeunload` guard runs while busy (`uploads.tsx:156-164`).
- Resume only works within the same tab: state is in memory (`uploads.tsx:44-46`). `File` handles are lost on reload. The Uploads page can list other server sessions but can only cancel them (`Uploads.tsx:67-83,130-133`).
- Reuse value: the chunk worker and retry policy are the right template for any browser-side transfer, such as restore-to-browser downloads or importing a recovery bundle. Requirements 2 and 9 assign the backup data path to the desktop, not to the browser.

### 1.6 Shared UI kit: web vs desktop

- **[H] `web/src/components/ui.tsx` and `desktop/frontend/src/ui.tsx` are byte-identical (409 lines, `diff` prints nothing). `web/src/index.css` and `desktop/frontend/src/index.css` are also byte-identical (94 lines).**
- `web/src/lib/format.ts` and `desktop/frontend/src/lib.ts` are near-duplicates that have drifted. The desktop version has no `PB` unit, a different `timeAgo` (no locale date fallback past 30 days), and `errMsg` where web has `errorMessage` (`desktop/frontend/src/lib.ts:1-34` vs `web/src/lib/format.ts:1-66`).
- Kit contents (`ui.tsx`): `cx`, `Button` (5 variants, 3 sizes, loading), `IconButton`, `Spinner`, `PageLoader`, `Badge` (6 tones), `PageHeader`, `EmptyState`, `ErrorState`, `ProgressBar` (incl. indeterminate), `Toggle`, `Field`, `Tabs`, `Stat`, `Modal` (portal, Esc, body scroll lock, 5 sizes), `UIProvider` with `toast()` and promise-based `confirm()` including a **type-to-confirm `requireText`** (`ui.tsx:225-313`), `Menu`/`ContextMenu` (viewport-clamped portal), `CopyButton`. CSS components: `.card`, `.input`, `.label`, `.kbd`, `.scroll-thin`, React Flow theme, indeterminate animation (`index.css:36-94`).
- Missing for the new UI: data table (sorting, column config, row selection, virtualization), stepper/wizard, tree view, date/time range picker, cron or schedule editor, tag/chip input (exists only inline in `Files.tsx:647-728`), list input (exists only in desktop `Configs.tsx` as `ListInput`), diff/plan viewer, simple bar/area chart, Modal focus trap, keyboard navigation in Menu.
- Desktop frontend dependencies are a strict subset of web's: no react-query, no router (`desktop/frontend/package.json:11-23`). Same Tailwind/Vite/TS versions.

### 1.7 Pages

| Route | File (lines) | Function |
|---|---|---|
| `/` | `pages/Dashboard.tsx` (170) | Greeting, 4 stats (`/stats`), storage-by-kind CSS bar, sync summary (`/sync/overview`), recent files, automation CTA |
| `/files`, `/files/:folderId` | `pages/Files.tsx` (871) | File manager, see 1.8 |
| `/uploads` | `pages/Uploads.tsx` (139) | Browser upload queue and server resumable sessions (cancel only) |
| `/sync` | `pages/Sync.tsx` (205) | **Read-only** per-device sync status from `d.sync_state` reported by the desktop (`Sync.tsx:61-147`), 24 h stats, recent failures, `TransfersTable` (exported and reused by Admin and Activity) |
| `/devices` | `pages/Devices.tsx` (260) | Pending approval cards with verification code and countdown, approve/reject/revoke/rename/remove history |
| `/automations` | `pages/Automations.tsx` (182) | List, enable toggle, duplicate, export, templates, import (uses `automation/*`) |
| `/automations/new`, `/:id` | `automation/EditorPage.tsx` | Out of scope (other agent) |
| `/activity` | `pages/Activity.tsx` (105) | Infinite activity log and transfer history. Exports `describe()` used by Admin |
| `/trash` | `pages/Trash.tsx` (89) | Restore, purge, empty (type "delete" to confirm). Replaced sync versions land here |
| `/account` | `pages/Account.tsx` (149) | Profile, password, browser sessions |
| `/admin/*` | `pages/admin/Admin.tsx` (793) and `Updates.tsx` (156) | See 1.9 |
| (anonymous) | `pages/Auth.tsx` (141) | Login/register, first-run setup |

Navigation is a static array of 8 entries plus an admin link (`Layout.tsx:11-20,148-156`). The header has global search that navigates to `/files?search=` (`Layout.tsx:190-199`), the upload indicator, theme toggle, notification bell (polls `/notifications`, mark-all-read only, `Layout.tsx:42-91`) and user menu.

### 1.8 Files page features (`Files.tsx`)

- Listing: `/files?parent=&sort=&order=&kind=` or `/files/search?q=...&limit=500` (`Files.tsx:72-78`). List and grid views are persisted in localStorage (`:49,65`). Client-side "filter this folder" (`:79-84`). Kind select (`:351-360`). Sort menu (`:361-370`).
- Selection: click, ctrl/cmd, shift-range, select-all checkbox, Ctrl+A, Esc, F2 rename, Enter open, Delete trash (`:111-173`).
- **Bulk operations already present:** zip download (`GET /files/zip?ids=` at `:29-38`), move (`POST /files/move` with a partial `failed[]` report at `:193-203`), trash (`POST /files/delete` with `failed[]` at `:134-153`). Drag-and-drop move onto folders and breadcrumbs (`:269-273,422-435`).
- Upload: drag-drop including folders (`filesFromDrop` in `uploads.tsx:188-213`), file and folder pickers (`:299-318`).
- Tags: per-file edit modal with suggestions from `/files/tags` (`:647-728`). Up to 3 tag badges per row (`:457-461`). **There is no tag filter in the UI** and folders cannot be tagged (`:222`).
- Preview modal (`:803-869`): image (except SVG), video and audio via `<video src>` with browser HTTP Range streaming (`:851-857`), PDF via `<iframe>` (`:858`), text via a ranged `fetch` of the first 512 KiB (`:785-801`), prev/next with arrow keys. Grid thumbnails load the **full original image** for anything under 25 MB, and `<video preload=metadata>` when the folder has 60 items or fewer (`:503`). There is no thumbnail endpoint.
- **Advanced search is supported by the server but not exposed:** `/files/search` accepts `tag`, `min_size`, `max_size`, `modified_after`, `modified_before` (`internal/api/files.go:336-339`). The UI sends only `q`, `sort`, `kind` and `limit`.
- No version history. With `keep_replaced_versions`, old versions only appear as trash items (`Admin.tsx:622`, `Trash.tsx:56`).
- Dead or odd exports: `MoveModal` is exported but used only in this file. `export { del }` at `:871`.

### 1.9 Admin console (`pages/admin/Admin.tsx`)

There are nine tabs from a static array (`Admin.tsx:13-23`) with nested `<Routes>` (`:779-790`), all in one 793-line file:

- Overview: stats, logical vs physical (dedup) bytes, location bars, recent audit (`:44-86`).
- Users: CRUD, quota, location, reset password, revoke sessions, recalculate usage, delete with type-the-username (`:109-325`).
- Storage: local directory locations under allowed roots, reserve, default and enabled flags, remove only when empty (`:327-445`). **This is the natural parent for storage providers (req 6).**
- Devices: list and revoke (`:447-512`).
- Transfers: active uploads (cancel) and failed or all transfers (`:514-571`).
- Settings: site, registration, quotas, max file size, min free space, trash and activity retention, device TTL, keep replaced versions, read-only env config (`:573-643`). **[H] Bug:** `if (isLoading || !s) return <PageLoader/>` runs before the error check (`:581-582`), so a failed load spins forever.
- Health: status, integrity check (`?deep=1`), "Run maintenance" (`POST /admin/maintenance/cleanup`), memory, DB and chunk slots (`:645-709`). **This is the existing hook for a GC UI.**
- Updates (`Updates.tsx`) and Audit (`:711-765`).

Cross-page coupling: Admin imports `TransfersTable` from `pages/Sync` and `describe` from `pages/Activity` (`Admin.tsx:9-10`).

### 1.10 Sync and Devices pages

- Sync is display-only. It has no rulesets, exclusions, schedules, conflicts or version history in web. Folder configuration (include/exclude patterns, extensions, subfolders, mode, stability) exists **only in the desktop UI** (`desktop/frontend/src/screens/Configs.tsx:21-33,283-345`) and reaches the server only as reported `sync_state.configs` (`Sync.tsx:64`). **[H] The web app has no channel to edit a device's configuration today.** Requirement 5 web editing needs a server-held config with device pull (see section 7).
- The Sync page says "retry from the app's Transfers view" (`Sync.tsx:175`). The web app has no remote retry.

### 1.11 CSP and preview constraints

- SPA document CSP (`internal/api/web.go:40`): `default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`.
- Global headers: `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin`, `COOP: same-origin` (`internal/api/server.go:306-321`). File content responses override this with `X-Frame-Options: SAMEORIGIN` plus `CSP: default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox` (`internal/api/files.go:419-422`). Only `inlineSafe` types render inline: video, audio, raster images, pdf, plain/markdown/csv/json/yaml/vtt. Everything else downloads (`files.go:351-361`). Range requests are handled by `http.ServeContent` (`files.go:428-430`).
- Implications for new UI:
  - **[H]** `connect-src 'self'` stops the browser from calling AI providers (OpenAI/Anthropic) or S3 endpoints directly. All AI and provider calls must be **proxied by the server**, which is also correct for key custody.
  - **[H]** `script-src 'self'` without `'wasm-unsafe-eval'` blocks WebAssembly compilation. Client-side zstd decompression or non-WebCrypto ciphers (for example XChaCha20) in the browser would need a CSP change. WebCrypto AES-GCM and pure-JS libraries work as is. This matters for requirement 9 (client-side encryption): if snapshots are zero-knowledge encrypted, web preview and download of snapshot contents needs either in-browser decryption or a device-mediated restore. The server cannot decrypt.
  - **[M]** The PDF iframe is served with CSP `sandbox` (no `allow-scripts`). Chromium's built-in PDF viewer is known to refuse rendering in sandboxed documents. PDF preview may already show a blocked page in Chrome/Edge. **[L] Verify in a browser** before reusing this preview in the snapshot browser.
  - **[H]** No `worker-src`, so it falls back to `default-src 'self'`. Same-origin Web Workers (for example hashing or a manifest parser) are allowed. Blob workers are not.
  - SVG and HTML are never inlined. Keep that rule for snapshot previews too: backups will contain arbitrary app data.

---

## 2. Reuse map (requirements 5, 7, 8, 10 emphasised)

| Req | Need | Reuse as-is | Extend / extract |
|---|---|---|---|
| 7 browse | Snapshot tree browser | `FileIcon`, breadcrumb pattern, list/grid views, selection model, `ContextMenu` (`Files.tsx:111-173,262-280,403-525`) | Extract the table, grid and selection into `components/files/*` with the content-URL and data source passed as props, so the same components browse a live tree **or** a snapshot tree |
| 7 preview | Preview snapshot files | `PreviewModal` and `TextPreview` (`Files.tsx:785-869`), Range streaming via `<video src>` | Make `contentURL` injectable. The server must serve snapshot content with the same `inlineSafe` and CSP rules |
| 7 search/filter | Advanced filtering | Server search params already exist (`files.go:336-339`) | Add a filter bar (tag, size range, date range, kind incl. pdf/text which are missing from the select at `Files.tsx:351-360`), and reuse it for snapshot search |
| 7 download | Single and bulk download | `downloadIDs` (`Files.tsx:29-38`) | GET with an `ids` list will hit URL limits for large selections. Switch to POST → download token → GET |
| 7 bulk ops | Partial-failure reporting | `{moved/deleted, failed[]}` handling (`Files.tsx:145,196`) | Standardise as a `BulkResult` type and a `<BulkResultToast>` |
| 7 retention/GC | Dry-run, then execute with strong confirmation | `confirm({requireText})` (`ui.tsx:225-310`), automation `DryRunModal` pattern (`automation/Modals.tsx:273`) | New `PlanPreview` component (counts, bytes reclaimed, per-snapshot keep/delete reasons) |
| 7 analytics | Storage charts | Dashboard CSS stacked bar (`Dashboard.tsx:54-73`), `LocationBar` (`Admin.tsx:88-107`), logical vs physical stat (`Admin.tsx:54`) | Extract `LocationBar` and the kind bar into `components/charts/*`. Plain SVG bars are enough at first, so no chart library is needed |
| 5 sync | Folders, rulesets, conflicts, versions | `DeviceSync` status card (`Sync.tsx:61-147`), `TransfersTable` (`Sync.tsx:10-59`) | Port desktop `Configs.tsx` field set (patterns, extensions, subfolders, mode) to a shared `RulesetEditor`. Needs a server-side config store (section 7) |
| 5 versions | Version history and conflict UI | Trash restore flow (`Trash.tsx:18-26`) | New version timeline panel in the preview modal (restore, download, compare metadata) |
| 8 automations | Backup schedules, retention, verification and recovery nodes | Catalog-driven node palette and `ParamEditor` from `/automations/reference` (owned by the automation agent) | New node kinds should come from the server catalog so this agent's UI needs no change. Expose shared pickers (destination, snapshot, job) as `ParamSpec` types the automation agent can render |
| 8 AI | User API keys, approval gates | `confirm()` type-to-confirm, notification bell (`Layout.tsx:42-91`) | Approvals inbox page and badge. Keys are write-only fields (never echoed back) |
| 10 progress | Transparent job progress | `ProgressBar` (incl. indeterminate), `formatSpeed/Duration`, `DeviceSync` stats grid (`Sync.tsx:84-99`) | `JobProgressCard` (per-job phase, bytes, files, ETA, upload queue depth) |
| 10 notifications | Configurable notifications | Bell, read/unread, levels (`Layout.tsx:51,73-84`) | Settings page: channels and per-event rules |
| 10 readiness | Recovery-readiness report | `Stat`, `Badge` tones, Health tab layout (`Admin.tsx:653-708`) | Per-device scorecard and checklist |
| 10 first-run | Simple setup | `Auth.tsx` Shell layout, Devices approval flow | Onboarding wizard reusing the Stepper |
| 4 errors | Skip / skip-all / retry-skipped | Recent-failures banner (`Sync.tsx:163-177`), failed transfer list | Job error table with bulk "retry skipped" and per-item reason |

---

## 3. Required UI: routes, pages, components, hooks, navigation

### 3.1 Navigation changes

Replace the flat nav (`Layout.tsx:11-20`) with **sections** declared by a registry (see 6.2):

- Main: Dashboard, Files, Uploads, Activity, Trash (unchanged).
- **Protection** (new): Backups, Snapshots, Apps, Restore, Readiness.
- **Sync**: Synchronisation (existing), Sync folders, Conflicts.
- **Storage** (new): Destinations, Policies, Retention, Analytics.
- Automation: Automations, Approvals (badge with pending count).
- Settings (user menu and nav): Account, Notifications, AI.
- Administration: Admin console. New admin tabs are GC, Providers (server-wide) and Backup overview (all users).

The Dashboard gains a "Protection" card (last successful backup per device, readiness score). Its owner is the Backups workstream through a dashboard widget slot (6.2), so Dashboard.tsx is not edited by several streams.

### 3.2 Routes and pages

All new pages are lazy-loaded. Paths are proposals and depend on server resource naming (section 7).

| Route | Page file (new) | Purpose |
|---|---|---|
| `/backups` | `features/backups/pages/BackupsOverview.tsx` | Devices × jobs matrix, last run status, next schedule, running jobs with live progress |
| `/backups/devices/:deviceId` | `features/backups/pages/DeviceBackups.tsx` | Jobs for a device (app/folder/project/game/registry/inventory), enable/schedule, run now |
| `/backups/jobs/:jobId` | `features/backups/pages/JobDetail.tsx` | Run history, current run progress (phase, files, bytes, chunks deduped, upload queue), **errors/warnings table with skip, skip-all-of-kind and Retry Skipped**, completion summary |
| `/backups/runs/:runId` | `features/backups/pages/RunSummary.tsx` | Detailed completion summary. Exportable as JSON |
| `/snapshots` | `features/snapshots/pages/SnapshotList.tsx` | Filter by device, job, time range, tags, protected flag. Timeline view. Compare two snapshots (added/changed/removed counts) |
| `/snapshots/:snapshotId/*` | `features/snapshots/pages/SnapshotBrowser.tsx` | Tree browser (reusing `components/files`), preview, download, search within snapshot, select → "Restore selected…" |
| `/apps` | `features/apps/pages/AppsView.tsx` | Backed-up apps per device: install source (winget/choco/msi/store/manual), mode (full-data / data-only / reinstall-inventory), data size, last snapshot, restore capability flags |
| `/restore` | `features/restore/pages/RestoreWizard.tsx` | Steps: 1 source (device/snapshot/time or "latest before T") → 2 selection (apps, folders, files) → 3 target device → 4 **destination mapping** (auto-detected profile/user path changes, per-root overrides, conflict policy: skip/overwrite/rename/newer) → 5 **dry-run plan** (counts, bytes, conflicts, items needing reinstall, ACL/SID notes) → 6 confirm and queue |
| `/restore/:restoreId` | `features/restore/pages/RestoreProgress.tsx` | Device-side execution progress, per-item results, retry failed |
| `/readiness` | `features/readiness/pages/ReadinessReport.tsx` | Per device: last good snapshot age, coverage (apps/profile/registry), verification status, untested restore age, encryption key escrow status, destination redundancy. Printable |
| `/storage/destinations` | `features/storage/pages/Destinations.tsx` | User-scoped destinations: local, SMB/NAS, S3/R2/B2, custom. Credential form (write-only secrets, "test connection"), health, capacity |
| `/storage/policies` | `features/storage/pages/Policies.tsx` | Replication policies: primary plus N copies, per-job assignment, replication lag and status |
| `/storage/retention` | `features/storage/pages/Retention.tsx` | Rule editor (keep last N, hourly/daily/weekly/monthly/yearly, keep within duration, by tag, protected snapshots never pruned). **Dry-run preview** of which snapshots go and bytes reclaimed (dedup-aware) |
| `/storage/analytics` | `features/storage/pages/Analytics.tsx` | Logical vs physical, dedup ratio, growth over time, top jobs/apps by unique bytes, per destination |
| `/sync/folders` | `features/sync/pages/SyncFolders.tsx` | Server-held sync folder and watched-folder definitions per device, rulesets, exclusions, schedules (requires server config, section 7) |
| `/sync/rulesets` | `features/sync/pages/Rulesets.tsx` | Reusable rulesets (patterns, extensions, size, age) |
| `/sync/conflicts` | `features/sync/pages/Conflicts.tsx` | Conflict list: keep local/remote/both, bulk resolve |
| (panel) | `features/sync/components/VersionHistoryPanel.tsx` | Mounted inside the file preview: versions list, restore version, download |
| `/approvals` | `features/approvals/pages/Approvals.tsx` | Pending destructive operations proposed by automations or AI: plan, requester, expiry, approve/reject (type-to-confirm for high risk) |
| `/settings/notifications` | `features/settings/pages/Notifications.tsx` | Channels (in-app, email/webhook if server supports them), per-event rules (backup failed, skipped > N, readiness degraded, quota, replication lag), quiet hours |
| `/settings/ai` | `features/settings/pages/AISettings.tsx` | Provider, model, write-only API key, usage scope toggles (diagnostics, recommendations), "require approval for destructive" (always on, not disableable) |
| `/admin/gc` | `features/storage/admin/GcTab.tsx` | Dedup-aware GC: mark phase stats, dry-run (unreferenced blobs and bytes per location), run with progress, last-run report. Extends the Health "Run maintenance" (`Admin.tsx:664-681`) |
| `/admin/providers` | `features/storage/admin/ProvidersTab.tsx` | Server-wide provider allow-list and limits (only if section 7 makes providers admin-gated) |
| `/admin/backups` | `features/backups/admin/BackupsTab.tsx` | All users' job health |
| `/setup/backup` | `features/onboarding/pages/BackupSetup.tsx` | First-run wizard: pick device → recommended jobs → destination → schedule → done |

### 3.3 Shared components (new, owned by WS0 unless stated)

`components/data/DataTable.tsx` (sortable columns, selection, sticky header, optional virtualization), `components/data/FilterBar.tsx` (chips: kind, tag, size, date range, device, job), `components/Stepper.tsx` (wizard shell with per-step validation), `components/TreeView.tsx` (lazy children, checkboxes with tri-state for selective restore), `components/PlanPreview.tsx` (dry-run plan: summary stats, grouped item list, warnings, "execute with plan token"), `components/ScheduleEditor.tsx` (presets plus cron with a human-readable preview of the next 5 runs; the server computes next runs), `components/ListInput.tsx` and `components/TagInput.tsx` (extracted from desktop `Configs.tsx` and `Files.tsx:647-728`), `components/SecretField.tsx` (write-only, "set / replace / clear", never displays the stored value), `components/charts/{BarStack,Sparkline,LocationBar}.tsx` (SVG, no library), `components/JobProgress.tsx`, `components/BulkResultToast.tsx`, `components/TransfersTable.tsx` (moved from `Sync.tsx`), `lib/activity.ts` (`describe()` moved from `Activity.tsx`).

### 3.4 API hooks (per feature `api.ts`)

Each feature owns `features/<x>/api.ts` exporting typed query-key factories and hooks, for example:

```ts
export const snapshotKeys = { all: ['snapshots'] as const, list: (f: SnapshotFilter) => [...snapshotKeys.all, 'list', f] as const, tree: (id: string, path: string) => [...snapshotKeys.all, id, 'tree', path] as const }
export function useSnapshots(f: SnapshotFilter) { return useInfiniteQuery({ queryKey: snapshotKeys.list(f), queryFn: ({ pageParam }) => get<Page<Snapshot>>(`/snapshots?${qs({ ...f, cursor: pageParam })}`), initialPageParam: '', getNextPageParam: (p) => p.next_cursor || undefined }) }
```

The hook list is in section 7, next to the endpoints. Writes use `useApiMutation` (WS0 helper in `lib/query.ts`: toast on error, invalidates the given keys, and surfaces `ApiError.details` field errors).

Live progress: start with polling (2 s while a run is active, otherwise 30 s) through a shared `useLiveQuery(key, fn, {activeWhen})`. Add optional SSE later (`/api/v1/events`, which `connect-src 'self'` permits) behind the same hook, so pages don't change.

---

## 4. Tech debt and recommended prep refactors

### 4.1 Debt that will hurt

1. **`Files.tsx` (871 lines)** mixes page state, table, grid, five modals and preview. The snapshot browser needs the table, grid, selection and preview without the live-tree mutations. **Extract before building the snapshot browser.** [H]
2. **`Admin.tsx` (793 lines)** has a static `tabs` array (`:13-23`) and inline routes (`:779-790`). Every new admin tab (GC, providers, backups) would edit the same two blocks: a merge-conflict hotspot. **Split per tab and add registration.** [H]
3. **Hard-coded route table and nav** (`App.tsx:36-51`, `Layout.tsx:11-20`). This is the same hotspot problem for 20+ new routes. [H]
4. **Untyped API (`any`)**, no key factory, no mutations abstraction. With roughly 60 new endpoints this will produce cache-invalidation bugs. [H]
5. **Cross-page imports** (`Admin.tsx:9-10` importing from `pages/Sync` and `pages/Activity`). Move them to `components/` and `lib/`. [H]
6. **Duplicated UI kit with desktop** (identical copies today; `format.ts` and `lib.ts` already drifted). New components (Stepper, SecretField, ListInput) are needed by **both** apps, since the desktop needs onboarding, restore mapping and credentials too. Two copies will diverge. [H]
7. **No tests, no lint** in web CI (`ci.yml:31-46`). [H]
8. **Polling fan-out**: `PendingDevicesBanner` polls every 10 s on every page (`Layout.tsx:110`). Adding backup progress, approvals badge and notifications as more global pollers will multiply requests. Consolidate into one `/api/v1/summary` (or SSE) call that feeds badges. [M]
9. **No list virtualization.** Files renders up to 5000 DOM rows. Snapshot trees (AppData, node_modules-like trees) will exceed this. Needs server pagination plus a virtualized table. [H]
10. **Grid thumbnails fetch originals** (`Files.tsx:503`). This is unacceptable over remote destinations or for multi-GB snapshot browsing. Needs a server thumbnail endpoint or icon-only for snapshots. [H]
11. Accessibility: `Modal` has no focus trap and `Menu` has no arrow-key navigation (`ui.tsx:187-220,319-385`). Wizards and approval dialogs should be keyboard-safe. [H]
12. Logout cache leak (`auth.tsx:84-92`) and Settings tab infinite spinner (`Admin.tsx:581-582`). Small fixes, best done in WS0. [H]

### 4.2 Shared UI package between web and desktop: feasibility

Facts: same React 19, Tailwind 4, Vite 8 and TS 7 versions. `ui.tsx` and `index.css` are identical. The desktop has no router or react-query, and `ui.tsx` uses neither (it imports only `react`, `react-dom`, `lucide-react`; `ui.tsx:1-3`). Wails runs `npm install` and `npm run build` inside `desktop/frontend` (`desktop/wails.json`). CI builds each frontend in its own working directory with its own lockfile (`ci.yml:37-46,63-72`).

Options:

- **A. Repo-level npm workspace** (`/package.json` with `workspaces: ["web","desktop/frontend","packages/ui"]`). This is the cleanest long term, but it changes how Wails and CI install (one root lockfile, `npm ci` at root). Both workflows and the Docker build (`Dockerfile`, not audited here) would need changes. Risk: Wails' `frontend:install` runs `npm install` in `desktop/frontend` and will not hoist correctly without root-level install. **[M] Feasible, medium effort, touches build infra owned by others.**
- **B. Source-shared folder with Vite alias** (`packages/ui/src/*.tsx` imported via `resolve.alias`, `@source "../../packages/ui/src"` in each `index.css` so Tailwind 4 scans the classes). Pitfall: bare imports inside the shared folder (`react`, `lucide-react`) resolve from the shared file's location upward and **will not find** `web/node_modules`. Each app needs `resolve.dedupe: ['react','react-dom','lucide-react']` and aliases for those packages, plus `server.fs.allow` for dev, and the tsconfig `include`/`paths`. **[M] Feasible, low infra impact, slightly fragile.**
- **C. Keep copies plus a parity check** (script compares `web/src/components/ui.tsx` ↔ `desktop/frontend/src/ui.tsx` and the `index.css` pair, and fails CI on drift). **[H] Trivial and zero risk.**

**Recommendation:** do **C immediately** in WS0 to stop drift during the parallel build. Then do **B** for *new* cross-app components only (`packages/ui` holding Stepper, SecretField, ListInput, TagInput, PlanPreview, Format), after a one-day spike that proves a Wails production build works. Defer A unless the desktop and server teams want a monorepo install. Do not block web feature work on the shared package. Web-only components go in `web/src/components` first and move later.

### 4.3 Test tooling recommendation

- **Unit/component:** Vitest plus @testing-library/react plus `happy-dom` (or jsdom), and **MSW** for API mocking at the `fetch` layer. This exercises `lib/api.ts` CSRF and error parsing unchanged. **[M] Check Vitest's compatibility with Vite 8 and TS 7 at install time**: Vitest transpiles with Vite's own pipeline, not `tsc`, so TS 7 should not matter, but the version pairing must be checked.
- **Type check:** keep `tsc -b` (already in `build`). Add `npm run typecheck` to CI as a separate step (the script exists at `package.json:9`).
- **E2E:** Playwright against the real Go binary with the embedded `dist` (seeded test admin from a fixture, local host only). Cover login, files, snapshot browse, restore wizard dry-run, retention dry-run, approvals. Run it in a CI job after the web build.
- **Lint:** ESLint flat config with `typescript-eslint`, `react-hooks` (several effects omit dependencies, for example `Admin.tsx:30`, `Files.tsx:156-173`) and `jsx-a11y`.

---

## 5. Limitations and risks

| # | Risk | Confidence |
|---|---|---|
| 1 | The web app cannot configure devices today. Sync/backup config lives on the desktop and only status is reported (`Sync.tsx:61-66`). Web editing of jobs, folders or restore requires a server-side desired-state store and device pull. | H |
| 2 | Restore executes on a device. The web app can only author a plan and observe it. Restore to a reinstalled machine requires that device to be enrolled and approved first (Devices flow). Plan the wizard so step 3 deep-links to device approval. | H |
| 3 | Zero-knowledge client-side encryption (req 9) makes server-side preview, search-by-content and download impossible unless the browser decrypts. That needs WebCrypto-compatible ciphers or a CSP change for WASM (`web.go:40`). Metadata (names, sizes) must also be decided: encrypted names would break the server-rendered snapshot tree. **Cross-team decision.** | H (CSP fact), M (design impact) |
| 4 | AI and provider calls cannot originate in the browser (`connect-src 'self'`). All must be server-proxied. This is fine and desirable. | H |
| 5 | PDF preview may be blocked in Chromium due to `sandbox` in the content CSP (`files.go:419`). | L, verify |
| 6 | Large trees: no virtualization, a 5000-row cap, and zip via GET query string. Snapshot browsing needs cursor pagination and POST-based download tokens. | H |
| 7 | Polling cost multiplies with new live views. Without a summary or SSE endpoint, expect many requests per tab. | M |
| 8 | Merge-conflict hotspots (`App.tsx`, `Layout.tsx`, `Admin.tsx`, `Files.tsx`, `lib/api.ts`) if parallel workstreams start before WS0 lands. | H |
| 9 | Dry-run vs execute divergence: if execute recomputes the plan, the user approves one thing and gets another. Require a `plan_token` (section 7). | M |
| 10 | Credentials UI: secrets must never be returned by GET. Today's admin settings echo all values (`Admin.tsx:575-580`). That is fine for those settings, but the pattern must not be copied for credentials. | H |
| 11 | Vite 8, TS 7 and Tailwind 4 are recent majors. Test tooling and shared-package configs may need version pinning. | M |
| 12 | Dashboard and Layout are shared real estate. Without slots, every stream will want to add a card or badge. | H |

---

## 6. Implementation workstreams (exclusive file ownership)

Ordering: **WS0 must merge first.** It owns every hotspot and leaves registration points behind. After WS0, workstreams WS1–WS6 run in parallel and only **create** files in their own folders, plus the existing files explicitly handed to them below. "Handoff" means WS0 edits the file in phase 0 and ownership then moves; no two streams edit a file concurrently.

### 6.1 WS0 — Web foundation and prep refactors (single owner of all hotspots)

Modifies (exclusive):
- `web/src/App.tsx`: build `<Routes>` from the registry (6.2), keeping existing routes.
- `web/src/components/Layout.tsx`: nav sections from the registry, header badge slots, consolidated summary poller.
- `web/src/main.tsx`: export the `queryClient` for auth.
- `web/src/lib/auth.tsx`: `queryClient.clear()` on logout and forced logout; keep `registration`.
- `web/src/lib/api.ts`: add `Page<T>`, `BulkResult`, `qs()` helper and typed `ApiError.details` (field errors). Behaviour otherwise unchanged. **After WS0, `lib/api.ts` is frozen.** Features add endpoints only in their own `api.ts`.
- `web/src/lib/format.ts`: add `formatDateRange`, `formatPercent`, `formatCount`.
- `web/src/components/ui.tsx`: Modal focus trap, Menu keyboard navigation. Mirror the change to the desktop copy is **not** done here (another agent owns the desktop); the parity check flags it.
- `web/src/index.css`: `@source` for future shared packages (no-op for now).
- `web/src/pages/Files.tsx`: shrink to page orchestration (handoff to WS5).
- `web/src/pages/admin/Admin.tsx`: shell plus registry (handoff: frozen after WS0).
- `web/src/pages/Sync.tsx`: remove `TransfersTable` (handoff to WS4).
- `web/src/pages/Activity.tsx`: remove `describe` (stays WS0).
- `web/src/pages/Dashboard.tsx`: add widget slot rendering (stays WS0).
- `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`: test, lint and typecheck scripts and dev dependencies.
- `.github/workflows/ci.yml` web job: add `typecheck`, `lint`, `test`. **Coordinate with the CI owner. If CI is owned elsewhere, deliver this as a requested change.**

Creates: `web/src/app/registry.ts`, `web/src/app/types.ts`, `web/src/lib/query.ts` (`useApiMutation`, `useLiveQuery`), `web/src/lib/activity.ts`, `web/src/components/TransfersTable.tsx`, `web/src/components/files/{FileTable,FileGrid,useSelection,PreviewModal,TextPreview,FolderPicker,TagsModal,RenameModal,NewFolderModal}.tsx`, `web/src/pages/admin/tabs/{Overview,Users,Storage,Devices,Transfers,Settings,Health,Audit}.tsx` plus `web/src/pages/admin/LocationBar.tsx`, `web/src/components/{Stepper,TreeView,PlanPreview,ScheduleEditor,ListInput,TagInput,SecretField,JobProgress,BulkResultToast}.tsx`, `web/src/components/data/{DataTable,FilterBar}.tsx`, `web/src/components/charts/{BarStack,Sparkline}.tsx`, `web/vitest.config.ts`, `web/src/test/{setup.ts,server.ts,render.tsx}`, `web/eslint.config.js`, `web/e2e/*` (Playwright skeleton), `scripts/check-ui-parity.sh` (or `.ps1`; it reads only, never edits the desktop).

Also fixes: Settings tab error ordering (`Admin.tsx:581-582` moves into `tabs/Settings.tsx`), kind filter adds pdf/text.

### 6.2 Registration pattern (removes hotspots)

`web/src/app/registry.ts` collects feature modules with Vite's `import.meta.glob`. **No shared list file to edit:**

```ts
// app/types.ts
export type FeatureModule = {
  id: string
  routes?: { path: string; element: React.ReactNode; admin?: boolean }[]
  nav?: { section: 'main'|'protection'|'sync'|'storage'|'automation'|'settings'; to: string; label: string; icon: LucideIcon; order: number; badge?: () => number | undefined }[]
  adminTabs?: { to: string; label: string; icon: LucideIcon; order: number; element: React.ReactNode }[]
  dashboardWidgets?: { id: string; order: number; element: React.ReactNode }[]
  previewPanels?: { id: string; order: number; render: (node: { id: number; kind: 'live'|'snapshot' }) => React.ReactNode }[]
}
// app/registry.ts
const mods = import.meta.glob<{ default: FeatureModule }>('../features/*/module.tsx', { eager: true })
export const features = Object.values(mods).map((m) => m.default)
```

Route elements are `lazy()` inside each `module.tsx`, so code splitting is preserved. Badge counts come from the consolidated summary query in Layout (WS0) by key, so features don't add global pollers. Existing core pages stay hard-wired in `App.tsx`.

### 6.3 Feature workstreams (after WS0)

| WS | Scope (reqs) | Exclusive existing files | Creates (all new) |
|---|---|---|---|
| **WS1 Backups & Readiness** | Job list, job detail, progress, errors with skip/retry-skipped, run summaries, apps view, readiness report, admin backup tab, dashboard protection widget, onboarding (1, 2, 4, 10) | none | `web/src/features/backups/**` (`module.tsx`, `api.ts`, `types.ts`, `pages/*`, `components/*`, `admin/BackupsTab.tsx`, `widgets/ProtectionCard.tsx`, `*.test.tsx`), `web/src/features/apps/**`, `web/src/features/readiness/**`, `web/src/features/onboarding/**` |
| **WS2 Snapshots & Restore** | Snapshot list, compare, browser (reusing `components/files`), selective restore wizard, mapping, dry-run, restore progress (3, 7) | none | `web/src/features/snapshots/**`, `web/src/features/restore/**` |
| **WS3 Storage** | Destinations and credentials, replication policies, retention editor with dry-run, analytics, admin GC and providers tabs (6, 7) | none | `web/src/features/storage/**` (incl. `admin/GcTab.tsx`, `admin/ProvidersTab.tsx`) |
| **WS4 Sync extensions** | Server-held sync folders, rulesets, exclusions, schedules, conflicts, version history panel (5) | `web/src/pages/Sync.tsx` (after WS0 handoff) | `web/src/features/sync/**` (incl. `components/VersionHistoryPanel.tsx` registered as a `previewPanels` entry) |
| **WS5 Files advanced** | Filter bar with server search params, tag filter and folder tags, cursor pagination with virtualization, POST download tokens, bulk tag (7) | `web/src/pages/Files.tsx`, `web/src/components/files/**`, `web/src/components/FileIcon.tsx` (after WS0 handoff) | `web/src/features/files/api.ts` (typed file hooks), `web/src/features/files/module.tsx` (if any routes, e.g. saved searches) |
| **WS6 Settings, AI & Approvals** | Notification settings, AI settings and keys, approvals inbox and badge (8, 10) | `web/src/pages/Account.tsx` (adds links to the new settings) | `web/src/features/settings/**`, `web/src/features/approvals/**` |
| **(Automation agent)** | Backup/retention/replication/verify/recovery node kinds (8) | `web/src/automation/*` (not this plan) | It consumes WS0 `ScheduleEditor`, WS3 destination picker and WS2 snapshot picker by **import only** |

Shared-component handoffs: `components/files/**` belongs to WS5 after WS0. WS2 imports it but must not edit it. If WS2 needs a prop, it asks WS5 (or WS0 builds the snapshot-ready props up front: `dataSource`, `contentURL`, `readOnly`, `selectionMode: 'multi'|'tree'`). Generic components in `components/*` stay owned by WS0. After phase 0, additions go through a small WS0 follow-up PR queue.

Untouched by every workstream: `web/src/lib/uploads.tsx`, `web/src/pages/{Uploads,Trash,Devices,Auth}.tsx`, `web/src/pages/admin/Updates.tsx`, `web/embed.go`, `internal/api/web.go`. Any CSP change to `web.go` (for example WASM) belongs to the server owner on request.

---

## 7. Interfaces needed from server APIs

Conventions requested (apply to all new endpoints):
- Lists: `GET …?cursor=&limit=` returns `{items, next_cursor, total?}`.
- Bulk: `{ok_count, failed: [{id, code, message}]}`. This generalises `/files/move` and `/files/delete`.
- Errors: existing `{error:{code,message,details}}`, where `details.fields` holds validation errors.
- Long operations: `202 {operation_id}`, then `GET /operations/{id}` returns `{state, progress{done,total,bytes}, result?, error?}`.
- **Dry-run contract:** `POST /x/plan` returns `{plan, plan_token, expires_at}`, and `POST /x/execute {plan_token}` executes exactly that plan. A stale plan returns `409 plan_stale`.
- **Approvals:** any destructive execute may return `202 {approval_id}` when a gate applies.
- **Secrets:** never returned. GET shows `{has_secret: true, updated_at}`. PUT accepts a new value or `null` to clear.
- Content serving for snapshots uses the same `inlineSafe` and CSP rules as `files.go:405-422`.

| Area | Endpoints (proposed) | Web hook(s) |
|---|---|---|
| Summary (badges) | `GET /summary` → pending devices, approvals, running jobs, failed runs 24 h, unread notifications, conflicts | `useSummary` (WS0, Layout) |
| Jobs | `GET /backup/jobs?device=`, `GET/PUT /backup/jobs/{id}` (desired config: mode, schedule, rules, destinations), `POST /backup/jobs/{id}/run`, `POST /backup/jobs/{id}/pause` | `useJobs`, `useJob`, `useRunJob` |
| Runs | `GET /backup/runs?job=&state=`, `GET /backup/runs/{id}` (phase, files/bytes scanned/new/deduped/uploaded, queue depth, ETA), `GET /backup/runs/{id}/issues?kind=&state=` | `useRuns`, `useRun` (live), `useRunIssues` |
| Skip / retry | `POST /backup/runs/{id}/issues/skip {ids|kind}`, `POST /backup/jobs/{id}/retry-skipped` | `useSkipIssues`, `useRetrySkipped` |
| Snapshots | `GET /snapshots?device=&job=&from=&to=&tag=&protected=`, `GET /snapshots/{id}`, `PATCH /snapshots/{id} {protected,tags,note}`, `GET /snapshots/{id}/tree?path=&cursor=`, `GET /snapshots/{id}/search?q=&kind=&min_size=…`, `GET /snapshots/{id}/files/{fid}/content` (Range), `POST /snapshots/{id}/download {paths}` → token, `GET /snapshots/diff?a=&b=` | `useSnapshots`, `useSnapshotTree`, `useSnapshotSearch`, `useSnapshotDiff`, `useProtectSnapshot` |
| Apps | `GET /devices/{id}/apps` (inventory: id, name, version, source, installer id, data paths, mode, last snapshot, restorable flags) | `useDeviceApps` |
| Restore | `POST /restore/plan {snapshot_id, selection[], target_device_id, mapping{auto:true, overrides[]}, conflict_policy}` → plan with detected profile-path changes, per-item action, reinstall list, warnings (ACL/SID skipped, protected data); `POST /restore/execute {plan_token}`; `GET /restore/{id}` and `/restore/{id}/items` | `useRestorePlan`, `useExecuteRestore`, `useRestore` |
| Destinations | `GET/POST /storage/destinations`, `PATCH/DELETE /storage/destinations/{id}`, `POST /storage/destinations/test` (unsaved config), `GET /storage/destinations/{id}/health`, `GET /storage/providers` (schema-driven form: fields, secret fields, validation) | `useDestinations`, `useProviderSchemas`, `useTestDestination` |
| Policies | `GET/POST/PATCH/DELETE /storage/policies`, `GET /storage/policies/{id}/status` (replication lag, pending bytes) | `usePolicies` |
| Retention | `GET/PUT /storage/retention/rules` (scoped per job or global), `POST /storage/retention/plan` → per-snapshot keep/remove with reason and **unique bytes reclaimed**, `POST /storage/retention/execute {plan_token}` | `useRetentionRules`, `useRetentionPlan` |
| Analytics | `GET /storage/analytics?group=job|app|device|destination&range=` → logical, physical, unique, growth series | `useStorageAnalytics` |
| GC (admin) | `POST /admin/gc/plan` → unreferenced blobs and bytes per location; `POST /admin/gc/execute {plan_token}` → operation; `GET /admin/gc/last` | `useGcPlan`, `useGcRun` |
| Readiness | `GET /readiness?device=` → checks `[{id, status, message, fix_link}]`, score, last verified restore | `useReadiness` |
| Verification | `POST /snapshots/{id}/verify` → operation | `useVerifySnapshot` |
| Sync (req 5) | `GET/PUT /devices/{id}/sync/folders` (desired state the device pulls), `GET/POST/PATCH/DELETE /sync/rulesets`, `GET /sync/conflicts`, `POST /sync/conflicts/resolve {ids, choice}`, `GET /files/{id}/versions`, `POST /files/{id}/versions/{vid}/restore`, `GET /files/{id}/versions/{vid}/content` | `useSyncFolders`, `useRulesets`, `useConflicts`, `useFileVersions` |
| Files (WS5) | `POST /files/download {ids}` → token; `GET /files/search` adds `cursor`; `GET /files/{id}/thumbnail?size=` (optional, avoids originals) | `useFileSearch`, `useDownloadToken` |
| Notifications | `GET/PUT /account/notification-settings`, `GET /notifications/channels`, `POST /notifications/test` | `useNotificationSettings` |
| AI | `GET/PUT /account/ai` (provider, model, `has_key`, scopes), `POST /ai/diagnose {run_id|device_id}` → recommendations with optional proposed actions, which become approvals | `useAISettings`, `useDiagnose` |
| Approvals | `GET /approvals?state=pending`, `GET /approvals/{id}` (plan snapshot, requester: automation/AI/user, risk level), `POST /approvals/{id}/approve {confirm_text?}`, `POST /approvals/{id}/reject` | `useApprovals`, `useDecideApproval` |
| Events (optional) | `GET /events` SSE: `run.progress`, `summary.changed`, `approval.created` | `useLiveQuery` transport |

Device-side counterpart (not web, listed for the server and desktop agents): device pull of desired job and sync config, and of restore plans. Device push of run progress and issues, app inventory, and snapshot manifests.

---

## 8. Testing plan

1. **Phase 0 (WS0):** install Vitest, RTL, happy-dom and MSW. Write unit tests for `lib/api.ts` (CSRF header only on mutating methods; `ApiError` parsing of `{error}` and non-JSON bodies; 401 listener except on `/auth/login` and `/setup`), `lib/format.ts` (edge cases: null, PB, negative durations), `lib/uploads.tsx` (chunk resume set, retry and backoff with fake timers, no retry on 507, cancel deletes the session), `lib/auth.tsx` (cache cleared on logout), `ui.tsx` (`confirm` with `requireText` disables the button until it matches; Modal Esc; focus trap). Snapshot-free component tests for the extracted `components/files/*` (selection: shift-range, ctrl-toggle, select-all). Parity script runs in CI.
2. **Per workstream (definition of done):** each `features/<x>` ships MSW handlers in `features/<x>/test/handlers.ts` and tests covering:
   - WS1: progress renders phases and ETA; the issues table supports skip, skip-all-of-kind and retry-skipped calls with correct bodies; the run summary never hides failed items (req 4 "never silently discard").
   - WS2: wizard step validation; mapping auto-detects a changed profile path and an override wins; the execute button is disabled until the plan exists and sends the `plan_token`; a stale-plan 409 forces a re-plan; tri-state tree selection.
   - WS3: provider form generated from schema; secret fields never prefilled; "test connection" before save; the retention dry-run shows keep/remove reasons and protected snapshots are never in the remove set; GC execute requires a plan and type-to-confirm.
   - WS4: conflict bulk resolve; version restore; ruleset pattern list editing.
   - WS5: the filter bar maps to search query params; virtualized list renders a 10k-item page; the download token flow is used for more than N ids.
   - WS6: the AI key is write-only (`has_key` toggles UI, value never displayed); approve requires the confirm text for high risk.
3. **Registry contract test:** every `features/*/module.tsx` default-exports a valid `FeatureModule`, has unique route paths and nav `to`, and admin routes appear only for admins.
4. **E2E (Playwright, local Go server with embedded dist, seeded test admin from a fixture):** smoke runs on every PR (login → files upload/preview/trash → admin tabs render). Feature E2E is added as server endpoints land: snapshot browse and download, restore dry-run, retention dry-run, approvals. Use a fake device client (from `pkg/client`, owned by others) to post progress for live-view tests.
5. **Security checks:** E2E asserts that snapshot content responses carry the sandbox CSP and `attachment` for non-`inlineSafe` types; that no secret value appears in any GET response captured by Playwright's network log; and that logout then login as another user shows none of the previous user's data.
6. **Manual verification item:** PDF preview in Chromium under the current content CSP (section 5 risk 5).
7. **CI:** the web job runs `npm ci`, `typecheck`, `lint`, `test`, `build`, then E2E smoke (after the Go build), plus the UI parity check.
