export function formatBytes(n: number | undefined | null, digits = 1): string {
  if (n === undefined || n === null || isNaN(n)) return '-'
  if (n < 1024) return `${Math.round(n)} B`
  // Binary units throughout: 1 TB is 1024 GB, never shown as "1024 GB".
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  let text = v.toFixed(v >= 100 ? 0 : digits)
  if (Number(text) >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
    text = v.toFixed(digits)
  }
  return `${text} ${units[i]}`
}
export const formatSpeed = (b: number) => (b > 1 ? formatBytes(b) + '/s' : '-')
export function formatDuration(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return '-'
  if (sec < 60) return `${Math.ceil(sec)}s`
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`
  return `${Math.floor(sec / 3600)}h ${Math.round((sec % 3600) / 60)}m`
}
const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
export function timeAgo(ms: number | null | undefined): string {
  if (!ms) return 'never'
  const diff = (ms - Date.now()) / 1000
  const abs = Math.abs(diff)
  if (abs < 45) return 'just now'
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
  return rtf.format(Math.round(diff / 86400), 'day')
}
export function errMsg(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}
