# P1-34 (WEB-6) Web approvals inbox
Status: completed (the nav pending-count badge is not done: `FeatureNav` has no badge field; see Notes)

## Goal
Add the approvals feature to the web app (api-v1 §8):
- a filterable inbox of approvals;
- a detail dialog with approve (type-to-confirm for high risk) and reject (with a reason);
- clear handling of `plan_stale`, `approval_not_pending` and `not_superuser`;
- a "Pending approvals" dashboard widget;
- tests.

## Changes (repo vad-server, all new, under `web/src/features/approvals/`)
| File | Why |
|---|---|
| `types.ts` | Hand-written mirror of `wire.Approval` (`sdk/wire/approvals.go`), plus `ApprovalDecided` (Approval + `operation_id`, per P1-17), the request bodies, the kind/state lists and labels (`consumed` is shown as "Executed"). It also has an optional `plan?: unknown` that is not on the wire (see Contract issues). |
| `api.ts` | `approvalKeys = createKeys('approvals')` and the hooks below. `decisionErrorMessage` maps the error codes to user-facing text. Both approve and reject invalidate `approvals` and `summary`. |
| `module.tsx` | `FeatureModule` `approvals`: routes `/approvals` and `/approvals/:id` (deep link, for example from notifications), nav "Approvals" (ShieldCheck, order 65), `requiredFeatureFlag: 'approvals.v1'`. |
| `ApprovalsPage.tsx` | Page with tabs Pending / Recent / All, a FilterBar (search, Status chips, Kind chips) and a DataTable. Clicking a row opens a Modal at `/approvals/:id`. **Detail view:** kind, subject (`subject_type #subject_id`), risk, status, requester, expiry or decision time, reason and plan digest. **When pending:** high risk needs a confirmation text first (Approve stays disabled until it is filled); Reject reveals a reason textarea (max 1000). If the approval has a `plan`, approve goes through `PlanPreview`. If approve returns an `operation_id`, `OperationProgress` is shown. **Errors** are shown inline as `role=alert`; after `plan_stale` or `approval_not_pending` the approvals queries are invalidated. |
| `dashboard.tsx` | `PendingApprovalsWidget`: newest 5 pending approvals, a count badge ("5+" when there is a next page), and links to `/approvals` and `/approvals/:id`. It refetches every 30 s. Default export `{ id: 'approvals.pending', order: 35, span: 1, requiredFeatureFlag: 'approvals.v1' }`. |
| `__tests__/approvals.test.tsx` | 10 tests with mocked `fetch`. See Commands. |

## Interfaces exposed
```ts
// api.ts
export const approvalKeys: ReturnType<typeof createKeys<'approvals'>>
export type ApprovalListFilter = { state?: string }
export function useApprovals(filter?: ApprovalListFilter)          // GET /approvals?state=&limit=100 (infinite)
export function useApproval(id: number | null | undefined)         // GET /approvals/{id}
export function usePendingApprovals(limit = 5)                      // GET /approvals?state=pending&limit=N (Page<Approval>)
export function useApproveApproval()  // mutate({id, confirmText?}) -> POST /approvals/{id}/approve {confirm_text?}
export function useRejectApproval()   // mutate({id, reason})       -> POST /approvals/{id}/reject {reason}
export function decisionErrorMessage(e: unknown): string
export function isStateChangingError(e: unknown): boolean
// types.ts: Approval, ApprovalDecided, ApprovalKind/State/Risk/Requester/SubjectType, kindLabel(), stateLabel()
// ApprovalsPage.tsx: default ApprovalsPage, ApprovalDetail, riskTone(), stateTone()
// dashboard.tsx: PendingApprovalsWidget, default DashboardWidget
```
Error texts:
| Code | Message shown |
|---|---|
| `409 plan_stale` | The plan changed and the approval has expired; the requester must re-plan |
| `409 approval_not_pending` | Already decided, naming the new state from `details.state` |
| `403 not_superuser` | Only a superuser can decide this |
| `403 forbidden` | You are not allowed to decide this |
| `400 validation` with `fields.confirm_text` | The confirmation text does not match |

## Commands run and results
- `npx vitest run src/features/approvals`: 10/10 passed. The tests cover:
  - the list URL and the kind filter;
  - the high-risk approve flow (button disabled until text is entered, body `{confirm_text}`, toast, dialog closes);
  - low-risk approve with body `{}`;
  - reject with a reason;
  - the error messages for `plan_stale`, `approval_not_pending`, `not_superuser` and `validation`;
  - a refetch after `approval_not_pending`;
  - the widget count "2+" and its URL.
- `npm run build` (`tsc -b` + vite build): OK. The page builds as its own lazy chunk.
- `npx vitest run` (all): 8 files, 31 tests passed.
- `npm run lint`: 0 errors, 102 warnings, all pre-existing `no-explicit-any` warnings in other files. `npx eslint src/features/approvals` is clean.

## Contract issues
1. **The wire `Approval` carries no plan body and no subject name.** Only `plan_digest` and `summary` are sent.
   - The UI cannot show the plan through `PlanPreview`, and cannot tell the user the exact `confirm_text`. It asks for "the name shown in the summary", and the server validates it.
   - `PlanPreview` is used only if a future server adds `plan` (an extra field is allowed by §1).
   - A `subject_name` (and optionally `plan`) field on GET `/approvals/{id}` would let the UI pre-check the text and show the plan. This needs an INT amendment.
2. The approve/reject response and `approval_not_pending` are P1-17 implementer choices, not contract text (already listed in P1-17).

## Gaps / follow-ups
- **Nav pending-count badge:** not done. `FeatureNav` (`app/types.ts`) has no badge field, and Layout is not owned here.
- **Status filter:**
  - On the Recent/All tabs, the Status and Kind filters run on the client over the loaded pages ("Load more" pages further).
  - The server `state` filter is used only on the Pending tab, because the server takes a single `state` value.
- **No polling of the inbox list:** only the widget refetches (every 30 s).

## Notes for dependent tasks / INT
- **Feature flag:** `registry.ts` `featureFlags` is still an empty set. The page and widget stay hidden until `approvals.v1` from `/info` is wired into it.
- **Nav badge:** to add one, extend `FeatureNav` with something like `badge?: ComponentType` (a component that fetches its own count) and render it in Layout. Approvals would then supply a component using `usePendingApprovals` or `/summary.pending_approvals`.
- **Linking:** other features can link to `/approvals/{approval_id}` after a `202 {approval_id}` (for example from `mutate()` returning `kind:'approval'`).
