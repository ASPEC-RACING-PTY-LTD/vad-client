import { useEffect, useState } from 'react'
import { ArrowUpFromLine, RotateCcw } from 'lucide-react'
import { ListQueue, RetryFailed } from '../../wailsjs/go/main/App'
import type { engine, state } from '../../wailsjs/go/models'
import { Button, ProgressBar, useUI } from '../ui'
import { errMsg, formatBytes, formatSpeed } from '../lib'

// How many transfers the Sync page shows: few enough that it never scrolls.
const ROWS = 5

type Row = { key: string; path: string; detail: string; progress: number; busy: boolean; finishing: boolean }

function activeDetail(a: engine.ActiveView) {
  if (a.phase === 'hashing') return 'checking file…'
  if (a.phase === 'starting') return 'starting…'
  if (a.phase === 'finalizing') return 'finishing on the server…'
  return `${formatBytes(a.sent)} / ${formatBytes(a.size)} · ${formatSpeed(a.speed)}`
}

/**
 * TransfersCard is the compact transfer list on the Sync page: the transfers
 * furthest along first, then the next ones in the queue (first queued at the
 * top), at most ROWS rows, with the queued and failed counts below.
 */
export function TransfersCard({ snap }: { snap: engine.Snapshot | null }) {
  const [queue, setQueue] = useState<state.Item[]>([])
  const [failed, setFailed] = useState(0)
  const { toast } = useUI()
  const load = () => {
    ListQueue('pending')
      .then((q) => setQueue(q ?? []))
      .catch(() => {})
    ListQueue('failed')
      .then((f) => setFailed(f?.length ?? 0))
      .catch(() => {})
  }
  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])

  const active = [...(snap?.active ?? [])].sort((a, b) => {
    const pa = a.phase === 'finalizing' ? 1 : a.size ? a.sent / a.size : 0
    const pb = b.phase === 'finalizing' ? 1 : b.size ? b.sent / b.size : 0
    return pb - pa || b.sent - a.sent || a.started - b.started
  })
  const rows: Row[] = active.slice(0, ROWS).map((a) => ({
    key: 'a' + a.id,
    path: a.path,
    detail: activeDetail(a),
    progress: a.phase === 'finalizing' ? 100 : a.size ? (a.sent / a.size) * 100 : 0,
    busy: a.phase === 'hashing' || a.phase === 'starting',
    finishing: a.phase === 'finalizing',
  }))
  const activeIDs = new Set((snap?.active ?? []).map((a) => a.id))
  for (const q of queue) {
    if (rows.length >= ROWS) break
    if (activeIDs.has(q.id)) continue
    rows.push({ key: 'q' + q.id, path: q.rel_path, detail: `${formatBytes(q.size)} · queued`, progress: 0, busy: false, finishing: false })
  }
  const waiting = Math.max(0, queue.length - Math.max(0, rows.length - active.slice(0, ROWS).length))

  const retry = async () => {
    try {
      const n = await RetryFailed(0)
      toast('success', `Retrying ${n} file(s)`)
      load()
    } catch (e) {
      toast('error', 'Retry failed', errMsg(e))
    }
  }

  return (
    <div className="card mb-5 overflow-hidden">
      <div className="flex items-center gap-2 border-b border-slate-200 px-4 py-2.5 text-sm dark:border-slate-800">
        <ArrowUpFromLine className="size-4 text-brand-500" />
        <span className="font-semibold">Transfers</span>
        <span className="ml-auto text-xs text-slate-500">
          {snap?.stats.speed_bps ? formatSpeed(snap.stats.speed_bps) + ' · ' : ''}
          {waiting > 0 ? `${waiting} more queued` : rows.length ? 'all shown' : ''}
        </span>
        {failed > 0 && (
          <Button size="sm" variant="ghost" className="text-rose-600" icon={<RotateCcw className="size-3.5" />} onClick={retry}>
            Retry {failed} failed
          </Button>
        )}
      </div>
      {rows.length === 0 ? (
        <div className="px-4 py-3 text-sm text-slate-500">Nothing is transferring right now. Uploads resume automatically after network drops and restarts.</div>
      ) : (
        <div className="divide-y divide-slate-100 dark:divide-slate-800">
          {rows.map((r) => (
            <div key={r.key} className="flex h-11 items-center gap-3 px-4">
              <span className="min-w-0 flex-1 truncate text-sm" title={r.path}>
                {r.path}
              </span>
              {/* ProgressBar is full width: the wrapper sets its size so the name keeps its room. */}
              <div className="w-28 shrink-0">
                <ProgressBar value={r.progress} indeterminate={r.busy} className={r.finishing ? 'h-1.5 animate-pulse' : 'h-1.5'} />
              </div>
              <span className="w-52 shrink-0 truncate text-right text-xs text-slate-500 tabular-nums">{r.detail}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
