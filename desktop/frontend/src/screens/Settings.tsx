import { useEffect, useState } from 'react'
import { ArrowUpCircle, FileText, LogOut, RefreshCw, RotateCcw } from 'lucide-react'
import { CheckForUpdates, Disconnect, GetSettings, GetUpdateStatus, InstallUpdate, OpenLogsFolder, OpenURL, ResetSyncData, SaveSettings } from '../../wailsjs/go/main/App'
import type { agent, config, engine, updater } from '../../wailsjs/go/models'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { Button, Field, PageLoader, Toggle, useUI } from '../ui'
import { errMsg, timeAgo } from '../lib'

function UpdateLine({ u }: { u: updater.Status }) {
  const checked = u.checked_at ? ` Checked ${timeAgo(u.checked_at)}.` : ''
  switch (u.state) {
    case 'disabled':
      return <>Automatic updates are not available in this build.</>
    case 'checking':
      return <>Checking for updates…</>
    case 'available':
      return <>Version {u.latest} is available.{checked}</>
    case 'downloading':
      return <>Downloading version {u.latest}… {Math.round((u.progress || 0) * 100)}%</>
    case 'ready':
      return <>Version {u.latest} is downloaded and verified, ready to install.</>
    case 'installing':
      return <>Installing version {u.latest}. VaultDock restarts in a few seconds.</>
    case 'error':
      return <span className="text-red-600">{u.error}</span>
    case 'up_to_date':
      return <>You are running the latest version.{checked}</>
    default:
      return <>VaultDock checks for updates automatically.</>
  }
}

const autoHint =
  'VaultDock adds connections only while they make uploads faster, and keeps about a quarter of the processor, memory and disk free. While a game or other graphics-heavy program runs it uses much less, and when the computer is very busy new files wait.'

export function SettingsScreen({ boot, snap, onChanged }: { boot: agent.Bootstrap; snap: engine.Snapshot | null; onChanged: () => void }) {
  const [s, setS] = useState<config.Settings | null>(null)
  const [update, setUpdate] = useState<updater.Status | null>(null)
  const [checking, setChecking] = useState(false)
  const [installing, setInstalling] = useState(false)
  const [directURL, setDirectURL] = useState<string | null>(null)
  const { toast, confirm } = useUI()
  useEffect(() => {
    GetSettings().then(setS)
    GetUpdateStatus().then(setUpdate)
    return EventsOn('update', (u: updater.Status) => setUpdate(u))
  }, [])
  if (!s) return <PageLoader />
  const save = async (patch: Partial<config.Settings>) => {
    const next = { ...s, ...patch } as config.Settings
    setS(next)
    try {
      setS(await SaveSettings(next as any))
    } catch (e) {
      toast('error', 'Could not save settings', errMsg(e))
    }
  }
  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>

      <section className="card space-y-4 p-6">
        <h2 className="font-semibold">Application</h2>
        <Toggle checked={s.auto_start} onChange={(v) => save({ auto_start: v })} label="Start with Windows" description="Starts minimised to the notification area so synchronisation continues after you sign in." />
        <Toggle checked={s.close_to_tray} onChange={(v) => save({ close_to_tray: v })} label="Keep running in the notification area when the window is closed" description="Takes effect after restarting the app. Use Quit in the tray menu to exit." />
        <Toggle checked={s.notifications} onChange={(v) => save({ notifications: v })} label="Desktop notifications" description="Shown by automations that send notifications to the desktop." />
      </section>

      <section className="card space-y-4 p-6">
        <h2 className="font-semibold">Transfers</h2>
        {boot.server_info?.admin ? (
          <Field label="Parallel transfers" hint={autoHint}>
            <select className="input" value={s.concurrency_mode} onChange={(e) => save({ concurrency_mode: e.target.value })}>
              <option value="auto">Automatic: adapt to the connection, the server and this computer</option>
              <option value="manual">Fixed number (administrators only)</option>
            </select>
          </Field>
        ) : (
          <Field label="Parallel transfers" hint={autoHint}>
            <div className="input bg-slate-50 text-slate-600 dark:bg-slate-800/50 dark:text-slate-300">Automatic</div>
          </Field>
        )}
        {boot.server_info?.admin && s.concurrency_mode === 'manual' && (
          <Field label="Fixed parallel transfers" hint="Used as set, whatever else the computer is doing.">
            <input className="input" type="number" min={1} max={128} value={s.manual_parallel} onChange={(e) => save({ manual_parallel: Number(e.target.value) })} />
          </Field>
        )}
        {snap?.stats.governor && (
          <p className="text-sm text-slate-500">
            Now: up to {snap.stats.concurrency_limit} at once (this computer can take up to {snap.stats.governor.hardware}).
            {snap.stats.governor.reason ? ` ${snap.stats.governor.reason[0].toUpperCase()}${snap.stats.governor.reason.slice(1)}.` : ''}
          </p>
        )}
        <Field label="Upload bandwidth limit (KB/s)" hint="0 means unlimited. Leaves room for gaming and calls on slower connections.">
          <input className="input" type="number" min={0} value={s.bandwidth_kbps} onChange={(e) => save({ bandwidth_kbps: Number(e.target.value) })} />
        </Field>
      </section>

      <section className="card space-y-4 p-6">
        <h2 className="font-semibold">Direct uploads on your local network</h2>
        <Toggle
          checked={s.direct_upload}
          onChange={(v) => save({ direct_upload: v, direct_url: (directURL ?? s.direct_url ?? '').trim() })}
          label="Upload directly to the server's local address"
          description="For a PC on the same network as the server: sync and backup uploads skip the public address and its upload and speed limits. VaultDock checks that the address reaches this same server and uses the public address whenever it does not (for example away from home). Traffic over plain http:// is not encrypted on your network. An administrator can turn this off from the admin panel."
        />
        <Field label="Server's local address" hint="The server's IP and port on your network, for example 192.168.1.20:3000 (the port VaultDock is published on).">
          <div className="flex gap-2">
            <input
              className="input"
              placeholder="192.168.1.20:3000"
              value={directURL ?? s.direct_url ?? ''}
              onChange={(e) => setDirectURL(e.target.value)}
            />
            <Button
              disabled={directURL === null || directURL.trim() === (s.direct_url ?? '')}
              onClick={async () => {
                await save({ direct_url: (directURL ?? '').trim() })
                setDirectURL(null)
              }}
            >
              Save
            </Button>
          </div>
        </Field>
        {s.direct_upload && snap?.direct && (
          <p className={`text-sm ${snap.direct.active ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`}>
            {snap.direct.message || 'Checking the local address…'}
          </p>
        )}
        {!s.direct_upload && snap?.direct?.message && <p className="text-sm text-slate-500">{snap.direct.message}</p>}
      </section>

      <section className="card space-y-3 p-6">
        <h2 className="font-semibold">Connection</h2>
        <dl className="grid grid-cols-[160px_1fr] gap-y-2 text-sm">
          <dt className="text-slate-500">Server</dt>
          <dd className="font-mono text-xs break-all">{boot.settings.server_url}</dd>
          <dt className="text-slate-500">Account</dt>
          <dd>{boot.user}</dd>
          <dt className="text-slate-500">This device</dt>
          <dd>
            {boot.device_name} · {boot.os}
          </dd>
          <dt className="text-slate-500">Device key</dt>
          <dd className="font-mono text-xs">{boot.settings.key_fingerprint} (stored with Windows DPAPI)</dd>
          <dt className="text-slate-500">Certificate</dt>
          <dd className="font-mono text-xs break-all">{boot.settings.pinned_cert ? `pinned ${boot.settings.pinned_cert.slice(0, 32)}…` : 'verified by Windows'}</dd>
        </dl>
        <div className="flex flex-wrap gap-2 pt-2">
          <Button
            icon={<RotateCcw className="size-4" />}
            onClick={async () => {
              if (
                await confirm({
                  title: 'Reset sync data?',
                  message:
                    'This PC forgets which files it has already uploaded and clears its transfer history, then scans your sync folders and uploads everything again. Use it after the server was set up fresh. Your local files, sync folders and files already in VaultDock are not changed.',
                  confirmLabel: 'Reset and re-upload',
                })
              ) {
                try {
                  const n = await ResetSyncData()
                  toast('success', 'Sync data reset', `${n} file record(s) cleared. Your folders are being scanned and uploaded again.`)
                } catch (e) {
                  toast('error', 'Could not reset sync data', errMsg(e))
                }
              }
            }}
          >
            Reset sync data
          </Button>
          <Button
            variant="danger"
            icon={<LogOut className="size-4" />}
            onClick={async () => {
              if (await confirm({ title: 'Disconnect this device?', message: 'The device key is deleted from this PC and synchronisation stops. Your sync folders are kept. Files in VaultDock are not affected. Also revoke the device in the web interface if you no longer trust this PC.', confirmLabel: 'Disconnect' })) {
                await Disconnect()
                onChanged()
              }
            }}
          >
            Disconnect
          </Button>
        </div>
      </section>

      <section className="card space-y-4 p-6">
        <h2 className="font-semibold">Updates</h2>
        <Toggle
          checked={s.auto_update}
          onChange={(v) => save({ auto_update: v })}
          label="Install updates automatically"
          description="New versions are downloaded from the official releases, verified against their published checksum, and installed while nothing is syncing. VaultDock restarts by itself and keeps all settings."
        />
        <div className="flex flex-wrap items-center gap-3 rounded-xl bg-slate-50 p-4 text-sm dark:bg-slate-900">
          <ArrowUpCircle className="size-5 shrink-0 text-brand-600" />
          <p className="min-w-0 flex-1">{update ? <UpdateLine u={update} /> : 'Checking…'}</p>
          {update && ['available', 'ready'].includes(update.state) ? (
            <Button
              variant="primary"
              loading={installing}
              onClick={async () => {
                setInstalling(true)
                try {
                  await InstallUpdate()
                } catch (e) {
                  toast('error', 'Update failed', errMsg(e))
                  setInstalling(false)
                }
              }}
            >
              Install and restart
            </Button>
          ) : (
            <Button
              icon={<RefreshCw className="size-4" />}
              loading={checking || update?.state === 'checking'}
              disabled={update?.state === 'disabled' || update?.state === 'downloading' || update?.state === 'installing'}
              onClick={async () => {
                setChecking(true)
                try {
                  setUpdate(await CheckForUpdates())
                } catch (e) {
                  toast('error', 'Update check failed', errMsg(e))
                } finally {
                  setChecking(false)
                }
              }}
            >
              Check now
            </Button>
          )}
        </div>
        {update?.url && update.latest && update.state !== 'up_to_date' && (
          <button className="text-sm font-medium text-brand-600 hover:underline" onClick={() => OpenURL(update.url!)}>
            What's new in {update.latest}
          </button>
        )}
      </section>

      <section className="card space-y-3 p-6">
        <h2 className="font-semibold">About</h2>
        <p className="text-sm text-slate-500">VaultDock desktop {boot.version}</p>
        <div className="flex flex-wrap gap-2">
          <Button icon={<FileText className="size-4" />} onClick={() => OpenLogsFolder()}>
            Open logs folder
          </Button>
        </div>
      </section>
    </div>
  )
}
