####
## VaultDock installer: migration from the old per-user install (MASTER_PLAN
## section 4 C5). Needs common.nsh.
##
## The per-user releases installed into %LOCALAPPDATA%\Programs\VaultDock,
## registered HKCU\...\Uninstall\VaultDockVaultDock and an HKCU Run value
## "VaultDock", and kept settings, the user-scope DPAPI device key and sync
## state in %APPDATA%\VaultDock.
##
## Detection (first match wins):
##   1. /MIGRATE_FROM=<dir> [/MIGRATE_SID=<sid>]  passed by the last per-user
##      release ("Move to the new installer"). The SID is needed because an
##      over-the-shoulder elevation gives this installer the administrator's
##      HKCU and %LOCALAPPDATA%, not the user's.
##   2. /D=<dir> inside a user profile: an older per-user updater
##      (setup.exe /S /RELAUNCH /D=<dir>). The directory is not used as the
##      install directory (VD_VerifyInstDir); it becomes the migration source.
##   3. This user's own per-user install (HKCU uninstall key or the default
##      %LOCALAPPDATA%\Programs\VaultDock).
## A candidate counts only if <dir>\VaultDock.exe exists.
##
## What the installer does (VD_MigrateApply), all as plain data operations:
##   - the old UI is closed (VD_StopForUpgrade kills every VaultDock.exe);
##   - its HKCU Run value is removed (HKU\<sid> when the SID was passed), so
##     the old copy no longer starts at sign-in next to the new tray UI;
##   - %ProgramData%\VaultDock\config\migrate-<sid>.json is written:
##       {"version":1,"from":"<dir>","sid":"<sid>","sid_source":"arg|token",
##        "installer_version":"<v>"}
## It never copies %APPDATA%\VaultDock and never touches the device key:
## user-scope DPAPI only decrypts in that user's logon session. The new UI
## does that later (AdoptUserDevice, P1-29), then runs the per-user
## uninstaller as the user; its silent default keeps %APPDATA%\VaultDock as
## the rollback copy.
##
## The per-user uninstaller is NOT run from this elevated installer by
## default: it lives in a folder the user can write, so running it here
## would run user-replaceable code as administrator (a privilege escalation
## for over-the-shoulder elevation). Define VD_MIGRATE_RUN_PERUSER_UNINSTALLER
## to run it here anyway, only when the per-user install belongs to the
## elevated user itself (no /MIGRATE_SID or the same SID).
####

Var VD_MigrateFrom    ; per-user install directory to migrate ("" = none)
Var VD_MigrateSid     ; user SID for the marker ("" = unknown)
Var VD_MigrateSidSrc  ; "arg" (/MIGRATE_SID) | "token" (this process)

# Validates candidate $R0: must hold the UI exe and must not be a machine
# location. Sets VD_MigrateFrom on success. Uses $R0-$R4.
Function VD_MigrateCandidate
    ${If} $VD_MigrateFrom != ""
    ${OrIf} $R0 == ""
        Return
    ${EndIf}
    ${IfNot} ${FileExists} "$R0\${PRODUCT_EXECUTABLE}"
        Return
    ${EndIf}
    StrCpy $R1 $PROGRAMFILES64
    Call VD_IsUnder
    ${If} $R2 == "1"
        Return
    ${EndIf}
    StrCpy $R1 $WINDIR
    Call VD_IsUnder
    ${If} $R2 == "1"
        Return
    ${EndIf}
    StrCpy $VD_MigrateFrom $R0
FunctionEnd

# .onInit part, after VD_GetTokenSid. Takes the initial $INSTDIR (from /D=
# or the default) on the stack:
#   Push $INSTDIR
#   Call VD_MigrateDetect
Function VD_MigrateDetect
    Exch $9                       ; initial $INSTDIR
    StrCpy $VD_MigrateFrom ""
    StrCpy $VD_MigrateSid ""
    StrCpy $VD_MigrateSidSrc ""

    ${GetParameters} $8
    # 1. Explicit request from the last per-user release.
    ClearErrors
    ${GetOptions} $8 "/MIGRATE_FROM=" $R0
    ${IfNot} ${Errors}
        Call VD_MigrateCandidate
    ${EndIf}

    # 2. /D= pointing into a user profile (older per-user updater).
    ${GetParent} $PROFILE $R1          ; e.g. C:\Users
    StrCpy $R0 $9
    Call VD_IsUnder
    ${If} $R2 == "1"
        StrCpy $R0 $9
        Call VD_MigrateCandidate
    ${EndIf}

    # 3. This user's own per-user install.
    SetShellVarContext current
    ReadRegStr $R0 HKCU "${UNINST_KEY}" "DisplayIcon"
    ${If} $R0 != ""
        ${GetParent} $R0 $R0
        Call VD_MigrateCandidate
    ${EndIf}
    StrCpy $R0 "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
    Call VD_MigrateCandidate
    SetShellVarContext all

    ${If} $VD_MigrateFrom != ""
        ClearErrors
        ${GetOptions} $8 "/MIGRATE_SID=" $R0
        ${IfNot} ${Errors}
            Call VD_ValidUserSid
            ${If} $R1 == "1"
                StrCpy $VD_MigrateSid $R0
                StrCpy $VD_MigrateSidSrc "arg"
            ${EndIf}
        ${EndIf}
        ${If} $VD_MigrateSid == ""
            StrCpy $R0 $VD_TokenSid
            Call VD_ValidUserSid
            ${If} $R1 == "1"
                StrCpy $VD_MigrateSid $R0
                StrCpy $VD_MigrateSidSrc "token"
            ${EndIf}
        ${EndIf}
    ${EndIf}
    Pop $9
FunctionEnd

# ------------------------------------------------------- Migration page --
Function VD_MigratePageCreate
    ${If} $VD_MigrateFrom == ""
        Abort
    ${EndIf}
    !insertmacro MUI_HEADER_TEXT "Move VaultDock to the new installation" \
        "An earlier VaultDock that was installed for one user was found."

    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
        Abort
    ${EndIf}

    ${NSD_CreateLabel} 0 0 100% 24u "Setup found VaultDock installed for a single user in:$\r$\n$VD_MigrateFrom"
    Pop $0
    ${NSD_CreateLabel} 0 28u 100% 104u "This version installs VaultDock for the whole PC, with a background service. Setup will:$\r$\n- close the old copy and stop it from starting with Windows;$\r$\n- leave its settings, device registration and history untouched.$\r$\n$\r$\nThe next time that user opens VaultDock, it moves their settings and device registration to the new installation, so there is no need to sign in or register this PC again. The old program is then removed; its data folder is kept as a backup copy.$\r$\n$\r$\nClick Cancel to keep the old version instead. It keeps working but gets no more updates."
    Pop $0

    nsDialogs::Show
FunctionEnd

# --------------------------------------------------------------- Install --
# Call after VD_PrepareDataDir (needs config\).
Function VD_MigrateApply
    ${If} $VD_MigrateFrom == ""
        Return
    ${EndIf}
    DetailPrint "Migrating the per-user install in $VD_MigrateFrom."

    # The old copy must not start at sign-in next to the new tray UI.
    ${If} $VD_MigrateSidSrc == "arg"
        DeleteRegValue HKU "$VD_MigrateSid\${VD_RUN_KEY}" "${VD_RUN_VALUE}"
    ${Else}
        DeleteRegValue HKCU "${VD_RUN_KEY}" "${VD_RUN_VALUE}"
    ${EndIf}

    ${If} $VD_MigrateSid == ""
        DetailPrint "The user of the per-user install is unknown; VaultDock offers the move when that user opens it."
    ${Else}
        StrCpy $R0 $VD_MigrateFrom
        Call VD_JsonEscape
        StrCpy $5 "$VD_DataDir\config\migrate-$VD_MigrateSid.json"
        Delete "$5.tmp"
        ClearErrors
        FileOpen $0 "$5.tmp" w
        ${If} ${Errors}
            DetailPrint "Could not write $5; VaultDock offers the move when that user opens it."
        ${Else}
            FileWrite $0 '{"version":1,"from":"$R1","sid":"$VD_MigrateSid","sid_source":"$VD_MigrateSidSrc","installer_version":"${INFO_PRODUCTVERSION}"}$\r$\n'
            FileClose $0
            Delete "$5"
            Rename "$5.tmp" "$5"
        ${EndIf}
    ${EndIf}

    !ifdef VD_MIGRATE_RUN_PERUSER_UNINSTALLER
        ${If} $VD_MigrateSid != ""
        ${AndIf} $VD_MigrateSid == $VD_TokenSid
            ${If} ${FileExists} "$VD_MigrateFrom\uninstall.exe"
                # _?= runs the uninstaller in place and waits for it. Its
                # silent default keeps %APPDATA%\VaultDock.
                ExecWait '"$VD_MigrateFrom\uninstall.exe" /S _?=$VD_MigrateFrom' $0
                DetailPrint "Per-user uninstaller: exit $0"
                Delete "$VD_MigrateFrom\uninstall.exe"
                RMDir "$VD_MigrateFrom"
            ${EndIf}
        ${EndIf}
    !endif
FunctionEnd
