# P0-20 WEB-0: shared web components B
Status: completed

## Goal
Reusable, typed and accessible web components: Stepper, TreeView, PlanPreview, ScheduleEditor, JobProgress (plus an operation-polling variant) and dependency-free SVG charts. Tests for Stepper, TreeView and ScheduleEditor.

## Changes (repo: path, created/modified/deleted, why)
All in vad-server, all created. `ui.tsx` and `lib/*` are unchanged.
- `web/src/components/Stepper.tsx`: wizard shell with per-step (async) validation.
- `web/src/components/TreeView.tsx`: lazy ARIA tree with keyboard navigation and tri-state checkboxes.
- `web/src/components/PlanPreview.tsx`: api-v1 §1 plan/execute preview with an expiry countdown, plus the `useCountdown` hook.
- `web/src/components/ScheduleEditor.tsx`: interval, daily and weekly schedule editor, plus validate, describe and default helpers.
- `web/src/components/JobProgress.tsx`: `JobProgress` and `OperationProgress` (uses `useOperation`).
- `web/src/components/charts/colors.ts`: chart colours from the theme CSS variables, with fallbacks.
- `web/src/components/charts/BarChart.tsx`: `BarChart` (vertical bars) and `BarStack` (one horizontal stacked bar with a legend).
- `web/src/components/charts/Sparkline.tsx`: `Sparkline` (line or area) and the pure `sparkPath`.
- `web/src/components/charts/index.ts`: barrel file.
- `web/src/components/__tests__/{Stepper,TreeView,ScheduleEditor}.test.tsx`: 7 tests.

## Interfaces exposed
```ts
// Stepper.tsx
type StepValidation = string | boolean | null | undefined   // null/undefined/true = ok; string/false = error
type StepDef = { id: string; title: ReactNode; description?: ReactNode;
  content: ReactNode | ((ctx: StepContext) => ReactNode);
  validate?: () => StepValidation | Promise<StepValidation>; hidden?: boolean }
type StepContext = { index; isFirst; isLast; next: () => Promise<boolean>; back(): void; goTo(i: number): void }
type StepperProps = { steps: StepDef[]; onFinish: () => void | Promise<void>; onCancel?: () => void;
  index?: number; onIndexChange?: (i: number) => void;   // controlled index into the visible steps
  finishLabel?; nextLabel?; backLabel?; cancelLabel?: ReactNode; allowJumpBack?: boolean /* default true */; className? }
function Stepper(p: StepperProps)

// TreeView.tsx
type TreeNode<T = unknown> = { id: string; label: ReactNode; textLabel?: string; hasChildren?: boolean;
  icon?: ReactNode; disabled?: boolean; data?: T }
type CheckState = boolean | 'mixed'
type TreeViewProps<T = unknown> = { ariaLabel: string; roots?: TreeNode<T>[];
  onLoadChildren: (node: TreeNode<T> | null) => Promise<TreeNode<T>[]>;   // null = roots (used when roots is omitted)
  checkable?: boolean; checked?: string[]; onCheckedChange?: (ids: string[]) => void;
  selectedId?: string | null; onSelect?: (n: TreeNode<T>) => void; onActivate?: (n: TreeNode<T>) => void;
  defaultExpanded?: string[]; emptyMessage?: ReactNode; className? }
function TreeView<T>(p: TreeViewProps<T>)

// PlanPreview.tsx
type PlanResponse<P = unknown> = { plan: P; plan_token: string; plan_digest: string; expires_at: number /* Unix ms */ }
type PlanStat = { label: string; value: ReactNode; tone?: 'slate' | 'green' | 'amber' | 'red' }
type PlanPreviewProps<P = unknown> = { response: PlanResponse<P>; renderPlan?: (plan: P) => ReactNode;
  stats?: PlanStat[]; warnings?: ReactNode[];
  onConfirm: (planToken: string) => void | Promise<void>; onCancel: () => void; onReplan?: () => void | Promise<void>;
  confirmLabel?; cancelLabel?: ReactNode; danger?: boolean; requireText?: string; className? }
function PlanPreview<P>(p: PlanPreviewProps<P>)
function useCountdown(deadlineMs: number): number   // ms remaining, >= 0, ticks every 1 s

// ScheduleEditor.tsx
type Schedule = { kind: 'interval'; every_minutes: number } | { kind: 'daily'; at: string /* 'HH:MM' */ }
              | { kind: 'weekly'; days: number[] /* 0=Sun..6=Sat */; at: string }
type ScheduleKind = Schedule['kind']
type ScheduleEditorProps = { value: Schedule; onChange: (v: Schedule) => void; kinds?: ScheduleKind[];
  minIntervalMinutes?: number /* 5 */; nextRuns?: number[] /* server-computed Unix ms */; disabled?: boolean; label?: string; className? }
function ScheduleEditor(p: ScheduleEditorProps)
function validateSchedule(s: Schedule, minIntervalMinutes = 5): string | null
function describeSchedule(s: Schedule): string          // "Every 2 hours", "Weekdays at 09:00"
function defaultSchedule(kind: ScheduleKind, from?: Schedule): Schedule
const WEEKDAYS: readonly ['Sun', …, 'Sat']

// JobProgress.tsx
type JobProgressState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | (string & {})
type JobProgressProps = { title?: ReactNode; phase?: ReactNode; state?: JobProgressState; bytesDone?: number; bytesTotal?: number;
  filesDone?: number; filesTotal?: number; speed?: number /* B/s */; etaSec?: number /* derived from speed when omitted */;
  error?: ReactNode; actions?: ReactNode; className? }
function JobProgress(p: JobProgressProps)
function progressFraction(p): number | undefined   // bytes preferred, else files
type OperationProgressProps = Omit<JobProgressProps, 'state'|'bytesDone'|'filesDone'|'filesTotal'|'error'> &
  { operationId: OperationId | null | undefined; intervalMs?: number; onFinished?: (op: Operation) => void }
function OperationProgress(p: OperationProgressProps)   // progress {done,total,bytes} -> filesDone/filesTotal/bytesDone

// charts/
type ChartTone = 'brand'|'green'|'amber'|'red'|'sky'|'violet'|'slate'; const chartColors; function toneColor(c?, fallback?): string
type BarDatum = { label: string; value: number; color?: ChartTone | string }
type BarChartProps = { data: BarDatum[]; title: string; format?: (v: number) => string; height?: number; color?; showLabels?: boolean; className? }
type StackSegment = { label: string; value: number; color? }
type BarStackProps = { segments: StackSegment[]; title: string; format?; total?: number; height?: number; showLegend?: boolean; className? }
type SparklineProps = { values: number[]; title: string; format?; area?: boolean; color?; width?: number | string; height?: number; zeroBased?: boolean; className? }
function sparkPath(values, w, h, zeroBased = true): { line: string; area: string; last?: [number, number] }
```

Behaviour notes:
- **TreeView**
  - The selection value is the **minimal set of subtree roots**. An id means the node and everything below it, including unloaded children, which is the shape selective restore needs.
  - Unchecking a child of a checked folder splits the folder into its siblings.
  - When every sibling is checked, they collapse into the parent.
  - The tree renders flat, using `aria-level`, `aria-setsize` and `aria-posinset`, with roving tabindex.
  - Keys follow the APG tree pattern: Up/Down/Home/End/Right/Left, Enter (`onActivate`, or toggle expand) and Space (check, or select).
  - A load error shows inline with a Retry control.
  - Remount it with a new `key` to reset its cache (for example on a snapshot change).
- **PlanPreview**
  - Disables confirm when the plan expires or when execution returns `409 plan_stale`, and then offers Re-plan if `onReplan` is given.
  - A new `plan_token` resets its state.
- **Charts**
  - `role="img"`, with `aria-labelledby` pointing at `<title>` and `<desc>` (the desc lists the values), and a per-bar `<title>` tooltip.
  - Colours use `var(--color-*-500, #fallback)`; the gridline and track use Tailwind `stroke-`/`fill-` classes with dark variants.

## Commands run and results
- `npx tsc -b --noEmit`: clean.
- `npx vitest run src/components/__tests__/{Stepper,TreeView,ScheduleEditor}.test.tsx`: 7/7 passed, 3 runs in a row.
  - One early run of the whole `__tests__` directory showed a failure. It came from my own TreeView test (focus state that had not flushed), and I fixed it by making the keydown handler use the event target's row. Later runs of the whole directory were also green (15/15 together with the other tasks' DataTable and SecretField tests).
- `npx eslint <owned files>`: clean, after fixing one `no-useless-assignment`.
- `npm run build`: OK; `tsc -b` and the vite build succeeded.

## Contract issues
- None new. PlanPreview treats `expires_at` as Unix ms, per api-v1 §1 Timestamps.

## Gaps / follow-ups
- **No cron mode in ScheduleEditor.** Research §3.3 suggests "presets plus cron with next-5-runs computed server-side", but `sdk/schedule` (P1-08) doesn't exist yet and the task fixed the 3-kind type. Cron can be added as a 4th `kind` once SDK-SCHED defines it. The `nextRuns` prop is already there for the server-computed preview.
- **Possible type mismatch.** The `Schedule` type must be kept identical to the `sdk/schedule` JSON format. If P1-08 picks other field names or weekday numbering, update this file. It currently uses 0 = Sunday, local `HH:MM`.
- **Tree size.** TreeView has no virtualization, so very large expanded folders render every row. A follow-up could add windowing, or page children through `onLoadChildren`.
- **Not extracted.** `LocationBar` (Admin) and the Dashboard kind bar were not extracted. `BarStack` can replace both; that is the owning pages' job.

## Notes for dependent tasks / INT
- Import charts from `components/charts` (the barrel file).
- Use `OperationProgress` after `mutate()` returns `{kind:'operation'}`.
- Typical restore and prune flow: `post('/…/plan')` → `<PlanPreview response onConfirm={(t) => post('/…/execute', {plan_token: t})} onReplan={…}>`.
- `vitest.config.ts` imports `./vite.config` without an extension, and Vite warns about this (not my file).
