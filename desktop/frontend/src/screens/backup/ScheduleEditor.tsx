import type { wire } from '../../../wailsjs/go/models'
import { Field, cx } from '../../ui'
import { scheduleError, weekdayNames } from './schedule'

const types: { id: string; label: string }[] = [
  { id: 'daily', label: 'Every day' },
  { id: 'weekly', label: 'Some days' },
  { id: 'interval', label: 'Every few hours' },
  { id: 'manual', label: 'Only by hand' },
]

const intervals = [60, 120, 240, 360, 720]

// ScheduleEditor edits a job schedule (wire.JobSchedule) with the Simple
// choices: daily, weekly, interval and manual.
export function ScheduleEditor({ value, onChange }: { value: wire.JobSchedule; onChange: (s: wire.JobSchedule) => void }) {
  const set = (p: Partial<wire.JobSchedule>) => onChange({ ...value, ...p } as wire.JobSchedule)
  const err = scheduleError(value)
  return (
    <div className="space-y-3">
      <div role="radiogroup" aria-label="When to back up" className="flex flex-wrap gap-1.5">
        {types.map((t) => (
          <button
            key={t.id}
            type="button"
            role="radio"
            aria-checked={value.type === t.id}
            onClick={() => {
              if (t.id === 'interval') set({ type: 'interval', every_minutes: value.every_minutes || 240, jitter_minutes: 0 })
              else if (t.id === 'weekly') set({ type: 'weekly', weekdays: value.weekdays?.length ? value.weekdays : [1, 2, 3, 4, 5], at: value.at || '12:30' })
              else set({ type: t.id, at: value.at || '12:30' })
            }}
            className={cx('rounded-lg border px-3 py-1.5 text-sm', value.type === t.id ? 'border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300' : 'border-slate-300 dark:border-slate-700')}
          >
            {t.label}
          </button>
        ))}
        {value.type === 'cron' && <span className="rounded-lg border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700">Custom: {value.expr}</span>}
      </div>
      {value.type === 'weekly' && (
        <div role="group" aria-label="Days" className="flex flex-wrap gap-1">
          {weekdayNames.map((d, i) => {
            const n = i + 1
            const on = value.weekdays?.includes(n)
            return (
              <button key={d} type="button" aria-pressed={on} onClick={() => set({ weekdays: on ? value.weekdays.filter((x) => x !== n) : [...(value.weekdays ?? []), n].sort() })} className={cx('w-12 rounded-md border py-1 text-xs font-medium', on ? 'border-brand-500 bg-brand-600 text-white' : 'border-slate-300 dark:border-slate-700')}>
                {d}
              </button>
            )
          })}
        </div>
      )}
      {(value.type === 'daily' || value.type === 'weekly') && (
        <div className="max-w-40">
          <Field label="Time">
            <input type="time" className="input" value={value.at} onChange={(e) => set({ at: e.target.value })} />
          </Field>
        </div>
      )}
      {value.type === 'interval' && (
        <div className="max-w-56">
          <Field label="Every">
            <select className="input" value={value.every_minutes} onChange={(e) => set({ every_minutes: Number(e.target.value), jitter_minutes: 0 })}>
              {intervals.map((m) => (
                <option key={m} value={m}>
                  {m / 60} hour{m === 60 ? '' : 's'}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {value.type !== 'manual' && (
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={value.catch_up !== 'skip'} onChange={(e) => set({ catch_up: e.target.checked ? 'run_once' : 'skip' })} />
          If the PC was off at that time, back up once when it is back on
        </label>
      )}
      {err && (
        <p role="alert" className="text-xs text-rose-600">
          {err}
        </p>
      )}
    </div>
  )
}
