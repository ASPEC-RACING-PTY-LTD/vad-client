# P0-21 (WEB-0) ui.tsx accessibility + index.css
Status: completed

## Goal
Make the shared UI kit (`vad-server/web/src/components/ui.tsx`) and `web/src/index.css` more accessible without changing visuals or public component APIs. Keep `ui.tsx` self-contained (it still imports only `react`, `react-dom` and `lucide-react`) so P0-29 can mirror it verbatim to `VaultDock/desktop/frontend/src/ui.tsx`.

## Changes
- **vad-server: `web/src/components/ui.tsx`, modified.** Accessibility only (details below). No exports were added, removed or re-typed.
- **vad-server: `web/src/index.css`, modified.**
  - Documented the `:focus-visible` ring and added a dark-mode variant (`brand-400`).
  - Added a `forced-colors` (Windows high-contrast) outline fallback.
  - Added a `prefers-reduced-motion: reduce` block: near-zero animation and transition durations, and the indeterminate progress bar becomes a static, dimmed full-width bar.

## Interfaces exposed
No change. Every exported name, prop and type is unchanged: `cx`, `Button`, `IconButton`, `Spinner`, `PageLoader`, `Badge`, `PageHeader`, `EmptyState`, `ErrorState`, `ProgressBar`, `Toggle`, `Field`, `Tabs`, `Stat`, `Modal`, `useUI`, `UIProvider`, `MenuItem`, `Menu`, `ContextMenu`, `CopyButton`.

### API-compatible changes for P0-29 to mirror (copy both files verbatim)
1. **Imports.** Added `cloneElement`, `isValidElement`, `useId`, `type KeyboardEvent as ReactKeyboardEvent` and `type ReactElement` from `react`. There are still no app-lib imports.
2. **Button.**
   - `type` defaults to `"button"` (`type={rest.type ?? 'button'}`).
   - Sets `aria-busy` while `loading`.
   - The loader icon is `aria-hidden`.
3. **IconButton.** `type` defaults to `"button"`.
4. **Spinner.** `aria-hidden`.
5. **PageLoader.** `role="status"` and `aria-live="polite"`.
6. **ErrorState.** `role="alert"`; the icon is `aria-hidden`.
7. **ProgressBar.** `role="progressbar"` with `aria-valuemin`, `aria-valuemax` and `aria-valuenow`; `aria-valuenow` is omitted when `indeterminate`.
8. **Toggle.**
   - Already had `role="switch"` and `aria-checked`.
   - Now also sets `aria-labelledby` (label) and `aria-describedby` (description) using `useId`.
   - The knob is `aria-hidden`.
9. **Field.**
   - Uses `useId` ids.
   - If the single child is a native `input`, `select` or `textarea`, it is cloned with:
     - an `id` (the child's own `id` is kept if present), linked to the label by `htmlFor`;
     - `aria-describedby` pointing to the hint or error (merged with any existing value);
     - `aria-invalid` when there is an error.
   - Any other child (for example a wrapper `div`) leaves the outer `div` as `role="group"`, with `aria-labelledby` and `aria-describedby`.
   - The error `<p>` has `role="alert"`.
10. **Tabs.** Each button has `type="button"` and `aria-pressed` for the selected tab.
11. **Modal.**
    - `aria-labelledby` points to the title `<h2>`, and `aria-describedby` to the description when present.
    - The dialog container has `tabIndex={-1}` and `outline-none`.
    - **Focus handling:**
      - On open, focus goes into the dialog. A child with `autoFocus` keeps its focus; otherwise the container is focused.
      - On close, focus returns to the element that had it before.
      - A Tab/Shift+Tab focus trap runs inside the dialog's `onKeyDown`.
    - **Escape:**
      - A module-level `modalStack` makes only the topmost modal react to Escape, so nested confirms don't close their parent.
      - Escape is ignored if `defaultPrevented`.
    - `onClose` is now read through a ref, so the effect depends only on `open`. Inline `onClose` lambdas no longer re-run the effect each render.
    - The backdrop is `aria-hidden`.
12. **UIProvider.**
    - The toast stack is `aria-live="polite"`. Each toast is `role="alert"` (error) or `role="status"` (other tones).
    - Toast icons are `aria-hidden`; the dismiss button has `type="button"`.
    - The confirm `requireText` input is labelled with `htmlFor`/`id` (`useId`).
13. **ContextMenu (and Menu).**
    - Roles: the menu is `role="menu"`, each item is `role="menuitem"` with `type="button"` and `tabIndex={-1}`, dividers are `role="separator"`, and item icons are `aria-hidden`.
    - **Focus:** the first enabled item is focused once the menu is positioned. On close, focus returns to the previously focused element if focus fell to `body`.
    - **Keys:** ArrowUp/ArrowDown wrap, Home/End jump, and Tab closes the menu.
    - **Escape:** the listener now runs in the capture phase and calls `preventDefault`, so Escape closes only the menu and not an enclosing Modal.

## Commands run and results
- `cd D:/VaultDock/vad-server/web && npm run build` (`tsc -b && vite build`): **success**, built in about 0.4 s with no type errors.

## Contract issues
None. The task doesn't touch any contract.

## Gaps / follow-ups
- The Modal focus trap handles Tab only. It does not pull focus back if something outside the dialog is focused by mouse or script. This is deliberate, so menus and toasts that portal to `body` stay usable.
- `Menu`'s trigger is a render prop, so `ui.tsx` can't add `aria-haspopup`/`aria-expanded` to it; callers would have to.
- `Tabs` uses `aria-pressed` (toggle-button semantics) rather than full tablist semantics, because there are no associated tab panels or roving tabindex.
- No vitest tests were added; vitest isn't set up yet.

## Notes for dependent tasks / INT
- **P0-29 (desktop mirror):**
  - Copy `web/src/components/ui.tsx` to `desktop/frontend/src/ui.tsx` and `web/src/index.css` to `desktop/frontend/src/index.css` verbatim. Neither file has web-only imports.
  - `Button`/`IconButton` now default to `type="button"`. The only desktop `<form>` buttons, in `screens/Connect.tsx`, already pass `type="submit"` explicitly. Re-check any other desktop forms that rely on implicit submit.
  - In the web app, every Button used as a form submit already passes `type="submit"`: `Auth.tsx` explicitly, and the `Files.tsx` modals submit via `onClick`.
- **INT:** the build rewrote the tracked file `web/tsconfig.tsbuildinfo`, which is a build artifact and not an owned edit.
