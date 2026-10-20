import { useEffect, useState } from 'react'
import { ArrowUpFromLine, CheckCircle2, FolderPlus, Gauge, Layers, RefreshCw, Timer, X, XCircle } from 'lucide-react'
import { ListConfigs, SyncNow } from '../../wailsjs/go/main/App'
import type { agent, engine } from '../../wailsjs/go/models'
import { Button, EmptyState, ProgressBar, Stat, useUI } from '../ui'
import { formatBytes, formatDuration, formatSpeed, timeAgo } from '../lib'

const DISMISS_KEY = 'vd.failedDismissedAt'

// limitLine explains the parallel transfer limit under the stat.
function limitLine(s?: engine.Stats) {
  if (!s) return '-'
  const g = s.governor
  if (!g) return `fixed limit ${s.concurrency_limit}`
  if (g.held) return 'waiting: your computer is busy'
  if (g.heavy) return `limit ${s.concurrency_limit} (game running)`
  return `limit ${s.concurrency_limit} of ${g.hardware} for this PC`
}

// phaseLabel is the right-hand status of a transfer row.
function phaseLabel(a: engine.ActiveView) {
  if (a.phase === 'hashing') return 'checking file…'
  if (a.phase === 'starting') return 'starting…'
  if (a.phase === 'finalizing') return 'finishing on the server…'
  return `${formatBytes(a.sent)} / ${formatBytes(a.size)} · ${formatSpeed(a.speed)}`
}

export function ActiveList({ items }: { items: engine.ActiveView[] }) {
  if (!items.length) return <div className="px-5 py-8 text-center text-sm text-slate-500">Nothing is transferring right now.</div>
  return (
    <div className="divide-y divide-slate-100 dark:divide-slate-800">
      {items.map((a) => {
        const finishing = a.phase === 'finalizing'
        return (
          // Fixed row height and a fixed-width status column keep rows from
          // shifting as their numbers change.
          <div key={a.id} className="h-[68px] px-5 py-3">
            <div className="flex items-center justify-between gap-3 text-sm">
              <span className="min-w-0 truncate font-medium" title={a.path}>
                {a.path}
              </span>
              <span className="w-56 shrink-0 truncate text-right text-xs text-slate-500 tabular-nums">{phaseLabel(a)}</span>
            </div>
            <div className="mt-0.5 truncate text-xs text-slate-500">
              {a.config} → {a.remote}
            </div>
            {/* Finishing keeps a full bar (everything was sent) instead of switching to a sliding one. */}
            <ProgressBar
              value={finishing ? 100 : a.size ? (a.sent / a.size) * 100 : 0}
              indeterminate={a.phase === 'hashing' || a.phase === 'starting'}
              className={finishing ? 'mt-2 h-1.5 animate-pulse' : 'mt-2 h-1.5'}
            />
          </div>
        )
      })}
    </div>
  )
}

export function Overview({ snap, boot, goto }: { snap: engine.Snapshot | null; boot: agent.Bootstrap; goto: (t: any) => void }) {
  const [configs, setConfigs] = useState<engine.ConfigView[]>([])
  const { toast } = useUI()
  useEffect(() => {
    const load = () => ListConfigs().then(setConfigs)
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])
  const s = snap?.stats
  // A dismissed failure warning stays hidden until a newer failure.
  const [dismissedAt, setDismissedAt] = useState(() => {
    try {
      return Number(localStorage.getItem(DISMISS_KEY)) || 0
    } catch {
      return 0
    }
  })
  const dismiss = () => {
    const at = s?.last_failed_at ?? Date.now()
    setDismissedAt(at)
    try {
      localStorage.setItem(DISMISS_KEY, String(at))
    } catch {
      // not persisted; hidden for this session
    }
  }
  const showFailed = (s?.failed ?? 0) > 0 && (s?.last_failed_at ?? 0) > dismissedAt
  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Overview</h1>
          <p className="text-sm text-slate-500">
            Connected to {boot.settings.server_url} as {boot.user}
            {boot.server_info?.used !== undefined && ` · ${formatBytes(boot.server_info.used)} stored${boot.server_info.quota ? ` of ${formatBytes(boot.server_info.quota)}` : ''}`}
          </p>
        </div>
        <Button
          icon={<RefreshCw className="size-4" />}
          onClick={() => {
            SyncNow('')
            toast('info', 'Scanning all sync folders')
          }}
        >
          Sync all now
        </Button>
      </div>
      {/* Stats (left) beside the sync folders (right); transfers below. */}
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="grid grid-cols-2 content-start gap-3">
          <Stat compact label="Upload speed" value={formatSpeed(s?.speed_bps ?? 0)} sub={s?.eta_seconds ? `${formatDuration(s.eta_seconds)} remaining` : 'no active uploads'} icon={<Gauge className="size-5" />} />
          <Stat compact label="Parallel transfers" value={`${s?.concurrency ?? 0}`} sub={limitLine(s)} icon={<Layers className="size-5" />} tone="sky" />
          <Stat compact label="Queued" value={`${s?.queued ?? 0}`} sub={formatBytes(s?.pending_bytes ?? 0) + ' to upload'} icon={<Timer className="size-5" />} tone="amber" />
          <Stat compact label="Uploaded this session" value={`${s?.session_completed ?? 0}`} sub={formatBytes(s?.session_bytes ?? 0)} icon={<CheckCircle2 className="size-5" />} tone="green" />
        </div>
        <div className="card flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center justify-between border-b border-slate-200 px-5 py-3 dark:border-slate-800">
            <span className="text-sm font-semibold">Sync folders</span>
            <Button size="sm" variant="ghost" onClick={() => goto('configs')}>
              Manage
            </Button>
          </div>
          {configs.length === 0 ? (
            <EmptyState
              icon={<FolderPlus className="size-6" />}
              title="Choose what to synchronise"
              description="VaultDock only uploads folders and files you select. Add a sync folder to get started; there are presets for Medal clips and screenshots."
              action={
                <Button variant="primary" onClick={() => goto('configs')}>
                  Add a sync folder
                </Button>
              }
            />
          ) : (
            <div className="max-h-[232px] divide-y divide-slate-100 overflow-y-auto dark:divide-slate-800">
              {configs.map((c) => (
                <div key={c.id} className="flex items-center gap-4 px-5 py-3">
                  <span className={`size-2 rounded-full ${!c.enabled ? 'bg-slate-400' : c.last_error ? 'bg-rose-500' : c.files_pending > 0 ? 'bg-sky-500' : 'bg-emerald-500'}`} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium">{c.name}</div>
                    <div className="truncate text-xs text-slate-500">
                      {c.local_path} → {c.destination}
                    </div>
                  </div>
                  <div className="text-right text-xs text-slate-500">
                    <div>
                      {c.files_synced.toLocaleString()} synced{c.files_pending ? ` · ${c.files_pending} pending` : ''}
                    </div>
                    <div>{c.scanning ? 'scanning…' : `scanned ${timeAgo(c.last_scan_at)}`}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
      {(s?.verifying ?? 0) > 0 && (
        <div className="mt-4 rounded-2xl border border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-800 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-200">
          {s?.verifying} uploaded file(s) are being checked by the server. Large syncs are checked once everything is sent; anything that did not arrive intact is uploaded again.
        </div>
      )}
      {showFailed && (
        <div className="mt-4 flex items-center gap-3 rounded-2xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200">
          <button onClick={() => goto('transfers')} className="flex min-w-0 flex-1 items-center gap-3 text-left">
            <XCircle className="size-5 shrink-0" /> {s?.failed} file(s) failed to upload. Open Transfers to see why and retry.
          </button>
          <button onClick={dismiss} title="Dismiss" aria-label="Dismiss" className="shrink-0 rounded-lg p-1 hover:bg-rose-100 dark:hover:bg-rose-500/20">
            <X className="size-4" />
          </button>
        </div>
      )}
      <div className="card mt-6 overflow-hidden">
        <div className="flex items-center gap-2 border-b border-slate-200 px-5 py-3 text-sm font-semibold dark:border-slate-800">
          <ArrowUpFromLine className="size-4 text-brand-500" /> Transferring now
        </div>
        <ActiveList items={snap?.active ?? []} />
      </div>
    </div>
  )
}
