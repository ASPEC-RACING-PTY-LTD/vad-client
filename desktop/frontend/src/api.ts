// Errors from the backup/wizard/mode bindings (app_backup.go uiError) carry
// the agent's {"code","message"} as JSON; older bindings return plain text.
export type ApiError = { code: string; message: string }

export function apiError(e: unknown): ApiError {
  const text = typeof e === 'string' ? e : e instanceof Error ? e.message : String(e)
  try {
    const v = JSON.parse(text)
    if (v && typeof v.code === 'string' && typeof v.message === 'string') return v
  } catch {
    // plain text
  }
  return { code: '', message: text }
}

// apiMessage is the text to show for an error. access_denied and
// updates_managed are shown verbatim (INT-FIX-A); forbidden means the call
// needs a Windows administrator.
export function apiMessage(e: unknown): string {
  const err = apiError(e)
  // Bindings that return the agent's plain error text still mean forbidden.
  if (!err.code && /requires an elevated administrator|^forbidden:/i.test(err.message)) err.code = 'forbidden'
  switch (err.code) {
    case 'forbidden':
      window.dispatchEvent(new Event('vaultdock:needs-admin'))
      return 'This needs a Windows administrator. Restart VaultDock as administrator, or ask one to do this step.'
    case 'sik_mismatch':
      return "The server's identity has changed. Backups are stopped until an administrator checks it."
    case 'identity_unpinned':
      return "The server's identity has not been confirmed yet."
    default:
      return err.message
  }
}
