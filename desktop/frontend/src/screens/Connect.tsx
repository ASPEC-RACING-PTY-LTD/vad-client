import { useEffect, useState, type FormEvent } from 'react'
import { AlertTriangle, CheckCircle2, Clock, KeyRound, Loader2, Server, ShieldAlert, ShieldCheck, XCircle } from 'lucide-react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { CancelEnrollment, Connect, ProbeServer } from '../../wailsjs/go/main/App'
import type { agent } from '../../wailsjs/go/models'
import { Button, Field } from '../ui'
import { errMsg } from '../lib'
import { RestartAsAdminButton, isAdminRequired } from '../admin'
import logo from '../logo.png'

type Step = 'server' | 'trust' | 'credentials' | 'pending' | 'result'

function Countdown({ until }: { until: number }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  const s = Math.max(0, Math.floor((until - now) / 1000))
  return (
    <span className="tabular-nums">
      {Math.floor(s / 60)}:{String(s % 60).padStart(2, '0')}
    </span>
  )
}

export function ConnectScreen({ boot, revoked, onDone }: { boot: agent.Bootstrap; revoked: boolean; onDone: () => void }) {
  const [step, setStep] = useState<Step>(boot.enrollment?.status === 'pending' ? 'pending' : 'server')
  const [url, setUrl] = useState(boot.settings.server_url || '')
  const [probe, setProbe] = useState<agent.ProbeResult | null>(null)
  const [pin, setPin] = useState(boot.settings.pinned_cert || '')
  const [login, setLogin] = useState(boot.settings.username || '')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [enroll, setEnroll] = useState<agent.EnrollView | null>(boot.enrollment ?? null)

  useEffect(
    () =>
      EventsOn('enrollment', (v: agent.EnrollView) => {
        setEnroll(v)
        if (v.status === 'approved') {
          setStep('result')
          setTimeout(onDone, 1200)
        } else if (v.status !== 'pending') {
          setStep('result')
        }
      }),
    [onDone],
  )

  const checkServer = async (e?: FormEvent, withPin = pin) => {
    e?.preventDefault()
    setBusy(true)
    setError('')
    try {
      const res = await ProbeServer({ url, pin: withPin })
      setProbe(res)
      setUrl(res.url)
      if (res.untrusted) setStep('trust')
      else setStep('credentials')
    } catch (err) {
      setError(errMsg(err))
    } finally {
      setBusy(false)
    }
  }

  const connect = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const v = await Connect({ url, login, password, pin })
      setPassword('')
      setEnroll(v)
      setStep('pending')
    } catch (err) {
      setError(errMsg(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-full items-center justify-center overflow-hidden px-6 py-10">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_top,rgba(99,102,241,0.18),transparent_60%),radial-gradient(ellipse_at_bottom_right,rgba(14,165,233,0.14),transparent_55%)]" />
      <div className="relative w-full max-w-lg">
        <div className="mb-6 flex flex-col items-center text-center">
          <img src={logo} alt="" className="mb-3 size-20 object-contain drop-shadow-lg" />
          <h1 className="text-2xl font-semibold tracking-tight">{revoked ? 'Reconnect this device' : 'Connect to VaultDock'}</h1>
          <p className="mt-1 text-sm text-slate-500">{revoked ? 'This device’s access was revoked or its credentials are no longer valid. Sign in again and approve it in the web interface.' : 'Sync the folders you choose to your own VaultDock server.'}</p>
        </div>
        <div className="card p-7">
          {step === 'server' && (
            <form onSubmit={checkServer} className="space-y-4">
              <Field label="VaultDock server URL" hint="For example https://vault.example.com or https://192.168.1.20:8443">
                <div className="relative">
                  <Server className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-slate-400" />
                  <input className="input pl-9" autoFocus required placeholder="https://" value={url} onChange={(e) => setUrl(e.target.value)} />
                </div>
              </Field>
              {error && <ErrorBox msg={error} />}
              <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
                Continue
              </Button>
              <p className="text-center text-xs text-slate-500">Connections always use HTTPS with certificate verification. Plain http:// is only accepted for localhost.</p>
            </form>
          )}

          {step === 'trust' && probe && (
            <div className="space-y-4">
              <div className="flex gap-3 rounded-xl bg-amber-50 p-4 text-sm text-amber-900 dark:bg-amber-500/10 dark:text-amber-100">
                <ShieldAlert className="mt-0.5 size-5 shrink-0" />
                <div>
                  <div className="font-semibold">Windows does not trust this server’s certificate</div>
                  <div className="mt-1">Reason: {probe.reason}. This is expected for a server using a self-signed certificate. Only continue if the fingerprint below exactly matches the one shown in your VaultDock server logs (or by your administrator).</div>
                </div>
              </div>
              <div>
                <div className="label">SHA-256 fingerprint</div>
                <div className="rounded-lg bg-slate-100 p-3 font-mono text-xs leading-relaxed break-all dark:bg-slate-800">{probe.fingerprint}</div>
                <div className="mt-1 text-xs text-slate-500">{probe.subject}</div>
              </div>
              {error && <ErrorBox msg={error} />}
              <div className="flex gap-2">
                <Button className="flex-1" onClick={() => setStep('server')}>
                  Cancel
                </Button>
                <Button
                  variant="primary"
                  className="flex-1"
                  loading={busy}
                  icon={<ShieldCheck className="size-4" />}
                  onClick={() => {
                    const fp = (probe.fingerprint ?? '').replace(/:/g, '').toLowerCase()
                    setPin(fp)
                    checkServer(undefined, fp)
                  }}
                >
                  Fingerprint matches, trust it
                </Button>
              </div>
              <p className="text-xs text-slate-500">VaultDock pins exactly this certificate. If it ever changes, the app stops and asks you again.</p>
            </div>
          )}

          {step === 'credentials' && (
            <form onSubmit={connect} className="space-y-4">
              <div className="flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-500/10 dark:text-emerald-300">
                <CheckCircle2 className="size-4" /> {probe?.info?.site_name ?? 'VaultDock'} {probe?.info?.version ? `(${probe.info.version})` : ''} at {url}
                {pin && <span className="ml-auto text-xs">pinned certificate</span>}
              </div>
              <Field label="Username or email">
                <input className="input" autoFocus required autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} />
              </Field>
              <Field label="Password" hint="Used once to request access for this device. It is never stored.">
                <input className="input" type="password" required autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              </Field>
              {error && <ErrorBox msg={error} />}
              <div className="flex gap-2">
                <Button type="button" onClick={() => setStep('server')}>
                  Back
                </Button>
                <Button type="submit" variant="primary" className="flex-1" loading={busy} icon={<KeyRound className="size-4" />}>
                  Connect this device
                </Button>
              </div>
            </form>
          )}

          {step === 'pending' && enroll && (
            <div className="space-y-5 text-center">
              <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-brand-100 text-brand-700 dark:bg-brand-500/15 dark:text-brand-300">
                <Loader2 className="size-6 animate-spin" />
              </div>
              <div>
                <h2 className="text-lg font-semibold">Approve this device in the web interface</h2>
                <p className="mt-1 text-sm text-slate-500">
                  Open VaultDock in your browser, signed in as <strong>{enroll.username}</strong>, go to <strong>Devices</strong> and approve the request. This screen continues automatically.
                </p>
              </div>
              <div className="rounded-2xl border border-slate-200 p-5 dark:border-slate-700">
                <div className="text-xs font-semibold tracking-wide text-slate-500 uppercase">Verification code</div>
                <div className="mt-1 font-mono text-3xl font-bold tracking-[0.25em] text-brand-700 dark:text-brand-300">{enroll.user_code}</div>
                <div className="mt-3 grid grid-cols-2 gap-3 text-left text-xs">
                  <div>
                    <div className="text-slate-500">Device</div>
                    <div className="font-medium">{enroll.device_name}</div>
                  </div>
                  <div>
                    <div className="text-slate-500">Key fingerprint</div>
                    <div className="font-mono">{enroll.key_fingerprint}</div>
                  </div>
                </div>
              </div>
              <div className="flex items-center justify-center gap-1.5 text-sm text-slate-500">
                <Clock className="size-4" /> Request expires in <Countdown until={enroll.expires_at} />
              </div>
              <Button
                onClick={() => {
                  CancelEnrollment()
                  setStep('server')
                }}
              >
                Cancel
              </Button>
            </div>
          )}

          {step === 'result' && enroll && (
            <div className="space-y-4 text-center">
              {enroll.status === 'approved' ? (
                <>
                  <CheckCircle2 className="mx-auto size-12 text-emerald-500" />
                  <h2 className="text-lg font-semibold">Device approved</h2>
                  <p className="text-sm text-slate-500">Setting things up…</p>
                </>
              ) : (
                <>
                  {enroll.status === 'rejected' ? <XCircle className="mx-auto size-12 text-rose-500" /> : <AlertTriangle className="mx-auto size-12 text-amber-500" />}
                  <h2 className="text-lg font-semibold">{enroll.status === 'rejected' ? 'Request rejected' : enroll.status === 'expired' ? 'Request expired' : 'Something went wrong'}</h2>
                  <p className="text-sm text-slate-500">{enroll.error}</p>
                  <Button variant="primary" onClick={() => setStep('credentials')}>
                    Try again
                  </Button>
                </>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function ErrorBox({ msg }: { msg: string }) {
  if (isAdminRequired(msg)) {
    return (
      <div className="flex flex-col gap-2 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">
        <span>Connecting this PC to a server is a machine-wide change, so it needs administrator rights.</span>
        <div>
          <RestartAsAdminButton />
        </div>
      </div>
    )
  }
  return <div className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700 dark:bg-rose-500/10 dark:text-rose-300">{msg}</div>
}
