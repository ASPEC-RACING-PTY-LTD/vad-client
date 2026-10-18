import { useEffect, useState } from 'react'
import { Eye, Film, FileStack, Folder, FolderOpen, FolderPlus, Image, MoreHorizontal, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import { DeleteConfig, ListAutomations, ListConfigs, OpenFolder, PickFiles, PickFolder, PreviewConfig, SaveConfig, SuggestFolders, SyncNow } from '../../wailsjs/go/main/App'
import type { agent, config, engine } from '../../wailsjs/go/models'
import { Badge, Button, cx, EmptyState, Field, IconButton, Menu, Modal, PageLoader, Toggle, useUI } from '../ui'
import { errMsg, formatBytes, timeAgo } from '../lib'

type Cfg = config.SyncConfig

const blank = (): Cfg =>
  ({
    id: '',
    name: '',
    enabled: true,
    kind: 'folder',
    local_path: '',
    files: [],
    destination: '/',
    recursive: true,
    include_subfolders: [],
    exclude_subfolders: [],
    include_extensions: [],
    exclude_extensions: [],
    min_size: 0,
    max_size: 0,
    include_patterns: [],
    exclude_patterns: [],
    modified_after: '',
    skip_hidden: true,
    mode: 'auto',
    scan_interval_minutes: 15,
    stable_seconds: 10,
    automation_mode: 'all',
    automations: [],
    on_conflict: 'replace',
    created_at: 0,
    updated_at: 0,
  }) as unknown as Cfg

function ListInput({ value, onChange, placeholder, mono }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string; mono?: boolean }) {
  const [text, setText] = useState('')
  const add = () => {
    const parts = text.split(',').map((s) => s.trim()).filter(Boolean)
    if (parts.length) onChange([...(value ?? []), ...parts.filter((p) => !(value ?? []).includes(p))])
    setText('')
  }
  return (
    <div>
      {(value ?? []).length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {value.map((v) => (
            <Badge key={v} tone="violet" className={mono ? 'font-mono' : ''}>
              {v}
              <button onClick={() => onChange(value.filter((x) => x !== v))} aria-label={`Remove ${v}`}>
                <X className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
      <input
        className={cx('input', mono && 'font-mono text-xs')}
        value={text}
        placeholder={placeholder}
        onChange={(e) => setText(e.target.value)}
        onBlur={add}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ',') {
            e.preventDefault()
            add()
          }
        }}
      />
    </div>
  )
}

const MB = 1024 * 1024

function PreviewModal({ cfg, onClose }: { cfg: Cfg | null; onClose: () => void }) {
  const [res, setRes] = useState<agent.PreviewResult | null>(null)
  const [err, setErr] = useState('')
  const [filter, setFilter] = useState<'all' | 'upload' | 'skip' | 'filtered' | 'synced'>('all')
  useEffect(() => {
    if (!cfg) return
    setRes(null)
    setErr('')
    PreviewConfig(cfg as any)
      .then(setRes)
      .catch((e) => setErr(errMsg(e)))
  }, [cfg])
  const entries = (res?.entries ?? []).filter((e) => filter === 'all' || e.decision === filter)
  const counts = (res?.entries ?? []).reduce((acc: Record<string, number>, e) => ((acc[e.decision] = (acc[e.decision] ?? 0) + 1), acc), {})
  const tone: Record<string, any> = { upload: 'green', synced: 'slate', skip: 'amber', filtered: 'slate' }
  return (
    <Modal open={!!cfg} onClose={onClose} size="full" title={`Preview: ${cfg?.name || 'new sync folder'}`} description="A dry run of filters and automations against the files on disk. Nothing is uploaded.">
      {err ? (
        <div className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</div>
      ) : !res ? (
        <PageLoader label="Scanning files" />
      ) : (
        <>
          <div className="mb-3 flex flex-wrap items-center gap-2">
            {(['all', 'upload', 'synced', 'skip', 'filtered'] as const).map((f) => (
              <button key={f} onClick={() => setFilter(f)} className={cx('rounded-full px-3 py-1 text-xs font-medium', filter === f ? 'bg-brand-600 text-white' : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300')}>
                {f === 'all' ? `All (${res.entries.length})` : `${f} (${counts[f] ?? 0})`}
              </button>
            ))}
            <span className="ml-auto text-xs text-slate-500">{res.total > res.entries.length ? `Showing the first ${res.entries.length} of ${res.total.toLocaleString()} files` : `${res.total} files`}</span>
          </div>
          <div className="overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-800">
            <table className="w-full text-sm">
              <thead className="bg-slate-50 text-left text-xs text-slate-500 uppercase dark:bg-slate-800/60">
                <tr>
                  <th className="px-3 py-2">File</th>
                  <th className="px-3 py-2">Size</th>
                  <th className="px-3 py-2">Result</th>
                  <th className="px-3 py-2">Destination / reason</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => (
                  <tr key={e.rel_path} className="border-t border-slate-100 dark:border-slate-800">
                    <td className="max-w-xs truncate px-3 py-1.5 font-mono text-xs" title={e.rel_path}>
                      {e.rel_path}
                    </td>
                    <td className="px-3 py-1.5 text-xs text-slate-500">{formatBytes(e.size)}</td>
                    <td className="px-3 py-1.5">
                      <Badge tone={tone[e.decision]}>{e.decision === 'synced' ? 'already synced' : e.decision}</Badge>
                    </td>
                    <td className="px-3 py-1.5 text-xs">
                      {e.remote ? <span className="font-mono text-emerald-700 dark:text-emerald-300">{e.remote}</span> : <span className="text-slate-500">{e.reason}</span>}
                      {(e.tags ?? []).map((t) => (
                        <Badge key={t} tone="violet" className="ml-1">
                          #{t}
                        </Badge>
                      ))}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Modal>
  )
}

function Editor({ initial, onClose, onSaved }: { initial: Cfg | null; onClose: () => void; onSaved: () => void }) {
  const [c, setC] = useState<Cfg>(blank())
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [autos, setAutos] = useState<engine.AutomationView[]>([])
  const [suggest, setSuggest] = useState<Record<string, string>>({})
  const [preview, setPreview] = useState<Cfg | null>(null)
  const { toast } = useUI()
  useEffect(() => {
    if (initial) {
      setC({ ...blank(), ...initial } as Cfg)
      setErr('')
    }
  }, [initial])
  useEffect(() => {
    ListAutomations().then(setAutos)
    SuggestFolders().then(setSuggest)
  }, [])
  const set = (patch: Partial<Cfg>) => setC((x) => ({ ...x, ...patch }) as Cfg)
  const isNew = !initial?.id

  const preset = (kind: 'medal' | 'screenshots' | 'documents') => {
    if (kind === 'medal')
      set({ name: 'Medal clips', kind: 'folder', local_path: suggest.medal ?? c.local_path, destination: '/Medal', recursive: true, include_extensions: ['mp4', 'mkv', 'mov', 'webm'], min_size: 1 * MB, mode: 'auto', stable_seconds: 15, automation_mode: 'all' } as any)
    if (kind === 'screenshots') set({ name: 'Screenshots', kind: 'folder', local_path: suggest.screenshots ?? c.local_path, destination: '/Screenshots', recursive: true, include_extensions: ['png', 'jpg', 'jpeg', 'webp', 'gif'], mode: 'auto', stable_seconds: 5 } as any)
    if (kind === 'documents') set({ name: 'Documents backup', kind: 'folder', local_path: suggest.documents ?? c.local_path, destination: '/Backups/Documents', recursive: true, exclude_patterns: ['~$*', '*.tmp', 'desktop.ini'], mode: 'auto', stable_seconds: 30 } as any)
  }

  const save = async () => {
    setBusy(true)
    setErr('')
    try {
      await SaveConfig(c as any)
      toast('success', isNew ? 'Sync folder added' : 'Sync folder saved', c.mode === 'auto' && c.enabled ? 'Scanning for files now.' : undefined)
      onSaved()
      onClose()
    } catch (e) {
      setErr(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={!!initial}
      onClose={onClose}
      size="xl"
      title={isNew ? 'Add sync folder' : `Edit ${initial?.name}`}
      description="VaultDock only uploads what you select here. Local files are never changed or deleted."
      footer={
        <>
          <Button icon={<Eye className="size-4" />} onClick={() => setPreview({ ...c } as Cfg)} disabled={!c.local_path && !(c.files ?? []).length}>
            Preview
          </Button>
          <div className="flex-1" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} onClick={save}>
            {isNew ? 'Add sync folder' : 'Save changes'}
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        {isNew && (
          <div>
            <div className="label">Start from a preset</div>
            <div className="grid gap-2 sm:grid-cols-3">
              <button onClick={() => preset('medal')} className="flex items-center gap-3 rounded-xl border border-slate-200 p-3 text-left hover:border-brand-500 dark:border-slate-700">
                <Film className="size-5 text-violet-500" />
                <span>
                  <span className="block text-sm font-medium">Medal clips</span>
                  <span className="block text-xs text-slate-500">{suggest.medal ? 'Medal folder found' : 'Videos only, skips tiny files'}</span>
                </span>
              </button>
              <button onClick={() => preset('screenshots')} className="flex items-center gap-3 rounded-xl border border-slate-200 p-3 text-left hover:border-brand-500 dark:border-slate-700">
                <Image className="size-5 text-pink-500" />
                <span>
                  <span className="block text-sm font-medium">Screenshots</span>
                  <span className="block text-xs text-slate-500">Images only</span>
                </span>
              </button>
              <button onClick={() => preset('documents')} className="flex items-center gap-3 rounded-xl border border-slate-200 p-3 text-left hover:border-brand-500 dark:border-slate-700">
                <FileStack className="size-5 text-sky-500" />
                <span>
                  <span className="block text-sm font-medium">Documents backup</span>
                  <span className="block text-xs text-slate-500">Everything except temp files</span>
                </span>
              </button>
            </div>
          </div>
        )}

        <section className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">
            <input className="input" value={c.name} onChange={(e) => set({ name: e.target.value })} placeholder="Medal clips" />
          </Field>
          <Field label="Destination folder in VaultDock" hint="Created automatically. Automations can route files elsewhere.">
            <input className="input font-mono" value={c.destination} onChange={(e) => set({ destination: e.target.value })} placeholder="/Medal" />
          </Field>
        </section>

        <section>
          <div className="label">What to synchronise</div>
          <div className="mb-3 flex gap-2">
            <Button size="sm" variant={c.kind === 'folder' ? 'primary' : 'secondary'} icon={<Folder className="size-3.5" />} onClick={() => set({ kind: 'folder' })}>
              A folder
            </Button>
            <Button size="sm" variant={c.kind === 'files' ? 'primary' : 'secondary'} icon={<FileStack className="size-3.5" />} onClick={() => set({ kind: 'files' })}>
              Individual files
            </Button>
          </div>
          {c.kind === 'folder' ? (
            <div className="space-y-3">
              <div className="flex gap-2">
                <input className="input font-mono text-xs" value={c.local_path} onChange={(e) => set({ local_path: e.target.value })} placeholder="C:\Users\you\Videos\Medal\Clips" />
                <Button
                  icon={<FolderOpen className="size-4" />}
                  onClick={async () => {
                    const p = await PickFolder('Choose a folder to synchronise')
                    if (p) set({ local_path: p, name: c.name || p.split('\\').pop() || '' })
                  }}
                >
                  Browse
                </Button>
              </div>
              <Toggle checked={c.recursive} onChange={(v) => set({ recursive: v })} label="Include subfolders" description="Synchronise everything below this folder (recursively)." />
              {c.recursive && (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="Only these subfolders (optional)" hint="Relative paths, e.g. Valorant">
                    <ListInput value={c.include_subfolders ?? []} onChange={(v) => set({ include_subfolders: v })} placeholder="Add a subfolder" mono />
                  </Field>
                  <Field label="Exclude subfolders" hint="Relative paths, e.g. Temp or Archive/Old">
                    <ListInput value={c.exclude_subfolders ?? []} onChange={(v) => set({ exclude_subfolders: v })} placeholder="Add a subfolder" mono />
                  </Field>
                </div>
              )}
            </div>
          ) : (
            <div className="space-y-2">
              <Button
                icon={<Plus className="size-4" />}
                onClick={async () => {
                  const files = await PickFiles()
                  if (files?.length) set({ files: [...new Set([...(c.files ?? []), ...files])] })
                }}
              >
                Add files
              </Button>
              <div className="max-h-40 space-y-1 overflow-y-auto">
                {(c.files ?? []).map((f) => (
                  <div key={f} className="flex items-center gap-2 rounded-lg bg-slate-50 px-3 py-1.5 font-mono text-xs dark:bg-slate-800">
                    <span className="flex-1 truncate">{f}</span>
                    <button onClick={() => set({ files: (c.files ?? []).filter((x) => x !== f) })} aria-label="Remove file">
                      <X className="size-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </section>

        <section>
          <div className="label">Filters</div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Only these file types" hint="Empty means all types. e.g. mp4, mkv">
              <ListInput value={c.include_extensions ?? []} onChange={(v) => set({ include_extensions: v })} placeholder="mp4, mkv" />
            </Field>
            <Field label="Never these file types">
              <ListInput value={c.exclude_extensions ?? []} onChange={(v) => set({ exclude_extensions: v })} placeholder="tmp, log" />
            </Field>
            <Field label="Minimum size (MB)">
              <input className="input" type="number" min={0} step="any" value={c.min_size ? +(c.min_size / MB).toFixed(3) : ''} onChange={(e) => set({ min_size: Math.round(Number(e.target.value) * MB) })} placeholder="0" />
            </Field>
            <Field label="Maximum size (MB)">
              <input className="input" type="number" min={0} step="any" value={c.max_size ? +(c.max_size / MB).toFixed(3) : ''} onChange={(e) => set({ max_size: Math.round(Number(e.target.value) * MB) })} placeholder="no limit" />
            </Field>
            <Field label="Include name/path patterns" hint="Globs like Replay_* or **/Highlights/**">
              <ListInput value={c.include_patterns ?? []} onChange={(v) => set({ include_patterns: v })} placeholder="*clip*" mono />
            </Field>
            <Field label="Exclude name/path patterns">
              <ListInput value={c.exclude_patterns ?? []} onChange={(v) => set({ exclude_patterns: v })} placeholder="*_draft*" mono />
            </Field>
            <Field label="Only files modified after">
              <input className="input" type="date" value={c.modified_after ?? ''} onChange={(e) => set({ modified_after: e.target.value })} />
            </Field>
            <div className="pt-6">
              <Toggle checked={c.skip_hidden} onChange={(v) => set({ skip_hidden: v })} label="Skip hidden and system files" />
            </div>
          </div>
        </section>

        <section className="grid gap-4 sm:grid-cols-2">
          <Field label="When to synchronise">
            <select className="input" value={c.mode} onChange={(e) => set({ mode: e.target.value })}>
              <option value="auto">Automatically (watch for changes)</option>
              <option value="manual">Manually (only when I press Sync now)</option>
            </select>
          </Field>
          <Field label="If the file already exists in VaultDock">
            <select className="input" value={c.on_conflict} onChange={(e) => set({ on_conflict: e.target.value })}>
              <option value="replace">Replace it (old version goes to trash)</option>
              <option value="rename">Keep both (rename the new file)</option>
              <option value="skip">Keep the existing file</option>
            </select>
          </Field>
          <Field label="Wait until a file stops changing (seconds)" hint="Protects recordings that are still being written.">
            <input className="input" type="number" min={1} max={3600} value={c.stable_seconds} onChange={(e) => set({ stable_seconds: Number(e.target.value) })} />
          </Field>
          <Field label="Full rescan every (minutes)" hint="Catches changes missed while the app was closed.">
            <input className="input" type="number" min={1} max={1440} value={c.scan_interval_minutes} onChange={(e) => set({ scan_interval_minutes: Number(e.target.value) })} />
          </Field>
        </section>

        <section>
          <div className="label">Automations</div>
          <select className="input mb-3 sm:w-80" value={c.automation_mode} onChange={(e) => set({ automation_mode: e.target.value })}>
            <option value="all">Apply all my enabled automations</option>
            <option value="selected">Apply only selected automations</option>
            <option value="none">No automations (upload as-is)</option>
          </select>
          {c.automation_mode === 'selected' && (
            <div className="space-y-1.5">
              {autos.length === 0 && <p className="text-sm text-slate-500">No enabled automations on the server.</p>}
              {autos.map((a) => (
                <label key={a.id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-brand-600"
                    checked={(c.automations ?? []).includes(a.id)}
                    onChange={(e) => set({ automations: e.target.checked ? [...(c.automations ?? []), a.id] : (c.automations ?? []).filter((x) => x !== a.id) })}
                  />
                  {a.name}
                </label>
              ))}
            </div>
          )}
        </section>

        <Toggle checked={c.enabled} onChange={(v) => set({ enabled: v })} label="Enabled" description="Disabled sync folders are not scanned or uploaded." />
        {err && <div className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700 dark:bg-rose-500/10 dark:text-rose-300">{err}</div>}
      </div>
      <PreviewModal cfg={preview} onClose={() => setPreview(null)} />
    </Modal>
  )
}

export function ConfigsScreen() {
  const [configs, setConfigs] = useState<engine.ConfigView[] | null>(null)
  const [editing, setEditing] = useState<Cfg | null>(null)
  const [preview, setPreview] = useState<Cfg | null>(null)
  const { toast, confirm } = useUI()
  const load = () => ListConfigs().then(setConfigs)
  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])
  if (!configs) return <PageLoader />
  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Sync folders</h1>
          <p className="text-sm text-slate-500">One-way synchronisation from this PC to VaultDock. Deleting a local file never deletes its copy in VaultDock.</p>
        </div>
        <Button variant="primary" icon={<FolderPlus className="size-4" />} onClick={() => setEditing(blank())}>
          Add sync folder
        </Button>
      </div>
      {configs.length === 0 ? (
        <div className="card">
          <EmptyState icon={<FolderPlus className="size-6" />} title="Nothing selected yet" description="Pick folders or individual files to synchronise. VaultDock never synchronises a whole drive automatically." action={<Button variant="primary" onClick={() => setEditing(blank())}>Add sync folder</Button>} />
        </div>
      ) : (
        <div className="space-y-3">
          {configs.map((c) => (
            <div key={c.id} className={cx('card p-5', !c.enabled && 'opacity-70')}>
              <div className="flex items-start gap-4">
                <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-amber-100 text-amber-600 dark:bg-amber-500/15">
                  {c.kind === 'files' ? <FileStack className="size-5" /> : <Folder className="size-5" />}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold">{c.name}</span>
                    {!c.enabled && <Badge>disabled</Badge>}
                    <Badge tone={c.mode === 'auto' ? 'violet' : 'slate'}>{c.mode === 'auto' ? (c.watching ? 'watching' : 'automatic') : 'manual'}</Badge>
                    {c.scanning && <Badge tone="blue">scanning…</Badge>}
                  </div>
                  <button className="mt-0.5 block max-w-full truncate text-left font-mono text-xs text-slate-500 hover:underline" onClick={() => OpenFolder(c.local_path)}>
                    {c.kind === 'files' ? `${(c.files ?? []).length} file(s) in ${c.local_path}` : c.local_path}
                  </button>
                  <div className="font-mono text-xs text-slate-500">→ {c.destination}</div>
                  <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-500">
                    <span>{c.files_synced.toLocaleString()} synced</span>
                    {c.files_pending > 0 && <span className="text-sky-600">{c.files_pending} pending</span>}
                    {c.files_failed > 0 && <span className="text-rose-600">{c.files_failed} failed</span>}
                    <span>last scan {timeAgo(c.last_scan_at)}</span>
                    {(c.include_extensions ?? []).length > 0 && <span>types: {(c.include_extensions ?? []).join(', ')}</span>}
                    {c.automation_mode !== 'none' && <span>automations: {c.automation_mode}</span>}
                  </div>
                  {c.last_error && <div className="mt-2 text-xs text-rose-600">{c.last_error}</div>}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    size="sm"
                    icon={<RefreshCw className="size-3.5" />}
                    onClick={() => {
                      SyncNow(c.id)
                      toast('info', `Scanning ${c.name}`)
                    }}
                  >
                    Sync now
                  </Button>
                  <Menu
                    trigger={(o) => (
                      <IconButton label="More" onClick={o}>
                        <MoreHorizontal className="size-4" />
                      </IconButton>
                    )}
                    items={[
                      { label: 'Edit', icon: <Pencil className="size-4" />, onClick: () => setEditing(c as unknown as Cfg) },
                      { label: 'Preview what will sync', icon: <Eye className="size-4" />, onClick: () => setPreview(c as unknown as Cfg) },
                      { label: c.enabled ? 'Disable' : 'Enable', icon: <RefreshCw className="size-4" />, onClick: () => SaveConfig({ ...c, enabled: !c.enabled } as any).then(load) },
                      'divider',
                      {
                        label: 'Remove',
                        icon: <Trash2 className="size-4" />,
                        danger: true,
                        onClick: async () => {
                          if (await confirm({ title: `Remove ${c.name}?`, message: 'VaultDock stops synchronising this selection. Files already uploaded stay in VaultDock, and nothing on this PC is deleted.', confirmLabel: 'Remove' })) {
                            try {
                              await DeleteConfig(c.id)
                              load()
                            } catch (e) {
                              toast('error', 'Could not remove', errMsg(e))
                            }
                          }
                        },
                      },
                    ]}
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
      <Editor initial={editing} onClose={() => setEditing(null)} onSaved={load} />
      <PreviewModal cfg={preview} onClose={() => setPreview(null)} />
    </div>
  )
}
