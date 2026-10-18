import type { wire } from '../../../wailsjs/go/models'

// Weekdays use ISO numbering (sdk/schedule): 1 = Monday … 7 = Sunday.
export const weekdayNames = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

export function localTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

// defaultSchedule is the Simple default: every day at 12:30, catching up
// once after the PC was off.
export function defaultSchedule(): wire.JobSchedule {
  return { type: 'daily', expr: '', at: '12:30', weekdays: [], every_minutes: 0, timezone: localTimezone(), catch_up: 'run_once', jitter_minutes: 10 } as unknown as wire.JobSchedule
}

export function describeSchedule(s: wire.JobSchedule | undefined | null): string {
  if (!s) return 'No schedule'
  switch (s.type) {
    case 'daily':
      return `Every day at ${s.at || '00:00'}`
    case 'weekly': {
      const days = (s.weekdays ?? []).slice().sort().map((d) => weekdayNames[d - 1] ?? '?')
      return `${days.length ? days.join(', ') : 'Weekly'} at ${s.at || '00:00'}`
    }
    case 'interval': {
      const m = s.every_minutes || 0
      return m >= 60 && m % 60 === 0 ? `Every ${m / 60} hour${m === 60 ? '' : 's'}` : `Every ${m} minutes`
    }
    case 'cron':
      return `Custom (${s.expr})`
    case 'manual':
      return 'Only when started by hand'
    default:
      return s.type
  }
}

// scheduleError returns a message for an invalid schedule, or ''.
export function scheduleError(s: wire.JobSchedule): string {
  const timeOk = /^([01]\d|2[0-3]):[0-5]\d$/.test(s.at || '')
  switch (s.type) {
    case 'daily':
      return timeOk ? '' : 'Enter a time such as 12:30.'
    case 'weekly':
      if (!s.weekdays?.length) return 'Pick at least one day.'
      return timeOk ? '' : 'Enter a time such as 12:30.'
    case 'interval':
      return s.every_minutes >= 15 && s.every_minutes <= 7 * 24 * 60 ? '' : 'Choose between 15 minutes and 7 days.'
    case 'manual':
      return ''
    case 'cron':
      return s.expr.trim() ? '' : 'Enter a schedule expression.'
    default:
      return 'Choose a schedule.'
  }
}
