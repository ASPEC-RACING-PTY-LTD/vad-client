import { useCallback, useEffect, useState } from 'react'
import { Activity, AlertTriangle, FolderSync, Globe, HardDriveUpload, Layers, LayoutDashboard, Pause, Play, Settings as SettingsIcon, ShieldCheck, Workflow } from 'lucide-react'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { GetBootstrap, KeyStatus, OpenWebInterface, SetPaused } from '../wailsjs/go/main/App'
import type { agent, engine } from '../wailsjs/go/models'
import { Badge, Button, cx, PageLoader, useUI } from './ui'
import { ConnectScreen } from './screens/Connect'
import { Overview } from './screens/Overview'
import { ConfigsScreen } from './screens/Configs'
import { TransfersScreen } from './screens/Transfers'
import { AutomationsScreen } from './screens/Automations'
import { SettingsScreen } from './screens/Settings'
import { FirstRun, needsFirstRun } from './screens/FirstRun'
import { BackupsScreen } from './screens/Backups'
import { IssuesScreen } from './screens/Issues'
import { ReadinessScreen } from './screens/Readiness'
import { ModeScreen } from './screens/Mode'
import { enabledModules, modeLabel, screenVisible, type ScreenId } from './modules/registry'
import { useMode } from './modules/useMode'
import { AdminBanner } from './admin'
import { apiMessage } from './api'
import logo from './logo.png'

type Tab = ScreenId

const statusTone: Record<string, 'green' | 'blue' | 'amber' | 'red' | 'slate'> = { idle: 'green', syncing: 'blue', scanning: 'blue', paused: 'amber', offline: 'slate', error: 'red', revoked: 'red' }
const statusLabel: Record<string, string> = { idle: 'Up to date', syncing: 'Syncing', scanning: 'Scanning', paused: 'Paused', offline: 'Offline', error: 'Attention needed', revoked: 'Access revoked' }

const allNav: { id: Tab; label: string; icon: any }[] = [
  { id: 'readiness', label: 'Backup status', icon: ShieldCheck },
  { id: 'backups', label: 'Backups', icon: HardDriveUpload },
  { id: 'issues', label: 'Skipped files', icon: AlertTriangle },
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'configs', label: 'Sync folders', icon: FolderSync },
  { id: 'transfers', label: 'Transfers', icon: Activity },
  { id: 'automations', label: 'Automations', icon: Workflow },
  { id: 'mode', label: 'Mode', icon: Layers },
  { id: 'settings', label: 'Settings', icon: SettingsIcon },
]

// Backup screens also need the agent's backup stack (the service).
const backupScreens = new Set<Tab>(['readiness', 'backups', 'issues'])

export function App() {
  const [boot, setBoot] = useState<agent.Bootstrap | null>(null)
  const [snap, setSnap] = useState<engine.Snapshot | null>(null)
  const [tab, setTab] = useState<Tab | null>(null)
  const [revoked, setRevoked] = useState(false)
  const [keys, setKeys] = useState<agent.KeyStatus | null>(null)
  const [setupLater, setSetupLater] = useState(false)
  const [forceSetup, setForceSetup] = useState(false)
  const [issueJob, setIssueJob] = useState(0)
  const mode = useMode()
  const { toast } = useUI()

  // KeyStatus drives the backup first-run wizard; a host without a backup
  // stack answers available:false and the wizard never shows.
  const reloadKeys = useCallback(() => {
    KeyStatus()
      .then(setKeys)
      .catch(() => setKeys(null))
  }, [])
  const reload = useCallback(() => {
    GetBootstrap().then(setBoot)
    reloadKeys()
  }, [reloadKeys])

  useEffect(() => {
    reload()
    const offs = [
      EventsOn('snapshot', (s: engine.Snapshot) => setSnap(s)),
      EventsOn('connected', () => {
        setRevoked(false)
        reload()
      }),
      EventsOn('revoked', () => {
        setRevoked(true)
        reload()
      }),
      EventsOn('settings', reload),
      EventsOn('backup', reloadKeys),
    ]
    return () => offs.forEach((o) => o())
  }, [reload, reloadKeys])

  if (!boot) return <PageLoader label="Starting VaultDock" />

  if (!boot.configured || revoked || boot.revoked) {
    return <ConnectScreen boot={boot} revoked={revoked || boot.revoked} onDone={reload} />
  }

  if (keys && (forceSetup || (!setupLater && needsFirstRun(keys)))) {
    const finish = () => {
      setForceSetup(false)
      setSetupLater(true)
      reloadKeys()
    }
    return <FirstRun key={forceSetup ? 'forced' : 'auto'} initial={keys} onDone={finish} onSkip={finish} />
  }

  // Module-gated navigation (MASTER_PLAN §7.7): screens whose modules are
  // not enabled are hidden; their data is untouched.
  const enabled = enabledModules(mode)
  const backupsOn = !!keys?.available
  const nav = allNav.filter((n) => screenVisible(n.id, enabled) && (!backupScreens.has(n.id) || backupsOn))
  const syncOn = screenVisible('overview', enabled)
  const fallback: Tab = syncOn ? 'overview' : (nav[0]?.id ?? 'settings')
  const current: Tab = tab && nav.some((n) => n.id === tab) ? tab : fallback
  const status = snap?.status ?? 'offline'

  return (
    <div className="flex h-full overflow-hidden">
      <aside className="flex w-56 shrink-0 flex-col px-2.5 pt-1 pb-3">
        <div className="mb-3 flex items-center gap-2.5 px-2">
          <img src={logo} alt="" className="size-6 object-contain" />
          <div>
            <div className="text-[13px] leading-tight font-semibold">VaultDock</div>
            <div className="text-[11px] text-slate-500">{boot.user ? `${boot.user} · ` : ''}desktop {boot.version}</div>
          </div>
        </div>
        <button type="button" onClick={() => setTab('mode')} className="mb-4 ml-2 self-start" title="Install mode">
          <Badge tone={mode?.install_mode === 'advanced' ? 'violet' : mode?.install_mode === 'simple' ? 'blue' : 'slate'}>{modeLabel(mode)}</Badge>
        </button>
        <nav className="flex flex-col gap-0.5">
          {nav.map((n) => (
            <button key={n.id} onClick={() => setTab(n.id)} aria-current={current === n.id ? 'page' : undefined} className={cx('flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left text-[13px] font-medium transition-colors', current === n.id ? 'bg-slate-900/[.07] text-slate-900 dark:bg-white/[.07] dark:text-slate-50' : 'text-slate-600 hover:bg-slate-900/[.04] hover:text-slate-900 dark:text-slate-400 dark:hover:bg-white/[.04] dark:hover:text-slate-200')}>
              <n.icon className="size-4 opacity-80" />
              {n.label}
              {n.id === 'transfers' && (snap?.stats.failed ?? 0) > 0 && <span className="ml-auto rounded-full bg-rose-500 px-1.5 text-[10px] font-bold text-white">{snap?.stats.failed}</span>}
            </button>
          ))}
        </nav>
        <div className="mt-auto space-y-2">
          <button onClick={() => OpenWebInterface('/')} className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] text-slate-500 hover:bg-slate-900/[.04] hover:text-slate-900 dark:hover:bg-white/[.04] dark:hover:text-slate-200">
            <Globe className="size-4" /> Open web interface
          </button>
        </div>
      </aside>
      <div className="mr-2 mb-2 flex min-w-0 flex-1 flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-[0_1px_2px_rgb(0_0_0/0.04)] dark:border-white/[.06] dark:bg-slate-900">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b border-slate-200/80 px-5 dark:border-white/[.06]">
          {syncOn ? (
            <>
              <Badge tone={statusTone[status]}>{statusLabel[status] ?? status}</Badge>
              <span className="truncate text-sm text-slate-500">{snap?.message}</span>
            </>
          ) : (
            <span className="truncate text-sm text-slate-500">{boot.settings.server_url}</span>
          )}
          <div className="ml-auto">
            {!syncOn ? null : snap?.paused ? (
              <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} onClick={() => SetPaused(false).then(() => toast('success', 'Synchronisation resumed')).catch((e) => toast('error', 'Could not resume', apiMessage(e)))}>
                Resume
              </Button>
            ) : (
              <Button size="sm" icon={<Pause className="size-3.5" />} onClick={() => SetPaused(true).then(() => toast('info', 'Synchronisation paused', 'In-progress uploads stop and resume later from where they left off.')).catch((e) => toast('error', 'Could not pause', apiMessage(e)))}>
                Pause
              </Button>
            )}
          </div>
        </header>
        <main className="scroll-thin min-h-0 flex-1 overflow-y-auto px-6 py-5">
          <AdminBanner />
          {keys && !keys.available && (
            <div role="status" className="mb-4 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm dark:border-amber-700 dark:bg-amber-500/10">
              <strong>Backups are unavailable on this PC.</strong>{' '}
              {keys.error === 'backup is not available in this agent'
                ? 'VaultDock is not connected to its background service. Restart the PC; if this stays, reinstall VaultDock.'
                : keys.error || 'The backup service could not start.'}
            </div>
          )}
          {current === 'readiness' && <ReadinessScreen onSetup={() => setForceSetup(true)} goto={setTab} />}
          {current === 'backups' && (
            <BackupsScreen
              goIssues={(id) => {
                setIssueJob(id)
                setTab('issues')
              }}
            />
          )}
          {current === 'issues' && <IssuesScreen jobID={issueJob} onJob={setIssueJob} />}
          {current === 'overview' && <Overview snap={snap} boot={boot} goto={setTab} />}
          {current === 'configs' && <ConfigsScreen />}
          {current === 'transfers' && <TransfersScreen snap={snap} />}
          {current === 'automations' && <AutomationsScreen />}
          {current === 'mode' && <ModeScreen mode={mode} />}
          {current === 'settings' && <SettingsScreen boot={boot} snap={snap} onChanged={reload} />}
        </main>
      </div>
    </div>
  )
}
