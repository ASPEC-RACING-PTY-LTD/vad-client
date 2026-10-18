import { useEffect, useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { Elevation, RestartAsAdmin } from '../wailsjs/go/main/App'
import { Button } from './ui'

/** Fired by apiMessage when the agent refused an action that needs an elevated administrator. */
export const NEEDS_ADMIN_EVENT = 'vaultdock:needs-admin'

export function isAdminRequired(msg: string): boolean {
  return /forbidden|elevated administrator|Windows administrator/i.test(msg)
}

/** Restarts the app elevated (UAC prompt); this window closes once it is accepted. */
export function RestartAsAdminButton({ size = 'sm' }: { size?: 'sm' | 'md' }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  return (
    <span className="inline-flex flex-col gap-1">
      <Button
        size={size}
        variant="primary"
        icon={<ShieldAlert className="size-4" />}
        loading={busy}
        onClick={async () => {
          setBusy(true)
          setErr('')
          try {
            await RestartAsAdmin()
          } catch (e) {
            setErr(String(e))
          } finally {
            setBusy(false)
          }
        }}
      >
        Restart as administrator
      </Button>
      {err && <span className="text-xs text-rose-600">{err}</span>}
    </span>
  )
}

/** A banner shown after an action was refused for lack of administrator rights (only when the user is an administrator who is not elevated). */
export function AdminBanner() {
  const [show, setShow] = useState(false)
  const [canElevate, setCanElevate] = useState(false)
  useEffect(() => {
    Elevation()
      .then((e) => setCanElevate(e.admin && !e.elevated))
      .catch(() => setCanElevate(false))
    const on = () => setShow(true)
    window.addEventListener(NEEDS_ADMIN_EVENT, on)
    return () => window.removeEventListener(NEEDS_ADMIN_EVENT, on)
  }, [])
  if (!show || !canElevate) return null
  return (
    <div className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm dark:border-amber-700 dark:bg-amber-500/10">
      <span className="flex-1">That change needs administrator rights. Restart VaultDock as administrator, then try again.</span>
      <RestartAsAdminButton />
      <button type="button" className="text-xs text-slate-500 hover:underline" onClick={() => setShow(false)}>
        Dismiss
      </button>
    </div>
  )
}
