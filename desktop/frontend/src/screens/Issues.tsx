import { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, RotateCcw } from 'lucide-react'
import { ListBackupJobs, ListIssues, RetrySkipped, SkipIssues } from '../../wailsjs/go/main/App'
import type { agent } from '../../wailsjs/go/models'
import { Badge, Button, EmptyState, ErrorState, useUI } from '../ui'
import { timeAgo } from '../lib'
import { apiMessage } from '../api'
import { issueClass } from './backup/labels'

// IssuesScreen lists the files the latest run of each job skipped, and
// offers "Retry skipped files" per job. Skipped files are left out of that
// run's backup; nothing already backed up is affected.
export function IssuesScreen({ jobID, onJob }: { jobID: number; onJob: (id: number) => void }) {
  const [jobs, setJobs] = useState<agent.BackupJob[]>([])
  const [issues, setIssues] = useState<agent.BackupIssue[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(0)
  const { toast } = useUI()

  const load = useCallback(() => {
    ListBackupJobs()
      .then((j) => setJobs(j ?? []))
      .catch(() => {})
    ListIssues({ job_id: jobID || undefined } as agent.IssueQuery)
      .then((i) => {
        setIssues(i ?? [])
        setError('')
      })
      .catch((e) => setError(apiMessage(e)))
  }, [jobID])
  useEffect(() => {
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [load])

  const retry = async (id: number) => {
    setBusy(id)
    try {
      await RetrySkipped(id)
      toast('success', 'Retrying skipped files')
      load()
    } catch (e) {
      toast('error', 'Could not retry', apiMessage(e))
    } finally {
      setBusy(0)
    }
  }

  const ignore = async (i: agent.BackupIssue) => {
    setBusy(-i.id)
    try {
      await SkipIssues(i.job_id, [i.id], [])
      toast('success', 'Marked as ignored')
      load()
    } catch (e) {
      toast('error', 'Could not ignore', apiMessage(e))
    } finally {
      setBusy(0)
    }
  }

  const jobName = (id: number) => jobs.find((j) => j.id === id)?.name ?? `Backup ${id}`
  const byJob = new Map<number, agent.BackupIssue[]>()
  for (const i of issues ?? []) byJob.set(i.job_id, [...(byJob.get(i.job_id) ?? []), i])

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Skipped files</h1>
          <p className="text-sm text-slate-500">Files the latest backup could not read. They are tried again on the next run, or now with Retry.</p>
        </div>
        <select className="input w-56" aria-label="Backup" value={jobID} onChange={(e) => onJob(Number(e.target.value))}>
          <option value={0}>All backups</option>
          {jobs.map((j) => (
            <option key={j.id} value={j.id}>
              {j.name}
            </option>
          ))}
        </select>
      </div>
      {error && !issues ? (
        <ErrorState error={error} onRetry={load} />
      ) : issues && !issues.length ? (
        <EmptyState icon={<CheckCircle2 className="size-6" />} title="Nothing was skipped" description="The latest backups read every file." />
      ) : (
        <div className="space-y-4">
          {[...byJob.entries()].map(([id, list]) => (
            <div key={id} className="card">
              <div className="flex items-center justify-between gap-3 border-b border-slate-100 px-5 py-3 dark:border-slate-800">
                <div className="font-medium">
                  {jobName(id)} <span className="text-sm font-normal text-slate-500">· {list.length} item{list.length === 1 ? '' : 's'}</span>
                </div>
                <Button size="sm" icon={<RotateCcw className="size-3.5" />} loading={busy === id} disabled={!list.some((i) => i.retryable)} onClick={() => retry(id)}>
                  Retry skipped files
                </Button>
              </div>
              <ul className="divide-y divide-slate-100 dark:divide-slate-800">
                {list.slice(0, 200).map((i) => (
                  <li key={i.id} className="px-5 py-2.5 text-sm">
                    <div className="flex items-center justify-between gap-3">
                      <span className="truncate font-mono text-xs" title={i.path}>
                        {i.path}
                      </span>
                      <span className="flex shrink-0 items-center gap-2">
                        {i.count > 1 && <span className="text-xs text-slate-500">×{i.count}</span>}
                        <Badge tone={i.severity === 'error' ? 'red' : 'amber'}>{issueClass[i.class] ?? i.class}</Badge>
                        <Button size="sm" variant="ghost" loading={busy === -i.id} disabled={!i.reported} onClick={() => ignore(i)}>
                          Ignore
                        </Button>
                      </span>
                    </div>
                    <div className="mt-0.5 flex justify-between gap-3 text-xs text-slate-500">
                      <span className="truncate">{i.detail}</span>
                      <span className="shrink-0">{timeAgo(i.last_seen)}</span>
                    </div>
                  </li>
                ))}
              </ul>
              {list.length > 200 && <div className="px-5 py-2 text-xs text-slate-500">…and {list.length - 200} more</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
