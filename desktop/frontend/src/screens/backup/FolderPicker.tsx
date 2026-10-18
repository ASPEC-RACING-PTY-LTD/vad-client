import { useEffect, useState } from 'react'
import { FolderOpen, FolderPlus, X } from 'lucide-react'
import { EncodeFolder, KnownFolders, PickFolder } from '../../../wailsjs/go/main/App'
import type { main } from '../../../wailsjs/go/models'
import { Button, IconButton, cx } from '../../ui'
import { apiMessage } from '../../api'

// Known folders offered as one-click choices, in this order.
const common: { token: string; label: string }[] = [
  { token: '{DOCUMENTS}', label: 'Documents' },
  { token: '{DESKTOP}', label: 'Desktop' },
  { token: '{PICTURES}', label: 'Pictures' },
  { token: '{VIDEOS}', label: 'Videos' },
  { token: '{MUSIC}', label: 'Music' },
  { token: '{DOWNLOADS}', label: 'Downloads' },
  { token: '{SAVEDGAMES}', label: 'Saved Games' },
]

const tokenLabel: Record<string, string> = Object.fromEntries(common.map((c) => [c.token, c.label]))

// displayPath shows a token path as "Documents › Projects".
export function displayPath(tp: string): string {
  const m = /^(\{[^}]+\})(?:\/(.*))?$/.exec(tp)
  if (!m) return tp
  const head = tokenLabel[m[1]] ?? (m[1] === '{PROFILE}' ? 'Your profile' : m[1])
  return m[2] ? `${head} › ${m[2].split('/').join(' › ')}` : head
}

// FolderPicker edits a list of token paths (backup-format §6). Known
// folders are toggled directly; "Choose folder…" opens the Windows picker
// and converts the result with EncodeFolder (folders outside the user's
// known folders are refused in Simple mode).
export function FolderPicker({ value, onChange }: { value: string[]; onChange: (paths: string[]) => void }) {
  const [known, setKnown] = useState<main.KnownFolder[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    KnownFolders()
      .then(setKnown)
      .catch((e) => setError(apiMessage(e)))
  }, [])
  const byToken: Record<string, main.KnownFolder> = Object.fromEntries(known.map((k) => [k.token, k]))
  const toggle = (tp: string) => onChange(value.includes(tp) ? value.filter((p) => p !== tp) : [...value, tp])
  const browse = async () => {
    setError('')
    try {
      const p = await PickFolder('Choose a folder to back up')
      if (!p) return
      const tp = await EncodeFolder(p)
      if (!value.includes(tp)) onChange([...value, tp])
    } catch (e) {
      setError(apiMessage(e))
    }
  }
  return (
    <div>
      <div className="flex flex-wrap gap-1.5">
        {common
          .filter((c) => !known.length || byToken[c.token]?.exists)
          .map((c) => {
            const on = value.includes(c.token)
            return (
              <button key={c.token} type="button" aria-pressed={on} title={byToken[c.token]?.path} onClick={() => toggle(c.token)} className={cx('flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm', on ? 'border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300' : 'border-slate-300 dark:border-slate-700')}>
                <FolderOpen className="size-4" /> {c.label}
              </button>
            )
          })}
        <Button type="button" size="sm" icon={<FolderPlus className="size-4" />} onClick={browse}>
          Choose folder…
        </Button>
      </div>
      {value.length > 0 && (
        <ul className="mt-3 divide-y divide-slate-100 rounded-lg border border-slate-200 dark:divide-slate-800 dark:border-slate-800" aria-label="Selected folders">
          {value.map((tp) => (
            <li key={tp} className="flex items-center justify-between gap-2 px-3 py-1.5 text-sm">
              <span className="truncate" title={tp}>
                {displayPath(tp)}
              </span>
              <IconButton type="button" label={`Remove ${displayPath(tp)}`} onClick={() => onChange(value.filter((p) => p !== tp))}>
                <X className="size-4" />
              </IconButton>
            </li>
          ))}
        </ul>
      )}
      {error && (
        <p role="alert" className="mt-2 text-xs text-rose-600">
          {error}
        </p>
      )}
    </div>
  )
}
