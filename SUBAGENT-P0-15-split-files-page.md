# P0-15 (WEB-0) Split Files.tsx
Status: completed

## Goal
Split the 871-line `web/src/pages/Files.tsx` into focused components and hooks under `web/src/components/files/`. This is a pure refactor: behaviour, URLs, styling, query keys and API calls are unchanged, and `Files.tsx` is now a thin composition.

## Changes (repo: path, created/modified/deleted, why)
All paths are in the server repo `vad-server`, under `web/src/`.
- `pages/Files.tsx`: **modified** (871 → 190 lines). It now holds only page state (view, sort, kind, filter, modal/ctx state, refs), the `?select=` effect, upload enqueue/drop handling and the component wiring. It still exports `FilesPage`, re-exports `Node` (type), `MoveModal` and `del` for compatibility.
- `components/files/` (all **created**):
  - `types.ts`: `Node`, `Crumb`, `Sort`, `ContentURL`, `contentURL`, `NODES_MIME`, `downloadIDs`
  - `useFilesQuery.ts`: list/search `useQuery` (same key `['files', parent, sort.by, sort.desc, kind, searchQ]`), client filter, crumbs, folder, folderPath
  - `useSelection.ts`: click, ctrl/cmd and shift-range selection, `toggleOne`
  - `useFileShortcuts.ts`: Delete, Ctrl/Cmd+A, Esc, F2 and Enter (re-registers every render, as before)
  - `useFileMutations.ts`: `deleteIDs` (confirm + `POST /files/delete`), `moveTo` (`POST /files/move`), `refresh` (invalidates `files` and `stats`)
  - `FilesHeader.tsx` + `Breadcrumbs.tsx`: search title or breadcrumbs (drop targets) plus heading
  - `UploadControls.tsx`: New folder and upload buttons plus hidden file/folder inputs
  - `FilesToolbar.tsx`: bulk-action bar or filter input, kind select, sort menu, list/grid toggle
  - `FileTable.tsx`: list view, with row drag/drop move and the "Showing X of Y" footer
  - `FileGrid.tsx`: grid view with thumbnails
  - `rowActions.tsx`: context-menu item builder (`rowActions`)
  - `FilesEmpty.tsx`, `DropOverlay.tsx`: empty state and upload drop overlay
  - `NewFolderModal.tsx`, `RenameModal.tsx`, `TagsModal.tsx`, `MoveModal.tsx`: dialogs, moved verbatim. Delete confirmation still uses `useUI().confirm`, inside `useFileMutations`.
  - `PreviewModal.tsx`: preview modal plus internal `TextPreview`

## Interfaces exposed
- `useFilesQuery({ parent, sort, kind, searchQ, filter }) => { listQ, items: Node[], crumbs: Crumb[], folder: Node | null, folderPath: string }`
- `useSelection(items: Node[]) => { selected: Set<number>, setSelected, onRowClick(e, n, idx), toggleOne(id) }`
- `useFileMutations({ items, clearSelection }) => { refresh, deleteIDs(ids), moveTo(ids, target) }`
- `useFileShortcuts({ items, selected, setSelected, onDelete, onRename, onOpen })`
- `FileViewProps = { items, selected, onRowClick, onToggle, onOpen, onContextMenu(n, x, y), onActions(n, x, y) }`, shared by `FileTable` and `FileGrid`
- `FileTable` adds `{ onSelectAll(all), onMoveTo(ids, target), showPath, total, filtered }`
- `FileGrid` adds optional `contentURL?: ContentURL`
- `PreviewModal({ node, list, onNavigate, onClose, contentURL? })`. `contentURL` defaults to `/api/v1/files/{id}/content[?download=1]`, so the snapshot browser (WS2) can inject its own URL builder without changing the live page.
- `rowActions(n, selected, inSearch, handlers: RowActionHandlers): MenuItem[]`
- `MoveModal({ ids, onClose, onMove, title? })`: unchanged signature

## Commands run and results
- `cd vad-server/web && npm run build`: OK (`tsc -b` clean, vite build OK).
- No browser check: running it needs the backend and a dev server, and the brief forbids leaving long-running processes behind.

## Contract issues
None.

## Gaps / follow-ups
- `contentURL` is injectable only in `FileGrid` and `PreviewModal`. `downloadIDs` still hard-codes the live URLs; per the research doc (WS5), it moves to POST download tokens later.
- The `any` typing of list responses is unchanged on purpose. A typed `features/files/api.ts` belongs to WS5.
- `deleteIDs` keeps its original `useCallback` dependency list `[items, confirm, toast]`.

## Notes for dependent tasks / INT
- `lib/api.ts` was not edited; only the existing `del/get/patch/post/put/errorMessage/ApiError` exports are used.
- `pages/Files.tsx` still exports `Node`, `MoveModal` and `del`, so any external importer keeps working. New code should import from `components/files/*` directly.
