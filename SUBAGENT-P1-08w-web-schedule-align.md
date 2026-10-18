# P1-08w WEB-0 align web ScheduleEditor with sdk/schedule
Status: completed

## Goal
Make the web `Schedule` type and `ScheduleEditor` match the wire `JobSchedule` (`sdk/wire/jobs.go`) and the `sdk/schedule` semantics (P1-08): `type` instead of `kind`, ISO `weekdays` (1 = Monday … 7 = Sunday) instead of `days` (0 = Sunday), plus `expr`, `timezone`, `catch_up` and `jitter_minutes`; support all five types.

## Changes (repo: path, created/modified/deleted, why)
vad-server, both modified:
- `web/src/components/ScheduleEditor.tsx`
  - The type is now flat and mirrors the wire JSON (every field always present, as Go marshals them).
  - Adds the cron type (monospace text input with a 5-field hint) and the manual type ("No schedule" text, no extra fields, no next-runs list).
  - Adds a shared row for time zone (hidden for interval and manual), catch-up policy and jitter.
  - The weekday toggles use ISO numbering.
  - `validateSchedule` mirrors `Schedule.Validate`: interval 5..44640 with jitter below the interval; HH:MM; weekdays 1..7, non-empty, no duplicates; a light 5-field cron check; catch_up in `''|run_once|skip`; jitter 0..1440; time zone `''`, `Local`, or an IANA zone accepted by `Intl.DateTimeFormat`.
  - `describeSchedule` covers all types and appends a non-local time zone.
  - `defaultSchedule` carries the other fields over when you switch type.
  - The prop `kinds` is renamed to `types`.
- `web/src/components/__tests__/ScheduleEditor.test.tsx`: rewritten as 4 tests covering validation, description (ISO weekdays), the wire key set, and UI switching across weekly, interval, cron and manual.

## Interfaces exposed
```ts
type ScheduleType = 'interval' | 'daily' | 'weekly' | 'cron' | 'manual'
type CatchUp = 'run_once' | 'skip'
type Schedule = { type: ScheduleType; expr: string; at: string; weekdays: number[] | null; every_minutes: number;
  timezone: string; catch_up: CatchUp | ''; jitter_minutes: number }
type ScheduleEditorProps = { value: Schedule; onChange: (v: Schedule) => void; types?: ScheduleType[];
  minIntervalMinutes?: number /* 5 */; nextRuns?: number[] /* Unix ms */; disabled?: boolean; label?: string; className?: string }
function ScheduleEditor(p: ScheduleEditorProps)
function validateSchedule(s: Schedule, minIntervalMinutes = 5): string | null
function describeSchedule(s: Schedule): string
function defaultSchedule(type: ScheduleType, from?: Schedule): Schedule
function isoWeekday(jsDay: number): number          // Date.getDay() -> ISO (0 -> 7)
const WEEKDAYS: readonly ['Mon', …, 'Sun']          // name of ISO day d = WEEKDAYS[d - 1]
const MAX_EVERY_MINUTES = 44640; const MAX_JITTER_MINUTES = 1440
```

## Commands run and results
All in `vad-server/web`:
- `npm run build`: OK.
- `npx vitest run`: 7 files, 21/21 tests passed.
- `npm run lint`: 0 errors and 102 warnings. All the warnings are pre-existing `no-explicit-any` in other files; `eslint` on the two owned files is clean.

## Contract issues
- None new. This follows P1-08's ISO weekday reading (1 = Monday), which still needs INT confirmation.

## Gaps / follow-ups
- Cron is only checked for 5 fields on the client; the server's `Validate` is authoritative, and its errors should be shown on save.
- The time zone is a free-text field (no IANA picker).

## Notes for dependent tasks / INT
- No page uses `ScheduleEditor` yet, so nothing else had to change.
- **Breaking change** from P0-20: `kind` becomes `type`, `days` becomes `weekdays` (ISO), and the prop `kinds` becomes `types`.
- Job pages can pass the wire `schedule` object straight through. The server may send `weekdays: null`; the editor handles it.
