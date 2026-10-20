import { FolderSync } from "lucide-react";
import { cx } from "../ui";
import type { SyncSection } from "../modules/registry";
import { ConfigsScreen } from "./Configs";
import { BackupsScreen } from "./Backups";
import { ReadinessScreen } from "./Readiness";
import { IssuesScreen } from "./Issues";

const labels: Record<SyncSection, string> = {
  configs: "Folders & files",
  backups: "Backups",
  readiness: "Backup status",
  issues: "Skipped files",
};

/**
 * Sync: everything this PC keeps on the server, one page. Synced folders and files, and (with the
 * backup service) backups, their status and the files they skipped.
 */
export function SyncScreen({
  sections,
  section,
  onSection,
  issueJob,
  onIssueJob,
  onSetup,
}: {
  sections: SyncSection[];
  section: SyncSection;
  onSection: (s: SyncSection) => void;
  issueJob: number;
  onIssueJob: (id: number) => void;
  onSetup: () => void;
}) {
  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <FolderSync className="size-6" /> Sync
        </h1>
        {sections.length > 1 && (
          <div
            role="tablist"
            className="flex gap-1 rounded-lg bg-slate-100 p-1 dark:bg-white/[.06]"
          >
            {sections.map((s) => (
              <button
                key={s}
                role="tab"
                aria-selected={section === s}
                onClick={() => onSection(s)}
                className={cx(
                  "rounded-md px-3 py-1 text-[13px] font-medium transition",
                  section === s
                    ? "bg-white text-slate-900 shadow-sm dark:bg-slate-700 dark:text-white"
                    : "text-slate-500 hover:text-slate-800 dark:hover:text-slate-200",
                )}
              >
                {labels[s]}
              </button>
            ))}
          </div>
        )}
      </div>
      {section === "configs" && <ConfigsScreen />}
      {section === "backups" && (
        <BackupsScreen
          goIssues={(id) => {
            onIssueJob(id);
            onSection("issues");
          }}
        />
      )}
      {section === "readiness" && (
        <ReadinessScreen onSetup={onSetup} goto={(s) => onSection(s)} />
      )}
      {section === "issues" && (
        <IssuesScreen jobID={issueJob} onJob={onIssueJob} />
      )}
    </div>
  );
}
