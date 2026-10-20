import { useCallback, useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, HardDriveUpload, Pencil, Play, Plus, RotateCcw } from 'lucide-react'
import { ListBackupJobs, ListBackupRuns, RetrySkipped, RunBackup, SaveBackupJob } from '../../wailsjs/go/main/App'
import type { agent, main } from '../../wailsjs/go/models'
import { Badge, Button, EmptyState, ErrorState, Field, Modal, PageLoader, useUI } from '../ui'
import { formatBytes, timeAgo } from '../lib'
import { apiError, apiMessage } from '../api'
import { FolderPicker, displayPath } from './backup/FolderPicker'
import { ScheduleEditor } from './backup/ScheduleEditor'
import { defaultSchedule, describeSchedule, scheduleError } from './backup/schedule'
import { jobState, runLabel, runTrigger } from './backup/labels'

// JobSpec is the part of wire.JobSpec the screens use (the binding
// generator types BackupJob.spec as any).
type JobSpec = { paths?: string[]; exclude?: string[] }

function jobSpec(j: agent.BackupJob): JobSpec {
  return (j.spec ?? {}) as JobSpec
}

// BackupsScreen lists the backup jobs with their last run and "Run now";
// a job expands to its recent runs. New and edited folder jobs use the
// folder picker and schedule editor.
export function BackupsScreen({ goIssues }: { goIssues: (jobID: number) => void }) {
  const [jobs, setJobs] = useState<agent.BackupJob[] | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [open, setOpen] = useState<number | null>(null)
  const [edit, setEdit] = useState<main.BackupJobDraft | null>(null)
  const { toast } = useUI()

  const load = useCallback(() => {
    ListBackupJobs()
      .then((j) => {
        setJobs(j ?? [])
        setError(null)
      })
      .catch(setError)
  }, [])
  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [load])

  const runNow = async (j: agent.BackupJob) => {
    try {
      await RunBackup(j.id)
      toast('success', `Backing up ${j.name}`)
      load()
    } catch (e) {
      toast('error', 'Could not start the backup', apiMessage(e))
    }
  }
  const retry = async (j: agent.BackupJob) => {
    try {
      await RetrySkipped(j.id)
      toast('success', 'Retrying skipped files', j.name)
      load()
    } catch (e) {
      toast('error', 'Could not retry', apiMessage(e))
    }
  }

  const newDraft = (): main.BackupJobDraft => ({ id: 0, name: 'My files', paths: ['{DOCUMENTS}'], exclude: [], schedule: defaultSchedule(), enabled: true }) as unknown as main.BackupJobDraft

  if (error && !jobs) {
    const unavailable = apiError(error).code === 'unavailable'
    return unavailable ? <EmptyState icon={<HardDriveUpload className="size-6" />} title="Backups are not available" description={apiMessage(error)} /> : <ErrorState error={apiMessage(error)} onRetry={load} />
  }
  if (!jobs) return <PageLoader label="Loading backups" />

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 flex items-end justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">Backups</h2>
          <p className="text-sm text-slate-500">Your folders are backed up automatically on their schedule.</p>
        </div>
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit(newDraft())}>
          New backup
        </Button>
      </div>
      {!jobs.length ? (
        <EmptyState icon={<HardDriveUpload className="size-6" />} title="No backups yet" description="Choose the folders to back up and when." action={<Button variant="primary" onClick={() => setEdit(newDraft())}>Choose folders</Button>} />
      ) : (
        <div className="card divide-y divide-slate-100 dark:divide-slate-800">
          {jobs.map((j) => {
            const last = j.last_run
            const st = runLabel(j.running ? 'uploading' : last?.status)
            const js = jobState[j.effective_state] ?? { label: j.effective_state, tone: 'slate' as const }
            const spec = jobSpec(j)
            const expanded = open === j.id
            return (
              <div key={j.id}>
                <div className="flex flex-wrap items-center gap-3 px-5 py-4">
                  <button type="button" aria-expanded={expanded} aria-label={expanded ? 'Hide runs' : 'Show runs'} onClick={() => setOpen(expanded ? null : j.id)} className="text-slate-500">
                    {expanded ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                  </button>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{j.name}</span>
                      {j.effective_state !== 'active' && <Badge tone={js.tone}>{js.label}</Badge>}
                      {!j.enabled && <Badge>Off</Badge>}
                    </div>
                    <div className="truncate text-xs text-slate-500">
                      {(spec.paths ?? []).map(displayPath).join(', ') || j.kind} · {describeSchedule(j.schedule)}
                    </div>
                  </div>
                  <div className="text-right text-xs">
                    <Badge tone={st.tone}>{st.label}</Badge>
                    <div className="mt-1 text-slate-500">{last ? timeAgo(last.finished_at || last.started_at) : 'never'}</div>
                  </div>
                  <div className="flex gap-1.5">
                    {last?.status === 'partial' || last?.status === 'skipped' ? (
                      <Button size="sm" icon={<RotateCcw className="size-3.5" />} onClick={() => retry(j)} disabled={j.running}>
                        Retry skipped
                      </Button>
                    ) : null}
                    {j.kind === 'folder' && (
                      <Button size="sm" icon={<Pencil className="size-3.5" />} onClick={() => setEdit({ id: j.id, revision: j.revision, name: j.name, paths: spec.paths ?? [], exclude: spec.exclude ?? [], schedule: j.schedule, enabled: j.enabled } as unknown as main.BackupJobDraft)}>
                        Edit
                      </Button>
                    )}
                    <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} onClick={() => runNow(j)} disabled={j.running || j.effective_state !== 'active'}>
                      Run now
                    </Button>
                  </div>
                </div>
                {expanded && <RunList jobID={j.id} onIssues={() => goIssues(j.id)} />}
              </div>
            )
          })}
        </div>
      )}
      {edit && (
        <JobEditor
          draft={edit}
          onClose={() => setEdit(null)}
          onSaved={() => {
            setEdit(null)
            load()
          }}
        />
      )}
    </div>
  )
}

function RunList({ jobID, onIssues }: { jobID: number; onIssues: () => void }) {
  const [runs, setRuns] = useState<agent.BackupRun[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    ListBackupRuns(jobID)
      .then((r) => setRuns(r ?? []))
      .catch((e) => setError(apiMessage(e)))
  }, [jobID])
  if (error) return <div className="px-12 pb-4 text-sm text-rose-600">{error}</div>
  if (!runs) return <div className="px-12 pb-4 text-sm text-slate-500">Loading…</div>
  if (!runs.length) return <div className="px-12 pb-4 text-sm text-slate-500">This backup has not run yet.</div>
  return (
    <div className="px-12 pb-4">
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-slate-500">
          <tr>
            <th className="py-1 font-medium">Started</th>
            <th className="py-1 font-medium">Result</th>
            <th className="py-1 font-medium">Files</th>
            <th className="py-1 font-medium">New data</th>
            <th className="py-1 font-medium">Trigger</th>
          </tr>
        </thead>
        <tbody>
          {runs.slice(0, 10).map((r) => {
            const st = runLabel(r.status)
            return (
              <tr key={r.client_run_uuid} className="border-t border-slate-100 dark:border-slate-800">
                <td className="py-1.5">{r.started_at ? new Date(r.started_at).toLocaleString() : '-'}</td>
                <td className="py-1.5">
                  <Badge tone={st.tone}>{st.label}</Badge>
                  {r.error && <div className="mt-0.5 text-xs text-rose-600">{r.error}</div>}
                </td>
                <td className="py-1.5 tabular-nums">{r.stats?.files_done ?? 0}</td>
                <td className="py-1.5 tabular-nums">{formatBytes(r.stats?.bytes_new ?? 0)}</td>
                <td className="py-1.5 text-slate-500">{runTrigger[r.trigger] ?? r.trigger}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <button type="button" className="mt-2 text-xs font-medium text-brand-600 hover:underline" onClick={onIssues}>
        Show skipped files
      </button>
    </div>
  )
}

function JobEditor({ draft, onClose, onSaved }: { draft: main.BackupJobDraft; onClose: () => void; onSaved: () => void }) {
  const [d, setD] = useState(draft)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { toast } = useUI()
  const set = (p: Partial<main.BackupJobDraft>) => setD({ ...d, ...p } as main.BackupJobDraft)
  const invalid = !d.name.trim() || !d.paths.length || !!scheduleError(d.schedule)
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      await SaveBackupJob(d)
      toast('success', 'Backup saved', d.name)
      onSaved()
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={d.id ? 'Edit backup' : 'New backup'}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={invalid} onClick={save}>
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <Field label="Name">
          <input className="input" value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder="My files" />
        </Field>
        <Field label="Folders" hint="Only folders in your own Windows profile can be backed up in Simple mode.">
          <FolderPicker value={d.paths} onChange={(paths) => set({ paths })} />
        </Field>
        <Field label="When">
          <ScheduleEditor value={d.schedule} onChange={(schedule) => set({ schedule })} />
        </Field>
        {error && (
          <p role="alert" className="text-sm text-rose-600">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}
