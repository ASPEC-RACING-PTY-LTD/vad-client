# P0-19 WEB-0: shared web components A
Status: completed

## Goal
Reusable, accessible, typed web components for later features: DataTable (+ helpers), FilterBar, ListInput, TagInput, SecretField (api-v1 §1 secret semantics) and BulkResultToast (api-v1 bulk result).

## Changes (repo: vad-server, all created)
- `web/src/components/data/DataTable.tsx`: generic table. Column defs, client-side or manual sort with `aria-sort`, checkbox selection (controlled or uncontrolled) with a bulk-action toolbar, loading, empty and error states, and "Load more" with an inline later-page error. Rows support keyboard focus and navigation (ArrowUp/Down, Home/End, Enter/Space).
- `web/src/components/data/sort.ts`: `compareValues`, `sortRows` (stable, empties last, numeric collation) and `nextSort` (asc, then desc, then none).
- `web/src/components/data/useRowSelection.ts`: selection state, controlled or uncontrolled.
- `web/src/components/data/useDebouncedValue.ts`: generic debounce hook.
- `web/src/components/data/infinite.ts`: `infiniteTableProps(q)` maps a `useApiInfiniteList` result onto DataTable props.
- `web/src/components/data/FilterBar.tsx`: debounced search box (Enter commits now, Escape clears) and chip filter groups (single or multi select, `aria-pressed`), plus "Clear filters".
- `web/src/components/ListInput.tsx`: an ordered string list (paths, patterns) with entries editable in place. Pasting several lines adds one entry per line. Commas are not split. Supports validate, max and dedupe.
- `web/src/components/TagInput.tsx`: chip tags. Enter, comma or Tab adds a tag; Backspace removes the last one. Datalist suggestions; normalize, validate, max and maxLength.
- `web/src/components/SecretField.tsx`: write-only secret editor, plus the `withSecret` body helper.
- `web/src/components/BulkResultToast.tsx`: inline dismissible panel, `summarizeBulk` and the `useBulkResultToast` hook (toast via UIProvider).
- `web/src/components/__tests__/DataTable.test.tsx` and `SecretField.test.tsx`: 8 tests.

`ui.tsx` and `lib/*` were not modified.

## Interfaces exposed
```ts
// data/sort.ts
type SortDir = 'asc' | 'desc'; type SortState = { id: string; dir: SortDir } | null
type SortValue = string | number | boolean | null | undefined
compareValues(a, b, dir): number; sortRows<T>(rows, sort, value?): readonly T[]; nextSort(cur, id): SortState
// data/useRowSelection.ts
type RowKey = string | number
useRowSelection(selected?: ReadonlySet<RowKey>, onChange?: (s: Set<RowKey>) => void): { selected; set; toggle; clear }
// data/useDebouncedValue.ts
useDebouncedValue<T>(value: T, delayMs = 300): T
// data/DataTable.tsx
type Column<T> = { id: string; header: ReactNode; cell: (row: T) => ReactNode; sortValue?: (row: T) => SortValue;
  sortable?: boolean; align?: 'left'|'center'|'right'; width?: string; className?: string; headerClassName?: string; label?: string }
type DataTableLoadProps<T> = { rows: readonly T[]; loading?: boolean; error?: unknown; onRetry?: () => void;
  hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; loadMoreError?: unknown }
type DataTableProps<T> = DataTableLoadProps<T> & { columns: Column<T>[]; rowKey: (row: T) => RowKey; label: string; empty?: ReactNode;
  sort?: SortState; defaultSort?: SortState; onSortChange?: (s: SortState) => void; manualSort?: boolean;
  selectable?: boolean; selected?: ReadonlySet<RowKey>; onSelectedChange?: (s: Set<RowKey>) => void; isRowSelectable?: (row: T) => boolean;
  bulkActions?: (selectedRows: T[], clear: () => void) => ReactNode; onRowClick?: (row: T) => void;
  rowClassName?: (row: T) => string | undefined; stickyHeader?: boolean; dense?: boolean; className?: string }
function DataTable<T>(props: DataTableProps<T>): JSX.Element
// data/infinite.ts
type InfiniteListLike<T> = { items: T[]; isPending; error; refetch; hasNextPage; isFetchingNextPage; fetchNextPage }
infiniteTableProps<T>(q: InfiniteListLike<T>): DataTableLoadProps<T>
//   usage: const q = useApiInfiniteList<Job>(jobKeys.list(f), '/jobs', f); <DataTable {...infiniteTableProps(q)} columns={cols} rowKey={(r) => r.id} label="Jobs" />
// data/FilterBar.tsx
type FilterOption = { value: string; label: ReactNode; count?: number }
type ChipFilter = { id: string; label: string; options: FilterOption[]; multiple?: boolean }
type FilterValues = Record<string, string[]>
type FilterBarProps = { search?: string; onSearchChange?: (q: string) => void; searchPlaceholder?: string; searchLabel?: string;
  debounceMs?: number /*300*/; filters?: ChipFilter[]; values?: FilterValues; onValuesChange?: (v: FilterValues) => void; actions?: ReactNode; className?: string }
hasActiveFilters(values?, search?): boolean
// ListInput.tsx
type ListInputProps = { value: string[]; onChange: (v: string[]) => void; label: string; placeholder?: string; mono?: boolean /*true*/;
  validate?: (item: string) => string | null | undefined; allowDuplicates?: boolean; splitLines?: boolean /*true*/; max?: number;
  disabled?: boolean; addLabel?: string; emptyText?: string }
// TagInput.tsx
type TagInputProps = { value: string[]; onChange: (v: string[]) => void; label: string; placeholder?: string; suggestions?: string[];
  normalize?: (t: string) => string /*trim*/; validate?: (t: string) => string | null | undefined; max?: number; maxLength?: number /*64*/; disabled?: boolean; mono?: boolean }
// SecretField.tsx
type SecretValue = string | null | undefined   // undefined = keep (omit), null = clear, string = set
type SecretFieldProps = { label: string; hasSecret: boolean; updatedAt?: number | string | null; value: SecretValue;
  onChange: (v: SecretValue) => void; placeholder?: string; hint?: string; allowClear?: boolean /*true*/; disabled?: boolean }
withSecret<B extends object>(body: B, value: SecretValue, key = 'secret'): B & Record<string, string | null>  // '' and undefined -> omitted
// BulkResultToast.tsx
type BulkSummary = { tone: 'success'|'warning'|'error'; title: string; message?: string }
summarizeBulk(r: BulkResult, verb = 'updated', noun = 'item'): BulkSummary
useBulkResultToast(): (r: BulkResult, verb?: string, noun?: string) => BulkSummary   // needs UIProvider
type BulkResultToastProps = { result: BulkResult | null | undefined; verb?: string; noun?: string; describe?: (id: string | number) => string; onDismiss?: () => void; className?: string }
```

## Commands run and results
- `npx vitest run src/components/__tests__`: 2 files and 8 tests passed.
- `npm run build` (tsc -b plus vite build): OK.
- `npx eslint` on all the new files: clean.

## Contract issues
- api-v1 §1 doesn't specify the type of `secret_updated_at`. SecretField accepts unix ms or an ISO string.

## Gaps / follow-ups
- DataTable has no virtualization (research §… mentions it as optional). Load-more pagination covers current needs.
- SecretField treats an empty string while editing as "keep" via `withSecret`. Setting a deliberately empty secret isn't supported (the contract doesn't call for it).
- FilterBar's chip set is generic. Date-range and size chips (research list) are left to feature pages, which build `ChipFilter` options.

## Notes for dependent tasks / INT
- With manual (server-side) sort, pass `manualSort`, `sort` and `onSortChange` and mark columns `sortable: true`. Otherwise give `sortValue` for client sort.
- When both `onRowClick` and `selectable` are set, Enter/Space on a row activates it and the row checkboxes stay in the tab order. With only `selectable`, Space/Enter on a row toggles its selection.
- `useBulkResultToast` and `BulkResultToast` use `pluralize` from `lib/format`, so pass the singular noun (for example `'device'`).
