# P1-31 Install-mode info texts (Simple and Advanced)
Status: completed

## Goal
Final plain-language info (i) texts for the installer mode page and Settings > Mode:
Simple, Advanced, a side-by-side comparison and a "switching later" text. Each mode
text covers functionality, scope, permissions/privileges, resources and differences.

## Changes (repo: path, created/modified/deleted, why)
- VaultDock: `desktop/build/windows/installer/modes/simple.en.txt` (created): Simple mode text.
- VaultDock: `desktop/build/windows/installer/modes/advanced.en.txt` (created): Advanced mode text.
- VaultDock: `desktop/build/windows/installer/modes/compare.en.txt` (created): side-by-side summary.
- VaultDock: `desktop/build/windows/installer/modes/switching.en.txt` (created): switching later.

All files: ASCII only, LF line endings, no BOM, max line length 74 (limit 80).
Word counts (wc -w, including headings): simple 223, advanced 241, compare 140,
switching 188. Without the section headings both mode texts are about 210-225 words,
at the top of the 120-220 target.

## Interfaces exposed
- File names follow MASTER_PLAN §7.7: `modes/{simple,advanced}.<lang>.txt`, plus the new
  `compare.<lang>.txt` and `switching.<lang>.txt` with the same naming.
- Format: plain text, first line is an upper-case title, blank-line-separated sections
  with a one-line header, bullets start with "- " and continuation lines are indented
  two spaces. LF endings (the spike's NSIS loader normalises LF to CRLF).

## Commands run and results
- `wc -w`, an awk max-line-length check, a non-ASCII grep and `od -c`/`tr -cd '\r'`
  byte checks on all four files: all ASCII, LF only, no BOM, all lines <= 74 chars.
- No build or tests needed (text files only).

## Contract issues
None.

## Gaps / follow-ups
Wording choices INT should confirm (facts not yet settled in the plan or spikes):
1. **Privilege list is unsettled.** The installer spike lists Simple =
   SeChangeNotify/SeImpersonate/SeCreateGlobal and Advanced adds
   SeBackup/SeRestore/SeManageVolume; the VSS and S4U spikes and C6 say SeTcb is needed
   for user tokens (incl. logged-off backups of your own profile) and SeRestore is not
   needed. The texts therefore name no privileges except "the Windows backup privilege"
   in Advanced, and say only "Windows limits the service to a short list of privileges".
   Revisit if Simple ends up holding SeBackup or SeTcb is surfaced in UI.
2. **Size and RAM figures are not specified anywhere.** Texts say "Roughly tens of MB"
   for components (carried over from the spike draft) and describe CPU/memory
   qualitatively. Replace with real numbers once release artefacts exist.
3. **Encryption sentence** says only "Files are encrypted on this PC before they are
   uploaded". It does not mention server-side key escrow (O1/C1, default `full`). INT
   may want a second line, e.g. "Your server also holds keys so you can restore on
   another PC", if the mode texts should cover that.
4. **"Paused items can run again after you switch back to Advanced"** (switching text):
   the plan/contract define `suspended_by_mode` on downgrade but do not state the
   upgrade-time resume behaviour. Confirm DSK-MOD resumes them (or that the user can).
5. **Offline installer** (C16/Q13) is mentioned in no mode text (dropped for length);
   could be added to Advanced > Resources if wanted.
6. Logged-off limitations for other profiles (EFS files wait for sign-in, Entra-only
   accounts only while signed in) were left out of the Advanced text for length; the
   S4U spike recommends showing them on the consent screen instead.
7. Simple says other profiles are "never included". With the Q14 snapshot add-on,
   Simple still covers only your own profile, so this stays true.

## Notes for dependent tasks / INT
- P1-30 (installer): the spike harness loads only simple/advanced; compare and switching
  are intended for the UI (Settings > Mode, first-run screen and the downgrade preview).
  If the installer should show compare.en.txt, add a third (i) link.
- The UI should embed these same files at build time (single source of truth, §7.7).
- The spike drafts in `planning/spikes/installer-modes/modes/` are now superseded.

## Full texts

### simple.en.txt

```text
SIMPLE FOLDER BACKUPS

What it does
- You pick folders, such as Documents, Desktop or Pictures.
- VaultDock backs them up on a schedule to your VaultDock server.
- You can browse your backups and restore files or folders, including
  older versions.

Scope
- Only your own Windows profile (the user who sets up VaultDock).
- Other people's profiles on this PC are never included.

Permissions
- Installing needs administrator rights once. VaultDock adds a Windows
  service, so backups also run when the window is closed.
- The service reads your files using your own Windows permissions, so
  it cannot read a file you could not open.
- Windows limits the service to a short list of privileges.
- VaultDock never changes Windows system files or system settings.
- Files are encrypted on this PC before they are uploaded.

Resources
- Download: the core app only.
- Disk: the program, plus free space for a temporary upload queue.
- Memory and CPU: low; busier only while a backup runs.
- Network: uploads go only to the VaultDock server you connect to.

Compared with Advanced
- No sync, automations, app tools, settings backup or other profiles.
- Files locked by another program, such as an open Outlook file, are
  skipped and retried later. You can add snapshot support for these
  without leaving Simple mode.
```

### advanced.en.txt

```text
FULL ACCESS TO ALL TOOLS (ADVANCED)

What it does
- Everything in Simple Folder Backups, plus:
- One-way and two-way folder sync with your VaultDock server.
- Automations that run backups, syncs or clean-ups for you.
- Finds installed apps, games and dev projects; reinstalls apps on a
  new PC after you confirm each one.
- Backs up app settings and their registry keys.
- Snapshots (Windows shadow copies), so locked files can be backed up.
- Protects other Windows profiles on this PC, if those users agree.

Scope
- Only the folders, apps and settings you select.
- Another user's profile only after that user signs in, opens VaultDock
  and agrees. VaultDock never asks for or stores passwords.

Permissions
- Same administrator install and Windows service as Simple. Files are
  still read with each user's own Windows permissions.
- The service also gets the Windows backup privilege, used only to read
  snapshots. A file the user could not open is skipped.
- Two-way sync can change files in synced folders. Deleted files go to
  the Recycle Bin.
- Apps are installed with winget, Chocolatey or Scoop. VaultDock itself
  never changes Windows system files.

Resources
- Download: extra components from the official VaultDock releases on
  GitHub, checked before use. Roughly tens of MB.
- Memory and CPU: higher during app scans and snapshots. Snapshots
  also use temporary disk space.

Compared with Simple
- More tools, but more permissions, downloads and resources.
```

### compare.en.txt

```text
SIMPLE OR ADVANCED?

                    Simple Folder Backups     Full Access (Advanced)
                    ---------------------     ----------------------
Folder backups      Yes                       Yes
Browse and restore  Yes                       Yes
Sync (1- and 2-way) No                        Yes
Automations         No                        Yes
App discovery and   No                        Yes, you confirm each
  reinstall                                   app
App settings and    No                        Yes, per-app keys only
  registry backup
Locked files        Skipped and retried;      Backed up from snapshots
                    snapshot add-on optional
Whose files         Only yours                Yours, plus other users
                                              who agree
Windows service     Yes                       Yes, plus the Windows
                                              backup privilege
Admin to install    Yes                       Yes
Extra downloads     None                      Components from GitHub,
                                              roughly tens of MB
CPU, memory, disk   Low                       Higher during scans
                                              and snapshots

Both modes:
- Read files with each user's own Windows permissions.
- Never change Windows system files.
- Encrypt files on this PC before upload.
- Can be switched later in Settings > Mode, without reinstalling.
```

### switching.en.txt

```text
SWITCHING MODES LATER

You can switch between Simple and Advanced at any time in Settings > Mode.
You do not need to reinstall. Your backups and settings are kept. Windows
asks for administrator approval before the switch.

Simple to Advanced
- VaultDock downloads and checks the extra components from GitHub.
- The Advanced tools and screens are turned on.
- Your existing backups and jobs are not changed.

Advanced to Simple
- VaultDock first shows what will be paused and asks you to confirm.
- Sync, automations, app and registry jobs, and other users' profiles
  are paused, not deleted.
- Two-way sync stops. No files are deleted on this PC or on the server.
- All backups stay browsable, and files can still be restored.
  Reinstalling apps needs Advanced mode again.
- The extra components are kept for 30 days by default, so switching
  back is quick. You can remove them sooner.

Paused items can run again after you switch back to Advanced.

An administrator can also request a mode change from the VaultDock web
app. It only happens after a local administrator confirms it on this PC.
```
