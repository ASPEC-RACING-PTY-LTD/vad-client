import { useEffect, useState } from "react";
import { Info, Layers } from "lucide-react";
import { ModeTexts } from "../../wailsjs/go/main/App";
import type { main } from "../../wailsjs/go/models";
import { Badge, Button, Modal } from "../ui";
import { modeLabel } from "../modules/registry";

// useModeTexts loads the P1-31 mode texts embedded in the app (the same
// files as the installer).
function useModeTexts() {
  const [texts, setTexts] = useState<Record<string, string>>({});
  useEffect(() => {
    ModeTexts().then(setTexts);
  }, []);
  return texts;
}

function TextBlock({ text }: { text?: string }) {
  if (!text)
    return <p className="text-sm text-slate-500">No description available.</p>;
  return (
    <div className="text-sm leading-relaxed whitespace-pre-wrap text-slate-700 dark:text-slate-300">
      {text.trim()}
    </div>
  );
}

// ModeInfo is the ⓘ row used on the first-run screen: one button per mode
// that opens its plain-language description.
export function ModeInfo() {
  const texts = useModeTexts();
  const [open, setOpen] = useState<"" | "simple" | "advanced" | "compare">("");
  const titles = {
    simple: "Simple Folder Backups",
    advanced: "Full Access to All Tools (Advanced)",
    compare: "Simple and Advanced compared",
  };
  return (
    <div className="flex flex-wrap gap-2">
      {(["simple", "advanced", "compare"] as const).map((k) => (
        <Button
          key={k}
          size="sm"
          icon={<Info className="size-3.5" />}
          onClick={() => setOpen(k)}
        >
          {k === "compare" ? "Compare modes" : titles[k]}
        </Button>
      ))}
      <Modal
        open={open !== ""}
        onClose={() => setOpen("")}
        title={open ? titles[open] : ""}
        size="lg"
        footer={<Button onClick={() => setOpen("")}>Close</Button>}
      >
        <TextBlock text={open ? texts[open] : ""} />
      </Modal>
    </div>
  );
}

const stateTone: Record<string, "green" | "slate" | "amber" | "red" | "blue"> =
  {
    enabled: "green",
    disabled: "slate",
    suspended: "amber",
    wanted: "blue",
    installing: "blue",
    failed: "red",
  };

// ModeSection is Settings → Mode: the current mode, its modules and the
// mode texts (behind a button). Switching is requested from the web
// interface or the installer until the agent API exposes SetMode.
export function ModeSection({ mode }: { mode: main.ModeView | null }) {
  const texts = useModeTexts();
  const [about, setAbout] = useState(false);
  const current =
    mode?.install_mode === "advanced"
      ? "advanced"
      : mode?.install_mode === "simple"
        ? "simple"
        : "";
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 font-semibold">
            <Layers className="size-4" /> Mode{" "}
            <Badge
              tone={
                current === "advanced"
                  ? "violet"
                  : current === "simple"
                    ? "blue"
                    : "slate"
              }
            >
              {modeLabel(mode)}
            </Badge>
          </h2>
          <p className="text-sm text-slate-500">
            What VaultDock can do on this PC.
          </p>
        </div>
        <Button
          size="sm"
          icon={<Info className="size-3.5" />}
          onClick={() => setAbout(true)}
        >
          About the modes
        </Button>
      </div>
      {mode?.source === "mode_file" && (
        <p className="mb-4 text-xs text-slate-500">
          Read from the installer's settings; the service has not reported its
          modules yet.
        </p>
      )}
      {mode?.pending_request && (
        <div className="mb-4 rounded-lg border border-amber-300 p-3 text-sm">
          An administrator asked to switch this PC to{" "}
          <strong>{mode.pending_request.target_mode}</strong> mode. To switch,
          run the VaultDock installer again and choose that mode.
        </div>
      )}
      <div>
        <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-500 uppercase">
          Modules
        </h3>
        {mode?.modules?.length ? (
          <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {mode.modules.map((m) => (
              <li
                key={m.id}
                className="flex items-center justify-between rounded-lg bg-slate-50 px-3 py-2 text-sm dark:bg-slate-800/60"
              >
                <span className="font-medium">{m.id}</span>
                <Badge tone={stateTone[m.state] ?? "slate"}>{m.state}</Badge>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-slate-500">
            The module list is not available.
          </p>
        )}
      </div>
      <Modal
        open={about}
        onClose={() => setAbout(false)}
        title="Modes"
        size="lg"
        footer={<Button onClick={() => setAbout(false)}>Close</Button>}
      >
        <div className="space-y-5">
          <div>
            <h3 className="mb-1 text-sm font-semibold">
              Simple Folder Backups{" "}
              {current === "simple" && <Badge tone="blue">Current</Badge>}
            </h3>
            <TextBlock text={texts.simple} />
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">
              Full Access to All Tools{" "}
              {current === "advanced" && <Badge tone="violet">Current</Badge>}
            </h3>
            <TextBlock text={texts.advanced} />
          </div>
          <div>
            <h3 className="mb-1 text-sm font-semibold">Switching later</h3>
            <TextBlock text={texts.switching} />
          </div>
        </div>
      </Modal>
    </div>
  );
}
