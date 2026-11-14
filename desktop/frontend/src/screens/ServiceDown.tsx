import { useEffect, useState } from "react";
import { RefreshCw, ShieldAlert } from "lucide-react";
import { ServiceHealth, StartService } from "../../wailsjs/go/main/App";
import type { main } from "../../wailsjs/go/models";
import { Button, useUI } from "../ui";
import { apiMessage } from "../api";
import logo from "../logo.png";

// ServiceDown is shown while the VaultDock background service cannot be
// reached: right after Windows starts, when it was stopped, or when security
// software blocked or quarantined it. The app reloads once it answers.
export function ServiceDown({ onUp }: { onUp: () => void }) {
  const [h, setH] = useState<main.ServiceHealth | null>(null);
  const [busy, setBusy] = useState(false);
  const { toast } = useUI();

  useEffect(() => {
    let live = true;
    const check = () =>
      ServiceHealth()
        .then((v) => {
          if (!live) return;
          setH(v);
          if (v.reachable) onUp();
        })
        .catch(() => {});
    check();
    const t = setInterval(check, 3000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [onUp]);

  const starting = !h || (!h.problem && !h.reachable) || h.state === "start_pending" || h.state === "running";
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-lg space-y-4 text-center">
        <img src={logo} alt="" className="mx-auto size-14" />
        {starting ? (
          <>
            <h1 className="text-xl font-semibold">Starting VaultDock</h1>
            <p className="text-sm text-slate-500">
              Waiting for the VaultDock background service. This usually takes a few seconds after Windows starts.
            </p>
            <RefreshCw className="mx-auto size-5 animate-spin text-slate-400" />
          </>
        ) : (
          <>
            <ShieldAlert className="mx-auto size-8 text-amber-500" />
            <h1 className="text-xl font-semibold">The background service is not running</h1>
            <p className="text-sm text-slate-600 dark:text-slate-300">{h?.problem}</p>
            {h?.hint && <p className="text-sm text-slate-500">{h.hint}</p>}
            {!h?.missing && h?.installed && (
              <Button
                variant="primary"
                loading={busy}
                onClick={() => {
                  setBusy(true);
                  StartService()
                    .catch((e) => toast("error", "Could not start the service", apiMessage(e)))
                    .finally(() => setBusy(false));
                }}
              >
                Start the service
              </Button>
            )}
            <p className="text-xs text-slate-400">Your settings and this PC's connection are kept; nothing needs to be set up again.</p>
          </>
        )}
      </div>
    </div>
  );
}
