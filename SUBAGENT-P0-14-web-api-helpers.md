# P0-14 WEB-0: typed API helpers, query hooks, formatting utils
Status: completed

## Goal
Typed API layer for the web app (api-v1 §1 conventions), React Query wrappers and conventions, and the shared formatting utils.

## Changes
- vad-server: `web/src/lib/api.ts`, modified. Existing exports are unchanged in behaviour: `api`, `get/post/put/patch/del`, `setCsrf`, `onUnauthenticated`, `csrfHeader`, `errorMessage`, and `ApiError` with the same positional constructor. Added:
  - `apiRaw` (status + headers);
  - the `Retry-After` parse;
  - `qs`/`withQuery`;
  - cursor-page helpers;
  - 202 operation/approval classification;
  - operation types.
- vad-server: `web/src/lib/query.ts`, created. Key factory, retry policy, `useApiQuery`, `useApiMutation`, `useApiInfiniteList`, `useOperation`.
- vad-server: `web/src/lib/format.ts`, modified, additions only: `formatDurationMs`, `formatRelative`, `formatIso`, `formatTime`, `formatDateRange`, `formatPercent`, `formatCount`.

## Interfaces exposed
`lib/api.ts`
```ts
class ApiError extends Error {
  status: number; code: string; details?: unknown; body?: any; retryAfterMs?: number
  constructor(status, code, message, details?, body?, retryAfterMs?)
  get fieldErrors(): Record<string, string>   // from details.fields
  get isCapacity(): boolean                   // status === 503
}
function parseRetryAfter(v: string | null): number | undefined          // seconds or HTTP date -> ms
type ApiResponse<T> = { status: number; data: T; headers: Headers }
function apiRaw<T = any>(method: string, path: string, body?: unknown, init?: RequestInit): Promise<ApiResponse<T>>
function api<T = any>(method, path, body?, init?): Promise<T>
const get/post/put/patch/del: <T = any>(p: string, b?: unknown) => Promise<T>   // unchanged
function setCsrf(token: string): void; function onUnauthenticated(fn): () => void
function csrfHeader(): Record<string, string>; function errorMessage(e: unknown): string
type QueryValue = string | number | boolean | null | undefined | readonly (string | number)[]
type QueryParams = Record<string, QueryValue>
function qs(params?: QueryParams): string                 // skips null/undefined/'', arrays repeat key
function withQuery(path: string, params?: QueryParams): string
type Page<T> = { items: T[]; next_cursor: string }
type PageParams = QueryParams & { cursor?: string; limit?: number }
const MAX_PAGE_LIMIT = 500
function getPage<T>(path: string, params?: PageParams): Promise<Page<T>>
function listAll<T>(path: string, params?: QueryParams, maxItems = 10000): Promise<T[]>
type BulkResult = { ok_count: number; failed: { id: string | number; code: string; message: string }[] }
type OperationId = string | number
type OperationAccepted = { operation_id: OperationId }
type ApprovalAccepted = { approval_id: string | number; operation_id: OperationId | null }
type OperationState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | (string & {})
type Operation<R = unknown> = { id; kind: string; state: OperationState; progress?: {done?,total?,bytes?}; result?: R; error?; created_at: number; updated_at: number }
type MutationResult<T> = { kind:'done'; status; data: T } | { kind:'operation'; status:202; operation_id } | { kind:'approval'; status:202; approval_id; operation_id: OperationId|null }
function isApprovalAccepted(x: unknown): x is ApprovalAccepted
function isOperationAccepted(x: unknown): x is OperationAccepted
function mutate<T = unknown>(method: 'POST'|'PUT'|'PATCH'|'DELETE', path: string, body?: unknown): Promise<MutationResult<T>>
function getOperation<R = unknown>(id: OperationId): Promise<Operation<R>>
function isOperationFinished(op?: Pick<Operation,'state'>): boolean
```
`lib/query.ts`
```ts
function createKeys<const R extends string>(root: R): {
  all: readonly [R]; lists(): [R,'list']; list(filter?): [R,'list',filter|{}];
  details(): [R,'detail']; detail(id: string|number): [R,'detail',id]; sub(...parts): [R, ...parts] }
const queryKeys: { summary: readonly ['summary']; operations: ReturnType<typeof createKeys<'operations'>> }
function shouldRetry(failureCount: number, err: unknown, max = 2): boolean     // never retries 4xx
function retryDelay(attempt: number, err: unknown): number                     // Retry-After (cap 120 s) else 2^n s (cap 30 s)
function useApiQuery<T, K extends QueryKey>(key: K, pathOrFn: string | (() => Promise<T>), options?): UseQueryResult<T, ApiError>
type ApiMutationOptions<TData, TVars> = Omit<UseMutationOptions<TData, ApiError, TVars>, 'mutationFn'> & {
  invalidate?: QueryKey[]; onErrorMessage?: (message: string, err: ApiError) => void }
function useApiMutation<TData = unknown, TVars = void>(fn: (vars: TVars) => Promise<TData>, options?): UseMutationResult<TData, ApiError, TVars>
function useApiInfiniteList<T, K extends QueryKey>(key: K, path: string, params?: QueryParams, options?): UseInfiniteQueryResult<...> & { items: T[] }
function useOperation<R = unknown>(id: OperationId | null | undefined, intervalMs = 1500): UseQueryResult<Operation<R>, ApiError>  // polls until terminal
```
Convention: each feature declares `const xKeys = createKeys('x')` and invalidates `xKeys.all`.

`lib/format.ts`: the existing exports are unchanged (`formatBytes`, `formatSpeed`, `formatDuration(sec)`, `timeAgo(ms)`, `formatDate(ms)`, `fileKind`, `previewable`, `pluralize`). New exports:
```ts
formatDurationMs(ms) ; formatRelative = timeAgo ; formatIso(ms) ; formatTime(ms)
formatDateRange(fromMs, toMs) ; formatPercent(fraction, digits = 0) ; formatCount(n)
```

## Commands run and results
- `cd D:/VaultDock/vad-server/web && npm run build`: OK. `tsc -b` was clean and the vite build succeeded.

## Contract issues
- api-v1 §3 does not list the values of the `GET /operations/{id}` `state` field. I assumed the terminal states are `succeeded|failed|cancelled` (the type is open to other strings), and `isOperationFinished`/`useOperation` depend on that assumption. INT should confirm it against the server implementation.
- The type of `operation_id`/`approval_id` (int64 row id or string) isn't specified, so they are typed `string | number`.

## Gaps / follow-ups
- No unit tests yet, because vitest is not installed (that's the WS0 test-tooling task).
- No duplicated format helpers were found in web pages or components, so nothing needed moving. The drift between desktop `desktop/frontend/src/lib.ts` and web `format.ts` remains, as research §1 notes.
- `useApiMutation` does not toast by itself, because `lib/` must not depend on `components/ui`. Callers pass `onErrorMessage: (m) => toast('error', '…', m)`.

## Notes for dependent tasks / INT
- `main.tsx` (not owned) should use `retry: shouldRetry, retryDelay` from `lib/query.ts` in the QueryClient defaults, so that 503 `Retry-After` is honoured globally. Today only queries made through `useApiQuery`/`useApiInfiniteList` get `retryDelay`.
- The `ApiError` constructor stays backward compatible: `uploads.tsx` and `Files.tsx` call it positionally.
- Use `mutate()` for any write that may return 202 (prune, gc, restore execute, approval-gated deletes). Then use `useOperation(id)` to track progress.
