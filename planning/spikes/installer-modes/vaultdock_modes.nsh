####
## VaultDock admin installer pieces (spike P0-S5; not wired into the build).
##
## Include from project.nsi AFTER MUI.nsh and wails_tools.nsh, then:
##   - call VD_Init from .onInit;
##   - call VD_VerifyInstDir from .onVerifyInstDir;
##   - add the two pages:  Page custom VD_ModePageCreate VD_ModePageLeave
##                         Page custom VD_InfoPageCreate
##   - in the install section: VD_StopForUpgrade before copying files, then
##     VD_PrepareDataDir, VD_WriteModeFile, VD_RegisterService (in that order);
##   - in the uninstall section: !insertmacro VD_UninstallService.
##
## The mode texts are a build-time input (single source of truth, MASTER_PLAN
## §7.7): ${VD_MODES_DIR}\simple.en.txt and advanced.en.txt. They must be
## plain ASCII (or converted to UTF-16LE and read with FileReadUTF16LE).
####

!include "LogicLib.nsh"
!include "nsDialogs.nsh"
!include "FileFunc.nsh"
!include "WinMessages.nsh"

!ifndef VD_MODES_DIR
    !define VD_MODES_DIR "modes"
!endif
!ifndef VD_SERVICE_NAME
    !define VD_SERVICE_NAME "VaultDockAgent"
!endif
!ifndef VD_SERVICE_DISPLAY
    !define VD_SERVICE_DISPLAY "VaultDock Agent"
!endif
!ifndef VD_AGENT_EXE
    !define VD_AGENT_EXE "vaultdock-agent.exe"
!endif
!ifndef VD_MODE_FILE_NAME
    # Task text says %ProgramData%\VaultDock\install-mode.json; MASTER_PLAN
    # §7.6 says config\mode.json. P1-30 must pick one (see README).
    !define VD_MODE_FILE_NAME "install-mode.json"
!endif

# Well-known SIDs: locale independent (group names are translated on
# non-English Windows, so never use "Administrators" by name).
!define VD_SID_SYSTEM "*S-1-5-18"
!define VD_SID_ADMINS "*S-1-5-32-544"
!define VD_SID_USERS  "*S-1-5-32-545"

# Service privilege allowlists (sc privs). Simple mode never gets the
# backup/restore privileges; the agent rewrites this list on a mode switch.
!define VD_PRIVS_SIMPLE "SeChangeNotifyPrivilege/SeImpersonatePrivilege/SeCreateGlobalPrivilege"
!define VD_PRIVS_ADVANCED "${VD_PRIVS_SIMPLE}/SeBackupPrivilege/SeRestorePrivilege/SeManageVolumePrivilege"

!ifndef EM_REPLACESEL
    !define EM_REPLACESEL 0x00C2
!endif
!ifndef ES_AUTOVSCROLL
    !define ES_AUTOVSCROLL 0x0040
!endif
!ifndef ES_READONLY
    !define ES_READONLY 0x0800
!endif
!ifndef EM_LIMITTEXT
    !define EM_LIMITTEXT 0x00C5
!endif
!define VD_WM_NOTIFY_OUTER_NEXT 0x408
!define VD_FILE_ATTRIBUTE_REPARSE_POINT 0x400

Var VD_Mode          ; "simple" | "advanced" | "" (keep the existing mode file)
Var VD_ModeExplicit  ; "1" when /MODE= was given on the command line
Var VD_SkipModePage  ; "1" on upgrade (mode already chosen) or silent install
Var VD_IsUpgrade     ; "1" when an admin install of VaultDock already exists
Var VD_InfoWhich     ; "" | "simple" | "advanced": which (i) page to show
Var VD_DataDir       ; %ProgramData%\VaultDock (resolved via the shell API)
Var VD_RadioSimple
Var VD_RadioAdvanced
Var VD_InfoEdit

# ---------------------------------------------------------------- .onInit --
Function VD_Init
    # %ProgramData% from the known-folder API, never from the environment:
    # an elevated installer inherits user-controlled environment variables.
    SetShellVarContext all
    StrCpy $VD_DataDir "$APPDATA\VaultDock"

    SetRegView 64
    StrCpy $VD_IsUpgrade "0"
    ReadRegStr $0 HKLM "${UNINST_KEY}" "UninstallString"
    ${If} $0 != ""
        StrCpy $VD_IsUpgrade "1"
        # Keep the existing install directory on upgrade.
        ReadRegStr $1 HKLM "${UNINST_KEY}" "InstallLocation"
        ${If} $1 != ""
            StrCpy $INSTDIR $1
        ${EndIf}
    ${EndIf}

    # /MODE=simple|advanced (silent installs, fleet deployment).
    StrCpy $VD_Mode ""
    StrCpy $VD_ModeExplicit "0"
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 "/MODE=" $R1
    ${IfNot} ${Errors}
        ${If} $R1 == "simple"
        ${OrIf} $R1 == "advanced"
            StrCpy $VD_Mode $R1
            StrCpy $VD_ModeExplicit "1"
        ${Else}
            MessageBox MB_ICONSTOP "Unknown /MODE value '$R1'. Use /MODE=simple or /MODE=advanced." /SD IDOK
            SetErrorLevel 2
            Quit
        ${EndIf}
    ${EndIf}

    StrCpy $VD_SkipModePage "0"
    ${If} $VD_ModeExplicit == "1"
        StrCpy $VD_SkipModePage "1"
    ${ElseIf} $VD_IsUpgrade == "1"
    ${AndIf} ${FileExists} "$VD_DataDir\${VD_MODE_FILE_NAME}"
        # Upgrade: the mode is changed in Settings > Mode, not by reinstalling.
        StrCpy $VD_SkipModePage "1"
    ${Else}
        StrCpy $VD_Mode "simple"   ; default for fresh and silent installs
    ${EndIf}

    StrCpy $VD_InfoWhich ""
FunctionEnd

# ------------------------------------------------------- .onVerifyInstDir --
# Per-machine only: refuse user profiles (per-user install path, C5), the
# Windows folder and drive roots.
Function VD_VerifyInstDir
    StrLen $1 $WINDIR
    StrCpy $0 $INSTDIR $1
    ${If} $0 == $WINDIR
        Abort
    ${EndIf}
    StrLen $1 $PROFILE
    StrCpy $0 $INSTDIR $1
    ${If} $0 == $PROFILE
        Abort
    ${EndIf}
    StrLen $1 $INSTDIR
    ${If} $1 <= 3          ; "C:\" or "C:"
        Abort
    ${EndIf}
FunctionEnd

# -------------------------------------------------------------- Mode page --
Function VD_ModePageCreate
    ${If} $VD_SkipModePage == "1"
        Abort
    ${EndIf}
    StrCpy $VD_InfoWhich ""

    InitPluginsDir
    File "/oname=$PLUGINSDIR\vd-simple.txt" "${VD_MODES_DIR}\simple.en.txt"
    File "/oname=$PLUGINSDIR\vd-advanced.txt" "${VD_MODES_DIR}\advanced.en.txt"

    !ifmacrodef MUI_HEADER_TEXT
        !insertmacro MUI_HEADER_TEXT "Choose how you want to use VaultDock" \
            "You can switch later in Settings without reinstalling or losing backups."
    !endif

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    ${NSD_CreateRadioButton} 0 0 70% 12u "&Simple Folder Backups (recommended)"
    Pop $VD_RadioSimple
    ${NSD_AddStyle} $VD_RadioSimple ${WS_GROUP}
    ${NSD_CreateLink} 72% 1u 28% 10u "${U+24D8} What does this include?"
    Pop $0
    ${NSD_OnClick} $0 VD_OnInfoSimple
    ${NSD_CreateLabel} 12u 14u -12u 28u "Pick folders in your profile, back them up automatically and restore files. Smallest download, fewest permissions."
    Pop $0

    ${NSD_CreateRadioButton} 0 50u 70% 12u "&Full Access to All Tools (Advanced)"
    Pop $VD_RadioAdvanced
    ${NSD_CreateLink} 72% 51u 28% 10u "${U+24D8} What does this include?"
    Pop $0
    ${NSD_OnClick} $0 VD_OnInfoAdvanced
    ${NSD_CreateLabel} 12u 64u -12u 36u "Everything in Simple plus sync, automations, app discovery and reinstall, settings backup, snapshots of locked files and multi-profile protection. Downloads extra components after installation."
    Pop $0

    ${If} $VD_Mode == "advanced"
        ${NSD_Check} $VD_RadioAdvanced
    ${Else}
        ${NSD_Check} $VD_RadioSimple
    ${EndIf}

    nsDialogs::Show
FunctionEnd

Function VD_ModePageLeave
    ${NSD_GetState} $VD_RadioAdvanced $0
    ${If} $0 == ${BST_CHECKED}
        StrCpy $VD_Mode "advanced"
    ${Else}
        StrCpy $VD_Mode "simple"
    ${EndIf}
FunctionEnd

Function VD_OnInfoSimple
    Pop $0
    StrCpy $VD_InfoWhich "simple"
    SendMessage $HWNDPARENT ${VD_WM_NOTIFY_OUTER_NEXT} 1 0
FunctionEnd

Function VD_OnInfoAdvanced
    Pop $0
    StrCpy $VD_InfoWhich "advanced"
    SendMessage $HWNDPARENT ${VD_WM_NOTIFY_OUTER_NEXT} 1 0
FunctionEnd

# ----------------------------------------------------- (i) info "dialog" --
# A wizard page that acts as a modal: it only appears when an (i) link was
# clicked, hides Next, and Back returns to the mode page with the selection
# preserved. It is skipped in both directions otherwise.
Function VD_InfoPageCreate
    ${If} $VD_InfoWhich == ""
        Abort
    ${EndIf}

    !ifmacrodef MUI_HEADER_TEXT
        ${If} $VD_InfoWhich == "advanced"
            !insertmacro MUI_HEADER_TEXT "Full Access to All Tools (Advanced)" "What this mode does, what it needs and how it differs."
        ${Else}
            !insertmacro MUI_HEADER_TEXT "Simple Folder Backups" "What this mode does, what it needs and how it differs."
        ${EndIf}
    !endif

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    nsDialogs::CreateControl EDIT ${DEFAULT_STYLES}|${WS_TABSTOP}|${WS_VSCROLL}|${ES_MULTILINE}|${ES_AUTOVSCROLL}|${ES_READONLY} ${WS_EX_CLIENTEDGE} 0 0 100% 100% ""
    Pop $VD_InfoEdit
    SendMessage $VD_InfoEdit ${EM_LIMITTEXT} 0 0
    Call VD_LoadInfoText

    GetDlgItem $0 $HWNDPARENT 1          ; Next
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 3          ; Back
    SendMessage $0 ${WM_SETTEXT} 0 "STR:< Back to the choice"

    nsDialogs::Show

    # Page left (Back or Cancel): restore the wizard buttons.
    GetDlgItem $0 $HWNDPARENT 1
    ShowWindow $0 ${SW_SHOW}
    GetDlgItem $0 $HWNDPARENT 3
    SendMessage $0 ${WM_SETTEXT} 0 "STR:$(^BackBtn)"
    StrCpy $VD_InfoWhich ""
FunctionEnd

# Appends the text file to the edit control line by line, so the length is
# not limited by NSIS_MAX_STRLEN. Lines are normalised to CRLF.
Function VD_LoadInfoText
    ClearErrors
    FileOpen $1 "$PLUGINSDIR\vd-$VD_InfoWhich.txt" r
    ${If} ${Errors}
        SendMessage $VD_InfoEdit ${WM_SETTEXT} 0 "STR:The description could not be loaded. See Settings > Mode after installation."
        Return
    ${EndIf}
    ${Do}
        ClearErrors
        FileRead $1 $2
        ${If} ${Errors}
            ${Break}
        ${EndIf}
        StrCpy $3 $2 1 -1
        ${If} $3 == "$\n"
            StrCpy $2 $2 -1
            StrCpy $3 $2 1 -1
            ${If} $3 == "$\r"
                StrCpy $2 $2 -1
            ${EndIf}
            StrCpy $2 "$2$\r$\n"
        ${EndIf}
        SendMessage $VD_InfoEdit ${WM_GETTEXTLENGTH} 0 0 $4
        SendMessage $VD_InfoEdit ${EM_SETSEL} $4 $4
        SendMessage $VD_InfoEdit ${EM_REPLACESEL} 0 "STR:$2"
    ${Loop}
    FileClose $1
    SendMessage $VD_InfoEdit ${EM_SETSEL} 0 0
FunctionEnd

# --------------------------------------------------------------- Install --
# Stops the agent and every UI instance (all sessions) before files are
# replaced. `net stop` waits for SERVICE_STOPPED; exit codes are not
# localised (2 = not started, also fine).
Function VD_StopForUpgrade
    nsExec::ExecToLog '"$SYSDIR\net.exe" stop ${VD_SERVICE_NAME}'
    Pop $0
    DetailPrint "Stop ${VD_SERVICE_NAME}: exit $0"
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${VD_AGENT_EXE}" /F'
    Pop $0
    nsExec::Exec '"$SYSDIR\taskkill.exe" /IM "${PRODUCT_EXECUTABLE}" /F'
    Pop $0
    Sleep 800
FunctionEnd

!macro VD_Icacls ARGS
    nsExec::ExecToLog '"$SYSDIR\icacls.exe" ${ARGS}'
    Pop $0
    ${If} $0 != 0
        DetailPrint 'icacls ${ARGS} failed: $0'
        MessageBox MB_ICONSTOP "VaultDock could not secure its data folder ($0). Setup will stop to keep your PC safe." /SD IDOK
        Abort
    ${EndIf}
!macroend

# Creates %ProgramData%\VaultDock with: owner Administrators, no inherited
# entries, SYSTEM + Administrators full control, Users read on logs only.
# A pre-existing folder that we did not create (any user may create folders
# in ProgramData) or a reparse point is moved aside, never trusted: running
# icacls /T over planted hard links would change ACLs on their targets.
Function VD_PrepareDataDir
    # Any existing entry counts (directory, file, or dangling junction).
    System::Call 'kernel32::GetFileAttributesW(w "$VD_DataDir") i .r0'
    ${If} $0 <> -1
        IntOp $1 $0 & ${VD_FILE_ATTRIBUTE_REPARSE_POINT}
        IntOp $3 $0 & 0x10                ; FILE_ATTRIBUTE_DIRECTORY
        ${If} $3 = 0
            StrCpy $1 1                   ; a plain file is never trusted
        ${EndIf}
        ${If} $1 <> 0
        ${OrIf} $VD_IsUpgrade != "1"
            System::Call 'kernel32::GetTickCount() i .r2'
            DetailPrint "Untrusted $VD_DataDir found; moving it aside."
            ClearErrors
            Rename "$VD_DataDir" "$VD_DataDir.untrusted-$2"
            ${If} ${Errors}
                MessageBox MB_ICONSTOP "An existing folder $VD_DataDir could not be checked or moved. Remove it and run Setup again." /SD IDOK
                Abort
            ${EndIf}
        ${EndIf}
    ${EndIf}

    CreateDirectory "$VD_DataDir"
    !insertmacro VD_Icacls '"$VD_DataDir" /setowner ${VD_SID_ADMINS}'
    !insertmacro VD_Icacls '"$VD_DataDir" /inheritance:r /grant:r ${VD_SID_SYSTEM}:(OI)(CI)F ${VD_SID_ADMINS}:(OI)(CI)F'
    ${If} $VD_IsUpgrade == "1"
        # Trusted tree (created locked down by an earlier admin install):
        # re-apply inheritance to children to repair drift.
        !insertmacro VD_Icacls '"$VD_DataDir\*" /reset /T /C /L /Q'
    ${EndIf}

    CreateDirectory "$VD_DataDir\config"
    CreateDirectory "$VD_DataDir\state"
    CreateDirectory "$VD_DataDir\logs"
    !insertmacro VD_Icacls '"$VD_DataDir\logs" /grant ${VD_SID_USERS}:(OI)(CI)RX'
FunctionEnd

# Writes the installer's mode choice. The agent imports it on first start
# into state.db (modules table) and is the authority afterwards; it never
# reads this file again except to detect a /MODE= reinstall.
Function VD_WriteModeFile
    ${If} $VD_Mode == ""
        DetailPrint "Keeping the existing install mode."
        Return
    ${EndIf}
    StrCpy $5 "$VD_DataDir\${VD_MODE_FILE_NAME}"
    Delete "$5.tmp"
    ClearErrors
    FileOpen $0 "$5.tmp" w
    ${If} ${Errors}
        MessageBox MB_ICONSTOP "Could not write $5." /SD IDOK
        Abort
    ${EndIf}
    FileWrite $0 '{"version":1,"mode":"$VD_Mode","source":"installer","installer_version":"${INFO_PRODUCTVERSION}"}$\r$\n'
    FileClose $0
    Delete "$5"
    Rename "$5.tmp" "$5"
    # Explicit, non-inherited ACL: only SYSTEM and Administrators (write or read).
    !insertmacro VD_Icacls '"$5" /inheritance:r /grant:r ${VD_SID_SYSTEM}:F ${VD_SID_ADMINS}:F'
    DetailPrint "Install mode: $VD_Mode"
FunctionEnd

!macro VD_Sc ARGS
    nsExec::ExecToLog '"$SYSDIR\sc.exe" ${ARGS}'
    Pop $0
!macroend

# Registers (or reconfigures on upgrade) and starts the agent service.
# The binary path is quoted (no unquoted-service-path issue) and lives
# under Program Files, which only administrators can write.
Function VD_RegisterService
    StrCpy $6 'binPath= "\"$INSTDIR\${VD_AGENT_EXE}\" service" start= delayed-auto obj= LocalSystem DisplayName= "${VD_SERVICE_DISPLAY}"'
    !insertmacro VD_Sc 'create ${VD_SERVICE_NAME} $6'
    ${If} $0 == 1073                      ; ERROR_SERVICE_EXISTS
        !insertmacro VD_Sc 'config ${VD_SERVICE_NAME} $6'
    ${EndIf}
    ${If} $0 != 0
        MessageBox MB_ICONSTOP "Could not register the VaultDock service (error $0)." /SD IDOK
        Abort
    ${EndIf}

    !insertmacro VD_Sc 'description ${VD_SERVICE_NAME} "Runs VaultDock backups and sync in the background."'
    # Restart after 5 s, then 30 s, then 2 min; reset the count after a day.
    !insertmacro VD_Sc 'failure ${VD_SERVICE_NAME} reset= 86400 actions= restart/5000/restart/30000/restart/120000'
    !insertmacro VD_Sc 'failureflag ${VD_SERVICE_NAME} 1'
    !insertmacro VD_Sc 'sidtype ${VD_SERVICE_NAME} unrestricted'

    # Privileges follow the mode. On upgrade without /MODE the agent already
    # owns the list; reapply from the mode file's value only when we wrote it.
    ${If} $VD_Mode == "advanced"
        !insertmacro VD_Sc 'privs ${VD_SERVICE_NAME} ${VD_PRIVS_ADVANCED}'
    ${ElseIf} $VD_Mode == "simple"
        !insertmacro VD_Sc 'privs ${VD_SERVICE_NAME} ${VD_PRIVS_SIMPLE}'
    ${EndIf}
    ${If} $0 != 0
        MessageBox MB_ICONSTOP "Could not restrict the VaultDock service privileges (error $0)." /SD IDOK
        Abort
    ${EndIf}

    # UI tray for every user (HKLM Run, 64-bit view).
    SetRegView 64
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "VaultDock" '"$INSTDIR\${PRODUCT_EXECUTABLE}" --tray'
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"

    !insertmacro VD_Sc 'start ${VD_SERVICE_NAME}'
    ${If} $0 != 0
    ${AndIf} $0 != 1056                   ; ERROR_SERVICE_ALREADY_RUNNING
        # Not fatal: the files are installed; the UI reports the service state.
        DetailPrint "The VaultDock service did not start (error $0)."
    ${EndIf}
FunctionEnd

# ------------------------------------------------------------- Uninstall --
# A macro so it can be used inside Section "uninstall" (un. functions are
# separate in NSIS). Program data is handled by the caller (asks first).
!macro VD_UninstallService
    nsExec::ExecToLog '"$SYSDIR\net.exe" stop ${VD_SERVICE_NAME}'
    Pop $0
    nsExec::ExecToLog '"$SYSDIR\sc.exe" delete ${VD_SERVICE_NAME}'
    Pop $0
    SetRegView 64
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "VaultDock"
!macroend
