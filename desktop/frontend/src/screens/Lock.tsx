import { useEffect, useRef, useState } from "react";
import { Fingerprint, Lock as LockIcon, LockKeyhole } from "lucide-react";
import {
  DisableHello,
  EnableHello,
  GetAppLock,
  LockNow,
  SetupAppLock,
  UnlockWithHello,
  UnlockWithPIN,
} from "../../wailsjs/go/main/App";
import type { main } from "../../wailsjs/go/models";
import { Button, Field, Modal, Toggle, useUI } from "../ui";
import { apiMessage } from "../api";
import logo from "../logo.png";

const LEN = 6;

// PinInput is a 6-digit PIN field (digits only, hidden).
function PinInput({ value, onChange, onDone, autoFocus }: { value: string; onChange: (v: string) => void; onDone?: (v: string) => void; autoFocus?: boolean }) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (autoFocus) ref.current?.focus();
  }, [autoFocus]);
  return (
    <input
      ref={ref}
      className="input text-center font-mono text-2xl tracking-[0.6em]"
      type="password"
      inputMode="numeric"
      autoComplete="off"
      maxLength={LEN}
      value={value}
      placeholder="••••••"
      onChange={(e) => {
        const v = e.target.value.replace(/\D/g, "").slice(0, LEN);
        onChange(v);
        if (v.length === LEN) onDone?.(v);
      }}
    />
  );
}

// LockScreen is shown whenever the app is locked: after every Windows start,
// restart or update, and after Lock now.
export function LockScreen({ lock, onUnlocked }: { lock: main.AppLock; onUnlocked: () => void }) {
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const tried = useRef(false);

  const hello = () => {
    setBusy(true);
    setError("");
    UnlockWithHello()
      .then(onUnlocked)
      .catch((e) => setError(apiMessage(e)))
      .finally(() => setBusy(false));
  };
  // Windows Hello opens by itself once per lock screen.
  useEffect(() => {
    if (lock.hello && lock.hello_supported && !tried.current) {
      tried.current = true;
      hello();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lock.hello, lock.hello_supported]);

  const submit = (v: string) => {
    setBusy(true);
    setError("");
    UnlockWithPIN(v)
      .then(onUnlocked)
      .catch((e) => {
        setError(apiMessage(e));
        setPin("");
      })
      .finally(() => setBusy(false));
  };

  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="w-full max-w-xs space-y-5 text-center">
        <img src={logo} alt="" className="mx-auto size-14" />
        <div>
          <h1 className="flex items-center justify-center gap-2 text-xl font-semibold">
            <LockIcon className="size-5" /> VaultDock is locked
          </h1>
          <p className="mt-1 text-sm text-slate-500">Syncing and backups continue once you unlock.</p>
        </div>
        <PinInput value={pin} onChange={setPin} onDone={submit} autoFocus />
        {error && <p className="text-sm text-rose-600">{error}</p>}
        <Button variant="primary" className="w-full" loading={busy} disabled={pin.length !== LEN} onClick={() => submit(pin)}>
          Unlock
        </Button>
        {lock.hello && lock.hello_supported && (
          <Button className="w-full" icon={<Fingerprint className="size-4" />} disabled={busy} onClick={hello}>
            Use Windows Hello
          </Button>
        )}
      </div>
    </div>
  );
}

// SetupLock asks for a PIN once: until then the device key is protected by
// Windows only.
export function SetupLock({ onDone }: { onDone: () => void }) {
  const [pin, setPin] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [helloOK, setHelloOK] = useState(false);
  const [useHello, setUseHello] = useState(true);
  const { toast } = useUI();

  useEffect(() => {
    GetAppLock()
      .then((l) => setHelloOK(l.hello_supported))
      .catch(() => {});
  }, []);

  const save = async () => {
    if (pin !== again) {
      setError("The PINs do not match.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await SetupAppLock(pin, "");
      if (helloOK && useHello) {
        await EnableHello().catch((e) => toast("warning", "Windows Hello was not set up", apiMessage(e)));
      }
      onDone();
    } catch (e) {
      setError(apiMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="w-full max-w-sm space-y-4">
        <div className="text-center">
          <LockKeyhole className="mx-auto size-10 text-brand-500" />
          <h1 className="mt-2 text-xl font-semibold">Protect VaultDock with a PIN</h1>
          <p className="mt-1 text-sm text-slate-500">
            This PC's VaultDock credentials are encrypted with your PIN{helloOK ? " or Windows Hello" : ""}. VaultDock starts locked every time Windows starts and
            syncs again once you unlock it.
          </p>
        </div>
        <Field label="Choose a 6-digit PIN">
          <PinInput value={pin} onChange={setPin} autoFocus />
        </Field>
        <Field label="Enter it again">
          <PinInput value={again} onChange={setAgain} />
        </Field>
        {helloOK && <Toggle checked={useHello} onChange={setUseHello} label="Also unlock with Windows Hello" description="Face, fingerprint or your Windows Hello PIN." />}
        {error && <p className="text-sm text-rose-600">{error}</p>}
        <Button variant="primary" className="w-full" loading={busy} disabled={pin.length !== LEN || again.length !== LEN} onClick={save}>
          Set PIN
        </Button>
        <p className="text-center text-xs text-slate-400">Forgot it later? Disconnect and reconnect this PC; your files on the server are not affected.</p>
      </div>
    </div>
  );
}

// LockSettings is the App lock card in Settings.
export function LockSettings({ className }: { className: string }) {
  const [lock, setLock] = useState<main.AppLock | null>(null);
  const [changing, setChanging] = useState(false);
  const [cur, setCur] = useState("");
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const { toast } = useUI();
  const reload = () => {
    GetAppLock()
      .then(setLock)
      .catch(() => setLock(null));
  };
  useEffect(reload, []);
  if (!lock?.enabled) return null;

  return (
    <section className={className}>
      <h2 className="font-semibold">App lock</h2>
      <p className="text-sm text-slate-500">VaultDock starts locked every time Windows starts. Unlock it with your PIN{lock.hello ? " or Windows Hello" : ""}.</p>
      {lock.hello_supported && (
        <Toggle
          checked={lock.hello}
          label="Unlock with Windows Hello"
          description="Face, fingerprint or your Windows Hello PIN."
          onChange={(v) =>
            (v ? EnableHello() : DisableHello())
              .then(() => toast("success", v ? "Windows Hello set up" : "Windows Hello removed"))
              .catch((e) => toast("error", "Not changed", apiMessage(e)))
              .finally(reload)
          }
        />
      )}
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => setChanging(true)}>Change PIN</Button>
        <Button
          icon={<LockIcon className="size-4" />}
          onClick={() => LockNow().catch((e) => toast("error", "Could not lock", apiMessage(e)))}
        >
          Lock now
        </Button>
      </div>
      <Modal
        open={changing}
        onClose={() => setChanging(false)}
        title="Change PIN"
        footer={
          <>
            <Button onClick={() => setChanging(false)}>Cancel</Button>
            <Button
              variant="primary"
              loading={busy}
              disabled={cur.length !== LEN || pin.length !== LEN}
              onClick={() => {
                setBusy(true);
                SetupAppLock(pin, cur)
                  .then(() => {
                    toast("success", "PIN changed");
                    setChanging(false);
                    setCur("");
                    setPin("");
                  })
                  .catch((e) => toast("error", "PIN not changed", apiMessage(e)))
                  .finally(() => setBusy(false));
              }}
            >
              Change PIN
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <Field label="Current PIN">
            <PinInput value={cur} onChange={setCur} autoFocus />
          </Field>
          <Field label="New PIN">
            <PinInput value={pin} onChange={setPin} />
          </Field>
        </div>
      </Modal>
    </section>
  );
}
