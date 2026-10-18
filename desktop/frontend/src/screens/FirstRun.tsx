import { useEffect, useState } from 'react'
import { CheckCircle2, KeyRound, ShieldCheck, ShieldAlert, UserCheck, Database } from 'lucide-react'
import { ConfirmRecoveryKey, ConfirmServerIdentity, GetConsentStatus, GiveProfileConsent, InitRepository, KeyStatus } from '../../wailsjs/go/main/App'
import type { agent, main } from '../../wailsjs/go/models'
import { Badge, Button, CopyButton, Field, cx, useUI } from '../ui'
import { apiMessage } from '../api'
import { ModeInfo } from './Mode'
import logo from '../logo.png'

// needsFirstRun reports whether the backup first-run steps are outstanding:
// the server identity is not pinned (or changed), the repository is not
// created yet, or this device created it and the recovery key was never
// confirmed. A host without a backup stack (per-user mode) never needs it.
export function needsFirstRun(ks: agent.KeyStatus | null): boolean {
  if (!ks || !ks.available || !ks.connected) return false
  if (ks.pin_state === 'unpinned' || ks.pin_state === 'mismatch') return true
  if (ks.repo_state === 'none') return true
  return ks.repo_state === 'initialised' && !ks.recovery_confirmed
}

type Step = 'identity' | 'repository' | 'recovery' | 'confirm' | 'consent' | 'done'

function firstStep(ks: agent.KeyStatus): Step {
  if (ks.pin_state !== 'verified') return 'identity'
  if (ks.repo_state === 'none') return 'repository'
  if (ks.repo_state === 'initialised' && !ks.recovery_confirmed) return 'confirm'
  return 'consent'
}

// formatFingerprint groups a hex fingerprint in fours for comparison.
export function formatFingerprint(fp: string): string {
  const clean = fp.replace(/[^0-9a-fA-F]/g, '').toUpperCase()
  if (!clean) return fp
  return clean.match(/.{1,4}/g)!.join(' ')
}

const stepList: { id: Step; label: string }[] = [
  { id: 'identity', label: 'Server identity' },
  { id: 'repository', label: 'Backup storage' },
  { id: 'recovery', label: 'Recovery key' },
  { id: 'consent', label: 'Your profile' },
]

export function FirstRun({ initial, onDone, onSkip }: { initial: agent.KeyStatus; onDone: () => void; onSkip: () => void }) {
  const [ks, setKs] = useState(initial)
  const [step, setStep] = useState<Step>(() => firstStep(initial))
  // The recovery key lives only in this component's memory, between
  // InitRepository and the confirmation; it is never stored.
  const [recoveryKey, setRecoveryKey] = useState('')
  const [shownBefore, setShownBefore] = useState(false)

  const advance = (next: agent.KeyStatus) => {
    setKs(next)
    setStep(firstStep(next))
  }

  return (
    <div className="flex min-h-full items-start justify-center px-6 py-10">
      <div className="w-full max-w-2xl">
        <div className="mb-6 flex items-center gap-3">
          <img src={logo} alt="" className="size-9 object-contain" />
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Set up backups</h1>
            <p className="text-sm text-slate-500">A few one-time steps keep your backups private and recoverable.</p>
          </div>
          {step !== 'done' && step !== 'recovery' && !(step === 'confirm' && shownBefore) && (
            <button type="button" onClick={onSkip} className="ml-auto text-sm text-slate-500 hover:underline">
              Later
            </button>
          )}
        </div>
        <ol className="mb-5 flex flex-wrap gap-2" aria-label="Setup steps">
          {stepList.map((s, i) => {
            const shown = step === 'confirm' ? 'recovery' : step // confirm only appears for an unfinished earlier setup
            const cur = stepList.findIndex((x) => x.id === shown)
            const done = step === 'done' || i < cur
            return (
              <li key={s.id} aria-current={s.id === shown ? 'step' : undefined} className={cx('rounded-full px-3 py-1 text-xs font-medium', s.id === shown ? 'bg-brand-600 text-white' : done ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300' : 'bg-slate-200 text-slate-600 dark:bg-slate-800 dark:text-slate-400')}>
                {i + 1}. {s.label}
              </li>
            )
          })}
        </ol>
        <div className="card p-6">
          {step === 'identity' && <IdentityStep ks={ks} onDone={advance} onRefresh={setKs} />}
          {step === 'repository' && (
            <RepositoryStep
              onCreated={(key) => {
                setRecoveryKey(key)
                setStep('recovery')
              }}
            />
          )}
          {step === 'recovery' && (
            <RecoveryShowStep
              text={recoveryKey}
              onNext={() => {
                setShownBefore(true)
                setRecoveryKey('')
                setStep('consent')
              }}
            />
          )}
          {step === 'confirm' && (
            <RecoveryConfirmStep
              justShown={shownBefore}
              onConfirmed={() => {
                setRecoveryKey('')
                setStep('consent')
              }}
            />
          )}
          {step === 'consent' && <ConsentStep onNext={() => setStep('done')} />}
          {step === 'done' && (
            <div>
              <div className="mb-4 flex items-center gap-3">
                <CheckCircle2 className="size-7 text-emerald-600" />
                <h2 className="text-lg font-semibold">Backups are ready</h2>
              </div>
              <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">Choose the folders to back up and when. You can change this at any time.</p>
              <ModeInfo />
              <div className="mt-6 flex justify-end">
                <Button variant="primary" onClick={onDone}>
                  Continue
                </Button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function IdentityStep({ ks, onDone, onRefresh }: { ks: agent.KeyStatus; onDone: (k: agent.KeyStatus) => void; onRefresh: (k: agent.KeyStatus) => void }) {
  const [checked, setChecked] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const mismatch = ks.pin_state === 'mismatch'
  const confirm = async () => {
    setBusy(true)
    setError('')
    try {
      onDone(await ConfirmServerIdentity(ks.server_fingerprint))
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        {mismatch ? <ShieldAlert className="size-7 text-rose-600" /> : <ShieldCheck className="size-7 text-brand-600" />}
        <h2 className="text-lg font-semibold">{mismatch ? "The server's identity has changed" : "Check the server's identity"}</h2>
      </div>
      <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">
        {mismatch
          ? 'Backups are stopped. This happens when the server was reinstalled or its keys were replaced, but it can also mean someone is impersonating it. Only continue if your administrator confirms the new fingerprint.'
          : 'Compare this fingerprint with the one shown in the VaultDock web interface (Devices) or given to you by your administrator. It proves your backups go to your server.'}
      </p>
      {mismatch && ks.pinned_fingerprint && (
        <Field label="Previously trusted fingerprint">
          <div className="rounded-lg bg-slate-100 p-3 font-mono text-sm break-all line-through decoration-rose-500/60 dark:bg-slate-800">{formatFingerprint(ks.pinned_fingerprint)}</div>
        </Field>
      )}
      <div className="mt-3">
        <Field label={mismatch ? 'New server fingerprint' : 'Server fingerprint'}>
          {ks.server_fingerprint ? (
            <div className="rounded-lg bg-slate-100 p-3 font-mono text-base tracking-wide break-all dark:bg-slate-800" data-testid="server-fingerprint">
              {formatFingerprint(ks.server_fingerprint)}
            </div>
          ) : (
            <div className="rounded-lg bg-slate-100 p-3 text-sm text-slate-500 dark:bg-slate-800">Waiting for the server…</div>
          )}
        </Field>
      </div>
      <label className="mt-4 flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5" checked={checked} onChange={(e) => setChecked(e.target.checked)} disabled={!ks.server_fingerprint} />
        <span>I compared every character and they match.</span>
      </label>
      {error && (
        <p role="alert" className="mt-3 text-sm text-rose-600">
          {error}
        </p>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button onClick={() => KeyStatus().then(onRefresh).catch((e) => setError(apiMessage(e)))}>Refresh</Button>
        <Button variant="primary" loading={busy} disabled={!checked || !ks.server_fingerprint} onClick={confirm}>
          Trust this server
        </Button>
      </div>
    </div>
  )
}

function RepositoryStep({ onCreated }: { onCreated: (recoveryKey: string) => void }) {
  const [escrow, setEscrow] = useState<'full' | 'metadata'>('full')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const r: agent.InitRepositoryResult = await InitRepository(escrow)
      onCreated(r.recovery_key)
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const options: { id: 'full' | 'metadata'; title: string; text: string }[] = [
    { id: 'full', title: 'Server can help me recover (recommended)', text: 'The server keeps an encrypted copy of the keys, so a new PC can be given access after your administrator approves it, and files can be downloaded from the web interface.' },
    { id: 'metadata', title: 'Only my devices hold the keys', text: 'The server cannot read your files. A new PC needs one of your existing devices, or the recovery key. Web downloads are not available.' },
  ]
  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <Database className="size-7 text-brand-600" />
        <h2 className="text-lg font-semibold">Create your backup storage</h2>
      </div>
      <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">This is the first device on your account, so VaultDock creates your encrypted backup storage now.</p>
      <div role="radiogroup" aria-label="Key escrow" className="space-y-2">
        {options.map((o) => (
          <label key={o.id} className={cx('flex cursor-pointer items-start gap-3 rounded-xl border p-3', escrow === o.id ? 'border-brand-500 bg-brand-50/50 dark:bg-brand-500/10' : 'border-slate-200 dark:border-slate-800')}>
            <input type="radio" name="escrow" className="mt-1" checked={escrow === o.id} onChange={() => setEscrow(o.id)} />
            <span>
              <span className="block text-sm font-medium">{o.title}</span>
              <span className="block text-xs text-slate-500">{o.text}</span>
            </span>
          </label>
        ))}
      </div>
      {error && (
        <p role="alert" className="mt-3 text-sm text-rose-600">
          {error}
        </p>
      )}
      <div className="mt-6 flex justify-end">
        <Button variant="primary" loading={busy} onClick={create}>
          Create backup storage
        </Button>
      </div>
    </div>
  )
}

function RecoveryShowStep({ text, onNext }: { text: string; onNext: () => void }) {
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // Next finalises the storage with the key shown here (no retyping step).
  const next = async () => {
    setBusy(true)
    setError('')
    try {
      await ConfirmRecoveryKey(text)
      onNext()
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <KeyRound className="size-7 text-amber-600" />
        <h2 className="text-lg font-semibold">Save your recovery key</h2>
      </div>
      <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">
        This key restores your backups even if the server is lost. It is shown <strong>only once</strong> and VaultDock does not keep a copy. Store it in a password manager or on paper, away from this PC.
      </p>
      <div className="rounded-xl border-2 border-dashed border-amber-400 bg-amber-50 p-4 font-mono text-base tracking-wide break-all select-text dark:bg-amber-500/10" data-testid="recovery-key">
        {text}
      </div>
      <div className="mt-2 flex justify-end">
        <CopyButton text={text} label="Copy recovery key" />
      </div>
      <label className="mt-4 flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5" checked={saved} onChange={(e) => setSaved(e.target.checked)} />
        <span>I have saved the recovery key somewhere safe.</span>
      </label>
      {error && (
        <p role="alert" className="mt-3 text-sm text-rose-600">
          {error}
        </p>
      )}
      <div className="mt-6 flex justify-end">
        <Button variant="primary" disabled={!saved} loading={busy} onClick={next}>
          Next
        </Button>
      </div>
    </div>
  )
}

function RecoveryConfirmStep({ justShown, onConfirmed }: { justShown: boolean; onConfirmed: () => void }) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const confirm = async () => {
    setBusy(true)
    setError('')
    try {
      await ConfirmRecoveryKey(text.trim())
      setText('')
      onConfirmed()
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (text.trim()) confirm()
      }}
    >
      <div className="mb-4 flex items-center gap-3">
        <KeyRound className="size-7 text-brand-600" />
        <h2 className="text-lg font-semibold">Confirm your recovery key</h2>
      </div>
      <p className="mb-4 text-sm text-slate-600 dark:text-slate-400">
        {justShown ? 'Type the recovery key from your copy to check it was written down correctly.' : 'Your backup storage was set up on this PC but the recovery key was never confirmed. Type the recovery key you saved.'}
      </p>
      <Field label="Recovery key" hint="Paste or type the recovery key you saved.">
        <textarea className="input font-mono" rows={3} autoComplete="off" spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} placeholder="VDRK1-…" />
      </Field>
      {error && (
        <p role="alert" className="mt-3 text-sm text-rose-600">
          {error}
        </p>
      )}
      <div className="mt-6 flex justify-end">
        <Button type="submit" variant="primary" loading={busy} disabled={!text.trim()}>
          Confirm
        </Button>
      </div>
    </form>
  )
}

function ConsentStep({ onNext }: { onNext: () => void }) {
  const [status, setStatus] = useState<main.ConsentStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { toast } = useUI()
  useEffect(() => {
    GetConsentStatus().then(setStatus)
  }, [])
  const allow = async () => {
    setBusy(true)
    setError('')
    try {
      await GiveProfileConsent()
      toast('success', 'Your profile is protected')
      onNext()
    } catch (e) {
      setError(apiMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div>
      <div className="mb-4 flex items-center gap-3">
        <UserCheck className="size-7 text-brand-600" />
        <h2 className="text-lg font-semibold">Allow VaultDock to protect your profile</h2>
      </div>
      <p className="mb-2 text-sm text-slate-600 dark:text-slate-400">
        VaultDock backs up folders in your Windows profile{status?.user ? <> (<span className="font-medium">{status.user}</span>)</> : null}, reading them as you. Nothing is read before you allow it, and you can withdraw this later.
      </p>
      {status && !status.supported && (
        <div className="mt-3 flex items-start gap-2 rounded-lg bg-slate-100 p-3 text-sm dark:bg-slate-800">
          <Badge tone="amber">Later</Badge>
          <span>The VaultDock service on this PC does not accept consent from the app yet. You will be asked again after an update.</span>
        </div>
      )}
      {error && (
        <p role="alert" className="mt-3 text-sm text-rose-600">
          {error}
        </p>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button onClick={onNext}>{status?.supported ? 'Not now' : 'Continue'}</Button>
        {status?.supported && (
          <Button variant="primary" loading={busy} onClick={allow}>
            Allow
          </Button>
        )}
      </div>
    </div>
  )
}
