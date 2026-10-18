import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, CheckCircle2, Clock, HardDriveUpload, ShieldCheck, XCircle } from 'lucide-react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { Readiness } from '../../wailsjs/go/main/App'
import type { agent } from '../../wailsjs/go/models'
import { Badge, Button, ErrorState, PageLoader, Stat } from '../ui'
import { timeAgo } from '../lib'
import { apiMessage } from '../api'
import { runLabel } from './backup/labels'

// Items the first-run wizard fixes.
const setupCodes = new Set(['identity_unpinned', 'identity_mismatch', 'repo_not_initialised', 'recovery_not_confirmed'])

// ReadinessScreen summarises whether this PC is backing up (agent
// Readiness) and links to the place that fixes each finding.
export function ReadinessScreen({ onSetup, goto }: { onSetup: () => void; goto: (screen: 'backups' | 'issues') => void }) {
  const [r, setR] = useState<agent.BackupReadiness | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    Readiness()
      .then((x) => {
        setR(x)
        setError('')
      })
      .catch((e) => setError(apiMessage(e)))
  }, [])
  useEffect(() => {
    load()
    const t = setInterval(load, 5000)
    const off = EventsOn('backup', load)
    return () => {
      clearInterval(t)
      off()
    }
  }, [load])

  if (error && !r) return <ErrorState error={error} onRetry={load} />
  if (!r) return <PageLoader label="Checking backups" />

  const items = r.items ?? []
  const action = (code: string) => {
    if (setupCodes.has(code)) return <Button size="sm" onClick={onSetup}>Fix</Button>
    if (code === 'no_jobs' || code === 'last_run_failed') return <Button size="sm" onClick={() => goto('backups')}>Open backups</Button>
    if (code === 'open_issues') return <Button size="sm" onClick={() => goto('issues')}>Show files</Button>
    return null
  }
  const last = runLabel(r.last_run?.status)
  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Backup status</h1>
          <p className="text-sm text-slate-500">{r.ready ? 'This PC is backing up.' : 'Something needs attention before backups can run.'}</p>
        </div>
        {r.ready ? <Badge tone="green">Ready</Badge> : <Badge tone="red">Not ready</Badge>}
      </div>
      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Backups" value={r.jobs} sub={`${r.runnable_jobs} active`} icon={<HardDriveUpload className="size-5" />} />
        <Stat label="Running now" value={r.running} icon={<Clock className="size-5" />} tone="sky" />
        <Stat label="Last success" value={timeAgo(r.last_success_at)} sub={r.last_run ? last.label : undefined} icon={<CheckCircle2 className="size-5" />} tone="green" />
        <Stat label="Skipped files" value={r.open_issues} icon={<AlertTriangle className="size-5" />} tone={r.open_issues ? 'amber' : 'brand'} />
      </div>
      <div className="card mb-6">
        {items.length === 0 ? (
          <div className="flex items-center gap-3 px-5 py-4 text-sm">
            <CheckCircle2 className="size-5 text-emerald-600" /> Everything looks good.
          </div>
        ) : (
          <ul className="divide-y divide-slate-100 dark:divide-slate-800">
            {items.map((i) => (
              <li key={i.code} className="flex items-center gap-3 px-5 py-3 text-sm">
                {i.severity === 'error' ? <XCircle className="size-5 shrink-0 text-rose-600" /> : <AlertTriangle className="size-5 shrink-0 text-amber-500" />}
                <span className="flex-1">{i.message}</span>
                {action(i.code)}
              </li>
            ))}
          </ul>
        )}
      </div>
      {r.keys?.available && (
        <div className="card flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-4 text-sm">
          <span className="flex items-center gap-2">
            <ShieldCheck className="size-4 text-slate-500" /> Server identity: <Badge tone={r.keys.pin_state === 'verified' ? 'green' : r.keys.pin_state === 'mismatch' ? 'red' : 'amber'}>{r.keys.pin_state}</Badge>
          </span>
          <span>
            Key escrow: <span className="font-medium">{r.keys.escrow_mode === 'metadata' ? 'only my devices' : r.keys.escrow_mode === 'full' ? 'server can help recover' : '-'}</span>
          </span>
          <span>
            Recovery key: <span className="font-medium">{r.keys.recovery_confirmed ? 'confirmed' : r.keys.repo_state === 'remote' ? 'kept by the first device' : 'not confirmed'}</span>
          </span>
        </div>
      )}
    </div>
  )
}
